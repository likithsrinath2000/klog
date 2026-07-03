package engine

import "testing"

func TestCompileRenderKinds(t *testing.T) {
	cases := map[string]string{
		"barchart":     "bar",
		"columnchart":  "column",
		"piechart":     "pie",
		"timechart":    "time",
		"linechart":    "line",
		"scatterchart": "scatter",
	}
	for in, want := range cases {
		op, err := compileRender(in)
		if err != nil {
			t.Fatalf("compileRender(%q): %v", in, err)
		}
		ro := op.(renderOp)
		if ro.spec.Kind != want {
			t.Fatalf("kind %q => %q, want %q", in, ro.spec.Kind, want)
		}
	}
	if _, err := compileRender("boguschart"); err == nil {
		t.Fatal("unknown kind should error")
	}
}

func TestCompileRenderWith(t *testing.T) {
	op, err := compileRender(`barchart with (title="Errors by svc", xcolumn=service, ycolumns=a;b)`)
	if err != nil {
		t.Fatal(err)
	}
	s := op.(renderOp).spec
	if s.Title != "Errors by svc" || s.XCol != "service" {
		t.Fatalf("spec: %+v", s)
	}
	if len(s.YCols) != 2 || s.YCols[0] != "a" || s.YCols[1] != "b" {
		t.Fatalf("ycols: %v", s.YCols)
	}
}

func TestRenderOpSetsChart(t *testing.T) {
	p, err := Compile(`summarize n=count() by level | render barchart`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Run([]Record{{"level": "INFO"}, {"level": "ERROR"}, {"level": "INFO"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Chart == nil || res.Chart.Kind != "bar" {
		t.Fatalf("chart not set: %+v", res.Chart)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("rows should pass through: %d", len(res.Rows))
	}
}
