package engine

import (
	"encoding/json"
	"fmt"
	"math/rand"
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

// FileLoader loads NDJSON rows from a path. Set by the CLI so engine stays
// decoupled from the filesystem. Used by union/join/lookup.
var FileLoader func(path string) ([]Record, error)

// ---- splitters (respect quotes/parens/brackets) ----

func splitTopN(s string, sep rune, keepEmpty bool) []string {
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
		case c == '(' || c == '[':
			depth++
			cur.WriteRune(c)
		case c == ')' || c == ']':
			depth--
			cur.WriteRune(c)
		case c == sep && depth == 0:
			parts = append(parts, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteRune(c)
		}
	}
	last := strings.TrimSpace(cur.String())
	if last != "" || keepEmpty {
		parts = append(parts, last)
	}
	return parts
}

func splitPipe(q string) []string          { return splitTopN(q, '|', false) }
func splitTop(s string, sep rune) []string { return splitTopN(s, sep, false) }

// findKeyword returns the rune index of a top-level, space-delimited keyword
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
		case c == '(' || c == '[':
			depth++
		case c == ')' || c == ']':
			depth--
		case depth == 0 && c == ' ':
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

// splitAssign splits "name = expr" on the first top-level lone '=' (not part of
// ==, >=, <=, !=, =~). Returns (name, exprText, true) if an assignment exists.
func splitAssign(s string) (string, string, bool) {
	depth := 0
	var quote rune
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '(' || c == '[':
			depth++
		case c == ')' || c == ']':
			depth--
		case depth == 0 && c == '=':
			prev := rune(0)
			if i > 0 {
				prev = rs[i-1]
			}
			next := rune(0)
			if i+1 < len(rs) {
				next = rs[i+1]
			}
			if strings.ContainsRune("<>!=", prev) || next == '=' || next == '~' {
				continue
			}
			name := strings.TrimSpace(string(rs[:i]))
			expr := strings.TrimSpace(string(rs[i+1:]))
			return name, expr, true
		}
	}
	return "", "", false
}

// namedExpr is "name = expr" or bare "expr" (name defaults to the expr text).
type namedExpr struct {
	name string
	text string
	expr Expr
}

func parseNamedExpr(s string) (namedExpr, error) {
	name, text, ok := splitAssign(s)
	if !ok {
		name, text = strings.TrimSpace(s), strings.TrimSpace(s)
	}
	e, err := ParseExpr(text)
	if err != nil {
		return namedExpr{}, err
	}
	return namedExpr{name: name, text: strings.TrimSpace(s), expr: e}, nil
}

// ---- pipeline ----

type Pipeline struct{ ops []operator }

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
		return compileProject(rest, projPlain)
	case "project-away":
		return compileProject(rest, projAway)
	case "project-keep":
		return compileProject(rest, projKeep)
	case "project-rename":
		return compileProjectRename(rest)
	case "project-reorder":
		return compileProjectReorder(rest)
	case "extend":
		return compileExtend(rest)
	case "summarize", "stats":
		return compileSummarize(rest)
	case "sort", "order":
		return compileSort(rest)
	case "top":
		return compileTop(rest)
	case "take", "limit", "head":
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(rest), "%d", &n); err != nil {
			return nil, fmt.Errorf("take needs a number")
		}
		return takeOp{n}, nil
	case "count":
		return countOp{}, nil
	case "distinct":
		return compileDistinct(rest)
	case "getschema":
		return getschemaOp{}, nil
	case "print":
		return compilePrint(rest)
	case "parse":
		return compileParse(rest)
	case "mv-expand", "mvexpand":
		return compileMvExpand(rest)
	case "sample":
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(rest), "%d", &n); err != nil {
			return nil, fmt.Errorf("sample needs a number")
		}
		return sampleOp{n}, nil
	case "sample-distinct":
		return compileSampleDistinct(rest)
	case "serialize":
		return compileSerialize(rest)
	case "union":
		return compileUnion(rest)
	case "join":
		return compileJoin(rest)
	case "lookup":
		return compileLookup(rest)
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
	return Result{Rows: []Record{{"count": float64(len(in.Rows))}}, Cols: []string{"count"}}, nil
}

// ---- project family ----

type projMode int

const (
	projPlain projMode = iota
	projAway
	projKeep
)

type projectOp struct {
	mode  projMode
	items []namedExpr
	names []string // for away/keep: plain column names/wildcards
}

