package engine

import (
	"math"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var (
	reCache   = map[string]*regexp.Regexp{}
	reCacheMu sync.RWMutex
)

func compileRegex(pat string) *regexp.Regexp {
	reCacheMu.RLock()
	re, ok := reCache[pat]
	reCacheMu.RUnlock()
	if ok {
		return re
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		re = nil
	}
	reCacheMu.Lock()
	reCache[pat] = re
	reCacheMu.Unlock()
	return re
}

func regexMatch(s, pat string) bool {
	re := compileRegex(pat)
	if re == nil {
		return false
	}
	return re.MatchString(s)
}

// nowFunc is overridable for deterministic tests.
var nowFunc = time.Now

func arg(args []any, i int) any {
	if i < len(args) {
		return args[i]
	}
	return nil
}

// callFunc dispatches a scalar function by (lowercased) name.
func callFunc(name string, args []any) any {
	switch name {
	// ---- string ----
	case "strlen", "string_size":
		return float64(utf8.RuneCountInString(toString(arg(args, 0))))
	case "strcat":
		var b strings.Builder
		for _, a := range args {
			b.WriteString(toString(a))
		}
		return b.String()
	case "strcat_delim":
		if len(args) < 1 {
			return ""
		}
		d := toString(args[0])
		parts := make([]string, 0, len(args)-1)
		for _, a := range args[1:] {
			parts = append(parts, toString(a))
		}
		return strings.Join(parts, d)
	case "substring":
		return fnSubstring(args)
	case "split":
		return fnSplit(args)
	case "replace_string", "replace":
		return strings.ReplaceAll(toString(arg(args, 0)), toString(arg(args, 1)), toString(arg(args, 2)))
	case "tolower":
		return strings.ToLower(toString(arg(args, 0)))
	case "toupper":
		return strings.ToUpper(toString(arg(args, 0)))
	case "trim":
		return fnTrim(args, strings.Trim, strings.TrimSpace)
	case "trim_start":
		return fnTrim(args, strings.TrimLeft, func(s string) string { return strings.TrimLeft(s, " \t\r\n") })
	case "trim_end":
		return fnTrim(args, strings.TrimRight, func(s string) string { return strings.TrimRight(s, " \t\r\n") })
	case "indexof":
		return float64(strings.Index(toString(arg(args, 0)), toString(arg(args, 1))))
	case "reverse":
		return reverseString(toString(arg(args, 0)))
	case "extract":
		return fnExtract(args)
	case "url_decode", "url_encode":
		return toString(arg(args, 0))

	// ---- type ----
	case "toint", "tolong":
		if n, ok := toNumber(arg(args, 0)); ok {
			return math.Trunc(n)
		}
		return nil
	case "todouble", "toreal":
		if n, ok := toNumber(arg(args, 0)); ok {
			return n
		}
		return nil
	case "tostring":
		return toString(arg(args, 0))
	case "tobool", "toboolean":
		return truthy(arg(args, 0))
	case "todatetime":
		if t, ok := toTime(arg(args, 0)); ok {
			return t
		}
		return nil
	case "datetime":
		if t, ok := toTime(arg(args, 0)); ok {
			return t
		}
		return nil
	case "totimespan":
		return fnTotimespan(arg(args, 0))

	// ---- conditional ----
	case "iff", "iif":
		if truthy(arg(args, 0)) {
			return arg(args, 1)
		}
		return arg(args, 2)
	case "case":
		return fnCase(args)
	case "coalesce":
		for _, a := range args {
			if !isNull(a) {
				return a
			}
		}
		return nil
	case "isnull":
		return arg(args, 0) == nil
	case "isnotnull", "notnull":
		return arg(args, 0) != nil
	case "isempty":
		return isEmpty(arg(args, 0))
	case "isnotempty", "notempty":
		return !isEmpty(arg(args, 0))
	case "isnan":
		n, ok := toNumber(arg(args, 0))
		return ok && math.IsNaN(n)
	case "isfinite":
		n, ok := toNumber(arg(args, 0))
		return ok && !math.IsInf(n, 0) && !math.IsNaN(n)

	// ---- math ----
	case "abs":
		return mathUnary(args, math.Abs)
	case "ceiling", "ceil":
		return mathUnary(args, math.Ceil)
	case "floor":
		if len(args) >= 2 {
			return fnBin(args) // floor(value, roundTo) alias of bin
		}
		return mathUnary(args, math.Floor)
	case "round":
		return fnRound(args)
	case "sqrt":
		return mathUnary(args, math.Sqrt)
	case "exp":
		return mathUnary(args, math.Exp)
	case "log", "ln":
		return mathUnary(args, math.Log)
	case "log10":
		return mathUnary(args, math.Log10)
	case "log2":
		return mathUnary(args, math.Log2)
	case "sign":
		return mathUnary(args, func(x float64) float64 {
			switch {
			case x > 0:
				return 1
			case x < 0:
				return -1
			}
			return 0
		})
	case "pow":
		a, aok := toNumber(arg(args, 0))
		b, bok := toNumber(arg(args, 1))
		if aok && bok {
			return math.Pow(a, b)
		}
		return nil
	case "min_of":
		return foldNum(args, math.Min)
	case "max_of":
		return foldNum(args, math.Max)
	case "bin", "floor_bin":
		return fnBin(args)

	// ---- datetime ----
	case "now":
		if len(args) == 1 {
			if ts, ok := toTimespan(arg(args, 0)); ok {
				return nowFunc().Add(time.Duration(ts))
			}
		}
		return nowFunc()
	case "ago":
		if ts, ok := toTimespan(arg(args, 0)); ok {
			return nowFunc().Add(-time.Duration(ts))
		}
		return nil
	case "getyear":
		return datePart(arg(args, 0), "year")
	case "getmonth", "monthofyear":
		return datePart(arg(args, 0), "month")
	case "dayofmonth":
		return datePart(arg(args, 0), "day")
	case "dayofweek":
		return datePart(arg(args, 0), "dayofweek")
	case "dayofyear":
		return datePart(arg(args, 0), "dayofyear")
	case "hourofday":
		return datePart(arg(args, 0), "hour")
	case "datetime_part":
		return datePart(arg(args, 1), strings.ToLower(toString(arg(args, 0))))
	case "startofday":
		return startOf(arg(args, 0), "day")
	case "startofhour":
		return startOf(arg(args, 0), "hour")
	case "startofminute":
		return startOf(arg(args, 0), "minute")
	case "endofday":
		if t, ok := toTime(arg(args, 0)); ok {
			s := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
			return s.Add(24*time.Hour - time.Nanosecond)
		}
		return nil
	case "format_datetime":
		if t, ok := toTime(arg(args, 0)); ok {
			return t.Format(kqlLayout(toString(arg(args, 1))))
		}
		return nil
	case "datetime_diff":
		return fnDatetimeDiff(args)
	case "datetime_add":
		return fnDatetimeAdd(args)

	// ---- dynamic / array ----
	case "array_length":
		if a, ok := arg(args, 0).([]any); ok {
			return float64(len(a))
		}
		return nil
	case "pack_array":
		out := make([]any, len(args))
		copy(out, args)
		return out
	case "gettype":
		return typeName(arg(args, 0))
	}
	return nil
}

func isNull(v any) bool { return v == nil }

func isEmpty(v any) bool {
	if v == nil {
		return true
	}
	if s, ok := v.(string); ok {
		return s == ""
	}
	return false
}

func mathUnary(args []any, f func(float64) float64) any {
	if n, ok := toNumber(arg(args, 0)); ok {
		return f(n)
	}
	return nil
}

func foldNum(args []any, f func(a, b float64) float64) any {
	var acc float64
	set := false
	for _, a := range args {
		if n, ok := toNumber(a); ok {
			if !set {
				acc = n
				set = true
			} else {
				acc = f(acc, n)
			}
		}
	}
	if !set {
		return nil
	}
	return acc
}

func fnRound(args []any) any {
	n, ok := toNumber(arg(args, 0))
	if !ok {
		return nil
	}
	digits := 0
	if len(args) >= 2 {
		if d, ok := toNumber(args[1]); ok {
			digits = int(d)
		}
	}
	p := math.Pow(10, float64(digits))
	return math.Round(n*p) / p
}

func fnSubstring(args []any) any {
	s := []rune(toString(arg(args, 0)))
	start := 0
	if n, ok := toNumber(arg(args, 1)); ok {
		start = int(n)
	}
	if start < 0 {
		start = len(s) + start
	}
	if start < 0 {
		start = 0
	}
	if start > len(s) {
		return ""
	}
	end := len(s)
	if len(args) >= 3 {
		if n, ok := toNumber(args[2]); ok {
			end = start + int(n)
		}
	}
	if end > len(s) {
		end = len(s)
	}
	if end < start {
		end = start
	}
	return string(s[start:end])
}

func fnSplit(args []any) any {
	parts := strings.Split(toString(arg(args, 0)), toString(arg(args, 1)))
	if len(args) >= 3 {
		if n, ok := toNumber(args[2]); ok {
			i := int(n)
			if i >= 0 && i < len(parts) {
				return parts[i]
			}
			return nil
		}
	}
	out := make([]any, len(parts))
	for i, p := range parts {
		out[i] = p
	}
	return out
}

func fnTrim(args []any, cut func(string, string) string, ws func(string) string) any {
	if len(args) >= 2 {
		return cut(toString(args[1]), toString(args[0]))
	}
	return ws(toString(arg(args, 0)))
}

func reverseString(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

func fnExtract(args []any) any {
	re := compileRegex(toString(arg(args, 0)))
	if re == nil {
		return nil
	}
	grp := 1
	if n, ok := toNumber(arg(args, 1)); ok {
		grp = int(n)
	}
	m := re.FindStringSubmatch(toString(arg(args, 2)))
	if m == nil || grp < 0 || grp >= len(m) {
		return nil
	}
	return m[grp]
}

func fnCase(args []any) any {
	i := 0
	for i+1 < len(args) {
		if truthy(args[i]) {
			return args[i+1]
		}
		i += 2
	}
	if i < len(args) {
		return args[i] // else value
	}
	return nil
}

func fnTotimespan(v any) any {
	switch t := v.(type) {
	case Timespan:
		return t
	case float64:
		return Timespan(time.Duration(t * float64(time.Second)))
	case string:
		toks, err := lex(t)
		if err == nil && len(toks) == 2 && toks[0].kind == tTimespan {
			ns := toks[0].val
			var n int64
			for _, c := range ns {
				n = n*10 + int64(c-'0')
			}
			return Timespan(n)
		}
	}
	return nil
}

func fnBin(args []any) any {
	val := arg(args, 0)
	round := arg(args, 1)
	if t, ok := val.(time.Time); ok {
		if ts, ok := toTimespan(round); ok && ts != 0 {
			d := time.Duration(ts)
			binned := t.Truncate(d)
			return binned
		}
		return nil
	}
	v, vok := toNumber(val)
	r, rok := toNumber(round)
	if vok && rok && r != 0 {
		return math.Floor(v/r) * r
	}
	return nil
}

func datePart(v any, part string) any {
	t, ok := toTime(v)
	if !ok {
		return nil
	}
	switch part {
	case "year":
		return float64(t.Year())
	case "month":
		return float64(int(t.Month()))
	case "day":
		return float64(t.Day())
	case "hour":
		return float64(t.Hour())
	case "minute":
		return float64(t.Minute())
	case "second":
		return float64(t.Second())
	case "dayofweek":
		return float64(int(t.Weekday()))
	case "dayofyear":
		return float64(t.YearDay())
	}
	return nil
}

func startOf(v any, unit string) any {
	t, ok := toTime(v)
	if !ok {
		return nil
	}
	switch unit {
	case "day":
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	case "hour":
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location())
	case "minute":
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, t.Location())
	}
	return nil
}

