package engine

import "time"

// Format renders a value as a display string using engine coercion rules.
func Format(v any) string { return toString(v) }

// ParseTime coerces a value (string/time) into a time.Time, reporting success.
func ParseTime(v any) (time.Time, bool) { return toTime(v) }

// Field resolves a possibly dotted key path against a record.
func Field(r Record, key string) (any, bool) { return getField(r, key) }

// Number coerces a value to float64, reporting success (parses numeric strings).
func Number(v any) (float64, bool) { return toNumber(v) }

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
