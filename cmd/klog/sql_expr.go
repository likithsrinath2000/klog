package main

import (
	"fmt"
	"regexp"
	"strings"
)

// ---- expression AST ----

type sx interface{ kql() string }

type sNum struct{ v string }

func (n sNum) kql() string { return n.v }

type sStr struct{ v string }

func (n sStr) kql() string { return kqlStr(n.v) }

type sIdent struct{ name string }

func (n sIdent) kql() string { return n.name }

type sLitKw struct{ v string } // true/false/null/*

func (n sLitKw) kql() string { return n.v }

type sBin struct {
	op   string
	l, r sx
}

func (n sBin) kql() string {
	op := n.op
	switch op {
	case "=":
		op = "=="
	case "<>":
		op = "!="
	case "AND":
		op = "and"
	case "OR":
		op = "or"
	}
	return "(" + n.l.kql() + " " + op + " " + n.r.kql() + ")"
}

type sUnary struct {
	op string
	x  sx
}

func (n sUnary) kql() string {
	if n.op == "NOT" {
		return "not (" + n.x.kql() + ")"
	}
	return "-" + n.x.kql()
}

type sCall struct {
	name     string
	args     []sx
	distinct bool
}

var sqlFuncMap = map[string]string{
	"UPPER": "toupper", "LOWER": "tolower", "LENGTH": "strlen", "LEN": "strlen",
	"SUBSTR": "substring", "SUBSTRING": "substring", "TRIM": "trim",
	"ABS": "abs", "ROUND": "round", "FLOOR": "floor", "CEIL": "ceiling",
	"CEILING": "ceiling", "SQRT": "sqrt", "COALESCE": "coalesce",
	"IFNULL": "coalesce", "NOW": "now", "CONCAT": "strcat", "REPLACE": "replace",
	"POW": "pow", "POWER": "pow",
}

var sqlAggMap = map[string]string{
	"COUNT": "count", "SUM": "sum", "AVG": "avg", "MIN": "min", "MAX": "max",
}

func (n sCall) kql() string {
	up := strings.ToUpper(n.name)
	if agg, ok := sqlAggMap[up]; ok {
		if up == "COUNT" {
			if n.distinct && len(n.args) == 1 {
				return "dcount(" + n.args[0].kql() + ")"
			}
			return "count()" // COUNT(*) or COUNT(col)
		}
		return agg + "(" + argKQL(n.args) + ")"
	}
	name := n.name
	if m, ok := sqlFuncMap[up]; ok {
		name = m
	}
	return strings.ToLower(name) + "(" + argKQL(n.args) + ")"
}

func argKQL(args []sx) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = a.kql()
	}
	return strings.Join(parts, ", ")
}

type sLike struct {
	x   sx
	pat string
	neg bool
}

func (n sLike) kql() string {
	e := likeToKQL(n.x.kql(), n.pat)
	if n.neg {
		return "not (" + e + ")"
	}
	return e
}

type sBetween struct {
	x, lo, hi sx
	neg       bool
}

func (n sBetween) kql() string {
	e := "(" + n.x.kql() + " between (" + n.lo.kql() + " .. " + n.hi.kql() + "))"
	if n.neg {
		return "not " + e
	}
	return e
}

type sIn struct {
	x    sx
	list []sx
	neg  bool
}

func (n sIn) kql() string {
	op := "in"
	if n.neg {
		op = "!in"
	}
	return n.x.kql() + " " + op + " (" + argKQL(n.list) + ")"
}

type sIsNull struct {
	x   sx
	neg bool
}

func (n sIsNull) kql() string {
	if n.neg {
		return "isnotnull(" + n.x.kql() + ")"
	}
	return "isnull(" + n.x.kql() + ")"
}

type sCast struct {
	x   sx
	typ string
}

func (n sCast) kql() string {
	fn := map[string]string{
		"INT": "toint", "INTEGER": "toint", "BIGINT": "tolong", "LONG": "tolong",
		"REAL": "todouble", "DOUBLE": "todouble", "FLOAT": "todouble",
		"TEXT": "tostring", "VARCHAR": "tostring", "STRING": "tostring",
		"BOOL": "tobool", "BOOLEAN": "tobool", "DATETIME": "todatetime",
		"TIMESTAMP": "todatetime",
	}[strings.ToUpper(n.typ)]
	if fn == "" {
		fn = "tostring"
	}
	return fn + "(" + n.x.kql() + ")"
}

// aggRef describes an aggregate found in an expression.
type aggRef struct {
	node sx     // the sCall node
	kqlS string // klog aggregate expression, e.g. "count()" or "sum(ms)"
	sig  string // canonical signature to dedupe
}

