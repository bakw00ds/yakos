package doctor

import (
	"io"
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
	hookFallbackMaxBytes = 1 << 20
)

// readTail reads at most max bytes from the end of the regular file at path.
// ok is false when it cannot be read or turns out not to be regular after the
// open; truncated reports that bytes before the window were skipped.
func readTail(path string, max int64) (data []byte, truncated, ok bool) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil, false, false
	}
	defer f.Close() //nolint:errcheck
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return nil, false, false
	}
	if st.Size() > max {
		if _, err := f.Seek(st.Size()-max, io.SeekStart); err != nil {
			return nil, false, false
		}
		truncated = true
	}
	b, err := io.ReadAll(io.LimitReader(f, max))
	if err != nil {
		return nil, false, false
	}
	return b, truncated, true
}

// trimLog replaces path with lines via a 0600 temp file in the same directory
// and a rename, so a link swapped in meanwhile is replaced, never written
// through. Best effort.
func trimLog(path string, lines []string) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".hook-fallback-*")
	if err != nil {
		return
	}
	_, werr := tmp.WriteString(strings.Join(lines, "\n") + "\n")
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), path) != nil {
		_ = os.Remove(tmp.Name())
	}
}

// checkHookFallback warns when the fallback wrapper around a Go hook
// (supervisor-stream) absorbed failures in the last 7 days. The wrapper maps a
// non-zero Go exit to exit 0 so the tool call continues, which would otherwise
// hide a crashing hook; it records one line per failure in
// <state>/hook-fallback.log. Reading trims the file to its last 200 lines.
// Silent when the file is absent or has no recent entries.
func (r *runner) checkHookFallback() {
	path := filepath.Join(r.stateDir(), hookguard.FallbackLogName)
	// Lstat, not Stat: a symlink (or FIFO, or device such as /dev/zero) must never
	// be read or trimmed, since the trim would rewrite whatever it points at.
	fi, err := os.Lstat(path)
	if err != nil {
		return
	}
	if !fi.Mode().IsRegular() {
		writeln(r, "Hook fallback")
		r.warn(SectionHookFallback, "%s is not a regular file (symlink, FIFO or device); ignoring it. Remove it so Go hook failures can be recorded", path)
		writeln(r, "")
		return
	}
	data, truncated, ok := readTail(path, hookFallbackMaxBytes)
	if !ok {
		return
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if truncated {
		lines = lines[1:] // the first line of a mid-file read is partial
	}
	if truncated || len(lines) > hookFallbackMaxLines {
		if len(lines) > hookFallbackMaxLines {
			lines = lines[len(lines)-hookFallbackMaxLines:]
		}
		trimLog(path, lines)
	}
	type tally struct {
		n    int
		last string // rc of the newest entry
		ts   string
	}
	cutoff := time.Now().UTC().Add(-hookFallbackWindow)
	by := map[string]*tally{}
	for _, ln := range lines {
		f := strings.Fields(ln) // <ts> <hook> rc=<n> | reason=unusable
		if len(f) != 3 || !(strings.HasPrefix(f[2], "rc=") || f[2] == "reason=unusable") {
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
		e.last, e.ts = f[2], f[0]
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
		r.warn(SectionHookFallback, "%s: the Go hook failed %d time(s) in the last 7 days (last entry %s at %s). rc=N means the Go hook crashed and that call was unsupervised; reason=unusable means the binary was missing or unusable and the bash twin ran instead. Run 'yakos refresh', or 'yakos hook run --impl go %s' to see the error (log: %s)",
			n, e.n, e.last, e.ts, n, path)
	}
	writeln(r, "")
}
