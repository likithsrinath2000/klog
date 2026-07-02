package engine

import (
	"fmt"
	"strings"
)

// loadSource resolves a union/join/lookup source spec into rows.
// Spec forms:
//
//	path.log
//	"path.log"
//	(path.log | where x==1 | project a,b)
func loadSource(spec string) ([]Record, error) {
	spec = strings.TrimSpace(spec)
	sub := ""
	if strings.HasPrefix(spec, "(") && strings.HasSuffix(spec, ")") {
		inner := strings.TrimSpace(spec[1 : len(spec)-1])
		stages := splitPipe(inner)
		if len(stages) == 0 {
			return nil, fmt.Errorf("empty source")
		}
		spec = strings.TrimSpace(stages[0])
		if len(stages) > 1 {
			sub = strings.Join(stages[1:], " | ")
		}
	}
	spec = strings.Trim(spec, `"'`)
	if FileLoader == nil {
		return nil, fmt.Errorf("no file loader configured (cannot read %q)", spec)
	}
	rows, err := FileLoader(spec)
	if err != nil {
		return nil, err
	}
	if sub != "" {
		p, err := Compile(sub)
		if err != nil {
			return nil, err
		}
		res, err := p.Run(rows)
		if err != nil {
			return nil, err
		}
		return res.Rows, nil
	}
	return rows, nil
}

// ---- union ----

type unionOp struct{ specs []string }

func compileUnion(rest string) (operator, error) {
	// strip optional "kind=..." / "withsource=..." prefixes
	specs := splitTop(rest, ',')
	var clean []string
	for _, s := range specs {
		l := strings.ToLower(s)
		if strings.HasPrefix(l, "kind=") || strings.HasPrefix(l, "withsource=") || strings.HasPrefix(l, "isfuzzy=") {
			continue
		}
		clean = append(clean, s)
	}
	if len(clean) == 0 {
		return nil, fmt.Errorf("union needs at least one source")
	}
	return unionOp{specs: clean}, nil
}

func (o unionOp) apply(in Result) (Result, error) {
	rows := append([]Record{}, in.Rows...)
	for _, s := range o.specs {
		extra, err := loadSource(s)
		if err != nil {
			return Result{}, err
		}
		rows = append(rows, extra...)
	}
	return Result{Rows: rows}, nil
}

// ---- join / lookup shared key spec ----

type joinKey struct{ left, right string }

func parseOnKeys(spec string) ([]joinKey, error) {
	var keys []joinKey
	for _, k := range splitTop(spec, ',') {
		k = strings.TrimSpace(k)
		if idx := strings.Index(k, "=="); idx >= 0 {
			l := cleanKeyRef(k[:idx])
			r := cleanKeyRef(k[idx+2:])
			keys = append(keys, joinKey{left: l, right: r})
		} else {
			keys = append(keys, joinKey{left: k, right: k})
		}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("join/lookup needs 'on <keys>'")
	}
	return keys, nil
}

func cleanKeyRef(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "$left.")
	s = strings.TrimPrefix(s, "$right.")
	return s
}

func keyString(r Record, fields []string) string {
	var b strings.Builder
	for _, f := range fields {
		v, _ := getField(r, f)
		b.WriteString(toString(v))
		b.WriteByte('\x00')
	}
	return b.String()
}

