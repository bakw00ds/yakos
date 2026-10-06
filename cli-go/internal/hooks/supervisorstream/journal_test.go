package supervisorstream

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeRec(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Only complete, well-formed counter records are folded: a symlink, a directory,
// a half-written (empty) file and a wrong value are all left alone.
func TestFoldTicksRules(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(dir, ".supervisor-counter")
	writeRec(t, counter+".add.1.1", "1\n")
	writeRec(t, counter+".add.2.1", "1")        // no newline: still complete
	writeRec(t, counter+".add.3.1", "1\nextra") // only the first line counts
	writeRec(t, counter+".add.4.1", "")         // created, not yet written
	writeRec(t, counter+".add.5.1", "11\n")
	writeRec(t, counter+".add.6.1", "x\n")
	if err := os.Mkdir(counter+".add.7.1", 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "elsewhere")
	writeRec(t, target, "1\n")
	if err := os.Symlink(target, counter+".add.8.1"); err != nil {
		t.Skip("symlinks unavailable")
	}
	writeRec(t, filepath.Join(dir, ".supervisor-counter-other.add.1.1"), "1\n") // another base
	got := foldTicks(counter)
	if len(got) != 3 {
		t.Fatalf("folded %v, want exactly the three complete records", got)
	}
}

func TestFoldTicksIsCapped(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(dir, ".supervisor-counter")
	for i := 0; i < maxFold+25; i++ {
		writeRec(t, fmt.Sprintf("%s.add.1.%d", counter, i), "1\n")
	}
	if got := foldTicks(counter); len(got) != maxFold {
		t.Errorf("folded %d records, want the cap %d", len(got), maxFold)
	}
}

// A gate record carries a high flag and one event line; folding bumps pending,
// bumps high only when flagged, and appends the preview owner-only.
func TestFoldGate(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, ".supervisor-run.s")
	pending := filepath.Join(dir, ".supervisor-pending.s")
	journalGate(state, true, map[string]any{"tool": "Bash", "n": 1})
	journalGate(state, false, map[string]any{"tool": "Edit", "n": 2})
	writeRec(t, state+".add.9.1", "high=2\n{}\n")   // malformed flag
	writeRec(t, state+".add.9.2", "high=1")         // flag without an event: still counted
	writeRec(t, state+".add.9.3", "")               // empty: not yet written
	writeRec(t, state+".sibling.add.1", "high=1\n") // another file name, not a record
	st := runState{pending: 4, high: 1}
	files := foldGate(state, pending, &st)
	if len(files) != 3 {
		t.Fatalf("folded %d records, want 3 (%v)", len(files), files)
	}
	if st.pending != 7 || st.high != 3 {
		t.Errorf("pending=%d high=%d, want 7 and 3", st.pending, st.high)
	}
	b, err := os.ReadFile(pending)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0]+lines[1], `"tool":"Bash"`) || !strings.Contains(lines[0]+lines[1], `"tool":"Edit"`) {
		t.Errorf("pending previews = %q", b)
	}
	if fi, _ := os.Stat(pending); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 { // Windows has no mode bits
		t.Errorf("pending file mode %v, want 0600", fi.Mode().Perm())
	}
	removeFiles(files)
	if left := journalFiles(state); len(left) != 2 {
		t.Errorf("records left after removal = %v, want the empty and malformed ones", left)
	}
}

func TestJournalNamesAreUniquePerCall(t *testing.T) {
	a := journalName("/x/.supervisor-counter")
	b := journalName("/x/.supervisor-counter")
	if a == b {
		t.Errorf("two journal names collide: %s", a)
	}
	if !strings.HasPrefix(a, "/x/.supervisor-counter.add.") {
		t.Errorf("name %q", a)
	}
}

// A fold reads, appends and counts every record under the lock, so it takes a
// bounded few at a time; the rest wait for the next holder.
func TestFoldGateIsCapped(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, ".supervisor-run.s")
	pending := filepath.Join(dir, ".supervisor-pending.s")
	for i := 0; i < maxFoldGate+10; i++ {
		writeRec(t, fmt.Sprintf("%s.add.1.%d", state, i), "high=0\n{\"n\":1}\n")
	}
	st := runState{}
	if got := foldGate(state, pending, &st); len(got) != maxFoldGate || st.pending != maxFoldGate {
		t.Errorf("folded %d records, pending %d; want %d", len(got), st.pending, maxFoldGate)
	}
	if n := strings.Count(readTestFile(t, pending), "\n"); n != maxFoldGate {
		t.Errorf("pending previews = %d, want %d", n, maxFoldGate)
	}
}

// The journal is bounded: once maxWaiting records of a kind wait, a hook drops its
// tick as it did before K-128 (and says so) instead of growing the directory.
func TestWriteJournalIsBounded(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(dir, ".supervisor-counter")
	for i := 0; i < maxWaiting; i++ {
		writeRec(t, fmt.Sprintf("%s.add.1.%d", counter, i), "1\n")
	}
	if journalTick(counter) {
		t.Error("a record was written past the bound")
	}
	if n := len(journalFiles(counter)); n != maxWaiting {
		t.Errorf("records = %d, want %d", n, maxWaiting)
	}
	if !journalTick(filepath.Join(dir, ".supervisor-other")) {
		t.Error("the bound is per kind, not global")
	}
}

func readTestFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
