package main

import (
	"bufio"
	"bytes"
	"strings"
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

func TestHistChart(t *testing.T) {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	rows := []engine.Record{
		{"ms": float64(1)}, {"ms": float64(2)}, {"ms": float64(2)},
		{"ms": float64(9)}, {"ms": float64(10)},
	}
	if err := histChart(w, rows, "ms", 5); err != nil {
		t.Fatal(err)
	}
	w.Flush()
	out := buf.String()
	// 5 bins over [1,10]; first bin should hold the three low values.
	if !strings.Contains(out, "1..") {
		t.Fatalf("histogram missing first bin label:\n%s", out)
	}
	lines := strings.Count(out, "\n")
	if lines != 5 {
		t.Fatalf("histogram want 5 bin lines, got %d:\n%s", lines, out)
	}
}

func TestHistChartNoNumeric(t *testing.T) {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	rows := []engine.Record{{"ms": "abc"}}
	if err := histChart(w, rows, "ms", 5); err == nil {
		t.Fatal("expected error when column has no numeric values")
	}
}

func TestColumnChartValues(t *testing.T) {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	rows := []engine.Record{
		{"service": "auth", "n": float64(229)},
		{"service": "api", "n": float64(30)},
	}
	if err := columnChart(w, rows, "service", []string{"n"}); err != nil {
		t.Fatal(err)
	}
	w.Flush()
	out := buf.String()
	// values shown on top of bars, plus an indexed legend
	if !strings.Contains(out, "229") || !strings.Contains(out, "1) auth = 229") {
		t.Fatalf("column chart missing values/legend:\n%s", out)
	}
}

func TestFirstNumeric(t *testing.T) {
	rows := []engine.Record{{"a": "x", "b": float64(5)}}
	if _, ok := firstNumeric(rows, "a"); ok {
		t.Fatal("column a is not numeric")
	}
	if v, ok := firstNumeric(rows, "b"); !ok || v != 5 {
		t.Fatalf("column b should be numeric 5, got %v %v", v, ok)
	}
}
