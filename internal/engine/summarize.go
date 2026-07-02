package engine

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// aggregator is a compiled aggregation; newState builds a per-group accumulator.
type aggregator interface {
	outputCols() []string
	dynamic() bool // true if extra columns are produced at finalize (arg_max *)
	newState() aggAcc
}

type aggAcc interface {
	add(r Record)
	result(dst Record)
}

type summarizeOp struct {
	aggs []aggregator
	keys []namedExpr
}

func compileSummarize(rest string) (operator, error) {
	rest = strings.TrimSpace(rest)
	aggPart, keyPart := rest, ""
	if idx := findKeyword(rest, "by"); idx >= 0 {
		aggPart = strings.TrimSpace(rest[:idx])
		keyPart = strings.TrimSpace(rest[idx+len("by"):])
	}
	var op summarizeOp
	for _, a := range splitTop(aggPart, ',') {
		if a == "" {
			continue
		}
		ag, err := parseAgg(a)
		if err != nil {
			return nil, err
		}
		op.aggs = append(op.aggs, ag)
	}
	if len(op.aggs) == 0 {
		return nil, fmt.Errorf("summarize needs at least one aggregation")
	}
	if keyPart != "" {
		for _, k := range splitTop(keyPart, ',') {
			if k == "" {
				continue
			}
			ne, err := parseSummarizeKey(k)
			if err != nil {
				return nil, err
			}
			op.keys = append(op.keys, ne)
		}
	}
	return op, nil
}

func parseSummarizeKey(k string) (namedExpr, error) {
	name, text, ok := splitAssign(k)
	if !ok {
		text = strings.TrimSpace(k)
		name = keyDefaultName(text)
	}
	e, err := ParseExpr(text)
	if err != nil {
		return namedExpr{}, err
	}
	return namedExpr{name: name, text: text, expr: e}, nil
}

// keyDefaultName mimics KQL: `bin(ts, 1h)` -> "ts"; bare field -> field.
func keyDefaultName(text string) string {
	if i := strings.IndexByte(text, '('); i >= 0 {
		inner := text[i+1:]
		if j := strings.IndexAny(inner, ",)"); j >= 0 {
			inner = inner[:j]
		}
		inner = strings.TrimSpace(inner)
		if isSimpleIdent(inner) {
			return inner
		}
	}
	if isSimpleIdent(text) {
		return text
	}
	return sanitize(text)
}

