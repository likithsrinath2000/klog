package main

import (
	"fmt"
	"strings"
)

// This file implements a small SQL frontend that translates a common SQL
// subset into a klog (KQL-style) pipeline string, which is then executed by the
// existing engine. Supported:
//
//	SELECT [DISTINCT] <items> [FROM <src>] [<join> ...]
//	[WHERE <cond>] [GROUP BY <exprs>] [HAVING <cond>]
//	[ORDER BY <expr> [ASC|DESC], ...] [LIMIT <n>]
//
// Expressions support = <> != < > <= >=, AND/OR/NOT, IN, IS [NOT] NULL,
// BETWEEN .. AND .., LIKE, arithmetic, and a set of scalar/aggregate functions.
// Not supported: subqueries, CTEs, window functions, UNION.

// ---- tokenizer ----

type sqlTokKind int

const (
	sqlEOF sqlTokKind = iota
	sqlIdent
	sqlNum
	sqlStr
	sqlOp
	sqlLParen
	sqlRParen
	sqlComma
	sqlStar
	sqlKw
)

type sqlTok struct {
	kind sqlTokKind
	val  string
	up   string // uppercased value for keywords
}

var sqlKeywords = map[string]bool{
	"SELECT": true, "DISTINCT": true, "FROM": true, "WHERE": true,
	"GROUP": true, "BY": true, "HAVING": true, "ORDER": true, "LIMIT": true,
	"OFFSET": true, "AS": true, "JOIN": true, "INNER": true, "LEFT": true,
	"RIGHT": true, "FULL": true, "OUTER": true, "CROSS": true, "ON": true,
	"AND": true, "OR": true, "NOT": true, "IN": true, "IS": true, "NULL": true,
	"BETWEEN": true, "LIKE": true, "ASC": true, "DESC": true, "TRUE": true,
	"FALSE": true, "CAST": true,
}

func sqlLex(s string) ([]sqlTok, error) {
	var toks []sqlTok
	rs := []rune(s)
	i := 0
	for i < len(rs) {
		c := rs[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(':
			toks = append(toks, sqlTok{kind: sqlLParen, val: "("})
			i++
		case c == ')':
			toks = append(toks, sqlTok{kind: sqlRParen, val: ")"})
			i++
		case c == ',':
			toks = append(toks, sqlTok{kind: sqlComma, val: ","})
			i++
		case c == ';':
			i++
		case c == '*':
			// could be multiply or star; treat as star, parser disambiguates
			toks = append(toks, sqlTok{kind: sqlStar, val: "*"})
			i++
		case c == '\'':
			i++
			var b strings.Builder
			for i < len(rs) {
				if rs[i] == '\'' {
					if i+1 < len(rs) && rs[i+1] == '\'' { // '' escape
						b.WriteRune('\'')
						i += 2
						continue
					}
					break
				}
				b.WriteRune(rs[i])
				i++
			}
			if i >= len(rs) {
				return nil, fmt.Errorf("unterminated string literal")
			}
			i++
			toks = append(toks, sqlTok{kind: sqlStr, val: b.String()})
		case c == '"' || c == '`':
			// quoted identifier
			q := c
			i++
			start := i
			for i < len(rs) && rs[i] != q {
				i++
			}
			if i >= len(rs) {
				return nil, fmt.Errorf("unterminated quoted identifier")
			}
			name := string(rs[start:i])
			i++
			toks = append(toks, sqlTok{kind: sqlIdent, val: name})
		case c >= '0' && c <= '9' || (c == '.' && i+1 < len(rs) && rs[i+1] >= '0' && rs[i+1] <= '9'):
			start := i
			i++
			for i < len(rs) && (rs[i] >= '0' && rs[i] <= '9' || rs[i] == '.') {
				i++
			}
			toks = append(toks, sqlTok{kind: sqlNum, val: string(rs[start:i])})
		case isSQLIdentStart(c):
			start := i
			i++
			for i < len(rs) && isSQLIdentPart(rs[i]) {
				i++
			}
			word := string(rs[start:i])
			up := strings.ToUpper(word)
			if sqlKeywords[up] {
				toks = append(toks, sqlTok{kind: sqlKw, val: word, up: up})
			} else {
				toks = append(toks, sqlTok{kind: sqlIdent, val: word})
			}
		default:
			two := ""
			if i+1 < len(rs) {
				two = string(rs[i : i+2])
			}
			switch two {
			case "<>", "!=", "<=", ">=":
				toks = append(toks, sqlTok{kind: sqlOp, val: two})
				i += 2
				continue
			}
			switch c {
			case '=', '<', '>', '+', '-', '/', '%':
				toks = append(toks, sqlTok{kind: sqlOp, val: string(c)})
				i++
			default:
				return nil, fmt.Errorf("unexpected character %q in SQL", string(c))
			}
		}
	}
	toks = append(toks, sqlTok{kind: sqlEOF})
	return toks, nil
}

func isSQLIdentStart(c rune) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isSQLIdentPart(c rune) bool {
	return isSQLIdentStart(c) || (c >= '0' && c <= '9') || c == '.'
}