func aggOf(n sx) (aggRef, bool) {
	c, ok := n.(sCall)
	if !ok {
		return aggRef{}, false
	}
	if _, isAgg := sqlAggMap[strings.ToUpper(c.name)]; !isAgg {
		return aggRef{}, false
	}
	k := c.kql()
	return aggRef{node: n, kqlS: k, sig: k}, true
}

// containsAgg reports whether an expression tree contains an aggregate.
func containsAgg(n sx) bool {
	switch t := n.(type) {
	case sCall:
		if _, ok := sqlAggMap[strings.ToUpper(t.name)]; ok {
			return true
		}
		for _, a := range t.args {
			if containsAgg(a) {
				return true
			}
		}
	case sBin:
		return containsAgg(t.l) || containsAgg(t.r)
	case sUnary:
		return containsAgg(t.x)
	case sBetween:
		return containsAgg(t.x) || containsAgg(t.lo) || containsAgg(t.hi)
	case sIn:
		if containsAgg(t.x) {
			return true
		}
		for _, a := range t.list {
			if containsAgg(a) {
				return true
			}
		}
	case sIsNull:
		return containsAgg(t.x)
	case sLike:
		return containsAgg(t.x)
	case sCast:
		return containsAgg(t.x)
	}
	return false
}

// ---- helpers ----

