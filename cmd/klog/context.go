package main

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/likithsrinath2000/klog/internal/engine"
)

// idxKey is the hidden ingestion-order index attached to each input row when
// --context is active, used to map anchor rows back to the original log.
const idxKey = "__idx"

// matchKey marks context output rows: ">" for query matches, "" for neighbors.
const matchKey = "_m"

type contextOpts struct {
	active    bool
	before    int           // lines before each match
	after     int           // lines after each match
	dur       time.Duration // +/- time window (0 = disabled)
	timeField string
}

func (c contextOpts) needsIndex() bool { return c.active }

var durRe = regexp.MustCompile(`^(\d+(?:\.\d+)?)(d|h|m|s|ms)$`)

// parseContextDuration parses values like "2m", "30s", "1h", "500ms", "1d".
func parseContextDuration(v string) (time.Duration, error) {
	m := durRe.FindStringSubmatch(v)
	if m == nil {
		return 0, fmt.Errorf("bad duration %q (use e.g. 2m, 30s, 1h)", v)
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, err
	}
	unit := map[string]time.Duration{
		"d": 24 * time.Hour, "h": time.Hour, "m": time.Minute,
		"s": time.Second, "ms": time.Millisecond,
	}[m[2]]
	return time.Duration(n * float64(unit)), nil
}

// applyContext expands the anchor rows in res into their surrounding rows drawn
// from orig (the original input in ingestion order). It returns the ordered
// output rows and a parallel slice of group ids (contiguous runs share an id),
// so the renderer can separate distinct incidents.
func applyContext(orig []engine.Record, res engine.Result, o contextOpts) (engine.Result, []int, error) {
	// Collect anchor indices; every anchor must still carry idxKey.
	matched := map[int]bool{}
	for _, r := range res.Rows {
		v, ok := r[idxKey]
		if !ok {
			return res, nil, fmt.Errorf("--context needs a row-preserving query " +
				"(use where/sort/take; not summarize/project that drop rows/columns)")
		}
		matched[int(toFloat(v))] = true
	}

	// Precompute times for time-window context.
	var times []time.Time
	haveTime := make([]bool, len(orig))
	if o.dur > 0 {
		times = make([]time.Time, len(orig))
		for i, r := range orig {
			if tv, ok := engine.Field(r, o.timeField); ok {
				if t, ok := engine.ParseTime(tv); ok {
					times[i] = t
					haveTime[i] = true
				}
			}
		}
	}

	selected := map[int]bool{}
	for idx := range matched {
		selected[idx] = true
		lo, hi := idx-o.before, idx+o.after
		for j := lo; j <= hi; j++ {
			if j >= 0 && j < len(orig) {
				selected[j] = true
			}
		}
		if o.dur > 0 && idx < len(orig) && haveTime[idx] {
			at := times[idx]
			for j := range orig {
				if haveTime[j] && absDur(times[j].Sub(at)) <= o.dur {
					selected[j] = true
				}
			}
		}
	}

	ordered := make([]int, 0, len(selected))
	for j := range selected {
		ordered = append(ordered, j)
	}
	sort.Ints(ordered)

	rows := make([]engine.Record, 0, len(ordered))
	groups := make([]int, 0, len(ordered))
	gid, prev := 0, math.MinInt
	for _, j := range ordered {
		if j != prev+1 {
			gid++
		}
		prev = j
		nr := engine.Record{}
		for k, v := range orig[j] {
			if k == idxKey {
				continue
			}
			nr[k] = v
		}
		if matched[j] {
			nr[matchKey] = ">"
		} else {
			nr[matchKey] = ""
		}
		rows = append(rows, nr)
		groups = append(groups, gid)
	}

	// Column order: marker first, then the original schema.
	cols := []string{matchKey}
	cols = append(cols, columns(engine.Result{Rows: rows})...)
	cols = dedupeCols(cols)
	return engine.Result{Rows: rows, Cols: cols}, groups, nil
}

func dedupeCols(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range in {
		if c == matchKey && seen[c] {
			continue
		}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

func toFloat(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// renderContextTable renders context results with a dim "--" divider between
// non-adjacent groups (like grep -C).
func renderContextTable(res engine.Result, groups []int, colorize bool) error {
	cols := columns(res)
	if len(cols) == 0 {
		return nil
	}
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = len(c)
	}
	cells := make([][]string, len(res.Rows))
	for ri, r := range res.Rows {
		row := make([]string, len(cols))
		for i, c := range cols {
			row[i] = cell(r, c)
			if len(row[i]) > widths[i] {
				widths[i] = len(row[i])
			}
		}
		cells[ri] = row
	}
	bw := bufio.NewWriter(os.Stdout)
	defer bw.Flush()
	writeRow(bw, cols, cols, widths, false)
	seps := make([]string, len(cols))
	for i := range seps {
		seps[i] = strings.Repeat("-", widths[i])
	}
	writeRow(bw, seps, cols, widths, false)
	prevGroup := -1
	for ri, row := range cells {
		if ri > 0 && groups[ri] != prevGroup {
			fmt.Fprintln(bw, "\x1b[2m--\x1b[0m")
		}
		prevGroup = groups[ri]
		writeRow(bw, row, cols, widths, colorize)
	}
	return nil
}
