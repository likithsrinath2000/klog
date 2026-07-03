package main

import (
	"testing"

	"github.com/likithsrinath2000/klog/internal/engine"
)

func TestNumericColumns(t *testing.T) {
	rows := []engine.Record{
		{"service": "auth", "ms": float64(40), "user": "a"},
		{"service": "api", "ms": float64(88), "user": "b"},
	}
	got := numericColumns(rows, []string{"service", "ms", "user"}, "service")
	if len(got) != 1 || got[0] != "ms" {
		t.Fatalf("numericColumns = %v, want [ms]", got)
	}
}

func TestFmtNum(t *testing.T) {
	cases := map[float64]string{
		42:    "42",
		3.14:  "3.14",
		3.100: "3.1",
		-5:    "-5",
	}
	for in, want := range cases {
		if got := fmtNum(in); got != want {
			t.Fatalf("fmtNum(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Fatalf("no truncate: %q", got)
	}
	if got := truncate("hello world", 5); got != "hell…" {
		t.Fatalf("truncate: %q", got)
	}
}

func TestScaledBar(t *testing.T) {
	if got := scaledBar(0, 10, 20); got != "" {
		t.Fatalf("zero bar should be empty, got %q", got)
	}
	full := scaledBar(10, 10, 20)
	if len([]rune(full)) != 20 {
		t.Fatalf("full bar width = %d, want 20", len([]rune(full)))
	}
	half := scaledBar(5, 10, 20)
	if len([]rune(half)) == 0 || len([]rune(half)) > 20 {
		t.Fatalf("half bar width invalid: %d", len([]rune(half)))
	}
}
