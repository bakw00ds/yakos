package metrics

import (
	"fmt"
	"time"
)

// parseISO parses an ISO 8601 / RFC 3339 timestamp string. It tries several
// common formats that git --format=%aI can emit.
// Internal callers use the unexported alias; external callers (e.g. metricsdash)
// use the exported ParseISO to avoid duplicating the format list.
func parseISO(s string) (time.Time, error) {
	return ParseISO(s)
}

// ParseISO is the exported form of parseISO. It parses an ISO 8601 / RFC 3339
// timestamp string, trying several common formats. Exported so that consumers
// like metricsdash can share the canonical implementation without duplicating
// the format list.
func ParseISO(s string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05-0700",
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse time %q", s)
}