func kqlStr(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, c := range s {
		switch c {
		case '"':
			b.WriteString("\\\"")
		case '\\':
			b.WriteString("\\\\")
		default:
			b.WriteRune(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

var likeMeta = regexp.MustCompile(`[.^$*+?()\[\]{}|\\]`)

// likeToKQL converts `left LIKE pattern` into a klog boolean expression.
func likeToKQL(left, pat string) string {
	lead := strings.HasPrefix(pat, "%")
	trail := strings.HasSuffix(pat, "%")
	core := strings.TrimSuffix(strings.TrimPrefix(pat, "%"), "%")
	// simple cases without inner wildcards
	if !strings.ContainsAny(core, "%_") {
		switch {
		case lead && trail:
			return left + " contains " + kqlStr(core)
		case trail:
			return left + " startswith " + kqlStr(core)
		case lead:
			return left + " endswith " + kqlStr(core)
		default:
			return "(" + left + " == " + kqlStr(pat) + ")"
		}
	}
	// general: build an anchored regex
	var b strings.Builder
	b.WriteByte('^')
	for _, c := range pat {
		switch c {
		case '%':
			b.WriteString(".*")
		case '_':
			b.WriteByte('.')
		default:
			s := string(c)
			if likeMeta.MatchString(s) {
				b.WriteByte('\\')
			}
			b.WriteString(s)
		}
	}
	b.WriteByte('$')
	return left + " matches regex " + kqlStr(b.String())
}

// ---- expression parser ----

type sqlParser struct {
	toks []sqlTok
	pos  int
}

func (p *sqlParser) peek() sqlTok { return p.toks[p.pos] }
func (p *sqlParser) next() sqlTok { t := p.toks[p.pos]; p.pos++; return t }
func (p *sqlParser) isKw(k string) bool {
	return p.peek().kind == sqlKw && p.peek().up == k
}
func (p *sqlParser) isOp(o string) bool {
	return p.peek().kind == sqlOp && p.peek().val == o
}

func (p *sqlParser) parseExpr() (sx, error) { return p.parseOr() }

func (p *sqlParser) parseOr() (sx, error) {
	l, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.isKw("OR") {
		p.next()
		r, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		l = sBin{"OR", l, r}
	}
	return l, nil
}

func (p *sqlParser) parseAnd() (sx, error) {
	l, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.isKw("AND") {
		p.next()
		r, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		l = sBin{"AND", l, r}
	}
	return l, nil
}

func (p *sqlParser) parseNot() (sx, error) {
	if p.isKw("NOT") {
		p.next()
		x, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return sUnary{"NOT", x}, nil
	}
	return p.parseCmp()
}

func (p *sqlParser) parseCmp() (sx, error) {
	l, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	// postfix predicates
	neg := false
	if p.isKw("NOT") && (p.toks[p.pos+1].up == "LIKE" || p.toks[p.pos+1].up == "IN" || p.toks[p.pos+1].up == "BETWEEN") {
		neg = true
		p.next()
	}
	switch {
	case p.isKw("LIKE"):
		p.next()
		pat := p.next()
		if pat.kind != sqlStr {
			return nil, fmt.Errorf("LIKE expects a string pattern")
		}
		return sLike{x: l, pat: pat.val, neg: neg}, nil
	case p.isKw("IN"):
		p.next()
		if p.next().kind != sqlLParen {
			return nil, fmt.Errorf("IN expects '('")
		}
		var list []sx
		for p.peek().kind != sqlRParen {
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			list = append(list, e)
			if p.peek().kind == sqlComma {
				p.next()
			}
		}
		p.next()
		return sIn{x: l, list: list, neg: neg}, nil
	case p.isKw("BETWEEN"):
		p.next()
		lo, err := p.parseAdd()
		if err != nil {
			return nil, err
		}
		if !p.isKw("AND") {
			return nil, fmt.Errorf("BETWEEN expects AND")
		}
		p.next()
		hi, err := p.parseAdd()
		if err != nil {
			return nil, err
		}
		return sBetween{x: l, lo: lo, hi: hi, neg: neg}, nil
	case p.isKw("IS"):
		p.next()
		isNeg := false
		if p.isKw("NOT") {
			isNeg = true
			p.next()
		}
		if !p.isKw("NULL") {
			return nil, fmt.Errorf("IS expects NULL")
		}
		p.next()
		return sIsNull{x: l, neg: isNeg}, nil
	}
	if p.peek().kind == sqlOp && isCmpOp(p.peek().val) {
		op := p.next().val
		r, err := p.parseAdd()
		if err != nil {
			return nil, err
		}
		return sBin{op, l, r}, nil
	}
	return l, nil
}

func isCmpOp(o string) bool {
	switch o {
	case "=", "<>", "!=", "<", ">", "<=", ">=":
		return true
	}
	return false
}

func (p *sqlParser) parseAdd() (sx, error) {
	l, err := p.parseMul()
	if err != nil {
		return nil, err
	}
	for p.isOp("+") || p.isOp("-") {
		op := p.next().val
		r, err := p.parseMul()
		if err != nil {
			return nil, err
		}
		l = sBin{op, l, r}
	}
	return l, nil
}

func (p *sqlParser) parseMul() (sx, error) {
	l, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.isOp("*") || p.isOp("/") || p.isOp("%") || p.peek().kind == sqlStar {
		var op string
		if p.peek().kind == sqlStar {
			op = "*"
			p.next()
		} else {
			op = p.next().val
		}
		r, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		l = sBin{op, l, r}
	}
	return l, nil
}

func (p *sqlParser) parseUnary() (sx, error) {
	if p.isOp("-") {
		p.next()
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return sUnary{"-", x}, nil
	}
	return p.parsePrimary()
}

func (p *sqlParser) parsePrimary() (sx, error) {
	t := p.peek()
	switch {
	case t.kind == sqlNum:
		p.next()
		return sNum{t.val}, nil
	case t.kind == sqlStr:
		p.next()
		return sStr{t.val}, nil
	case t.kind == sqlStar:
		p.next()
		return sLitKw{"*"}, nil
	case t.kind == sqlLParen:
		p.next()
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.next().kind != sqlRParen {
			return nil, fmt.Errorf("expected ')'")
		}
		return e, nil
	case t.kind == sqlKw && t.up == "CAST":
		return p.parseCast()
	case t.kind == sqlKw && (t.up == "TRUE" || t.up == "FALSE"):
		p.next()
		return sLitKw{strings.ToLower(t.up)}, nil
	case t.kind == sqlKw && t.up == "NULL":
		p.next()
		return sLitKw{"null"}, nil
	case t.kind == sqlIdent:
		p.next()
		if p.peek().kind == sqlLParen {
			return p.parseCall(t.val)
		}
		return sIdent{t.val}, nil
	}
	return nil, fmt.Errorf("unexpected token %q in SQL expression", t.val)
}

func (p *sqlParser) parseCall(name string) (sx, error) {
	p.next() // (
	call := sCall{name: name}
	if p.isKw("DISTINCT") {
		call.distinct = true
		p.next()
	}
	for p.peek().kind != sqlRParen {
		if p.peek().kind == sqlEOF {
			return nil, fmt.Errorf("unterminated function call %s(", name)
		}
		if p.peek().kind == sqlStar {
			p.next()
			call.args = append(call.args, sLitKw{"*"})
		} else {
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			call.args = append(call.args, e)
		}
		if p.peek().kind == sqlComma {
			p.next()
		}
	}
	p.next() // )
	return call, nil
}

func (p *sqlParser) parseCast() (sx, error) {
	p.next() // CAST
	if p.next().kind != sqlLParen {
		return nil, fmt.Errorf("CAST expects '('")
	}
	x, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if !p.isKw("AS") {
		return nil, fmt.Errorf("CAST expects AS")
	}
	p.next()
	typ := p.next()
	if typ.kind != sqlIdent && typ.kind != sqlKw {
		return nil, fmt.Errorf("CAST expects a type name")
	}
	if p.next().kind != sqlRParen {
		return nil, fmt.Errorf("CAST expects ')'")
	}
	return sCast{x: x, typ: typ.val}, nil
}
