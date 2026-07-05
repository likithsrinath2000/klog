package main

import (
	"encoding/json"
	"testing"
)

var benchLine = []byte(`{"ts":"2026-07-02T09:00:00.428000Z","level":"WARN","service":"auth","route":"/search","status":201,"ms":38.1,"user":"user035","bytes":5817,"meta":{"region":"ap-south","host":"auth-01"},"tags":["canary","p1","cold"],"msg":"user=user006 ip=10.1.119.130 route=/health"}`)

func BenchmarkJSONLite(b *testing.B) {
	p := newJSONParser()
	b.SetBytes(int64(len(benchLine)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := p.parseObject(benchLine); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStdlibJSON(b *testing.B) {
	b.SetBytes(int64(len(benchLine)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var m map[string]any
		if err := json.Unmarshal(benchLine, &m); err != nil {
			b.Fatal(err)
		}
	}
}
