package engine

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// ---- Expression lexer ----

type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tNumber
	tString
	tOp
	tLParen
	tRParen
	tComma
)

type token struct {
	kind tokKind
	val  string
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
		case c == ',':
			toks = append(toks, token{tComma, ","})
			i++
		case c == '"' || c == '\'':
			quote := c
			i++
			var sb strings.Builder
			for i < len(rs) && rs[i] != quote {
				if rs[i] == '\\' && i+1 < len(rs) {
					i++
				}
				sb.WriteRune(rs[i])
				i++
			}
			if i >= len(rs) {
				return nil, fmt.Errorf("unterminated string")
			}
			i++ // closing quote
			toks = append(toks, token{tString, sb.String()})
		case unicode.IsDigit(c) || (c == '-' && i+1 < len(rs) && unicode.IsDigit(rs[i+1]) && lastIsValuePos(toks)):
			start := i
			i++
			for i < len(rs) && (unicode.IsDigit(rs[i]) || rs[i] == '.') {
				i++
			}
			toks = append(toks, token{tNumber, string(rs[start:i])})
		case isIdentStart(c):
			start := i
			i++
			for i < len(rs) && isIdentPart(rs[i]) {
				i++
			}
			word := string(rs[start:i])
			// keyword operators
			switch strings.ToLower(word) {
			case "and", "or", "not", "contains", "startswith", "endswith", "in":
				toks = append(toks, token{tOp, strings.ToLower(word)})
			case "true", "false", "null":
				toks = append(toks, token{tIdent, strings.ToLower(word)})
			default:
				toks = append(toks, token{tIdent, word})
			}
		default:
			// multi-char operators
			two := ""
			if i+1 < len(rs) {
				two = string(rs[i : i+2])
			}
			switch two {
			case "==", "!=", ">=", "<=", "&&", "||":
				toks = append(toks, token{tOp, two})
				i += 2
				continue
			}
			switch c {
			case '>', '<', '!', '=':
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

// lastIsValuePos reports whether a preceding token means '-' is a binary minus
// rather than a negative-number sign. We only support unary minus in numbers
// when at expression start or after an operator/paren/comma.
func lastIsValuePos(toks []token) bool {
	if len(toks) == 0 {
		return true
	}
	switch toks[len(toks)-1].kind {
	case tOp, tLParen, tComma:
		return true
	}
	return false
}

func isIdentStart(c rune) bool {
	return unicode.IsLetter(c) || c == '_'
}
func isIdentPart(c rune) bool {
	return unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_' || c == '.'
}

// ---- Expression parser (recursive descent, precedence climbing) ----

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

type unaryExpr struct {
	op string
	x  Expr
}

func (e unaryExpr) eval(r Record) any {
	if e.op == "not" || e.op == "!" {
		return !truthy(e.x.eval(r))
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
	case "contains":
		return strings.Contains(toString(l), toString(r))
	case "startswith":
		return strings.HasPrefix(toString(l), toString(r))
	case "endswith":
		return strings.HasSuffix(toString(l), toString(r))
	}
	return nil
}

type parser struct {
	toks []token
	pos  int
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) next() token { t := p.toks[p.pos]; p.pos++; return t }

// ParseExpr parses a boolean/comparison expression string.
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
	for p.peek().kind == tOp && (p.peek().val == "or" || p.peek().val == "||") {
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
	for p.peek().kind == tOp && (p.peek().val == "and" || p.peek().val == "&&") {
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
	if p.peek().kind == tOp && (p.peek().val == "not" || p.peek().val == "!") {
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
	"contains": true, "startswith": true, "endswith": true,
}

func (p *parser) parseCmp() (Expr, error) {
	l, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	if p.peek().kind == tOp && cmpOps[p.peek().val] {
		op := p.next().val
		r, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		return binExpr{op, l, r}, nil
	}
	return l, nil
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
		return fieldExpr{t.val}, nil
	}
	return nil, fmt.Errorf("unexpected token %q", t.val)
}
