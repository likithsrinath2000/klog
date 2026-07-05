package main

import (
	"regexp"
	"testing"
)

func TestParseLogfmt(t *testing.T) {
	m := parseLogfmt(`ts=2026-07-03T04:00:02Z level=error msg="charge failed" ms=812 ok`)
	if m["level"] != "error" || m["ms"] != "812" || m["msg"] != "charge failed" {
		t.Fatalf("logfmt parse: %v", m)
	}
	if m["ok"] != "true" {
		t.Fatalf("bare key should be true, got %q", m["ok"])
	}
}

func TestCoerceScalar(t *testing.T) {
	if v := coerceScalar("812"); v != float64(812) {
		t.Fatalf("812 -> %#v", v)
	}
	if v := coerceScalar("3.5"); v != 3.5 {
		t.Fatalf("3.5 -> %#v", v)
	}
	if v := coerceScalar("true"); v != true {
		t.Fatalf("true -> %#v", v)
	}
	// leading zeros / IPs / versions stay strings
	for _, s := range []string{"007", "10.0.0.1", "v1.2", "2026-07-03"} {
		if v := coerceScalar(s); v != s {
			t.Fatalf("%q should stay string, got %#v", s, v)
		}
	}
}

func TestLogfmtParserLine(t *testing.T) {
	rec, ok := parseLogfmtLine([]byte(`level=error service=payments ms=812`), 5)
	if !ok {
		t.Fatal("should emit")
	}
	if rec["service"] != "payments" || rec["ms"] != float64(812) {
		t.Fatalf("record: %v", rec)
	}
	if rec["_line"] != float64(5) {
		t.Fatalf("_line: %v", rec["_line"])
	}
}

func TestDelimParserCSV(t *testing.T) {
	p := newDelimParser(',')
	if _, ok := p([]byte("ts,level,ms"), 1); ok {
		t.Fatal("header row should be consumed (ok=false)")
	}
	rec, ok := p([]byte("2026-07-03,ERROR,812"), 2)
	if !ok {
		t.Fatal("data row should emit")
	}
	if rec["level"] != "ERROR" || rec["ms"] != float64(812) {
		t.Fatalf("csv record: %v", rec)
	}
}

func TestRegexParser(t *testing.T) {
	re := regexp.MustCompile(`(?P<ip>\S+) "(?P<method>\S+) (?P<path>\S+)" (?P<status>\d+)`)
	p := newRegexParser(re)
	rec, ok := p([]byte(`10.0.0.2 "POST /checkout" 500`), 3)
	if !ok {
		t.Fatal("should emit")
	}
	if rec["ip"] != "10.0.0.2" || rec["method"] != "POST" || rec["status"] != float64(500) {
		t.Fatalf("regex record: %v", rec)
	}
	// non-matching line falls back to _raw
	rec2, _ := p([]byte("garbage line"), 4)
	if rec2["_raw"] != "garbage line" {
		t.Fatalf("non-match should keep _raw: %v", rec2)
	}
}

func TestAutoParser(t *testing.T) {
	p := newAutoParser()
	// JSON
	rec, _ := p([]byte(`{"level":"INFO","ms":40}`), 1)
	if rec["level"] != "INFO" {
		t.Fatalf("auto json: %v", rec)
	}
	// logfmt
	rec, _ = p([]byte(`level=error ms=812`), 2)
	if rec["level"] != "error" || rec["ms"] != float64(812) {
		t.Fatalf("auto logfmt: %v", rec)
	}
	// plain text
	rec, _ = p([]byte(`==== restarted ====`), 3)
	if rec["_raw"] != "==== restarted ====" {
		t.Fatalf("auto raw: %v", rec)
	}
	if _, ok := rec["level"]; ok {
		t.Fatalf("plain text should have no level: %v", rec)
	}
}

func TestNewParserFactoryErrors(t *testing.T) {
	if _, err := newParserFactory("regex", "", ""); err == nil {
		t.Fatal("regex without pattern should error")
	}
	if _, err := newParserFactory("regex", `no groups here`, ""); err == nil {
		t.Fatal("regex without named groups should error")
	}
	if _, err := newParserFactory("bogus", "", ""); err == nil {
		t.Fatal("unknown mode should error")
	}
	if _, err := newParserFactory("csv", "", ""); err != nil {
		t.Fatalf("csv should be valid: %v", err)
	}
}
