package engine

import (
	"fmt"
	"strings"
)

// ChartSpec describes a requested chart. The CLI turns it into terminal output.
type ChartSpec struct {
	Kind  string   // bar | column | pie | line | time | area | scatter | histogram
	Title string   // optional title
	XCol  string   // x-axis column ("" => first column)
	YCols []string // y series ("" => all numeric columns except XCol)
	Bins  int      // histogram bin count (0 => default)
}

type renderOp struct{ spec *ChartSpec }

// compileRender parses: render <kind> [with (k=v, k=v, ...)]
func compileRender(rest string) (operator, error) {
	rest = strings.TrimSpace(rest)
	kindWord, after := firstWord(rest)
	kind, err := normalizeChartKind(kindWord)
	if err != nil {
		return nil, err
	}
	spec := &ChartSpec{Kind: kind}
	after = strings.TrimSpace(after)
	if after != "" {
		if lw, body := firstWord(after); strings.EqualFold(lw, "with") {
			body = strings.TrimSpace(body)
			body = strings.TrimPrefix(body, "(")
			body = strings.TrimSuffix(body, ")")
			for _, kv := range splitTop(body, ',') {
				k, v, ok := splitAssign(kv)
				if !ok {
					continue
				}
				v = strings.Trim(strings.TrimSpace(v), `"'`)
				switch strings.ToLower(strings.TrimSpace(k)) {
				case "title":
					spec.Title = v
				case "xcolumn", "xaxis":
					spec.XCol = v
				case "ycolumns", "yaxis", "series":
					for _, y := range strings.FieldsFunc(v, func(r rune) bool { return r == ';' || r == '+' }) {
						if y = strings.TrimSpace(y); y != "" {
							spec.YCols = append(spec.YCols, y)
						}
					}
				case "kind":
					if nk, err := normalizeChartKind(v); err == nil {
						spec.Kind = nk
					}
				case "bins":
					fmt.Sscanf(strings.TrimSpace(v), "%d", &spec.Bins)
				}
			}
		} else {
			return nil, fmt.Errorf("render: expected 'with (...)', got %q", after)
		}
	}
	return renderOp{spec: spec}, nil
}

func normalizeChartKind(k string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(k)) {
	case "barchart", "bar":
		return "bar", nil
	case "columnchart", "column":
		return "column", nil
	case "piechart", "pie":
		return "pie", nil
	case "timechart", "time":
		return "time", nil
	case "linechart", "line":
		return "line", nil
	case "areachart", "area":
		return "area", nil
	case "scatterchart", "scatter":
		return "scatter", nil
	case "table":
		return "table", nil
	case "histogram", "hist":
		return "histogram", nil
	}
	return "", fmt.Errorf("unknown chart kind %q", k)
}

func (o renderOp) apply(in Result) (Result, error) {
	return Result{Rows: in.Rows, Cols: in.Cols, Chart: o.spec}, nil
}
