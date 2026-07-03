package main

import (
	"strings"
	"testing"
)

func tr(t *testing.T, sql string) string {
	t.Helper()
	pipe, _, err := translateSQL(sql)
	if err != nil {
		t.Fatalf("translateSQL(%q): %v", sql, err)
	}
	return pipe
}

func mustContain(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Fatalf("expected %q to contain %q", s, sub)
	}
}

func TestSQLSimpleSelect(t *testing.T) {
	p := tr(t, "SELECT ts, service FROM logs WHERE level = 'ERROR' AND ms > 100 ORDER BY ms DESC LIMIT 5")
	mustContain(t, p, `where ((level == "ERROR") and (ms > 100))`)
	mustContain(t, p, "sort by ms desc")
	mustContain(t, p, "project ts, service")
	mustContain(t, p, "take 5")
}

func TestSQLSelectStar(t *testing.T) {
	p := tr(t, "SELECT * FROM logs WHERE status = 500")
	mustContain(t, p, `where (status == 500)`)
	if strings.Contains(p, "project") {
		t.Fatalf("SELECT * should not project: %q", p)
	}
}

func TestSQLDistinct(t *testing.T) {
	p := tr(t, "SELECT DISTINCT service, level FROM logs")
	mustContain(t, p, "project service, level")
	mustContain(t, p, "distinct service, level")
}

func TestSQLGroupByHaving(t *testing.T) {
	p := tr(t, "SELECT service, COUNT(*) AS n, AVG(ms) AS a FROM logs GROUP BY service HAVING COUNT(*) > 800 ORDER BY n DESC")
	mustContain(t, p, "summarize n=count(), a=avg(ms) by service")
	mustContain(t, p, "where (n > 800)")
	mustContain(t, p, "sort by n desc")
	mustContain(t, p, "project service, n, a")
}

func TestSQLCountDistinct(t *testing.T) {
	p := tr(t, "SELECT COUNT(DISTINCT user) AS u FROM logs")
	mustContain(t, p, "summarize u=dcount(user)")
}

func TestSQLLikeInBetween(t *testing.T) {
	mustContain(t, tr(t, "SELECT * FROM logs WHERE route LIKE '/che%'"), `route startswith "/che"`)
	mustContain(t, tr(t, "SELECT * FROM logs WHERE route LIKE '%out'"), `route endswith "out"`)
	mustContain(t, tr(t, "SELECT * FROM logs WHERE route LIKE '%ck%'"), `route contains "ck"`)
	mustContain(t, tr(t, "SELECT * FROM logs WHERE status IN (500, 503)"), "status in (500, 503)")
	mustContain(t, tr(t, "SELECT * FROM logs WHERE ms BETWEEN 100 AND 200"), "ms between (100 .. 200)")
	mustContain(t, tr(t, "SELECT * FROM logs WHERE user IS NOT NULL"), "isnotnull(user)")
	mustContain(t, tr(t, "SELECT * FROM logs WHERE user IS NULL"), "isnull(user)")
}

func TestSQLJoin(t *testing.T) {
	p := tr(t, "SELECT team, COUNT(*) AS n FROM logs INNER JOIN 'dims.log' ON service = service GROUP BY team")
	mustContain(t, p, "join kind=inner (dims.log) on service")
	mustContain(t, p, "summarize n=count() by team")
}

func TestSQLJoinDifferentKeys(t *testing.T) {
	p := tr(t, "SELECT * FROM logs LEFT JOIN 'dims.log' ON logs.svc = dims.service")
	mustContain(t, p, "join kind=leftouter (dims.log) on svc == service")
}

func TestSQLScalarOverAggregate(t *testing.T) {
	p := tr(t, "SELECT service, ROUND(AVG(ms), 1) AS avg_ms FROM logs GROUP BY service")
	mustContain(t, p, "summarize avg_ms=avg(ms) by service")
	mustContain(t, p, "extend avg_ms=round(avg_ms, 1)")
	mustContain(t, p, "project service, avg_ms")
}

func TestSQLFunctions(t *testing.T) {
	p := tr(t, "SELECT UPPER(service) AS s, LOWER(level) AS l FROM logs")
	mustContain(t, p, `project s=toupper(service), l=tolower(level)`)
}

func TestSQLFromFile(t *testing.T) {
	_, from, err := translateSQL("SELECT COUNT(*) AS n FROM 'sample/app.log'")
	if err != nil {
		t.Fatal(err)
	}
	if from != "sample/app.log" {
		t.Fatalf("fromFile = %q, want sample/app.log", from)
	}
	// bare table name is not treated as a file
	_, from2, _ := translateSQL("SELECT COUNT(*) AS n FROM logs")
	if from2 != "" {
		t.Fatalf("bare table should not be a file, got %q", from2)
	}
}

func TestSQLOrderByPosition(t *testing.T) {
	p := tr(t, "SELECT service, COUNT(*) AS n FROM logs GROUP BY service ORDER BY 2 DESC")
	mustContain(t, p, "sort by n desc")
}

func TestSQLErrors(t *testing.T) {
	cases := []string{
		"SELECT",
		"SELECT a FROM t WHERE",
		"SELECT a, COUNT(*) FROM t",  // a not grouped
		"DELETE FROM t",              // not SELECT
		"SELECT * FROM t GROUP BY a", // star with group by
	}
	for _, q := range cases {
		if _, _, err := translateSQL(q); err == nil {
			t.Fatalf("expected error for %q", q)
		}
	}
}

func TestWantSQL(t *testing.T) {
	if !wantSQL("sql", "anything") {
		t.Fatal("explicit sql")
	}
	if wantSQL("kql", "SELECT 1") {
		t.Fatal("explicit kql should stay kql")
	}
	if !wantSQL("auto", "SELECT a FROM t") {
		t.Fatal("auto should detect SELECT")
	}
	if wantSQL("auto", "where level==\"ERROR\"") {
		t.Fatal("auto should treat KQL as kql")
	}
}