func compileProject(rest string, mode projMode) (operator, error) {
	parts := splitTop(rest, ',')
	if len(parts) == 0 {
		return nil, fmt.Errorf("project needs at least one column")
	}
	op := projectOp{mode: mode}
	if mode == projPlain {
		for _, f := range parts {
			ne, err := parseNamedExpr(f)
			if err != nil {
				return nil, err
			}
			op.items = append(op.items, ne)
		}
	} else {
		op.names = parts
	}
	return op, nil
}

func (o projectOp) apply(in Result) (Result, error) {
	if o.mode == projPlain {
		cols := make([]string, len(o.items))
		out := make([]Record, 0, len(in.Rows))
		for _, r := range in.Rows {
			nr := Record{}
			for i, it := range o.items {
				cols[i] = it.name
				nr[it.name] = it.expr.eval(r)
			}
			out = append(out, nr)
		}
		return Result{Rows: out, Cols: cols}, nil
	}
	// away / keep operate on existing columns
	allCols := columnsOf(in)
	keep := map[string]bool{}
	for _, c := range allCols {
		match := matchesAny(c, o.names)
		if (o.mode == projKeep && match) || (o.mode == projAway && !match) {
			keep[c] = true
		}
	}
	var cols []string
	for _, c := range allCols {
		if keep[c] {
			cols = append(cols, c)
		}
	}
	out := make([]Record, 0, len(in.Rows))
	for _, r := range in.Rows {
		nr := Record{}
		for _, c := range cols {
			if v, ok := r[c]; ok {
				nr[c] = v
			}
		}
		out = append(out, nr)
	}
	return Result{Rows: out, Cols: cols}, nil
}

func matchesAny(col string, pats []string) bool {
	for _, p := range pats {
		if p == col {
			return true
		}
		if strings.HasSuffix(p, "*") && strings.HasPrefix(col, strings.TrimSuffix(p, "*")) {
			return true
		}
	}
	return false
}

// ---- project-rename ----

type projectRenameOp struct{ pairs [][2]string } // [new, old]

func compileProjectRename(rest string) (operator, error) {
	var op projectRenameOp
	for _, p := range splitTop(rest, ',') {
		name, old, ok := splitAssign(p)
		if !ok {
			return nil, fmt.Errorf("project-rename needs new=old")
		}
		op.pairs = append(op.pairs, [2]string{name, old})
	}
	return op, nil
}

func (o projectRenameOp) apply(in Result) (Result, error) {
	cols := append([]string{}, columnsOf(in)...)
	rename := map[string]string{}
	for _, pr := range o.pairs {
		rename[pr[1]] = pr[0]
	}
	for i, c := range cols {
		if nn, ok := rename[c]; ok {
			cols[i] = nn
		}
	}
	out := make([]Record, 0, len(in.Rows))
	for _, r := range in.Rows {
		nr := Record{}
		for k, v := range r {
			if nn, ok := rename[k]; ok {
				nr[nn] = v
			} else {
				nr[k] = v
			}
		}
		out = append(out, nr)
	}
	return Result{Rows: out, Cols: cols}, nil
}

// ---- project-reorder ----

type projectReorderOp struct{ order []string }

func compileProjectReorder(rest string) (operator, error) {
	return projectReorderOp{order: splitTop(rest, ',')}, nil
}

func (o projectReorderOp) apply(in Result) (Result, error) {
	all := columnsOf(in)
	used := map[string]bool{}
	var cols []string
	for _, p := range o.order {
		for _, c := range all {
			if !used[c] && matchesAny(c, []string{p}) {
				cols = append(cols, c)
				used[c] = true
			}
		}
	}
	for _, c := range all {
		if !used[c] {
			cols = append(cols, c)
		}
	}
	return Result{Rows: in.Rows, Cols: cols}, nil
}

// ---- extend ----

type extendOp struct{ items []namedExpr }

func compileExtend(rest string) (operator, error) {
	var op extendOp
	for _, f := range splitTop(rest, ',') {
		ne, err := parseNamedExpr(f)
		if err != nil {
			return nil, err
		}
		if _, _, ok := splitAssign(f); !ok {
			return nil, fmt.Errorf("extend needs name=expr")
		}
		op.items = append(op.items, ne)
	}
	return op, nil
}

