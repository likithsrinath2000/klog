package engine

import (
	"fmt"
	"strings"
)

type aggFunc struct {
	name  string // output column name
	fn    string // count|sum|avg|min|max|dcount
	field string // "" for count
}

type summarizeOp struct {
	aggs []aggFunc
	keys []string
}

// findKeyword returns the byte index of a top-level, space-delimited keyword
// (case-insensitive), or -1.
func findKeyword(s, kw string) int {
	lower := strings.ToLower(s)
	depth := 0
	var quote rune
	rs := []rune(s)
	lrs := []rune(lower)
	kwr := []rune(kw)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '(':
			depth++
		case c == ')':
			depth--
		case depth == 0 && c == ' ':
			// check keyword follows, bounded by spaces
			if i+1+len(kwr) <= len(lrs) && string(lrs[i+1:i+1+len(kwr)]) == string(kwr) {
				after := i + 1 + len(kwr)
				if after >= len(lrs) || lrs[after] == ' ' {
					return i + 1
				}
			}
		}
	}
	return -1
}

func compileSummarize(rest string) (operator, error) {
	rest = strings.TrimSpace(rest)
	aggPart := rest
	keyPart := ""
	if idx := findKeyword(rest, "by"); idx >= 0 {
		aggPart = strings.TrimSpace(rest[:idx])
		keyPart = strings.TrimSpace(rest[idx+len("by"):])
	}
	var op summarizeOp
	for _, a := range splitTop(aggPart, ',') {
		if a == "" {
			continue
		}
		af, err := parseAgg(a)
		if err != nil {
			return nil, err
		}
		op.aggs = append(op.aggs, af)
	}
	if len(op.aggs) == 0 {
		return nil, fmt.Errorf("summarize needs at least one aggregation")
	}
	if keyPart != "" {
		for _, k := range splitTop(keyPart, ',') {
			if k != "" {
				op.keys = append(op.keys, k)
			}
		}
	}
	return op, nil
}

func parseAgg(s string) (aggFunc, error) {
	s = strings.TrimSpace(s)
	name := ""
	if kv := splitTop(s, '='); len(kv) == 2 {
		name = strings.TrimSpace(kv[0])
		s = strings.TrimSpace(kv[1])
	}
	open := strings.IndexByte(s, '(')
	if open < 0 || !strings.HasSuffix(s, ")") {
		return aggFunc{}, fmt.Errorf("bad aggregation %q (expected fn(field))", s)
	}
	fn := strings.ToLower(strings.TrimSpace(s[:open]))
	field := strings.TrimSpace(s[open+1 : len(s)-1])
	switch fn {
	case "count":
		// count() ignores field
	case "sum", "avg", "mean", "min", "max", "dcount":
		if field == "" {
			return aggFunc{}, fmt.Errorf("%s() needs a field", fn)
		}
	default:
		return aggFunc{}, fmt.Errorf("unknown aggregation %q", fn)
	}
	if fn == "mean" {
		fn = "avg"
	}
	if name == "" {
		if fn == "count" {
			name = "count"
		} else {
			name = fn + "_" + field
		}
	}
	return aggFunc{name: name, fn: fn, field: field}, nil
}

type aggState struct {
	count  int
	sum    float64
	min    float64
	max    float64
	hasNum bool
	seen   map[string]bool
}

func (o summarizeOp) apply(in Result) (Result, error) {
	type group struct {
		key    string
		keyVal []any
		states []*aggState
	}
	order := []string{}
	groups := map[string]*group{}

	for _, r := range in.Rows {
		kv := make([]any, len(o.keys))
		var kb strings.Builder
		for i, k := range o.keys {
			v, _ := getField(r, k)
			kv[i] = v
			kb.WriteString(toString(v))
			kb.WriteByte('\x00')
		}
		key := kb.String()
		g, ok := groups[key]
		if !ok {
			g = &group{key: key, keyVal: kv, states: make([]*aggState, len(o.aggs))}
			for i := range g.states {
				g.states[i] = &aggState{seen: map[string]bool{}}
			}
			groups[key] = g
			order = append(order, key)
		}
		for i, af := range o.aggs {
			st := g.states[i]
			st.count++
			if af.fn == "dcount" {
				if v, ok := getField(r, af.field); ok {
					st.seen[toString(v)] = true
				}
				continue
			}
			if af.field != "" {
				if v, ok := getField(r, af.field); ok {
					if n, ok := toNumber(v); ok {
						if !st.hasNum {
							st.min, st.max = n, n
							st.hasNum = true
						} else {
							if n < st.min {
								st.min = n
							}
							if n > st.max {
								st.max = n
							}
						}
						st.sum += n
					}
				}
			}
		}
	}

	cols := append(append([]string{}, o.keys...), aggNames(o.aggs)...)
	out := make([]Record, 0, len(order))
	for _, key := range order {
		g := groups[key]
		rec := Record{}
		for i, k := range o.keys {
			rec[k] = g.keyVal[i]
		}
		for i, af := range o.aggs {
			st := g.states[i]
			rec[af.name] = aggValue(af, st)
		}
		out = append(out, rec)
	}
	return Result{Rows: out, Cols: cols}, nil
}

func aggNames(aggs []aggFunc) []string {
	names := make([]string, len(aggs))
	for i, a := range aggs {
		names[i] = a.name
	}
	return names
}

func aggValue(af aggFunc, st *aggState) any {
	switch af.fn {
	case "count":
		return float64(st.count)
	case "dcount":
		return float64(len(st.seen))
	case "sum":
		return st.sum
	case "avg":
		if st.count == 0 {
			return float64(0)
		}
		return st.sum / float64(st.count)
	case "min":
		if !st.hasNum {
			return nil
		}
		return st.min
	case "max":
		if !st.hasNum {
			return nil
		}
		return st.max
	}
	return nil
}
