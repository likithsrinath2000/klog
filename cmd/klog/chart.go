package main

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/likithsrinath2000/klog/internal/engine"
)

// termWidth returns a usable terminal width.
func termWidth() int {
	if s := os.Getenv("COLUMNS"); s != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n > 20 {
			return n
		}
	}
	if fi, err := os.Stdout.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		if w := ttyWidth(); w > 20 {
			return w
		}
	}
	return 100
}

// renderChart draws a chart for res.Chart to stdout.
func renderChart(res engine.Result, colorize bool) error {
	spec := res.Chart
	cols := columns(engine.Result{Rows: res.Rows, Cols: res.Cols})
	if len(cols) == 0 || len(res.Rows) == 0 {
		fmt.Fprintln(os.Stdout, "(no data to chart)")
		return nil
	}
	xcol := spec.XCol
	if xcol == "" {
		xcol = cols[0]
	}
	ycols := spec.YCols
	if len(ycols) == 0 {
		ycols = numericColumns(res.Rows, cols, xcol)
	}
	if len(ycols) == 0 {
		return fmt.Errorf("render %s: no numeric y column found", spec.Kind)
	}

	bw := bufio.NewWriter(os.Stdout)
	defer bw.Flush()
	if spec.Title != "" {
		fmt.Fprintf(bw, "%s\n", spec.Title)
	}

	switch spec.Kind {
	case "bar", "column", "pie":
		return barChart(bw, res.Rows, xcol, ycols, spec.Kind == "pie")
	case "line", "time", "area", "scatter":
		return lineChart(bw, res.Rows, xcol, ycols)
	}
	return fmt.Errorf("unsupported chart kind %q", spec.Kind)
}

func numericColumns(rows []engine.Record, cols []string, xcol string) []string {
	var out []string
	for _, c := range cols {
		if c == xcol || c == matchKey || c == idxKey {
			continue
		}
		num, seen := 0, 0
		for _, r := range rows {
			if v, ok := r[c]; ok && v != nil {
				seen++
				if _, ok := engine.Number(v); ok {
					num++
				}
			}
		}
		if seen > 0 && num*2 >= seen {
			out = append(out, c)
		}
	}
	return out
}

func numAt(r engine.Record, col string) (float64, bool) {
	if v, ok := r[col]; ok {
		return engine.Number(v)
	}
	return 0, false
}

// ---- bar / pie ----

var blocks = []rune("▏▎▍▌▋▊▉█")

func barChart(w *bufio.Writer, rows []engine.Record, xcol string, ycols []string, pie bool) error {
	type bar struct {
		label string
		val   float64
	}
	var bars []bar
	total := 0.0
	multi := len(ycols) > 1
	for _, r := range rows {
		label := engine.Format(r[xcol])
		for _, y := range ycols {
			v, ok := numAt(r, y)
			if !ok {
				continue
			}
			lbl := label
			if multi {
				lbl = label + "/" + y
			}
			bars = append(bars, bar{lbl, v})
			if v > 0 {
				total += v
			}
		}
	}
	if len(bars) == 0 {
		return fmt.Errorf("no data")
	}

	maxVal, labelW, valW := 0.0, 0, 0
	for _, b := range bars {
		if b.val > maxVal {
			maxVal = b.val
		}
		if l := len([]rune(b.label)); l > labelW {
			labelW = l
		}
		if l := len(fmtNum(b.val)); l > valW {
			valW = l
		}
	}
	if labelW > 28 {
		labelW = 28
	}
	if maxVal <= 0 {
		maxVal = 1
	}
	barArea := termWidth() - labelW - valW - 6
	if pie {
		barArea -= 7
	}
	if barArea < 10 {
		barArea = 10
	}

	for _, b := range bars {
		label := truncate(b.label, labelW)
		bar := scaledBar(b.val, maxVal, barArea)
		line := fmt.Sprintf("%-*s │ %s %*s", labelW, label, bar, valW, fmtNum(b.val))
		if pie && total > 0 {
			line += fmt.Sprintf("  %5.1f%%", 100*b.val/total)
		}
		fmt.Fprintln(w, line)
	}
	return nil
}

