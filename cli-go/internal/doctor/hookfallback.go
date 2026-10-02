package doctor

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/hookguard"
)

const (
	hookFallbackWindow   = 7 * 24 * time.Hour
	hookFallbackMaxLines = 200
)

// checkHookFallback warns when the fallback wrapper around a Go hook
// (supervisor-stream) absorbed failures in the last 7 days. The wrapper maps a
// non-zero Go exit to exit 0 so the tool call continues, which would otherwise
// hide a crashing hook; it records one line per failure in
// <state>/hook-fallback.log. Reading trims the file to its last 200 lines.
// Silent when the file is absent or has no recent entries.
func (r *runner) checkHookFallback() {
	path := filepath.Join(r.stateDir(), hookguard.FallbackLogName)
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > hookFallbackMaxLines {
		lines = lines[len(lines)-hookFallbackMaxLines:]
		// Best effort: keep owner-only mode, ignore failure.
		_ = os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	}
	type tally struct {
		n    int
		last string // rc of the newest entry
		ts   string
	}
	cutoff := time.Now().UTC().Add(-hookFallbackWindow)
	by := map[string]*tally{}
	for _, ln := range lines {
		f := strings.Fields(ln) // <ts> <hook> rc=<n>
		if len(f) != 3 || !strings.HasPrefix(f[2], "rc=") {
			continue
		}
		t, err := time.Parse(time.RFC3339, f[0])
		if err != nil || t.Before(cutoff) {
			continue
		}
		e := by[f[1]]
		if e == nil {
			e = &tally{}
			by[f[1]] = e
		}
		e.n++
		e.last, e.ts = strings.TrimPrefix(f[2], "rc="), f[0]
	}
	if len(by) == 0 {
		return
	}
	writeln(r, "Hook fallback")
	names := make([]string, 0, len(by))
	for n := range by {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		e := by[n]
		r.warn(SectionHookFallback, "%s: the Go hook failed %d time(s) in the last 7 days (last rc=%s at %s); the wrapper let the call continue unsupervised. Run 'yakos refresh', or 'yakos hook run --impl go %s' to see the error (log: %s)",
			n, e.n, e.last, e.ts, n, path)
	}
	writeln(r, "")
}
