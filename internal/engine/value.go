package engine

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Record is a single parsed log line.
type Record map[string]any

// Timespan is a KQL timespan value (duration).
type Timespan time.Duration

// datetimeFormats are tried in order when coercing strings to time.Time.
var datetimeFormats = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04",
	"2006-01-02",
	"2006/01/02 15:04:05",
	"01/02/2006 15:04:05",
	time.RFC1123Z,
	time.RFC1123,
}

// getField resolves a possibly dotted key path (e.g. "a.b") against a record.
func getField(r Record, key string) (any, bool) {
	if v, ok := r[key]; ok {
		return v, true
	}
	if !strings.Contains(key, ".") {
		return nil, false
	}
	var cur any = map[string]any(r)
	for _, part := range strings.Split(key, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// toNumber attempts to coerce a value to float64.
func toNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case Timespan:
		return time.Duration(n).Seconds(), true
	case bool:
		if n {
			return 1, true
		}
		return 0, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		if err == nil {
			return f, true
		}
	}
	return 0, false
}

// toTime coerces a value to time.Time.
func toTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, true
	case string:
		s := strings.TrimSpace(t)
		for _, f := range datetimeFormats {
			if tm, err := time.Parse(f, s); err == nil {
				return tm, true
			}
		}
	}
	return time.Time{}, false
}

// toTimespan coerces a value to a Timespan.
func toTimespan(v any) (Timespan, bool) {
	switch t := v.(type) {
	case Timespan:
		return t, true
	case float64:
		return Timespan(time.Duration(t * float64(time.Second))), true
	}
	return 0, false
}

// toString renders a value for display / string comparison.
func toString(v any) string {
	switch s := v.(type) {
	case nil:
		return ""
	case string:
		return s
	case float64:
		if s == math.Trunc(s) && !math.IsInf(s, 0) {
			return strconv.FormatInt(int64(s), 10)
		}
		return strconv.FormatFloat(s, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(s)
	case time.Time:
		return s.Format(time.RFC3339)
	case Timespan:
		return formatTimespan(s)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// formatTimespan renders a timespan in KQL-ish form (d.hh:mm:ss.fff).
func formatTimespan(ts Timespan) string {
	d := time.Duration(ts)
	neg := d < 0
	if neg {
		d = -d
	}
	days := d / (24 * time.Hour)
	d -= days * 24 * time.Hour
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	sec := d / time.Second
	d -= sec * time.Second
	ms := d / time.Millisecond
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	if days > 0 {
		fmt.Fprintf(&b, "%d.", days)
	}
	fmt.Fprintf(&b, "%02d:%02d:%02d", h, m, sec)
	if ms > 0 {
		fmt.Fprintf(&b, ".%03d", ms)
	}
	return b.String()
}

// truthy reports whether a value is logically true.
func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case float64:
		return t != 0
	case string:
		return t != ""
	default:
		return true
	}
}

// compare returns -1, 0, 1 for a<b, a==b, a>b.
// Order of preference: datetime, then numeric, then string.
func compare(a, b any) int {
	if at, aok := a.(time.Time); aok {
		if bt, bok := toTime(b); bok {
			return cmpInt64(at.UnixNano(), bt.UnixNano())
		}
	}
	if bt, bok := b.(time.Time); bok {
		if at, aok := toTime(a); aok {
			return cmpInt64(at.UnixNano(), bt.UnixNano())
		}
	}
	if ats, aok := a.(Timespan); aok {
		if bts, bok := b.(Timespan); bok {
			return cmpInt64(int64(ats), int64(bts))
		}
	}
	an, aok := toNumber(a)
	bn, bok := toNumber(b)
	if aok && bok {
		switch {
		case an < bn:
			return -1
		case an > bn:
			return 1
		default:
			return 0
		}
	}
	return strings.Compare(toString(a), toString(b))
}

func cmpInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// arith evaluates a numeric/temporal binary operation.
func arith(op string, l, r any) any {
	if lt, ok := l.(time.Time); ok {
		if rt, ok := r.(time.Time); ok && op == "-" {
			return Timespan(lt.Sub(rt))
		}
		if rs, ok := toTimespan(r); ok {
			switch op {
			case "+":
				return lt.Add(time.Duration(rs))
			case "-":
				return lt.Add(-time.Duration(rs))
			}
		}
	}
	if ls, ok := l.(Timespan); ok {
		if rs, ok := r.(Timespan); ok {
			switch op {
			case "+":
				return Timespan(int64(ls) + int64(rs))
			case "-":
				return Timespan(int64(ls) - int64(rs))
			case "/":
				if rs != 0 {
					return float64(ls) / float64(rs)
				}
			}
		}
		if rn, ok := toNumber(r); ok {
			switch op {
			case "*":
				return Timespan(int64(float64(ls) * rn))
			case "/":
				if rn != 0 {
					return Timespan(int64(float64(ls) / rn))
				}
			}
		}
	}
	if rs, ok := r.(Timespan); ok {
		if ln, ok := toNumber(l); ok && op == "*" {
			return Timespan(int64(ln * float64(rs)))
		}
	}
	ln, lok := toNumber(l)
	rn, rok := toNumber(r)
	if !lok || !rok {
		return nil
	}
	switch op {
	case "+":
		return ln + rn
	case "-":
		return ln - rn
	case "*":
		return ln * rn
	case "/":
		if rn == 0 {
			return nil
		}
		return ln / rn
	case "%":
		if rn == 0 {
			return nil
		}
		return math.Mod(ln, rn)
	}
	return nil
}
