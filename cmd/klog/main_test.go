package main

import "testing"

func TestTimeBoundExpr(t *testing.T) {
	cases := map[string]string{
		"-1h":                  "ago(1h)",
		"-15m":                 "ago(15m)",
		"30m":                  "(now() + 30m)",
		"+2h":                  "(now() + 2h)",
		"now":                  "now()",
		"2026-07-02T09:00:00Z": `datetime("2026-07-02T09:00:00Z")`,
		"2026-07-02 09:00:00":  `datetime("2026-07-02 09:00:00")`,
	}
	for in, want := range cases {
		got, err := timeBoundExpr(in)
		if err != nil {
			t.Fatalf("timeBoundExpr(%q) error: %v", in, err)
		}
		if got != want {
			t.Fatalf("timeBoundExpr(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTimeFilterStage(t *testing.T) {
	if s, _ := timeFilterStage("ts", "", ""); s != "" {
		t.Fatalf("no bounds should yield empty stage, got %q", s)
	}
	s, err := timeFilterStage("ts", "-1h", "now")
	if err != nil {
		t.Fatal(err)
	}
	want := "where todatetime(ts) >= ago(1h) and todatetime(ts) < now()"
	if s != want {
		t.Fatalf("stage = %q, want %q", s, want)
	}
	if _, err := timeFilterStage("", "-1h", ""); err == nil {
		t.Fatal("empty time-field should error")
	}
}

func TestLevelColor(t *testing.T) {
	if levelColor("ERROR") != cRed {
		t.Fatal("ERROR should be red")
	}
	if levelColor("warn") != cYellow {
		t.Fatal("warn should be yellow")
	}
	if levelColor("INFO") != cGreen {
		t.Fatal("INFO should be green")
	}
	if levelColor("mystery") != "" {
		t.Fatal("unknown level should have no color")
	}
}

func TestColorCell(t *testing.T) {
	// only the level column is colorized, and only when enabled
	if got := colorCell("service", "ERROR", true); got != "ERROR" {
		t.Fatalf("non-level column should not color, got %q", got)
	}
	if got := colorCell("level", "ERROR", false); got != "ERROR" {
		t.Fatalf("disabled color should be plain, got %q", got)
	}
	got := colorCell("level", "ERROR", true)
	if got != cRed+"ERROR"+cReset {
		t.Fatalf("level ERROR colored = %q", got)
	}
	if got := colorCell("Level", "INFO", true); got != cGreen+"INFO"+cReset {
		t.Fatalf("case-insensitive level column failed, got %q", got)
	}
}

func TestShouldColor(t *testing.T) {
	if shouldColor("always", nil) != true {
		t.Fatal("always should be true")
	}
	if shouldColor("never", nil) != false {
		t.Fatal("never should be false")
	}
}
