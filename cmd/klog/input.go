package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/likithsrinath2000/klog/internal/engine"
)

// rowParser converts one input line into a record. The bool reports whether the
// record should be emitted (header rows and blank lines return false).
type rowParser func(line string, lineNo int) (engine.Record, bool)

// newParserFactory returns a factory that builds a fresh parser per input
// stream (important for stateful formats like CSV that consume a header row).
func newParserFactory(mode, pattern, delim string) (func() rowParser, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "", "json":
		return func() rowParser { return parseJSONLine }, nil
	case "auto":
		return func() rowParser { return parseAutoLine }, nil
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

func rawRecord(line string, n int) engine.Record {
	return engine.Record{"_line": float64(n), "_raw": line}
}

func parseJSONLine(line string, n int) (engine.Record, bool) {
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err == nil {
		return engine.Record(m), true
	}
	return rawRecord(line, n), true
}

func parseRawLine(line string, n int) (engine.Record, bool) {
	return rawRecord(line, n), true
}

var logfmtHint = regexp.MustCompile(`(^|\s)[A-Za-z0-9_.\-]+=`)

func parseAutoLine(line string, n int) (engine.Record, bool) {
	t := strings.TrimSpace(line)
	if strings.HasPrefix(t, "{") && strings.HasSuffix(t, "}") {
		var m map[string]any
		if err := json.Unmarshal([]byte(t), &m); err == nil {
			return engine.Record(m), true
		}
	}
	if logfmtHint.MatchString(line) {
		return parseLogfmtLine(line, n)
	}
	return rawRecord(line, n), true
}

func parseLogfmtLine(line string, n int) (engine.Record, bool) {
	rec := engine.Record{"_line": float64(n), "_raw": line}
	for k, v := range parseLogfmt(line) {
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
	return func(line string, n int) (engine.Record, bool) {
		fields, err := splitDelim(line, comma)
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
	return func(line string, n int) (engine.Record, bool) {
		m := re.FindStringSubmatch(line)
		if m == nil {
			return rawRecord(line, n), true
		}
		rec := engine.Record{"_line": float64(n), "_raw": line}
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