func fnDatetimeDiff(args []any) any {
	part := strings.ToLower(toString(arg(args, 0)))
	a, aok := toTime(arg(args, 1))
	b, bok := toTime(arg(args, 2))
	if !aok || !bok {
		return nil
	}
	d := a.Sub(b)
	switch part {
	case "second":
		return math.Trunc(d.Seconds())
	case "minute":
		return math.Trunc(d.Minutes())
	case "hour":
		return math.Trunc(d.Hours())
	case "day":
		return math.Trunc(d.Hours() / 24)
	case "week":
		return math.Trunc(d.Hours() / 24 / 7)
	case "millisecond":
		return math.Trunc(float64(d.Milliseconds()))
	case "month":
		return float64((a.Year()-b.Year())*12 + int(a.Month()) - int(b.Month()))
	case "year":
		return float64(a.Year() - b.Year())
	}
	return nil
}

func fnDatetimeAdd(args []any) any {
	part := strings.ToLower(toString(arg(args, 0)))
	amt, ok := toNumber(arg(args, 1))
	t, tok := toTime(arg(args, 2))
	if !ok || !tok {
		return nil
	}
	n := int(amt)
	switch part {
	case "second":
		return t.Add(time.Duration(n) * time.Second)
	case "minute":
		return t.Add(time.Duration(n) * time.Minute)
	case "hour":
		return t.Add(time.Duration(n) * time.Hour)
	case "day":
		return t.AddDate(0, 0, n)
	case "week":
		return t.AddDate(0, 0, n*7)
	case "month":
		return t.AddDate(0, n, 0)
	case "year":
		return t.AddDate(n, 0, 0)
	}
	return nil
}

// kqlLayout converts a KQL/.NET datetime format to a Go reference layout.
func kqlLayout(f string) string {
	repl := strings.NewReplacer(
		"yyyy", "2006",
		"yy", "06",
		"MM", "01",
		"dd", "02",
		"HH", "15",
		"mm", "04",
		"ss", "05",
		"fff", "000",
	)
	return repl.Replace(f)
}

func typeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case float64:
		return "real"
	case string:
		return "string"
	case time.Time:
		return "datetime"
	case Timespan:
		return "timespan"
	case []any:
		return "array"
	case map[string]any:
		return "dictionary"
	}
	return "unknown"
}
