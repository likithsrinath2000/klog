package engine

import (
	"testing"
	"time"
)

func rowsFrom(t *testing.T, lines ...string) []Record {
	t.Helper()
	return rows(t, lines...)
}

func single(t *testing.T, q string, in []Record) Result {
	t.Helper()
	p, err := Compile(q)
	if err != nil {
		t.Fatalf("compile %q: %v", q, err)
	}
	res, err := p.Run(in)
	if err != nil {
		t.Fatalf("run %q: %v", q, err)
	}
	return res
}

func TestExtendArithmetic(t *testing.T) {
	res := run(t, `where service=="auth" | extend sec = ms / 1000.0 | project ms, sec | sort by ms desc | take 1`)
	if res.Rows[0]["sec"].(float64) != 0.812 {
		t.Fatalf("extend arithmetic got %v", res.Rows[0]["sec"])
	}
}

func TestExtendFunctions(t *testing.T) {
	res := run(t, `extend up = toupper(service), n = strlen(service) | project up, n | take 1`)
	if res.Rows[0]["up"] != "AUTH" || res.Rows[0]["n"].(float64) != 4 {
		t.Fatalf("func extend got %v", res.Rows[0])
	}
}

func TestIffCoalesceCase(t *testing.T) {
	res := run(t, `extend band = case(ms > 500, "slow", ms > 100, "mid", "fast") | summarize count() by band | sort by band asc`)
	got := map[string]float64{}
	for _, r := range res.Rows {
		got[r["band"].(string)] = r["count"].(float64)
	}
	if got["slow"] != 2 || got["mid"] != 2 || got["fast"] != 2 {
		t.Fatalf("case bands: %v", got)
	}
}

func TestInAndBetween(t *testing.T) {
	res := run(t, `where service in ("api", "auth") and ms between (100 .. 600)`)
	if len(res.Rows) != 3 {
		t.Fatalf("in/between want 3, got %d", len(res.Rows))
	}
}

func TestHasAndMatches(t *testing.T) {
	res := run(t, `where level has "error"`)
	if len(res.Rows) != 3 {
		t.Fatalf("has want 3, got %d", len(res.Rows))
	}
	res = run(t, `where service matches regex "^a"`)
	if len(res.Rows) != 6 {
		t.Fatalf("matches want 6, got %d", len(res.Rows))
	}
}

func TestTopOperator(t *testing.T) {
	res := run(t, `top 2 by ms`)
	if len(res.Rows) != 2 || res.Rows[0]["ms"].(float64) != 812 || res.Rows[1]["ms"].(float64) != 540 {
		t.Fatalf("top got %v", res.Rows)
	}
}

func TestDistinct(t *testing.T) {
	res := run(t, `distinct service`)
	if len(res.Rows) != 2 {
		t.Fatalf("distinct want 2, got %d", len(res.Rows))
	}
}

func TestProjectAwayKeepReorder(t *testing.T) {
	res := run(t, `project-away user, ts | take 1`)
	if _, ok := res.Rows[0]["user"]; ok {
		t.Fatalf("project-away kept user")
	}
	res = run(t, `project-keep service | take 1`)
	if len(res.Cols) != 1 || res.Cols[0] != "service" {
		t.Fatalf("project-keep cols %v", res.Cols)
	}
	res = run(t, `project-reorder ms`)
	if res.Cols[0] != "ms" {
		t.Fatalf("project-reorder cols %v", res.Cols)
	}
}

func TestProjectRenameOp(t *testing.T) {
	res := run(t, `project-rename svc=service | take 1`)
	if _, ok := res.Rows[0]["svc"]; !ok {
		t.Fatalf("rename missing svc: %v", res.Rows[0])
	}
	if _, ok := res.Rows[0]["service"]; ok {
		t.Fatalf("rename left service")
	}
}

func TestSummarizeExtras(t *testing.T) {
	res := run(t, `summarize p50=percentile(ms, 50), sd=stdev(ms), lst=make_list(user), st=make_set(user), errs=countif(level=="ERROR")`)
	r := res.Rows[0]
	if r["errs"].(float64) != 3 {
		t.Fatalf("countif got %v", r["errs"])
	}
	if _, ok := r["p50"].(float64); !ok {
		t.Fatalf("percentile missing")
	}
	if lst, ok := r["lst"].([]any); !ok || len(lst) != 6 {
		t.Fatalf("make_list got %v", r["lst"])
	}
	if st, ok := r["st"].([]any); !ok || len(st) != 3 {
		t.Fatalf("make_set got %v", r["st"])
	}
}

func TestArgMax(t *testing.T) {
	res := run(t, `summarize arg_max(ms, service, user)`)
	r := res.Rows[0]
	if r["ms"].(float64) != 812 || r["service"] != "auth" || r["user"] != "b" {
		t.Fatalf("arg_max got %v", r)
	}
}

func TestSummarizeByBin(t *testing.T) {
	in := rowsFrom(t,
		`{"ts":"2026-07-02T10:05:00Z","v":1}`,
		`{"ts":"2026-07-02T10:35:00Z","v":1}`,
		`{"ts":"2026-07-02T11:05:00Z","v":1}`,
	)
	res := single(t, `summarize count() by bucket = bin(todatetime(ts), 1h)`, in)
	if len(res.Rows) != 2 {
		t.Fatalf("bin buckets want 2, got %d: %v", len(res.Rows), res.Rows)
	}
}

func TestDatetimeFilter(t *testing.T) {
	future := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	past := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	in := rowsFrom(t,
		`{"ts":"`+future+`","id":1}`,
		`{"ts":"`+past+`","id":2}`,
	)
	res := single(t, `where todatetime(ts) > ago(1h)`, in)
	if len(res.Rows) != 1 || res.Rows[0]["id"].(float64) != 1 {
		t.Fatalf("datetime filter got %v", res.Rows)
	}
}

