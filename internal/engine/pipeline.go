package engine

import (
	"fmt"
	"sort"
	"strings"
)

// Result carries rows plus an optional explicit column order for display.
type Result struct {
	Rows []Record
	Cols []string // nil => infer union of keys
}

// operator transforms a Result.
type operator interface {
	apply(Result) (Result, error)
}

// ---- pipeline splitting (respect quotes/parens) ----

func splitPipe(q string) []string {
	var parts []string
	var cur strings.Builder
	depth := 0
	var quote rune
	for _, c := range q {
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
			cur.WriteRune(c)
		case c == '"' || c == '\'':
			quote = c
			cur.WriteRune(c)
		case c == '(':
			depth++
			cur.WriteRune(c)
		case c == ')':
			depth--
			cur.WriteRune(c)
		case c == '|' && depth == 0:
			parts = append(parts, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteRune(c)
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		parts = append(parts, s)
	}
	return parts
}

// splitTop splits on a rune at top level (ignoring quotes/parens).
func splitTop(s string, sep rune) []string {
	var parts []string
	var cur strings.Builder
	depth := 0
	var quote rune
	for _, c := range s {
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
			cur.WriteRune(c)
		case c == '"' || c == '\'':
			quote = c
			cur.WriteRune(c)
		case c == '(':
			depth++
			cur.WriteRune(c)
		case c == ')':
			depth--
			cur.WriteRune(c)
		case c == sep && depth == 0:
			parts = append(parts, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteRune(c)
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		parts = append(parts, s)
	}
	return parts
}

// Pipeline is a compiled query.
type Pipeline struct {
	ops []operator
}

// Compile parses a full query string into a Pipeline.
func Compile(query string) (*Pipeline, error) {
	stages := splitPipe(query)
	p := &Pipeline{}
	for _, st := range stages {
		op, err := compileStage(st)
		if err != nil {
			return nil, fmt.Errorf("in %q: %w", st, err)
		}
		p.ops = append(p.ops, op)
	}
	return p, nil
}

// Run executes the pipeline over rows.
func (p *Pipeline) Run(rows []Record) (Result, error) {
	res := Result{Rows: rows}
	for _, op := range p.ops {
		var err error
		res, err = op.apply(res)
		if err != nil {
			return res, err
		}
	}
	return res, nil
}

func compileStage(s string) (operator, error) {
	kw, rest := firstWord(s)
	switch strings.ToLower(kw) {
	case "where", "filter":
		e, err := ParseExpr(rest)
		if err != nil {
			return nil, err
		}
		return whereOp{e}, nil
	case "project", "select":
		return compileProject(rest)
	case "summarize", "stats":
		return compileSummarize(rest)
	case "sort", "order":
		return compileSort(rest)
	case "take", "limit", "head":
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(rest), "%d", &n); err != nil {
			return nil, fmt.Errorf("take needs a number")
		}
		return takeOp{n}, nil
	case "count":
		return countOp{}, nil
	}
	return nil, fmt.Errorf("unknown operator %q", kw)
}

func firstWord(s string) (string, string) {
	s = strings.TrimSpace(s)
	i := strings.IndexFunc(s, func(r rune) bool { return r == ' ' || r == '\t' })
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimSpace(s[i+1:])
}

// ---- where ----

type whereOp struct{ e Expr }

func (o whereOp) apply(in Result) (Result, error) {
	out := in.Rows[:0:0]
	for _, r := range in.Rows {
		if truthy(o.e.eval(r)) {
			out = append(out, r)
		}
	}
	return Result{Rows: out, Cols: in.Cols}, nil
}

// ---- take ----

type takeOp struct{ n int }

func (o takeOp) apply(in Result) (Result, error) {
	if o.n < len(in.Rows) {
		in.Rows = in.Rows[:o.n]
	}
	return in, nil
}

// ---- count ----

type countOp struct{}

func (countOp) apply(in Result) (Result, error) {
	return Result{
		Rows: []Record{{"count": float64(len(in.Rows))}},
		Cols: []string{"count"},
	}, nil
}

// ---- project ----

type projItem struct {
	name  string
	field string
}
type projectOp struct{ items []projItem }

func compileProject(rest string) (operator, error) {
	fields := splitTop(rest, ',')
	if len(fields) == 0 {
		return nil, fmt.Errorf("project needs at least one field")
	}
	var items []projItem
	for _, f := range fields {
		name, src := f, f
		if kv := splitTop(f, '='); len(kv) == 2 {
			name, src = kv[0], kv[1]
		}
		items = append(items, projItem{name: name, field: src})
	}
	return projectOp{items}, nil
}

func (o projectOp) apply(in Result) (Result, error) {
	cols := make([]string, len(o.items))
	out := make([]Record, 0, len(in.Rows))
	for _, r := range in.Rows {
		nr := Record{}
		for i, it := range o.items {
			cols[i] = it.name
			if v, ok := getField(r, it.field); ok {
				nr[it.name] = v
			} else {
				nr[it.name] = nil
			}
		}
		out = append(out, nr)
	}
	return Result{Rows: out, Cols: cols}, nil
}

// ---- sort ----

type sortKey struct {
	field string
	desc  bool
}
type sortOp struct{ keys []sortKey }

func compileSort(rest string) (operator, error) {
	rest = strings.TrimSpace(rest)
	if lw, r := firstWord(rest); strings.EqualFold(lw, "by") {
		rest = r
	}
	var keys []sortKey
	for _, k := range splitTop(rest, ',') {
		fw, dir := firstWord(k)
		sk := sortKey{field: fw}
		switch strings.ToLower(strings.TrimSpace(dir)) {
		case "desc", "-":
			sk.desc = true
		case "", "asc", "+":
		default:
			return nil, fmt.Errorf("bad sort direction %q", dir)
		}
		keys = append(keys, sk)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("sort needs a field")
	}
	return sortOp{keys}, nil
}

func (o sortOp) apply(in Result) (Result, error) {
	rows := in.Rows
	sort.SliceStable(rows, func(i, j int) bool {
		for _, k := range o.keys {
			a, _ := getField(rows[i], k.field)
			b, _ := getField(rows[j], k.field)
			c := compare(a, b)
			if c == 0 {
				continue
			}
			if k.desc {
				return c > 0
			}
			return c < 0
		}
		return false
	})
	return Result{Rows: rows, Cols: in.Cols}, nil
}
