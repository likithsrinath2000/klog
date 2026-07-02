package engine

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ---- lexer ----

type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tNumber
	tString
	tTimespan // value holds int64 nanoseconds as string
	tOp
	tLParen
	tRParen
	tLBracket
	tRBracket
	tComma
	tDotDot
)

type token struct {
	kind tokKind
	val  string
}

var tsUnits = map[string]time.Duration{
	"d":  24 * time.Hour,
	"h":  time.Hour,
	"m":  time.Minute,
	"s":  time.Second,
	"ms": time.Millisecond,
}

var keywordOps = map[string]bool{
	"and": true, "or": true, "not": true,
	"contains": true, "startswith": true, "endswith": true,
	"has": true, "in": true, "between": true, "matches": true,
}

func lex(s string) ([]token, error) {
	var toks []token
	rs := []rune(s)
	i := 0
	for i < len(rs) {
		c := rs[i]
		switch {
		case unicode.IsSpace(c):
			i++
		case c == '(':
			toks = append(toks, token{tLParen, "("})
			i++
		case c == ')':
			toks = append(toks, token{tRParen, ")"})
			i++
		case c == '[':
			toks = append(toks, token{tLBracket, "["})
			i++
		case c == ']':
			toks = append(toks, token{tRBracket, "]"})
			i++
		case c == ',':
			toks = append(toks, token{tComma, ","})
			i++
		case c == '.' && i+1 < len(rs) && rs[i+1] == '.':
			toks = append(toks, token{tDotDot, ".."})
			i += 2
		case c == '"' || c == '\'':
			quote := c
			i++
			var sb strings.Builder
			for i < len(rs) && rs[i] != quote {
				if rs[i] == '\\' && i+1 < len(rs) {
					i++
					sb.WriteRune(unescape(rs[i]))
				} else {
					sb.WriteRune(rs[i])
				}
				i++
			}
			if i >= len(rs) {
				return nil, fmt.Errorf("unterminated string")
			}
			i++
			toks = append(toks, token{tString, sb.String()})
		case unicode.IsDigit(c) || (c == '-' && i+1 < len(rs) && unicode.IsDigit(rs[i+1]) && valuePos(toks)):
			start := i
			i++
			for i < len(rs) && (unicode.IsDigit(rs[i]) || rs[i] == '.') {
				// stop before ".." range operator
				if rs[i] == '.' && i+1 < len(rs) && rs[i+1] == '.' {
					break
				}
				i++
			}
			num := string(rs[start:i])
			// timespan suffix?
			us := i
			for i < len(rs) && unicode.IsLetter(rs[i]) {
				i++
			}
			unit := string(rs[us:i])
			if unit != "" {
				dur, ok := tsUnits[unit]
				if !ok {
					return nil, fmt.Errorf("unknown timespan unit %q", unit)
				}
				f, err := strconv.ParseFloat(num, 64)
				if err != nil {
					return nil, err
				}
				ns := int64(f * float64(dur))
				toks = append(toks, token{tTimespan, strconv.FormatInt(ns, 10)})
			} else {
				toks = append(toks, token{tNumber, num})
			}
		case isIdentStart(c):
			start := i
			i++
			for i < len(rs) && isIdentPart(rs[i]) {
				i++
			}
			word := string(rs[start:i])
			lw := strings.ToLower(word)
			switch {
			case keywordOps[lw]:
				toks = append(toks, token{tOp, lw})
			case lw == "true" || lw == "false" || lw == "null" || lw == "regex":
				toks = append(toks, token{tIdent, lw})
			default:
				toks = append(toks, token{tIdent, word})
			}
		default:
			two := ""
			if i+1 < len(rs) {
				two = string(rs[i : i+2])
			}
			switch two {
			case "==", "!=", ">=", "<=", "&&", "||", "=~", "!~":
				toks = append(toks, token{tOp, two})
				i += 2
				continue
			}
			switch c {
			case '>', '<', '!', '=', '+', '-', '*', '/', '%':
				toks = append(toks, token{tOp, string(c)})
				i++
			default:
				return nil, fmt.Errorf("unexpected character %q", string(c))
			}
		}
	}
	toks = append(toks, token{tEOF, ""})
	return toks, nil
}

func unescape(c rune) rune {
	switch c {
	case 'n':
		return '\n'
	case 't':
		return '\t'
	case 'r':
		return '\r'
	}
	return c
}

func valuePos(toks []token) bool {
	if len(toks) == 0 {
		return true
	}
	switch toks[len(toks)-1].kind {
	case tOp, tLParen, tLBracket, tComma, tDotDot:
		return true
	}
	return false
}