func scaledBar(val, maxVal float64, area int) string {
	if val <= 0 {
		return ""
	}
	units := val / maxVal * float64(area) * 8 // eighths of a cell
	full := int(units) / 8
	rem := int(units) % 8
	var b strings.Builder
	for i := 0; i < full; i++ {
		b.WriteRune('█')
	}
	if rem > 0 {
		b.WriteRune(blocks[rem-1])
	}
	return b.String()
}

// ---- line / time ----

func lineChart(w *bufio.Writer, rows []engine.Record, xcol string, ycols []string) error {
	// sort rows by x (numeric/time if possible, else keep order)
	sorted := append([]engine.Record{}, rows...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return chartLess(sorted[i][xcol], sorted[j][xcol])
	})

	n := len(sorted)
	height := 15
	width := n
	maxW := termWidth() - 12
	if width > maxW {
		width = maxW
	}
	if width < 1 {
		width = 1
	}

	// find global y range
	minY, maxY := math.Inf(1), math.Inf(-1)
	for _, r := range sorted {
		for _, y := range ycols {
			if v, ok := numAt(r, y); ok {
				minY = math.Min(minY, v)
				maxY = math.Max(maxY, v)
			}
		}
	}
	if math.IsInf(minY, 1) {
		return fmt.Errorf("no numeric data")
	}
	if minY == maxY {
		maxY = minY + 1
	}

	markers := []rune{'•', '+', '×', '*', '○', '#'}
	grid := make([][]rune, height)
	for i := range grid {
		grid[i] = []rune(strings.Repeat(" ", width))
	}

	col := func(idx int) int {
		if n == 1 {
			return 0
		}
		return int(math.Round(float64(idx) / float64(n-1) * float64(width-1)))
	}
	row := func(v float64) int {
		frac := (v - minY) / (maxY - minY)
		return height - 1 - int(math.Round(frac*float64(height-1)))
	}

	for si, y := range ycols {
		m := markers[si%len(markers)]
		for idx, r := range sorted {
			if v, ok := numAt(r, y); ok {
				c := col(idx)
				rr := row(v)
				if rr >= 0 && rr < height && c >= 0 && c < width {
					grid[rr][c] = m
				}
			}
		}
	}

	// y-axis labels + grid
	axW := len(fmtNum(maxY))
	if l := len(fmtNum(minY)); l > axW {
		axW = l
	}
	for i := 0; i < height; i++ {
		var lbl string
		switch i {
		case 0:
			lbl = fmtNum(maxY)
		case height - 1:
			lbl = fmtNum(minY)
		}
		fmt.Fprintf(w, "%*s ┤%s\n", axW, lbl, string(grid[i]))
	}
	// x axis
	fmt.Fprintf(w, "%*s └%s\n", axW, "", strings.Repeat("─", width))
	first := truncate(engine.Format(sorted[0][xcol]), width)
	last := engine.Format(sorted[n-1][xcol])
	pad := width - len([]rune(first)) - len([]rune(last))
	if pad < 1 {
		pad = 1
	}
	fmt.Fprintf(w, "%*s  %s%s%s\n", axW, "", first, strings.Repeat(" ", pad), last)

	if len(ycols) > 1 {
		var leg []string
		for si, y := range ycols {
			leg = append(leg, fmt.Sprintf("%c %s", markers[si%len(markers)], y))
		}
		fmt.Fprintf(w, "%*s  %s\n", axW, "", strings.Join(leg, "   "))
	}
	return nil
}

func chartLess(a, b any) bool {
	if at, ok := engine.ParseTime(a); ok {
		if bt, ok := engine.ParseTime(b); ok {
			return at.Before(bt)
		}
	}
	if an, ok := engine.Number(a); ok {
		if bn, ok := engine.Number(b); ok {
			return an < bn
		}
	}
	return engine.Format(a) < engine.Format(b)
}

func fmtNum(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	s := strconv.FormatFloat(f, 'f', 2, 64)
	return strings.TrimRight(strings.TrimRight(s, "0"), ".")
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
