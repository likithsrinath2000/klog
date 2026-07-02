package engine

import (
	"encoding/json"
	"testing"
)

func rows(t *testing.T, lines ...string) []Record {
	t.Helper()
	var rs []Record
	for _, l := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("bad test json %q: %v", l, err)
		}
		rs = append(rs, Record(m))
	}
	return rs
}

var sample = []string{
	`{"level":"INFO","service":"auth","ms":40,"user":"a"}`,
	`{"level":"ERROR","service":"auth","ms":812,"user":"b"}`,
	`{"level":"ERROR","service":"api","ms":150,"user":"a"}`,
	`{"level":"INFO","service":"api","ms":88,"user":"c"}`,
	`{"level":"WARN","service":"auth","ms":300,"user":"a"}`,
	`{"level":"ERROR","service":"api","ms":540,"user":"b"}`,
}

func run(t *testing.T, q string) Result {
	t.Helper()
	p, err := Compile(q)
	if err != nil {
		t.Fatalf("compile %q: %v", q, err)
	}
	res, err := p.Run(rows(t, sample...))
	if err != nil {
		t.Fatalf("run %q: %v", q, err)
	}
	return res
}

func TestWhereEquals(t *testing.T) {
	res := run(t, `where level=="ERROR"`)
	if len(res.Rows) != 3 {
		t.Fatalf("want 3 errors, got %d", len(res.Rows))
	}
}

func TestWhereNumericAndBool(t *testing.T) {
	res := run(t, `where ms > 300 and service=="auth"`)
	if len(res.Rows) != 1 {
		t.Fatalf("want 1, got %d", len(res.Rows))
	}
	if res.Rows[0]["ms"].(float64) != 812 {
		t.Fatalf("unexpected row: %v", res.Rows[0])
	}
}

func TestWhereContains(t *testing.T) {
	res := run(t, `where service contains "ap"`)
	if len(res.Rows) != 3 {
		t.Fatalf("want 3, got %d", len(res.Rows))
	}
}

func TestWhereOrNot(t *testing.T) {
	res := run(t, `where not (level=="INFO") and (service=="api" or service=="auth")`)
	if len(res.Rows) != 4 {
		t.Fatalf("want 4, got %d", len(res.Rows))
	}
}

func TestSummarizeCountBy(t *testing.T) {
	res := run(t, `where level=="ERROR" | summarize n=count() by service | sort by n desc`)
	if len(res.Rows) != 2 {
		t.Fatalf("want 2 groups, got %d", len(res.Rows))
	}
	if res.Rows[0]["service"] != "api" || res.Rows[0]["n"].(float64) != 2 {
		t.Fatalf("unexpected top group: %v", res.Rows[0])
	}
	wantCols := []string{"service", "n"}
	for i, c := range wantCols {
		if res.Cols[i] != c {
			t.Fatalf("col %d = %q, want %q", i, res.Cols[i], c)
		}
	}
}

func TestSummarizeAvgAndFilter(t *testing.T) {
	res := run(t, `summarize avg(ms) by service | where avg_ms > 100`)
	got := map[string]float64{}
	for _, r := range res.Rows {
		got[r["service"].(string)] = r["avg_ms"].(float64)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 groups, got %d: %v", len(got), got)
	}
	if got["auth"] != 384 {
		t.Fatalf("auth avg_ms = %v, want 384", got["auth"])
	}
}

func TestSummarizeDcountMinMaxSum(t *testing.T) {
	res := run(t, `summarize u=dcount(user), lo=min(ms), hi=max(ms), s=sum(ms) by service`)
	for _, r := range res.Rows {
		if r["service"] == "auth" {
			if r["u"].(float64) != 2 {
				t.Fatalf("auth dcount user = %v want 2", r["u"])
			}
			if r["lo"].(float64) != 40 || r["hi"].(float64) != 812 {
				t.Fatalf("auth min/max = %v/%v", r["lo"], r["hi"])
			}
			if r["s"].(float64) != 1152 {
				t.Fatalf("auth sum = %v want 1152", r["s"])
			}
		}
	}
}

func TestProjectRename(t *testing.T) {
	res := run(t, `where level=="WARN" | project svc=service, ms`)
	if len(res.Rows) != 1 {
		t.Fatalf("want 1, got %d", len(res.Rows))
	}
	if res.Rows[0]["svc"] != "auth" {
		t.Fatalf("rename failed: %v", res.Rows[0])
	}
	if _, ok := res.Rows[0]["service"]; ok {
		t.Fatalf("original field should be dropped")
	}
}

func TestSortAscDesc(t *testing.T) {
	// KQL default sort is descending.
	res := run(t, `sort by ms | take 1`)
	if res.Rows[0]["ms"].(float64) != 812 {
		t.Fatalf("default (desc) sort failed: %v", res.Rows[0])
	}
	res = run(t, `sort by ms asc | take 1`)
	if res.Rows[0]["ms"].(float64) != 40 {
		t.Fatalf("asc sort failed: %v", res.Rows[0])
	}
	res = run(t, `sort by ms desc | take 1`)
	if res.Rows[0]["ms"].(float64) != 812 {
		t.Fatalf("desc sort failed: %v", res.Rows[0])
	}
}

func TestTakeAndCount(t *testing.T) {
	res := run(t, `take 2`)
	if len(res.Rows) != 2 {
		t.Fatalf("take want 2, got %d", len(res.Rows))
	}
	res = run(t, `where level=="ERROR" | count`)
	if res.Rows[0]["count"].(float64) != 3 {
		t.Fatalf("count want 3, got %v", res.Rows[0]["count"])
	}
}

func TestDottedField(t *testing.T) {
	p, _ := Compile(`where meta.region=="us" | count`)
	rs := rows(t,
		`{"meta":{"region":"us"}}`,
		`{"meta":{"region":"eu"}}`,
		`{"meta":{"region":"us"}}`,
	)
	res, err := p.Run(rs)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rows[0]["count"].(float64) != 2 {
		t.Fatalf("dotted field want 2, got %v", res.Rows[0]["count"])
	}
}

func TestCompileErrors(t *testing.T) {
	cases := []string{
		`where ms >`,
		`bogusop x`,
		`summarize weird(ms)`,
		`sort`,
	}
	for _, q := range cases {
		if _, err := Compile(q); err == nil {
			t.Fatalf("expected error compiling %q", q)
		}
	}
}
