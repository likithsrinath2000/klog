package engine

import (
	"fmt"
	"strconv"
	"strings"
)

// Record is a single parsed log line.
type Record map[string]any

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

// toString renders a value for display / string comparison.
func toString(v any) string {
	switch s := v.(type) {
	case nil:
		return ""
	case string:
		return s
	case float64:
		// Render integers without trailing ".0".
		if s == float64(int64(s)) {
			return strconv.FormatInt(int64(s), 10)
		}
		return strconv.FormatFloat(s, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(s)
	default:
		return fmt.Sprintf("%v", v)
	}
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

// compare returns -1, 0, 1 for a<b, a==b, a>b. Numeric if both coerce, else string.
func compare(a, b any) int {
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
	as, bs := toString(a), toString(b)
	return strings.Compare(as, bs)
}