func isIdentStart(c rune) bool { return unicode.IsLetter(c) || c == '_' }
func isIdentPart(c rune) bool {
	return unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_' || c == '.'
}

// ---- AST ----

// Expr is an evaluatable expression node.
type Expr interface {
	eval(r Record) any
}

type litExpr struct{ v any }

func (e litExpr) eval(Record) any { return e.v }

type fieldExpr struct{ name string }

func (e fieldExpr) eval(r Record) any {
	v, _ := getField(r, e.name)
	return v
}

type callExpr struct {
	name string
	args []Expr
}

func (e callExpr) eval(r Record) any {
	args := make([]any, len(e.args))
	for i, a := range e.args {
		args[i] = a.eval(r)
	}
	return callFunc(e.name, args)
}

type indexExpr struct {
	base Expr
	idx  Expr
}

func (e indexExpr) eval(r Record) any {
	base := e.base.eval(r)
	idx := e.idx.eval(r)
	switch b := base.(type) {
	case []any:
		n, ok := toNumber(idx)
		if !ok || int(n) < 0 || int(n) >= len(b) {
			return nil
		}
		return b[int(n)]
	case map[string]any:
		return b[toString(idx)]
	}
	return nil
}

type unaryExpr struct {
	op string
	x  Expr
}

func (e unaryExpr) eval(r Record) any {
	v := e.x.eval(r)
	switch e.op {
	case "not", "!":
		return !truthy(v)
	case "-":
		if n, ok := toNumber(v); ok {
			return -n
		}
		if ts, ok := v.(Timespan); ok {
			return Timespan(-int64(ts))
		}
	}
	return nil
}

type binExpr struct {
	op   string
	l, r Expr
}

func (e binExpr) eval(rec Record) any {
	switch e.op {
	case "and", "&&":
		return truthy(e.l.eval(rec)) && truthy(e.r.eval(rec))
	case "or", "||":
		return truthy(e.l.eval(rec)) || truthy(e.r.eval(rec))
	}
	l := e.l.eval(rec)
	r := e.r.eval(rec)
	switch e.op {
	case "+", "-", "*", "/", "%":
		return arith(e.op, l, r)
	case "==":
		return compare(l, r) == 0
	case "!=":
		return compare(l, r) != 0
	case ">":
		return compare(l, r) > 0
	case "<":
		return compare(l, r) < 0
	case ">=":
		return compare(l, r) >= 0
	case "<=":
		return compare(l, r) <= 0
	case "=~":
		return strings.EqualFold(toString(l), toString(r))
	case "!~":
		return !strings.EqualFold(toString(l), toString(r))
	case "contains":
		return strings.Contains(strings.ToLower(toString(l)), strings.ToLower(toString(r)))
	case "startswith":
		return strings.HasPrefix(strings.ToLower(toString(l)), strings.ToLower(toString(r)))
	case "endswith":
		return strings.HasSuffix(strings.ToLower(toString(l)), strings.ToLower(toString(r)))
	case "has":
		return hasTerm(toString(l), toString(r))
	case "matches":
		return regexMatch(toString(l), toString(r))
	}
	return nil
}

func hasTerm(s, term string) bool {
	s = strings.ToLower(s)
	term = strings.ToLower(term)
	for _, f := range strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if f == term {
			return true
		}
	}
	return false
}

type inExpr struct {
	left   Expr
	set    []Expr
	negate bool
}

func (e inExpr) eval(r Record) any {
	lv := e.left.eval(r)
	for _, s := range e.set {
		if compare(lv, s.eval(r)) == 0 {
			return !e.negate
		}
	}
	return e.negate
}

type betweenExpr struct {
	x, lo, hi Expr
}

func (e betweenExpr) eval(r Record) any {
	xv := e.x.eval(r)
	return compare(xv, e.lo.eval(r)) >= 0 && compare(xv, e.hi.eval(r)) <= 0
}

// ---- parser ----

type parser struct {
	toks []token
	pos  int
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) next() token { t := p.toks[p.pos]; p.pos++; return t }
func (p *parser) isOp(v string) bool {
	return p.peek().kind == tOp && p.peek().val == v
}

// ParseExpr parses a scalar/boolean expression string.
func ParseExpr(s string) (Expr, error) {
	toks, err := lex(s)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	e, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tEOF {
		return nil, fmt.Errorf("unexpected token %q", p.peek().val)
	}
	return e, nil
}

func (p *parser) parseOr() (Expr, error) {
	l, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.isOp("or") || p.isOp("||") {
		op := p.next().val
		r, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		l = binExpr{op, l, r}
	}
	return l, nil
}

func (p *parser) parseAnd() (Expr, error) {
	l, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.isOp("and") || p.isOp("&&") {
		op := p.next().val
		r, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		l = binExpr{op, l, r}
	}
	return l, nil
}

