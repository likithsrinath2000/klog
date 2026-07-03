// Package engine implements a KQL-lite query engine over records (parsed log
// lines). A query is a pipeline of tabular operators separated by "|":
//
//	where level=="ERROR" | summarize n=count() by service | sort by n desc
//
// Supported operators include where, extend, project (and project-away/-keep/
// -rename/-reorder), summarize, count, distinct, sort, top, take, sample,
// sample-distinct, serialize, parse, mv-expand, getschema, print, union, join,
// lookup and render.
//
// Expressions support arithmetic, comparisons, and/or/not, in, between, has,
// matches, string operators, datetime and timespan literals, and a library of
// scalar functions (see funcs.go). Values are dynamically typed: nil, bool,
// float64, string, time.Time, Timespan, arrays and objects.
//
// Compile parses a query into a Pipeline; Pipeline.Run applies it to rows and
// returns a Result. A trailing render stage attaches a ChartSpec that callers
// can turn into a chart.
package engine
