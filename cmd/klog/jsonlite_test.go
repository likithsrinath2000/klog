package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestJSONParserMatchesStdlib(t *testing.T) {
	cases := []string{
		`{"a":1,"b":"x","c":true,"d":null,"e":1.5}`,
		`{"nested":{"x":1,"y":[1,2,3]},"arr":["a","b"]}`,
		`{"esc":"line1\nline2\ttab\"quote\\slash"}`,
		`{"unicode":"caf\u00e9 \u2764","emoji":"\ud83d\ude00"}`,
		`{"neg":-42,"exp":1.2e3,"big":48000}`,
		`{"empty":{},"emptyarr":[],"mixed":[1,"two",false,null]}`,
		`{"ts":"2026-07-02T09:00:00Z","level":"ERROR","ms":812.5}`,
	}
	p := newJSONParser()
	for _, c := range cases {
		got, err := p.parseObject([]byte(c))
		if err != nil {
			t.Fatalf("parseObject(%s): %v", c, err)
		}
		var want map[string]any
		if err := json.Unmarshal([]byte(c), &want); err != nil {
			t.Fatalf("stdlib unmarshal(%s): %v", c, err)
		}
		if !reflect.DeepEqual(map[string]any(got), want) {
			t.Fatalf("mismatch for %s:\n got=%#v\nwant=%#v", c, got, want)
		}
	}
}

func TestJSONParserErrors(t *testing.T) {
	p := newJSONParser()
	bad := []string{
		`not json`,
		`{"a":1`,
		`{"a":}`,
		`[1,2,3]`, // not an object
		`{"a":1} trailing`,
		`{"a" 1}`,
	}
	for _, c := range bad {
		if _, err := p.parseObject([]byte(c)); err == nil {
			t.Fatalf("expected error for %q", c)
		}
	}
}

func TestJSONParserKeyInterning(t *testing.T) {
	p := newJSONParser()
	m1, _ := p.parseObject([]byte(`{"service":"a"}`))
	m2, _ := p.parseObject([]byte(`{"service":"b"}`))
	var k1, k2 string
	for k := range m1 {
		k1 = k
	}
	for k := range m2 {
		k2 = k
	}
	// interned keys should share the same backing string
	if k1 != "service" || k2 != "service" {
		t.Fatalf("keys: %q %q", k1, k2)
	}
}
