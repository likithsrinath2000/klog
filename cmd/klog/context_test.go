package main

import (
	"testing"
	"time"

	"github.com/likithsrinath2000/klog/internal/engine"
)

func TestParseContextDuration(t *testing.T) {
	cases := map[string]time.Duration{
		"30s":   30 * time.Second,
		"2m":    2 * time.Minute,
		"1h":    time.Hour,
		"500ms": 500 * time.Millisecond,
		"1d":    24 * time.Hour,
	}
	for in, want := range cases {
		got, err := parseContextDuration(in)
		if err != nil || got != want {
			t.Fatalf("parseContextDuration(%q) = %v,%v want %v", in, got, err, want)
		}
	}
	if _, err := parseContextDuration("nope"); err == nil {
		t.Fatal("expected error for bad duration")
	}
}

func mkRows(n int) []engine.Record {
	rows := make([]engine.Record, n)
	base := time.Date(2026, 7, 3, 4, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		rows[i] = engine.Record{
			idxKey: float64(i),
			"id":   float64(i),
			"ts":   base.Add(time.Duration(i) * 10 * time.Second).Format(time.RFC3339),
		}
	}
	return rows
}

func TestApplyContextLines(t *testing.T) {
	orig := mkRows(6)
	anchors := engine.Result{Rows: []engine.Record{orig[3]}}
	res, groups, err := applyContext(orig, anchors, contextOpts{active: true, before: 1, after: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("want 3 rows (2,3,4), got %d", len(res.Rows))
	}
	// middle row is the match
	var matchIDs []float64
	for _, r := range res.Rows {
		if r[matchKey] == ">" {
			matchIDs = append(matchIDs, r["id"].(float64))
		}
		if _, ok := r[idxKey]; ok {
			t.Fatalf("__idx should be stripped from output: %v", r)
		}
	}
	if len(matchIDs) != 1 || matchIDs[0] != 3 {
		t.Fatalf("match should be id 3, got %v", matchIDs)
	}
	// single contiguous group
	for _, g := range groups {
		if g != groups[0] {
			t.Fatalf("expected one group, got %v", groups)
		}
	}
}

func TestApplyContextGroups(t *testing.T) {
	orig := mkRows(10)
	anchors := engine.Result{Rows: []engine.Record{orig[1], orig[7]}}
	res, groups, err := applyContext(orig, anchors, contextOpts{active: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(res.Rows))
	}
	if groups[0] == groups[1] {
		t.Fatalf("non-adjacent matches should be different groups: %v", groups)
	}
}

func TestApplyContextTime(t *testing.T) {
	orig := mkRows(10) // 10s apart
	anchors := engine.Result{Rows: []engine.Record{orig[5]}}
	res, _, err := applyContext(orig, anchors, contextOpts{active: true, dur: 25 * time.Second, timeField: "ts"})
	if err != nil {
		t.Fatal(err)
	}
	// within +/-25s of index 5 => indices 3,4,5,6,7
	if len(res.Rows) != 5 {
		t.Fatalf("time window want 5 rows, got %d", len(res.Rows))
	}
}

func TestApplyContextRequiresIndex(t *testing.T) {
	orig := mkRows(3)
	// anchor lacks idxKey (simulates project/summarize)
	anchors := engine.Result{Rows: []engine.Record{{"id": float64(1)}}}
	if _, _, err := applyContext(orig, anchors, contextOpts{active: true, before: 1}); err == nil {
		t.Fatal("expected error when anchors lack __idx")
	}
}
