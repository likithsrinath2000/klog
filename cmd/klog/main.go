package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/likithsrinath2000/klog/internal/engine"
)

var version = "0.3.0"

const usage = `klog %s - a KQL-lite query engine for JSON/NDJSON logs

Usage:
  klog [flags] '<query>' [file ...]
  cat app.log | klog '<query>'

Query is a '|'-separated pipeline of KQL-style operators. Supported:
  Filter/shape:  where  project  project-away  project-keep  project-rename
                 project-reorder  extend  parse  mv-expand  distinct
  Aggregate:     summarize  count  getschema
  Order/limit:   sort  top  take  sample  sample-distinct  serialize
  Multi-table:   union  join  lookup   (sources are NDJSON files/subqueries)
  Constant:      print

summarize aggregations: count, countif, sum, sumif, avg, avgif, min, max,
  dcount, make_list, make_set, any, percentile, stdev, stdevp, variance, varp,
  arg_max, arg_min. Group with 'by <expr>' (e.g. by bin(todatetime(ts), 1h)).

Expressions: arithmetic (+ - * / %%), == != > < >= <= =~ !~, and/or/not,
  in (...), between (lo .. hi), contains startswith endswith has, matches regex,
  datetime/timespan literals (1h, 30m, 5s), 50+ scalar functions (strcat, split,
  substring, iff, case, coalesce, toint, todatetime, ago, now, bin, round, ...).

Examples:
  klog 'where level=="ERROR" | summarize n=count() by service | sort by n desc' app.log
  klog 'summarize p95=percentile(ms,95) by service | where p95 > 100' app.log
  klog --from -1h --to now 'summarize count() by service' app.log

Flags:
`

func main() {
	out := flag.String("o", "table", "output format: table|json|tsv")
	flag.StringVar(out, "output", "table", "output format: table|json|tsv")
	from := flag.String("from", "", "only rows with time-field >= this (datetime or relative, e.g. -1h)")
	to := flag.String("to", "", "only rows with time-field < this (datetime or relative, e.g. -15m)")
	timeField := flag.String("time-field", "ts", "field used by --from/--to")
	color := flag.String("color", "auto", "colorize level column: auto|always|never")
	showVer := flag.Bool("version", false, "print version and exit")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, usage, version)
		flag.PrintDefaults()
	}
	// Allow flags to appear before or after positional args (query/files).
	flagArgs, positional := splitArgs(os.Args[1:])
	flag.CommandLine.Parse(flagArgs)

	if *showVer {
		fmt.Println("klog", version)
		return
	}

	args := positional
	if len(args) == 0 {
		flag.Usage()
		os.Exit(2)
	}
	query := args[0]
	files := args[1:]

	// Prepend a time-range filter stage if --from/--to given.
	if tf, err := timeFilterStage(*timeField, *from, *to); err != nil {
		fatal("%v", err)
	} else if tf != "" {
		query = tf + " | " + query
	}

	// Let union/join/lookup read NDJSON files by path.
	engine.FileLoader = readFileRows

	pipe, err := engine.Compile(query)
	if err != nil {
		fatal("query error: %v", err)
	}

	rows, err := readRows(files)
	if err != nil {
		fatal("%v", err)
	}

	res, err := pipe.Run(rows)
	if err != nil {
		fatal("run error: %v", err)
	}

	colorize := shouldColor(*color, os.Stdout)
	if err := render(os.Stdout, res, *out, colorize); err != nil {
		fatal("%v", err)
	}
}

// timeFilterStage builds a `where` stage for --from/--to, or "" if neither set.
func timeFilterStage(field, from, to string) (string, error) {
	if from == "" && to == "" {
		return "", nil
	}
	if field == "" {
		return "", fmt.Errorf("--time-field must not be empty")
	}
	var clauses []string
	if from != "" {
		e, err := timeBoundExpr(from)
		if err != nil {
			return "", fmt.Errorf("bad --from: %w", err)
		}
		clauses = append(clauses, fmt.Sprintf("todatetime(%s) >= %s", field, e))
	}
	if to != "" {
		e, err := timeBoundExpr(to)
		if err != nil {
			return "", fmt.Errorf("bad --to: %w", err)
		}
		clauses = append(clauses, fmt.Sprintf("todatetime(%s) < %s", field, e))
	}
	return "where " + strings.Join(clauses, " and "), nil
}

