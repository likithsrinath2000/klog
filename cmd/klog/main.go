package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/likithsrinath2000/klog/internal/engine"
)

var version = "0.1.0"

const usage = `klog %s - a KQL-lite query engine for JSON/NDJSON logs

Usage:
  klog [flags] '<query>' [file ...]
  cat app.log | klog '<query>'

Query is a '|'-separated pipeline of operators:
  where <expr>              keep rows matching a boolean expression
  project a, b=oldname      select / rename fields
  summarize <aggs> by <k>   group and aggregate (count,sum,avg,min,max,dcount)
  sort by <field> [desc]    order rows
  take <n>                  keep first n rows
  count                     count rows

Expression operators: == != > < >= <= and or not ( )
                       contains startswith endswith

Examples:
  klog 'where level=="ERROR" | summarize n=count() by service | sort by n desc' app.log
  klog 'summarize avg(ms) by service | where avg_ms > 100' app.log
  klog 'where service=="auth" and ms > 500 | project ts, ms | take 20' app.log

Flags:
`

func main() {
	out := flag.String("o", "table", "output format: table|json|tsv")
	flag.StringVar(out, "output", "table", "output format: table|json|tsv")
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

	if err := render(os.Stdout, res, *out); err != nil {
		fatal("%v", err)
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
	valueFlags := map[string]bool{"-o": true, "--output": true}
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

func render(w io.Writer, res engine.Result, format string) error {
	switch format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(res.Rows)
	case "tsv":
		return renderDelim(w, res, "\t")
	case "table":
		return renderTable(w, res)
	default:
		return fmt.Errorf("unknown output format %q", format)
	}
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
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return trimFloat(t)
	case bool:
		return fmt.Sprintf("%v", t)
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

func trimFloat(f float64) string {
	s := fmt.Sprintf("%.4f", f)
	s = strings.TrimRight(s, "0")
	return strings.TrimRight(s, ".")
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

func renderTable(w io.Writer, res engine.Result) error {
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
	writeRow(bw, cols, widths)
	seps := make([]string, len(cols))
	for i := range seps {
		seps[i] = strings.Repeat("-", widths[i])
	}
	writeRow(bw, seps, widths)
	for _, row := range cells {
		writeRow(bw, row, widths)
	}
	return nil
}

func writeRow(w io.Writer, vals []string, widths []int) {
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = v + strings.Repeat(" ", widths[i]-len(v))
	}
	fmt.Fprintln(w, strings.Join(parts, "  "))
}