func (p *parser) parseNot() (Expr, error) {
	if p.isOp("not") || p.isOp("!") {
		op := p.next().val
		x, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return unaryExpr{op, x}, nil
	}
	return p.parseCmp()
}

var cmpOps = map[string]bool{
	"==": true, "!=": true, ">": true, "<": true, ">=": true, "<=": true,
	"=~": true, "!~": true, "contains": true, "startswith": true,
	"endswith": true, "has": true, "matches": true,
}

func (p *parser) parseCmp() (Expr, error) {
	l, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	// in / !in
	if p.isOp("in") {
		p.next()
		neg := false
		return p.parseInList(l, neg)
	}
	if p.isOp("!") && p.toks[p.pos+1].kind == tOp && p.toks[p.pos+1].val == "in" {
		p.next()
		p.next()
		return p.parseInList(l, true)
	}
	if p.isOp("between") {
		p.next()
		return p.parseBetween(l)
	}
	if p.peek().kind == tOp && cmpOps[p.peek().val] {
		op := p.next().val
		if op == "matches" && p.peek().kind == tIdent && p.peek().val == "regex" {
			p.next()
		}
		r, err := p.parseAdd()
		if err != nil {
			return nil, err
		}
		return binExpr{op, l, r}, nil
	}
	return l, nil
}

func (p *parser) parseInList(left Expr, neg bool) (Expr, error) {
	if p.next().kind != tLParen {
		return nil, fmt.Errorf("expected '(' after in")
	}
	var set []Expr
	for p.peek().kind != tRParen {
		e, err := p.parseAdd()
		if err != nil {
			return nil, err
		}
		set = append(set, e)
		if p.peek().kind == tComma {
			p.next()
		}
	}
	p.next() // )
	return inExpr{left: left, set: set, negate: neg}, nil
}

func (p *parser) parseBetween(x Expr) (Expr, error) {
	if p.next().kind != tLParen {
		return nil, fmt.Errorf("expected '(' after between")
	}
	lo, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	if p.next().kind != tDotDot {
		return nil, fmt.Errorf("expected '..' in between")
	}
	hi, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	if p.next().kind != tRParen {
		return nil, fmt.Errorf("expected ')' to close between")
	}
	return betweenExpr{x, lo, hi}, nil
}

func (p *parser) parseAdd() (Expr, error) {
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
		l = binExpr{op, l, r}
	}
	return l, nil
}

func (p *parser) parseMul() (Expr, error) {
	l, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.isOp("*") || p.isOp("/") || p.isOp("%") {
		op := p.next().val
		r, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		l = binExpr{op, l, r}
	}
	return l, nil
}

func (p *parser) parseUnary() (Expr, error) {
	if p.isOp("-") {
		p.next()
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return unaryExpr{"-", x}, nil
	}
	return p.parsePostfix()
}

func (p *parser) parsePostfix() (Expr, error) {
	e, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tLBracket {
		p.next()
		idx, err := p.parseAdd()
		if err != nil {
			return nil, err
		}
		if p.next().kind != tRBracket {
			return nil, fmt.Errorf("expected ']'")
		}
		e = indexExpr{base: e, idx: idx}
	}
	return e, nil
}

func (p *parser) parsePrimary() (Expr, error) {
	t := p.next()
	switch t.kind {
	case tNumber:
		f, err := strconv.ParseFloat(t.val, 64)
		if err != nil {
			return nil, err
		}
		return litExpr{f}, nil
	case tTimespan:
		ns, _ := strconv.ParseInt(t.val, 10, 64)
		return litExpr{Timespan(ns)}, nil
	case tString:
		return litExpr{t.val}, nil
	case tLParen:
		e, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.next().kind != tRParen {
			return nil, fmt.Errorf("expected ')'")
		}
		return e, nil
	case tIdent:
		switch t.val {
		case "true":
			return litExpr{true}, nil
		case "false":
			return litExpr{false}, nil
		case "null":
			return litExpr{nil}, nil
		}
		if p.peek().kind == tLParen {
			return p.parseCall(t.val)
		}
		return fieldExpr{t.val}, nil
	}
	return nil, fmt.Errorf("unexpected token %q", t.val)
}

func (p *parser) parseCall(name string) (Expr, error) {
	p.next() // (
	var args []Expr
	for p.peek().kind != tRParen {
		if p.peek().kind == tEOF {
			return nil, fmt.Errorf("unterminated call to %s(", name)
		}
		a, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		args = append(args, a)
		if p.peek().kind == tComma {
			p.next()
		}
	}
	p.next() // )
	return callExpr{name: strings.ToLower(name), args: args}, nil
}