var relTimeRe = regexp.MustCompile(`^([+-]?)(\d+(?:\.\d+)?)(d|h|m|s|ms)$`)

// timeBoundExpr turns a bound into a klog expression: relative offsets like
// "-1h"/"30m" become ago()/now()+, absolute values become datetime("...").
func timeBoundExpr(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "now" {
		return "now()", nil
	}
	if m := relTimeRe.FindStringSubmatch(v); m != nil {
		sign, mag, unit := m[1], m[2], m[3]
		if sign == "-" {
			return fmt.Sprintf("ago(%s%s)", mag, unit), nil
		}
		return fmt.Sprintf("(now() + %s%s)", mag, unit), nil
	}
	// absolute datetime literal (validated by the engine at runtime)
	return fmt.Sprintf("datetime(%q)", v), nil
}

// shouldColor decides whether to colorize based on the flag and TTY status.
func shouldColor(mode string, f *os.File) bool {
	switch mode {
	case "always":
		return true
	case "never":
		return false
	default: // auto
		if os.Getenv("NO_COLOR") != "" {
			return false
		}
		fi, err := f.Stat()
		return err == nil && fi.Mode()&os.ModeCharDevice != 0
	}
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "klog: "+format+"\n", a...)
	os.Exit(1)
}

// splitArgs separates flag arguments from positional ones so that flags may be
// interspersed with the query and file positionals.
func splitArgs(argv []string) (flags, positional []string) {
	// Flags that consume a following value when not given as --flag=value.
	valueFlags := map[string]bool{
		"-o": true, "--output": true,
		"--from": true, "--to": true, "--time-field": true, "--color": true,
	}
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if a == "--" {
			positional = append(positional, argv[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			if valueFlags[a] && !strings.Contains(a, "=") && i+1 < len(argv) {
				i++
				flags = append(flags, argv[i])
			}
			continue
		}
		positional = append(positional, a)
	}
	return flags, positional
}

func readRows(files []string) ([]engine.Record, error) {
	var readers []io.Reader
	var closers []io.Closer
	if len(files) == 0 {
		readers = append(readers, os.Stdin)
	} else {
		for _, f := range files {
			if f == "-" {
				readers = append(readers, os.Stdin)
				continue
			}
			fh, err := os.Open(f)
			if err != nil {
				return nil, err
			}
			readers = append(readers, fh)
			closers = append(closers, fh)
		}
	}
	defer func() {
		for _, c := range closers {
			c.Close()
		}
	}()

	var rows []engine.Record
	lineNo := 0
	for _, r := range readers {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		for sc.Scan() {
			lineNo++
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(line), &m); err == nil {
				rows = append(rows, engine.Record(m))
			} else {
				// Non-JSON line: expose raw text so it's still queryable.
				rows = append(rows, engine.Record{"_line": float64(lineNo), "_raw": line})
			}
		}
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	return rows, nil
}

// readFileRows loads a single NDJSON file (used by union/join/lookup).
func readFileRows(path string) ([]engine.Record, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	var rows []engine.Record
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err == nil {
			rows = append(rows, engine.Record(m))
		} else {
			rows = append(rows, engine.Record{"_line": float64(lineNo), "_raw": line})
		}
	}
	return rows, sc.Err()
}

func render(w io.Writer, res engine.Result, format string, colorize bool) error {
	switch format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(displayRows(res.Rows))
	case "tsv":
		return renderDelim(w, res, "\t")
	case "table":
		return renderTable(w, res, colorize)
	default:
		return fmt.Errorf("unknown output format %q", format)
	}
}

// ---- level colorization ----