func (o extendOp) apply(in Result) (Result, error) {
	base := columnsOf(in)
	cols := append([]string{}, base...)
	for _, it := range o.items {
		if !contains(cols, it.name) {
			cols = append(cols, it.name)
		}
	}
	out := make([]Record, 0, len(in.Rows))
	for _, r := range in.Rows {
		nr := Record{}
		for k, v := range r {
			nr[k] = v
		}
		for _, it := range o.items {
			nr[it.name] = it.expr.eval(r)
		}
		out = append(out, nr)
	}
	return Result{Rows: out, Cols: cols}, nil
}

// ---- sort ----

type sortKey struct {
	e    Expr
	desc bool
}
type sortOp struct{ keys []sortKey }

func compileSort(rest string) (operator, error) {
	rest = strings.TrimSpace(rest)
	if lw, r := firstWord(rest); strings.EqualFold(lw, "by") {
		rest = r
	}
	keys, err := parseSortKeys(rest)
	if err != nil {
		return nil, err
	}
	return sortOp{keys}, nil
}

func parseSortKeys(rest string) ([]sortKey, error) {
	var keys []sortKey
	for _, k := range splitTop(rest, ',') {
		exprText := k
		desc := true // KQL default sort is descending
		lower := strings.ToLower(k)
		switch {
		case strings.HasSuffix(lower, " desc"):
			exprText = strings.TrimSpace(k[:len(k)-5])
		case strings.HasSuffix(lower, " asc"):
			exprText = strings.TrimSpace(k[:len(k)-4])
			desc = false
		}
		e, err := ParseExpr(exprText)
		if err != nil {
			return nil, err
		}
		keys = append(keys, sortKey{e: e, desc: desc})
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("sort needs a field")
	}
	return keys, nil
}

