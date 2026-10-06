package modelreg

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// The overlay and the project's .yakos.yml are decoded into generic values and
// read key by key, the way internal/projectcfg reads the routing keys: a mistyped
// entry is dropped with a warning and its valid siblings survive. A file that is
// not YAML at all yields nothing.

// asStringMap normalizes a decoded mapping. yaml.v3 yields map[string]any when
// every key is a string and map[any]any otherwise.
func asStringMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, val := range m {
			ks, ok := k.(string)
			if !ok {
				return nil, false
			}
			out[ks] = val
		}
		return out, true
	}
	return nil, false
}

// sortedKeys returns the keys of m in order, so warnings and iteration are
// stable from run to run (Go randomizes map order).
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// asFloat reads a YAML number. Strings, booleans and everything else are not
// numbers here: a price written "3" in quotes is a mistake worth a warning.
func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

// firstLine collapses an error text to its first line and makes it plain text:
// no control or escape sequences (an OS error carries the project's path, and a
// directory can be named with anything), at most 200 runes. A warning never spans
// the log and never moves the operator's terminal.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return sanitizeText(s, 200)
}

// maxWarningsPerSource bounds how many problems one file reports. A hostile file
// with thousands of bad entries would otherwise print thousands of lines on every
// run in its directory.
const maxWarningsPerSource = 25

// warnList collects the warnings of one source, keeps the first
// maxWarningsPerSource and counts the rest.
type warnList struct {
	prefix  string
	items   []string
	dropped int
}

func (w *warnList) add(format string, a ...any) {
	if len(w.items) >= maxWarningsPerSource {
		w.dropped++
		return
	}
	w.items = append(w.items, w.prefix+fmt.Sprintf(format, a...))
}

// list returns the kept warnings and, when some were dropped, one line saying how
// many.
func (w *warnList) list() []string {
	out := append([]string(nil), w.items...)
	if w.dropped > 0 {
		out = append(out, fmt.Sprintf("%sand %d more problems not shown", w.prefix, w.dropped))
	}
	return out
}

// quote renders an untrusted key for a warning: %q escapes control characters,
// and the length is bounded.
func quote(s string) string {
	if len(s) > 64 {
		s = s[:64] + "..."
	}
	return fmt.Sprintf("%q", s)
}

// joinWords renders a word list for a warning.
func joinWords(words []string) string { return strings.Join(words, ", ") }
