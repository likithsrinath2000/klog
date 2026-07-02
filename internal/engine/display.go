package engine

import "time"

// Format renders a value as a display string using engine coercion rules.
func Format(v any) string { return toString(v) }

// DisplayValue converts engine-native values (datetime, timespan) into
// JSON-friendly representations for output.
func DisplayValue(v any) any {
	switch t := v.(type) {
	case time.Time:
		return t.Format(time.RFC3339)
	case Timespan:
		return formatTimespan(t)
	}
	return v
}