// extractOnClause pulls the source spec and key spec from "(...) on k1, k2".
func extractOnClause(rest string) (src, keys string, err error) {
	rest = strings.TrimSpace(rest)
	if !strings.HasPrefix(rest, "(") {
		return "", "", fmt.Errorf("expected '(<source>)'")
	}
	depth := 0
	end := -1
	for i, c := range rest {
		switch c {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return "", "", fmt.Errorf("unbalanced parentheses in source")
	}
	src = rest[:end+1]
	after := strings.TrimSpace(rest[end+1:])
	if lw, r := firstWord(after); strings.EqualFold(lw, "on") {
		keys = r
	} else {
		return "", "", fmt.Errorf("join/lookup needs 'on <keys>'")
	}
	return src, keys, nil
}

// ---- join ----

type joinOp struct {
	kind string
	src  string
	keys []joinKey
}

func compileJoin(rest string) (operator, error) {
	rest = strings.TrimSpace(rest)
	kind := "inner"
	for {
		lw, r := firstWord(rest)
		ll := strings.ToLower(lw)
		if strings.HasPrefix(ll, "kind=") {
			kind = strings.TrimPrefix(ll, "kind=")
			rest = r
			continue
		}
		if strings.HasPrefix(ll, "hint.") {
			rest = r
			continue
		}
		break
	}
	src, keySpec, err := extractOnClause(rest)
	if err != nil {
		return nil, err
	}
	keys, err := parseOnKeys(keySpec)
	if err != nil {
		return nil, err
	}
	return joinOp{kind: normalizeKind(kind), src: src, keys: keys}, nil
}

func normalizeKind(k string) string {
	switch k {
	case "", "inner":
		return "inner"
	case "left", "leftouter":
		return "leftouter"
	case "right", "rightouter":
		return "rightouter"
	case "full", "fullouter", "outer":
		return "fullouter"
	case "leftsemi":
		return "leftsemi"
	case "rightsemi":
		return "rightsemi"
	case "leftanti", "anti", "leftantisemi":
		return "leftanti"
	case "rightanti", "rightantisemi":
		return "rightanti"
	case "innerunique":
		return "inner"
	}
	return k
}

func (o joinOp) apply(in Result) (Result, error) {
	right, err := loadSource(o.src)
	if err != nil {
		return Result{}, err
	}
	leftCols := columnsOf(in)
	leftKeys := keyFields(o.keys, true)
	rightKeys := keyFields(o.keys, false)

	// index right by key
	rindex := map[string][]Record{}
	for _, rr := range right {
		k := keyString(rr, rightKeys)
		rindex[k] = append(rindex[k], rr)
	}

	var out []Record
	switch o.kind {
	case "leftsemi", "leftanti":
		for _, lr := range in.Rows {
			_, ok := rindex[keyString(lr, leftKeys)]
			if (o.kind == "leftsemi") == ok {
				out = append(out, lr)
			}
		}
		return Result{Rows: out, Cols: in.Cols}, nil
	case "rightsemi", "rightanti":
		lindex := map[string]bool{}
		for _, lr := range in.Rows {
			lindex[keyString(lr, leftKeys)] = true
		}
		for _, rr := range right {
			has := lindex[keyString(rr, rightKeys)]
			if (o.kind == "rightsemi") == has {
				out = append(out, rr)
			}
		}
		return Result{Rows: out}, nil
	}

	rMatched := map[int]bool{}
	rByKeyIdx := map[string][]int{}
	for i, rr := range right {
		rByKeyIdx[keyString(rr, rightKeys)] = append(rByKeyIdx[keyString(rr, rightKeys)], i)
	}

	for _, lr := range in.Rows {
		k := keyString(lr, leftKeys)
		matches := rByKeyIdx[k]
		if len(matches) == 0 {
			if o.kind == "leftouter" || o.kind == "fullouter" {
				out = append(out, mergeRows(lr, nil, leftCols))
			}
			continue
		}
		for _, mi := range matches {
			rMatched[mi] = true
			out = append(out, mergeRows(lr, right[mi], leftCols))
		}
	}
	if o.kind == "rightouter" || o.kind == "fullouter" {
		for i, rr := range right {
			if !rMatched[i] {
				out = append(out, mergeRows(nil, rr, leftCols))
			}
		}
	}
	return Result{Rows: out}, nil
}

func keyFields(keys []joinKey, left bool) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		if left {
			out[i] = k.left
		} else {
			out[i] = k.right
		}
	}
	return out
}

// mergeRows combines a left and right record; right columns that collide with
// left columns are suffixed with "1" (KQL behavior).
func mergeRows(left, right Record, leftCols []string) Record {
	nr := Record{}
	leftSet := map[string]bool{}
	for _, c := range leftCols {
		leftSet[c] = true
	}
	if left != nil {
		for k, v := range left {
			nr[k] = v
		}
	} else {
		for _, c := range leftCols {
			nr[c] = nil
		}
	}
	if right != nil {
		for k, v := range right {
			name := k
			if leftSet[k] {
				name = k + "1"
			}
			nr[name] = v
		}
	}
	return nr
}

// ---- lookup ----

type lookupOp struct {
	kind string
	src  string
	keys []joinKey
}

func compileLookup(rest string) (operator, error) {
	rest = strings.TrimSpace(rest)
	kind := "leftouter"
	for {
		lw, r := firstWord(rest)
		if strings.HasPrefix(strings.ToLower(lw), "kind=") {
			kind = strings.TrimPrefix(strings.ToLower(lw), "kind=")
			rest = r
			continue
		}
		break
	}
	src, keySpec, err := extractOnClause(rest)
	if err != nil {
		return nil, err
	}
	keys, err := parseOnKeys(keySpec)
	if err != nil {
		return nil, err
	}
	return lookupOp{kind: normalizeKind(kind), src: src, keys: keys}, nil
}

func (o lookupOp) apply(in Result) (Result, error) {
	right, err := loadSource(o.src)
	if err != nil {
		return Result{}, err
	}
	rightKeys := keyFields(o.keys, false)
	leftKeys := keyFields(o.keys, true)
	rkeySet := map[string]bool{}
	for _, k := range rightKeys {
		rkeySet[k] = true
	}
	// first match wins
	rindex := map[string]Record{}
	for _, rr := range right {
		k := keyString(rr, rightKeys)
		if _, ok := rindex[k]; !ok {
			rindex[k] = rr
		}
	}
	var out []Record
	for _, lr := range in.Rows {
		nr := Record{}
		for k, v := range lr {
			nr[k] = v
		}
		if match, ok := rindex[keyString(lr, leftKeys)]; ok {
			for k, v := range match {
				if rkeySet[k] {
					continue // don't duplicate join keys
				}
				if _, exists := nr[k]; !exists {
					nr[k] = v
				}
			}
		} else if o.kind == "inner" {
			continue
		}
		out = append(out, nr)
	}
	return Result{Rows: out}, nil
}