func TestTimespanArithmetic(t *testing.T) {
	in := rowsFrom(t, `{"x":1}`)
	res := single(t, `extend d = 90m + 30m | project d`, in)
	if ts, ok := res.Rows[0]["d"].(Timespan); !ok || time.Duration(ts) != 2*time.Hour {
		t.Fatalf("timespan arithmetic got %v", res.Rows[0]["d"])
	}
}

func TestParseOperator(t *testing.T) {
	in := rowsFrom(t,
		`{"msg":"user=alice ip=10.0.0.1 done"}`,
		`{"msg":"user=bob ip=10.0.0.2 done"}`,
	)
	res := single(t, `parse msg with "user=" user " ip=" ip " done" | project user, ip`, in)
	if res.Rows[0]["user"] != "alice" || res.Rows[0]["ip"] != "10.0.0.1" {
		t.Fatalf("parse got %v", res.Rows[0])
	}
	if res.Rows[1]["user"] != "bob" {
		t.Fatalf("parse row2 got %v", res.Rows[1])
	}
}

func TestMvExpand(t *testing.T) {
	in := rowsFrom(t, `{"id":1,"tags":["a","b","c"]}`)
	res := single(t, `mv-expand tags`, in)
	if len(res.Rows) != 3 || res.Rows[0]["tags"] != "a" || res.Rows[2]["tags"] != "c" {
		t.Fatalf("mv-expand got %v", res.Rows)
	}
}

func TestPrint(t *testing.T) {
	res := single(t, `print x = 2 + 3 * 4, name = strcat("a", "b")`, nil)
	if res.Rows[0]["x"].(float64) != 14 || res.Rows[0]["name"] != "ab" {
		t.Fatalf("print got %v", res.Rows[0])
	}
}

func TestGetschema(t *testing.T) {
	in := rowsFrom(t, `{"a":1,"b":"x","c":true}`)
	res := single(t, `getschema`, in)
	types := map[string]string{}
	for _, r := range res.Rows {
		types[r["ColumnName"].(string)] = r["ColumnType"].(string)
	}
	if types["a"] != "real" || types["b"] != "string" || types["c"] != "bool" {
		t.Fatalf("getschema types %v", types)
	}
}

func TestSerialize(t *testing.T) {
	res := run(t, `sort by ms asc | serialize rn = row_number()`)
	if res.Rows[0]["rn"].(float64) != 1 || res.Rows[5]["rn"].(float64) != 6 {
		t.Fatalf("serialize got %v", res.Rows)
	}
}

func TestUnion(t *testing.T) {
	FileLoader = func(path string) ([]Record, error) {
		return rowsFrom(t, `{"service":"cache","level":"INFO","ms":5,"user":"z"}`), nil
	}
	defer func() { FileLoader = nil }()
	res := run(t, `union extra.log | summarize count()`)
	if res.Rows[0]["count"].(float64) != 7 {
		t.Fatalf("union count want 7, got %v", res.Rows[0]["count"])
	}
}

func TestJoinInner(t *testing.T) {
	FileLoader = func(path string) ([]Record, error) {
		return rowsFrom(t,
			`{"service":"auth","team":"identity"}`,
			`{"service":"api","team":"gateway"}`,
		), nil
	}
	defer func() { FileLoader = nil }()
	res := run(t, `join kind=inner (teams.log) on service`)
	if len(res.Rows) != 6 {
		t.Fatalf("join inner want 6, got %d", len(res.Rows))
	}
	found := false
	for _, r := range res.Rows {
		if r["service"] == "auth" && r["team"] == "identity" {
			found = true
		}
	}
	if !found {
		t.Fatalf("join did not enrich team: %v", res.Rows)
	}
}

func TestLookup(t *testing.T) {
	FileLoader = func(path string) ([]Record, error) {
		return rowsFrom(t, `{"service":"auth","team":"identity"}`), nil
	}
	defer func() { FileLoader = nil }()
	res := run(t, `lookup (teams.log) on service`)
	if len(res.Rows) != 6 {
		t.Fatalf("lookup want 6 rows, got %d", len(res.Rows))
	}
	for _, r := range res.Rows {
		if r["service"] == "auth" && r["team"] != "identity" {
			t.Fatalf("auth not enriched: %v", r)
		}
		if r["service"] == "api" {
			if _, ok := r["team"]; ok && r["team"] != nil {
				t.Fatalf("api should have no team: %v", r)
			}
		}
	}
}

func TestLeftAnti(t *testing.T) {
	FileLoader = func(path string) ([]Record, error) {
		return rowsFrom(t, `{"service":"auth"}`), nil
	}
	defer func() { FileLoader = nil }()
	res := run(t, `join kind=leftanti (known.log) on service`)
	for _, r := range res.Rows {
		if r["service"] != "api" {
			t.Fatalf("leftanti should only keep api, got %v", r)
		}
	}
	if len(res.Rows) != 3 {
		t.Fatalf("leftanti want 3, got %d", len(res.Rows))
	}
}

func TestMathAndStringFuncs(t *testing.T) {
	res := single(t, `print a=abs(-5), b=round(3.14159, 2), c=substring("hello", 1, 3), d=indexof("abc","c"), e=strcat_delim("-","x","y")`, nil)
	r := res.Rows[0]
	if r["a"].(float64) != 5 || r["b"].(float64) != 3.14 || r["c"] != "ell" || r["d"].(float64) != 2 || r["e"] != "x-y" {
		t.Fatalf("funcs got %v", r)
	}
}