const (
	cReset  = "\x1b[0m"
	cRed    = "\x1b[31m"
	cBoldRd = "\x1b[1;31m"
	cYellow = "\x1b[33m"
	cGreen  = "\x1b[32m"
	cGray   = "\x1b[90m"
	cCyan   = "\x1b[36m"
)

func isLevelColumn(col string) bool {
	switch strings.ToLower(col) {
	case "level", "lvl", "severity", "loglevel", "log_level":
		return true
	}
	return false
}

func levelColor(v string) string {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case "ERROR", "ERR", "SEVERE", "SEV":
		return cRed
	case "FATAL", "CRIT", "CRITICAL", "PANIC", "EMERG", "ALERT":
		return cBoldRd
	case "WARN", "WARNING":
		return cYellow
	case "INFO", "NOTICE":
		return cGreen
	case "DEBUG":
		return cGray
	case "TRACE", "VERBOSE":
		return cCyan
	}
	return ""
}

// colorCell wraps a cell value in a color if it is a recognized level.
func colorCell(col, raw string, colorize bool) string {
	if !colorize || !isLevelColumn(col) {
		return raw
	}
	if c := levelColor(raw); c != "" {
		return c + raw + cReset
	}
	return raw
}

func columns(res engine.Result) []string {
	if res.Cols != nil {
		return res.Cols
	}
	seen := map[string]bool{}
	var cols []string
	for _, r := range res.Rows {
		for k := range r {
			if !seen[k] {
				seen[k] = true
				cols = append(cols, k)
			}
		}
	}
	sort.Strings(cols)
	return cols
}

func cell(r engine.Record, col string) string {
	v, ok := r[col]
	if !ok {
		return ""
	}
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return fmt.Sprintf("%v", t)
	case []any, map[string]any:
		b, _ := json.Marshal(engine.DisplayValue(v))
		return string(b)
	default:
		return engine.Format(v)
	}
}

// displayRows converts engine-native values (datetime, timespan) to
// JSON-friendly forms for output.
func displayRows(rows []engine.Record) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		m := make(map[string]any, len(r))
		for k, v := range r {
			m[k] = engine.DisplayValue(v)
		}
		out = append(out, m)
	}
	return out
}

func renderDelim(w io.Writer, res engine.Result, sep string) error {
	cols := columns(res)
	bw := bufio.NewWriter(w)
	defer bw.Flush()
	fmt.Fprintln(bw, strings.Join(cols, sep))
	for _, r := range res.Rows {
		vals := make([]string, len(cols))
		for i, c := range cols {
			vals[i] = cell(r, c)
		}
		fmt.Fprintln(bw, strings.Join(vals, sep))
	}
	return nil
}

func renderTable(w io.Writer, res engine.Result, colorize bool) error {
	cols := columns(res)
	if len(cols) == 0 {
		return nil
	}
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = len(c)
	}
	cells := make([][]string, 0, len(res.Rows))
	for _, r := range res.Rows {
		row := make([]string, len(cols))
		for i, c := range cols {
			row[i] = cell(r, c)
			if len(row[i]) > widths[i] {
				widths[i] = len(row[i])
			}
		}
		cells = append(cells, row)
	}
	bw := bufio.NewWriter(w)
	defer bw.Flush()
	writeRow(bw, cols, cols, widths, false)
	seps := make([]string, len(cols))
	for i := range seps {
		seps[i] = strings.Repeat("-", widths[i])
	}
	writeRow(bw, seps, cols, widths, false)
	for _, row := range cells {
		writeRow(bw, row, cols, widths, colorize)
	}
	return nil
}

// writeRow pads each cell by its raw (uncolored) length, then optionally
// applies level coloring so ANSI codes don't disturb column alignment.
func writeRow(w io.Writer, vals, cols []string, widths []int, colorize bool) {
	parts := make([]string, len(vals))
	for i, v := range vals {
		pad := strings.Repeat(" ", widths[i]-len(v))
		disp := v
		if colorize && i < len(cols) {
			disp = colorCell(cols[i], v, true)
		}
		parts[i] = disp + pad
	}
	fmt.Fprintln(w, strings.Join(parts, "  "))
}