func isSimpleIdent(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c == '_' || c == '.' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

func sanitize(s string) string {
	var b strings.Builder
	prevU := false
	for _, c := range s {
		if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
			prevU = false
		} else if !prevU {
			b.WriteByte('_')
			prevU = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func parseAgg(s string) (aggregator, error) {
	name, body, ok := splitAssign(s)
	if !ok {
		body = strings.TrimSpace(s)
	}
	open := strings.IndexByte(body, '(')
	if open < 0 || !strings.HasSuffix(body, ")") {
		return nil, fmt.Errorf("bad aggregation %q (expected fn(...))", body)
	}
	fn := strings.ToLower(strings.TrimSpace(body[:open]))
	inner := strings.TrimSpace(body[open+1 : len(body)-1])
	var argTexts []string
	if inner != "" {
		argTexts = splitTop(inner, ',')
	}

	// compile arg expressions (report-column idents handled specially per fn)
	argExpr := func(i int) (Expr, error) { return ParseExpr(argTexts[i]) }

	defName := func(def string) string {
		if name != "" {
			return name
		}
		return def
	}

	switch fn {
	case "count":
		return &countAgg{name: defName("count")}, nil
	case "countif":
		if len(argTexts) != 1 {
			return nil, fmt.Errorf("countif(predicate) needs 1 arg")
		}
		e, err := argExpr(0)
		if err != nil {
			return nil, err
		}
		return &countAgg{name: defName("countif"), cond: e}, nil
	case "sum", "avg", "mean", "min", "max", "stdev", "stdevp", "variance", "varp":
		if len(argTexts) < 1 {
			return nil, fmt.Errorf("%s(expr) needs an argument", fn)
		}
		e, err := argExpr(0)
		if err != nil {
			return nil, err
		}
		f := fn
		if f == "mean" {
			f = "avg"
		}
		return &numAgg{name: defName(f + "_" + sanitize(argTexts[0])), fn: f, expr: e}, nil
	case "sumif", "avgif":
		if len(argTexts) != 2 {
			return nil, fmt.Errorf("%s(expr, predicate) needs 2 args", fn)
		}
		e, err := argExpr(0)
		if err != nil {
			return nil, err
		}
		c, err := argExpr(1)
		if err != nil {
			return nil, err
		}
		return &numAgg{name: defName(strings.TrimSuffix(fn, "if") + "_" + sanitize(argTexts[0])), fn: strings.TrimSuffix(fn, "if"), expr: e, cond: c}, nil
	case "dcount", "count_distinct":
		e, err := argExpr(0)
		if err != nil {
			return nil, err
		}
		return &dcountAgg{name: defName("dcount_" + sanitize(argTexts[0])), expr: e}, nil
	case "make_list", "makelist", "make_set", "makeset":
		e, err := argExpr(0)
		if err != nil {
			return nil, err
		}
		set := strings.Contains(fn, "set")
		pfx := "list_"
		if set {
			pfx = "set_"
		}
		return &listAgg{name: defName(pfx + sanitize(argTexts[0])), expr: e, set: set}, nil
	case "any", "take_any", "arg_any":
		e, err := argExpr(0)
		if err != nil {
			return nil, err
		}
		return &anyAgg{name: defName("any_" + sanitize(argTexts[0])), expr: e}, nil
	case "percentile", "percentiles":
		if len(argTexts) < 2 {
			return nil, fmt.Errorf("percentile(expr, p) needs 2 args")
		}
		e, err := argExpr(0)
		if err != nil {
			return nil, err
		}
		var p float64
		fmt.Sscanf(strings.TrimSpace(argTexts[1]), "%g", &p)
		return &percentileAgg{name: defName(fmt.Sprintf("percentile_%s_%s", sanitize(argTexts[0]), sanitize(argTexts[1]))), expr: e, p: p}, nil
	case "arg_max", "arg_min":
		if len(argTexts) < 1 {
			return nil, fmt.Errorf("%s(expr, cols...) needs args", fn)
		}
		e, err := argExpr(0)
		if err != nil {
			return nil, err
		}
		ag := &argAgg{metricName: defName(sanitize(argTexts[0])), expr: e, isMax: fn == "arg_max"}
		for _, c := range argTexts[1:] {
			c = strings.TrimSpace(c)
			if c == "*" {
				ag.star = true
			} else {
				ag.cols = append(ag.cols, c)
			}
		}
		return ag, nil
	}
	return nil, fmt.Errorf("unknown aggregation %q", fn)
}

func (o summarizeOp) apply(in Result) (Result, error) {
	type group struct {
		keyVal []any
		accs   []aggAcc
	}
	order := []string{}
	groups := map[string]*group{}

	for _, r := range in.Rows {
		kv := make([]any, len(o.keys))
		var kb strings.Builder
		for i, k := range o.keys {
			v := k.expr.eval(r)
			kv[i] = v
			kb.WriteString(toString(v))
			kb.WriteByte('\x00')
		}
		key := kb.String()
		g, ok := groups[key]
		if !ok {
			g = &group{keyVal: kv, accs: make([]aggAcc, len(o.aggs))}
			for i, ag := range o.aggs {
				g.accs[i] = ag.newState()
			}
			groups[key] = g
			order = append(order, key)
		}
		for _, acc := range g.accs {
			acc.add(r)
		}
	}

	var cols []string
	for _, k := range o.keys {
		cols = append(cols, k.name)
	}
	dyn := false
	for _, ag := range o.aggs {
		cols = append(cols, ag.outputCols()...)
		if ag.dynamic() {
			dyn = true
		}
	}

	out := make([]Record, 0, len(order))
	for _, key := range order {
		g := groups[key]
		rec := Record{}
		for i, k := range o.keys {
			rec[k.name] = g.keyVal[i]
		}
		for _, acc := range g.accs {
			acc.result(rec)
		}
		out = append(out, rec)
	}
	if dyn {
		return Result{Rows: out, Cols: nil}, nil
	}
	return Result{Rows: out, Cols: cols}, nil
}

// ---- aggregator implementations ----

type countAgg struct {
	name string
	cond Expr
}

func (a *countAgg) outputCols() []string { return []string{a.name} }
func (a *countAgg) dynamic() bool        { return false }
func (a *countAgg) newState() aggAcc     { return &countAccT{a: a} }

type countAccT struct {
	a *countAgg
	n int
}

func (s *countAccT) add(r Record) {
	if s.a.cond == nil || truthy(s.a.cond.eval(r)) {
		s.n++
	}
}
func (s *countAccT) result(dst Record) { dst[s.a.name] = float64(s.n) }

type numAgg struct {
	name string
	fn   string
	expr Expr
	cond Expr
}

func (a *numAgg) outputCols() []string { return []string{a.name} }
func (a *numAgg) dynamic() bool        { return false }
func (a *numAgg) newState() aggAcc     { return &numAcc{a: a} }

type numAcc struct {
	a      *numAgg
	n      int
	sum    float64
	sumSq  float64
	min    float64
	max    float64
	hasNum bool
}

func (s *numAcc) add(r Record) {
	if s.a.cond != nil && !truthy(s.a.cond.eval(r)) {
		return
	}
	v, ok := toNumber(s.a.expr.eval(r))
	if !ok {
		return
	}
	if !s.hasNum {
		s.min, s.max = v, v
		s.hasNum = true
	} else {
		if v < s.min {
			s.min = v
		}
		if v > s.max {
			s.max = v
		}
	}
	s.n++
	s.sum += v
	s.sumSq += v * v
}

func (s *numAcc) result(dst Record) {
	switch s.a.fn {
	case "sum":
		dst[s.a.name] = s.sum
	case "avg":
		if s.n == 0 {
			dst[s.a.name] = nil
		} else {
			dst[s.a.name] = s.sum / float64(s.n)
		}
	case "min":
		if s.hasNum {
			dst[s.a.name] = s.min
		} else {
			dst[s.a.name] = nil
		}
	case "max":
		if s.hasNum {
			dst[s.a.name] = s.max
		} else {
			dst[s.a.name] = nil
		}
	case "stdev", "variance", "stdevp", "varp":
		dst[s.a.name] = s.dispersion()
	}
}

func (s *numAcc) dispersion() any {
	if s.n == 0 {
		return nil
	}
	mean := s.sum / float64(s.n)
	population := s.a.fn == "stdevp" || s.a.fn == "varp"
	denom := float64(s.n)
	if !population {
		if s.n < 2 {
			return nil
		}
		denom = float64(s.n - 1)
	}
	variance := (s.sumSq - float64(s.n)*mean*mean) / denom
	if variance < 0 {
		variance = 0
	}
	if s.a.fn == "variance" || s.a.fn == "varp" {
		return variance
	}
	return math.Sqrt(variance)
}

type dcountAgg struct {
	name string
	expr Expr
}

func (a *dcountAgg) outputCols() []string { return []string{a.name} }
func (a *dcountAgg) dynamic() bool        { return false }
func (a *dcountAgg) newState() aggAcc     { return &dcountAcc{a: a, seen: map[string]bool{}} }

type dcountAcc struct {
	a    *dcountAgg
	seen map[string]bool
}

func (s *dcountAcc) add(r Record) {
	v := s.a.expr.eval(r)
	if v != nil {
		s.seen[toString(v)] = true
	}
}
func (s *dcountAcc) result(dst Record) { dst[s.a.name] = float64(len(s.seen)) }

type listAgg struct {
	name string
	expr Expr
	set  bool
}

func (a *listAgg) outputCols() []string { return []string{a.name} }
func (a *listAgg) dynamic() bool        { return false }
func (a *listAgg) newState() aggAcc     { return &listAcc{a: a, seen: map[string]bool{}} }

type listAcc struct {
	a    *listAgg
	vals []any
	seen map[string]bool
}

func (s *listAcc) add(r Record) {
	v := s.a.expr.eval(r)
	if v == nil {
		return
	}
	if s.a.set {
		k := toString(v)
		if s.seen[k] {
			return
		}
		s.seen[k] = true
	}
	s.vals = append(s.vals, v)
}
func (s *listAcc) result(dst Record) {
	if s.vals == nil {
		s.vals = []any{}
	}
	dst[s.a.name] = s.vals
}

type anyAgg struct {
	name string
	expr Expr
}

func (a *anyAgg) outputCols() []string { return []string{a.name} }
func (a *anyAgg) dynamic() bool        { return false }
func (a *anyAgg) newState() aggAcc     { return &anyAcc{a: a} }

type anyAcc struct {
	a   *anyAgg
	got bool
	val any
}

func (s *anyAcc) add(r Record) {
	if s.got {
		return
	}
	v := s.a.expr.eval(r)
	if v != nil {
		s.val = v
		s.got = true
	}
}
func (s *anyAcc) result(dst Record) { dst[s.a.name] = s.val }

type percentileAgg struct {
	name string
	expr Expr
	p    float64
}

func (a *percentileAgg) outputCols() []string { return []string{a.name} }
func (a *percentileAgg) dynamic() bool        { return false }
func (a *percentileAgg) newState() aggAcc     { return &percentileAcc{a: a} }

type percentileAcc struct {
	a    *percentileAgg
	vals []float64
}

func (s *percentileAcc) add(r Record) {
	if v, ok := toNumber(s.a.expr.eval(r)); ok {
		s.vals = append(s.vals, v)
	}
}
func (s *percentileAcc) result(dst Record) {
	if len(s.vals) == 0 {
		dst[s.a.name] = nil
		return
	}
	sort.Float64s(s.vals)
	rank := s.a.p / 100 * float64(len(s.vals)-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	if lo == hi {
		dst[s.a.name] = s.vals[lo]
		return
	}
	frac := rank - float64(lo)
	dst[s.a.name] = s.vals[lo]*(1-frac) + s.vals[hi]*frac
}

type argAgg struct {
	metricName string
	expr       Expr
	cols       []string
	star       bool
	isMax      bool
}

func (a *argAgg) outputCols() []string {
	cols := []string{a.metricName}
	cols = append(cols, a.cols...)
	return cols
}
func (a *argAgg) dynamic() bool    { return a.star }
func (a *argAgg) newState() aggAcc { return &argAcc{a: a} }

type argAcc struct {
	a      *argAgg
	best   any
	winner Record
	set    bool
}

func (s *argAcc) add(r Record) {
	v := s.a.expr.eval(r)
	if v == nil {
		return
	}
	if !s.set {
		s.best, s.winner, s.set = v, r, true
		return
	}
	c := compare(v, s.best)
	if (s.a.isMax && c > 0) || (!s.a.isMax && c < 0) {
		s.best, s.winner = v, r
	}
}
func (s *argAcc) result(dst Record) {
	dst[s.a.metricName] = s.best
	if s.a.star {
		for k, v := range s.winner {
			if k != s.a.metricName {
				dst[k] = v
			}
		}
	}
	for _, c := range s.a.cols {
		v, _ := getField(s.winner, c)
		dst[c] = v
	}
}
