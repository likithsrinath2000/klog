package main

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/likithsrinath2000/klog/internal/engine"
)

// rowParser converts one input line into a record. The bool reports whether the
// record should be emitted (header rows and blank lines return false). The line
// slice is only valid for the duration of the call.
type rowParser func(line []byte, lineNo int) (engine.Record, bool)

// newParserFactory returns a factory that builds a fresh parser per input
// stream (important for stateful formats like CSV that consume a header row).
func newParserFactory(mode, pattern, delim string) (func() rowParser, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "", "json":
		return func() rowParser { return newJSONLineParser() }, nil
	case "auto":
		return func() rowParser { return newAutoParser() }, nil
	case "logfmt":
		return func() rowParser { return parseLogfmtLine }, nil
	case "raw", "text":
		return func() rowParser { return parseRawLine }, nil
	case "csv":
		return func() rowParser { return newDelimParser(',') }, nil
	case "tsv":
		return func() rowParser { return newDelimParser('\t') }, nil
	case "regex":
		if pattern == "" {
			return nil, fmt.Errorf("--input regex requires --pattern")
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("bad --pattern: %w", err)
		}
		if len(re.SubexpNames()) <= 1 {
			return nil, fmt.Errorf("--pattern must contain named groups, e.g. (?P<level>\\w+)")
		}
		return func() rowParser { return newRegexParser(re) }, nil
	}
	return nil, fmt.Errorf("unknown --input %q (json|auto|logfmt|csv|tsv|regex|raw)", mode)
}

func rawRecord(line []byte, n int) engine.Record {
	return engine.Record{"_line": float64(n), "_raw": string(line)}
}

func newJSONLineParser() rowParser {
	jp := newJSONParser()
	return func(line []byte, n int) (engine.Record, bool) {
		if m, err := jp.parseObject(line); err == nil {
			return engine.Record(m), true
		}
		return rawRecord(line, n), true
	}
}

func parseRawLine(line []byte, n int) (engine.Record, bool) {
	return rawRecord(line, n), true
}

var logfmtHint = regexp.MustCompile(`(^|\s)[A-Za-z0-9_.\-]+=`)

func newAutoParser() rowParser {
	jp := newJSONParser()
	return func(line []byte, n int) (engine.Record, bool) {
		t := bytes.TrimSpace(line)
		if len(t) > 1 && t[0] == '{' && t[len(t)-1] == '}' {
			if m, err := jp.parseObject(t); err == nil {
				return engine.Record(m), true
			}
		}
		if logfmtHint.Match(line) {
			return parseLogfmtLine(line, n)
		}
		return rawRecord(line, n), true
	}
}

func parseLogfmtLine(line []byte, n int) (engine.Record, bool) {
	s := string(line)
	rec := engine.Record{"_line": float64(n), "_raw": s}
	for k, v := range parseLogfmt(s) {
		rec[k] = coerceScalar(v)
	}
	return rec, true
}

// parseLogfmt scans `key=value` pairs, supporting quoted values and bare keys.
func parseLogfmt(s string) map[string]string {
	out := map[string]string{}
	i := 0
	rs := []rune(s)
	for i < len(rs) {
		for i < len(rs) && rs[i] == ' ' {
			i++
		}
		if i >= len(rs) {
			break
		}
		// key
		start := i
		for i < len(rs) && rs[i] != '=' && rs[i] != ' ' {
			i++
		}
		key := string(rs[start:i])
		if key == "" {
			i++
			continue
		}
		if i < len(rs) && rs[i] == '=' {
			i++ // consume '='
			var val string
			if i < len(rs) && (rs[i] == '"' || rs[i] == '\'') {
				quote := rs[i]
				i++
				var b strings.Builder
				for i < len(rs) && rs[i] != quote {
					if rs[i] == '\\' && i+1 < len(rs) {
						i++
					}
					b.WriteRune(rs[i])
					i++
				}
				i++ // closing quote
				val = b.String()
			} else {
				vs := i
				for i < len(rs) && rs[i] != ' ' {
					i++
				}
				val = string(rs[vs:i])
			}
			out[key] = val
		} else {
			out[key] = "true" // bare key
		}
	}
	return out
}

func newDelimParser(comma rune) rowParser {
	var header []string
	return func(line []byte, n int) (engine.Record, bool) {
		fields, err := splitDelim(string(line), comma)
		if err != nil {
			return rawRecord(line, n), true
		}
		if header == nil {
			header = fields
			return nil, false // consume header row
		}
		rec := engine.Record{"_line": float64(n)}
		for i, h := range header {
			if i < len(fields) {
				rec[h] = coerceScalar(fields[i])
			} else {
				rec[h] = nil
			}
		}
		return rec, true
	}
}

func splitDelim(line string, comma rune) ([]string, error) {
	r := csv.NewReader(strings.NewReader(line))
	r.Comma = comma
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	return r.Read()
}

func newRegexParser(re *regexp.Regexp) rowParser {
	names := re.SubexpNames()
	return func(line []byte, n int) (engine.Record, bool) {
		s := string(line)
		m := re.FindStringSubmatch(s)
		if m == nil {
			return engine.Record{"_line": float64(n), "_raw": s}, true
		}
		rec := engine.Record{"_line": float64(n), "_raw": s}
		for i, name := range names {
			if i == 0 || name == "" {
				continue
			}
			rec[name] = coerceScalar(m[i])
		}
		return rec, true
	}
}

var numRe = regexp.MustCompile(`^-?(0|[1-9]\d*)(\.\d+)?$`)

// coerceScalar upgrades obvious numbers/bools so downstream typing (getschema,
// JSON output) is clean; everything else stays a string.
func coerceScalar(s string) any {
	switch s {
	case "true":
		return true
	case "false":
		return false
	}
	if numRe.MatchString(s) {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	}
	return s
}
