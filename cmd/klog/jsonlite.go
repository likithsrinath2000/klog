package main

import (
	"fmt"
	"math"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
)

// A small single-pass JSON parser that decodes one value into Go natives
// (map[string]any, []any, string, float64, bool, nil). It avoids the
// reflection path of encoding/json and interns object keys, which are highly
// repetitive in logs, cutting both CPU and allocations at scale.

type jsonParser struct {
	data []byte
	pos  int
	keys map[string]string // key intern cache
}

func newJSONParser() *jsonParser {
	return &jsonParser{keys: make(map[string]string, 64)}
}

// parseObject parses a JSON object from b into a Record, or reports an error.
func (p *jsonParser) parseObject(b []byte) (map[string]any, error) {
	p.data = b
	p.pos = 0
	p.skipWS()
	if p.pos >= len(p.data) || p.data[p.pos] != '{' {
		return nil, fmt.Errorf("not a JSON object")
	}
	v, err := p.parseValue()
	if err != nil {
		return nil, err
	}
	p.skipWS()
	if p.pos != len(p.data) {
		return nil, fmt.Errorf("trailing data after JSON value")
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("not a JSON object")
	}
	return m, nil
}

func (p *jsonParser) skipWS() {
	for p.pos < len(p.data) {
		switch p.data[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func (p *jsonParser) parseValue() (any, error) {
	p.skipWS()
	if p.pos >= len(p.data) {
		return nil, fmt.Errorf("unexpected end of JSON")
	}
	switch c := p.data[p.pos]; {
	case c == '{':
		return p.parseObjectValue()
	case c == '[':
		return p.parseArray()
	case c == '"':
		return p.parseString()
	case c == 't':
		return p.parseLit("true", true)
	case c == 'f':
		return p.parseLit("false", false)
	case c == 'n':
		return p.parseLit("null", nil)
	case c == '-' || (c >= '0' && c <= '9'):
		return p.parseNumber()
	}
	return nil, fmt.Errorf("unexpected character %q in JSON", p.data[p.pos])
}

func (p *jsonParser) parseLit(lit string, val any) (any, error) {
	if p.pos+len(lit) > len(p.data) || string(p.data[p.pos:p.pos+len(lit)]) != lit {
		return nil, fmt.Errorf("invalid JSON literal")
	}
	p.pos += len(lit)
	return val, nil
}

func (p *jsonParser) parseObjectValue() (any, error) {
	p.pos++ // {
	m := make(map[string]any)
	p.skipWS()
	if p.pos < len(p.data) && p.data[p.pos] == '}' {
		p.pos++
		return m, nil
	}
	for {
		p.skipWS()
		if p.pos >= len(p.data) || p.data[p.pos] != '"' {
			return nil, fmt.Errorf("expected object key")
		}
		key, err := p.parseString()
		if err != nil {
			return nil, err
		}
		ik := p.intern(key)
		p.skipWS()
		if p.pos >= len(p.data) || p.data[p.pos] != ':' {
			return nil, fmt.Errorf("expected ':' in object")
		}
		p.pos++
		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		m[ik] = val
		p.skipWS()
		if p.pos >= len(p.data) {
			return nil, fmt.Errorf("unterminated object")
		}
		switch p.data[p.pos] {
		case ',':
			p.pos++
		case '}':
			p.pos++
			return m, nil
		default:
			return nil, fmt.Errorf("expected ',' or '}' in object")
		}
	}
}

func (p *jsonParser) parseArray() (any, error) {
	p.pos++ // [
	arr := []any{}
	p.skipWS()
	if p.pos < len(p.data) && p.data[p.pos] == ']' {
		p.pos++
		return arr, nil
	}
	for {
		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		arr = append(arr, val)
		p.skipWS()
		if p.pos >= len(p.data) {
			return nil, fmt.Errorf("unterminated array")
		}
		switch p.data[p.pos] {
		case ',':
			p.pos++
		case ']':
			p.pos++
			return arr, nil
		default:
			return nil, fmt.Errorf("expected ',' or ']' in array")
		}
	}
}

func (p *jsonParser) parseString() (string, error) {
	p.pos++ // opening quote
	start := p.pos
	// fast path: no escapes
	for p.pos < len(p.data) {
		c := p.data[p.pos]
		if c == '"' {
			s := string(p.data[start:p.pos])
			p.pos++
			return s, nil
		}
		if c == '\\' {
			return p.parseStringSlow(start)
		}
		p.pos++
	}
	return "", fmt.Errorf("unterminated string")
}

func (p *jsonParser) parseStringSlow(start int) (string, error) {
	buf := make([]byte, 0, p.pos-start+16)
	buf = append(buf, p.data[start:p.pos]...)
	for p.pos < len(p.data) {
		c := p.data[p.pos]
		switch c {
		case '"':
			p.pos++
			return string(buf), nil
		case '\\':
			p.pos++
			if p.pos >= len(p.data) {
				return "", fmt.Errorf("bad escape")
			}
			switch e := p.data[p.pos]; e {
			case '"', '\\', '/':
				buf = append(buf, e)
			case 'n':
				buf = append(buf, '\n')
			case 't':
				buf = append(buf, '\t')
			case 'r':
				buf = append(buf, '\r')
			case 'b':
				buf = append(buf, '\b')
			case 'f':
				buf = append(buf, '\f')
			case 'u':
				r, err := p.parseUnicode()
				if err != nil {
					return "", err
				}
				var tmp [4]byte
				n := utf8.EncodeRune(tmp[:], r)
				buf = append(buf, tmp[:n]...)
				p.pos++ // move past the final hex digit
				continue
			default:
				return "", fmt.Errorf("bad escape \\%c", e)
			}
			p.pos++
		default:
			buf = append(buf, c)
			p.pos++
		}
	}
	return "", fmt.Errorf("unterminated string")
}

// parseUnicode reads \uXXXX (already positioned at 'u') and any surrogate pair.
func (p *jsonParser) parseUnicode() (rune, error) {
	r1, err := p.hex4()
	if err != nil {
		return 0, err
	}
	if utf16.IsSurrogate(rune(r1)) {
		if p.pos+2 < len(p.data) && p.data[p.pos+1] == '\\' && p.data[p.pos+2] == 'u' {
			p.pos += 2
			r2, err := p.hex4()
			if err != nil {
				return 0, err
			}
			return utf16.DecodeRune(rune(r1), rune(r2)), nil
		}
		return utf8.RuneError, nil
	}
	return rune(r1), nil
}

func (p *jsonParser) hex4() (uint32, error) {
	// positioned at 'u'
	if p.pos+4 >= len(p.data) {
		return 0, fmt.Errorf("bad unicode escape")
	}
	v, err := strconv.ParseUint(string(p.data[p.pos+1:p.pos+5]), 16, 32)
	if err != nil {
		return 0, fmt.Errorf("bad unicode escape")
	}
	p.pos += 4
	return uint32(v), nil
}

func (p *jsonParser) parseNumber() (any, error) {
	start := p.pos
	if p.data[p.pos] == '-' {
		p.pos++
	}
	for p.pos < len(p.data) {
		c := p.data[p.pos]
		if (c >= '0' && c <= '9') || c == '.' || c == 'e' || c == 'E' || c == '+' || c == '-' {
			p.pos++
			continue
		}
		break
	}
	f, err := strconv.ParseFloat(string(p.data[start:p.pos]), 64)
	if err != nil || math.IsInf(f, 0) {
		return nil, fmt.Errorf("bad number")
	}
	return f, nil
}

func (p *jsonParser) intern(k string) string {
	if v, ok := p.keys[k]; ok {
		return v
	}
	p.keys[k] = k
	return k
}