func (o sortOp) apply(in Result) (Result, error) {
	rows := in.Rows
	sort.SliceStable(rows, func(i, j int) bool {
		for _, k := range o.keys {
			c := compare(k.e.eval(rows[i]), k.e.eval(rows[j]))
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

// ---- top ----

func compileTop(rest string) (operator, error) {
	nStr, byPart := firstWord(rest)
	var n int
	if _, err := fmt.Sscanf(nStr, "%d", &n); err != nil {
		return nil, fmt.Errorf("top needs a number")
	}
	if lw, r := firstWord(byPart); strings.EqualFold(lw, "by") {
		byPart = r
	} else {
		return nil, fmt.Errorf("top needs 'by <expr>'")
	}
	keys, err := parseSortKeys(byPart)
	if err != nil {
		return nil, err
	}
	return pipelineOp{[]operator{sortOp{keys}, takeOp{n}}}, nil
}

// pipelineOp runs a sequence of operators as one stage.
type pipelineOp struct{ ops []operator }

func (o pipelineOp) apply(in Result) (Result, error) {
	res := in
	for _, op := range o.ops {
		var err error
		if res, err = op.apply(res); err != nil {
			return res, err
		}
	}
	return res, nil
}

// ---- distinct ----

type distinctOp struct {
	cols []string
	all  bool
}

func compileDistinct(rest string) (operator, error) {
	rest = strings.TrimSpace(rest)
	if rest == "*" || rest == "" {
		return distinctOp{all: true}, nil
	}
	return distinctOp{cols: splitTop(rest, ',')}, nil
}

func (o distinctOp) apply(in Result) (Result, error) {
	seen := map[string]bool{}
	var out []Record
	cols := o.cols
	if o.all {
		cols = columnsOf(in)
	}
	for _, r := range in.Rows {
		var kb strings.Builder
		nr := Record{}
		for _, c := range cols {
			v, _ := getField(r, c)
			nr[c] = v
			kb.WriteString(toString(v))
			kb.WriteByte('\x00')
		}
		key := kb.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, nr)
	}
	return Result{Rows: out, Cols: cols}, nil
}

// ---- getschema ----

type getschemaOp struct{}

func (getschemaOp) apply(in Result) (Result, error) {
	cols := columnsOf(in)
	typeFor := map[string]string{}
	for _, c := range cols {
		typeFor[c] = "unknown"
	}
	for _, r := range in.Rows {
		for _, c := range cols {
			if typeFor[c] != "unknown" {
				continue
			}
			if v, ok := r[c]; ok && v != nil {
				typeFor[c] = typeName(v)
			}
		}
	}
	var out []Record
	for i, c := range cols {
		out = append(out, Record{"ColumnName": c, "ColumnOrdinal": float64(i), "ColumnType": typeFor[c]})
	}
	return Result{Rows: out, Cols: []string{"ColumnName", "ColumnOrdinal", "ColumnType"}}, nil
}

// ---- print ----

type printOp struct{ items []namedExpr }

func compilePrint(rest string) (operator, error) {
	var op printOp
	for i, f := range splitTop(rest, ',') {
		ne, err := parseNamedExpr(f)
		if err != nil {
			return nil, err
		}
		if _, _, ok := splitAssign(f); !ok {
			ne.name = fmt.Sprintf("print_%d", i)
		}
		op.items = append(op.items, ne)
	}
	return op, nil
}

func (o printOp) apply(Result) (Result, error) {
	rec := Record{}
	cols := make([]string, len(o.items))
	for i, it := range o.items {
		rec[it.name] = it.expr.eval(Record{})
		cols[i] = it.name
	}
	return Result{Rows: []Record{rec}, Cols: cols}, nil
}

// ---- parse ----

type parseOp struct {
	src    Expr
	tokens []parseTok
	fields []string
}
type parseTok struct {
	lit  bool
	text string // literal text or capture name
	skip bool   // '*'
}

func compileParse(rest string) (operator, error) {
	rest = strings.TrimSpace(rest)
	// optional "kind=..." prefix
	if lw, r := firstWord(rest); strings.HasPrefix(strings.ToLower(lw), "kind=") {
		rest = r
	}
	idx := findKeyword(rest, "with")
	if idx < 0 {
		return nil, fmt.Errorf("parse needs 'with <pattern>'")
	}
	srcPart := strings.TrimSpace(rest[:idx])
	patPart := strings.TrimSpace(rest[idx+len("with"):])
	src, err := ParseExpr(srcPart)
	if err != nil {
		return nil, err
	}
	toks, err := lex(patPart)
	if err != nil {
		return nil, err
	}
	op := parseOp{src: src}
	for _, t := range toks {
		switch t.kind {
		case tString:
			op.tokens = append(op.tokens, parseTok{lit: true, text: t.val})
		case tIdent:
			op.tokens = append(op.tokens, parseTok{text: t.val})
			op.fields = append(op.fields, t.val)
		case tOp:
			if t.val == "*" {
				op.tokens = append(op.tokens, parseTok{skip: true})
			}
		case tEOF:
		default:
			return nil, fmt.Errorf("unexpected token %q in parse pattern", t.val)
		}
	}
	return op, nil
}

func (o parseOp) apply(in Result) (Result, error) {
	cols := append([]string{}, columnsOf(in)...)
	for _, f := range o.fields {
		if !contains(cols, f) {
			cols = append(cols, f)
		}
	}
	out := make([]Record, 0, len(in.Rows))
	for _, r := range in.Rows {
		nr := Record{}
		for k, v := range r {
			nr[k] = v
		}
		caps := parseWith(toString(o.src.eval(r)), o.tokens)
		for k, v := range caps {
			nr[k] = v
		}
		out = append(out, nr)
	}
	return Result{Rows: out, Cols: cols}, nil
}

func parseWith(source string, tokens []parseTok) map[string]string {
	res := map[string]string{}
	pos := 0
	for i := 0; i < len(tokens); i++ {
		tk := tokens[i]
		if tk.lit {
			idx := strings.Index(source[pos:], tk.text)
			if idx < 0 {
				return res
			}
			pos += idx + len(tk.text)
			continue
		}
		// capture (or skip)
		var capture string
		if i+1 < len(tokens) && tokens[i+1].lit {
			next := tokens[i+1].text
			idx := strings.Index(source[pos:], next)
			if idx < 0 {
				capture = source[pos:]
				pos = len(source)
			} else {
				capture = source[pos : pos+idx]
				pos += idx
			}
		} else {
			capture = source[pos:]
			pos = len(source)
		}
		if !tk.skip {
			res[tk.text] = capture
		}
	}
	return res
}

// ---- mv-expand ----

type mvExpandOp struct{ cols []string }

func compileMvExpand(rest string) (operator, error) {
	cols := splitTop(rest, ',')
	if len(cols) == 0 {
		return nil, fmt.Errorf("mv-expand needs a column")
	}
	// strip optional "to typeof(...)" clauses
	for i, c := range cols {
		if k := findKeyword(c, "to"); k >= 0 {
			cols[i] = strings.TrimSpace(c[:k])
		}
	}
	return mvExpandOp{cols: cols}, nil
}

func (o mvExpandOp) apply(in Result) (Result, error) {
	out := make([]Record, 0, len(in.Rows))
	for _, r := range in.Rows {
		maxLen := 1
		arrays := map[string][]any{}
		for _, c := range o.cols {
			if v, ok := getField(r, c); ok {
				if a, ok := v.([]any); ok {
					arrays[c] = a
					if len(a) > maxLen {
						maxLen = len(a)
					}
				}
			}
		}
		if len(arrays) == 0 {
			out = append(out, r)
			continue
		}
		for i := 0; i < maxLen; i++ {
			nr := Record{}
			for k, v := range r {
				nr[k] = v
			}
			for _, c := range o.cols {
				if a, ok := arrays[c]; ok {
					if i < len(a) {
						nr[c] = a[i]
					} else {
						nr[c] = nil
					}
				}
			}
			out = append(out, nr)
		}
	}
	return Result{Rows: out, Cols: in.Cols}, nil
}

// ---- sample ----

type sampleOp struct{ n int }

func (o sampleOp) apply(in Result) (Result, error) {
	rows := append([]Record{}, in.Rows...)
	rand.Shuffle(len(rows), func(i, j int) { rows[i], rows[j] = rows[j], rows[i] })
	if o.n < len(rows) {
		rows = rows[:o.n]
	}
	return Result{Rows: rows, Cols: in.Cols}, nil
}

type sampleDistinctOp struct {
	n   int
	col string
}

func compileSampleDistinct(rest string) (operator, error) {
	// syntax: sample-distinct N of Column
	nStr, r := firstWord(rest)
	var n int
	if _, err := fmt.Sscanf(nStr, "%d", &n); err != nil {
		return nil, fmt.Errorf("sample-distinct needs a number")
	}
	if lw, r2 := firstWord(r); strings.EqualFold(lw, "of") {
		r = r2
	} else {
		return nil, fmt.Errorf("sample-distinct needs 'of <column>'")
	}
	return sampleDistinctOp{n: n, col: strings.TrimSpace(r)}, nil
}

func (o sampleDistinctOp) apply(in Result) (Result, error) {
	seen := map[string]bool{}
	var out []Record
	for _, r := range in.Rows {
		v, _ := getField(r, o.col)
		k := toString(v)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, Record{o.col: v})
		if len(out) >= o.n {
			break
		}
	}
	return Result{Rows: out, Cols: []string{o.col}}, nil
}

// ---- serialize / row_number ----

type serializeOp struct {
	col   string
	start int
}

func compileSerialize(rest string) (operator, error) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return serializeOp{}, nil
	}
	name, expr, ok := splitAssign(rest)
	if !ok {
		return nil, fmt.Errorf("serialize expects col=row_number(...)")
	}
	start := 1
	el := strings.ToLower(strings.TrimSpace(expr))
	if strings.HasPrefix(el, "row_number(") {
		inside := strings.TrimSuffix(strings.TrimPrefix(el, "row_number("), ")")
		if inside != "" {
			fmt.Sscanf(inside, "%d", &start)
		}
	}
	return serializeOp{col: name, start: start}, nil
}

func (o serializeOp) apply(in Result) (Result, error) {
	if o.col == "" {
		return in, nil
	}
	cols := append([]string{}, columnsOf(in)...)
	if !contains(cols, o.col) {
		cols = append(cols, o.col)
	}
	out := make([]Record, 0, len(in.Rows))
	for i, r := range in.Rows {
		nr := Record{}
		for k, v := range r {
			nr[k] = v
		}
		nr[o.col] = float64(o.start + i)
		out = append(out, nr)
	}
	return Result{Rows: out, Cols: cols}, nil
}

// ---- helpers ----

func columnsOf(res Result) []string {
	if res.Cols != nil {
		return res.Cols
	}
	seen := map[string]bool{}
	var cols []string
	for _, r := range res.Rows {
		for k := range r {
			if !seen[k] {
				seen[k] = true
				cols = append(cols, k)
			}
		}
	}
	sort.Strings(cols)
	return cols
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// canonical JSON key for whole-record dedupe (unused publicly, kept for tests).
func canonical(r Record) string {
	b, _ := json.Marshal(r)
	return string(b)
}
