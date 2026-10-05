package supervisorstream

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The lock is "the path exists": an O_EXCL file here, mkdir in older hooks. Each
// kind must exclude the other, so a mixed fleet still serialises.
func TestLockExcludesBothKinds(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "a.lock")
	if !tryLock(lock) {
		t.Fatal("could not create a free lock")
	}
	if tryLock(lock) {
		t.Error("took a lock that is held")
	}
	if os.Mkdir(lock, 0o700) == nil {
		t.Error("an older hook's mkdir succeeded over a held file lock")
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(lock, 0o700); err != nil {
		t.Fatal(err)
	}
	if tryLock(lock) {
		t.Error("took a lock an older hook holds as a directory")
	}
}

// Waiters back off: a 5 ms fixed poll would retry ~80 times in 400 ms; the
// exponential schedule (5..160 ms, jittered) needs a fraction of that, and the
// wait still ends at the budget.
func TestAcquireLockBacksOffAndStopsAtBudget(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "held.lock")
	if err := os.Mkdir(lock, 0o700); err != nil {
		t.Fatal(err)
	}
	stats := filepath.Join(dir, ".supervisor-lock-stats")
	t.Setenv("YAKOS_TEST_SEAMS", "1")
	t.Setenv("YAKOS_TEST_LOCK_STATS", "1")
	start := time.Now()
	if _, ok := acquireLockAs(lock, "probe", 400*time.Millisecond); ok {
		t.Fatal("took a held lock")
	}
	if d := time.Since(start); d < 400*time.Millisecond || d > 900*time.Millisecond {
		t.Errorf("waited %v, want ~400ms", d)
	}
	b, err := os.ReadFile(stats)
	if err != nil {
		t.Fatal(err)
	}
	rec := string(b)
	if !strings.Contains(rec, " probe FAIL wait_us=") {
		t.Fatalf("no FAIL record: %q", rec)
	}
	var tries int
	for _, f := range strings.Fields(rec) {
		if v, ok := strings.CutPrefix(f, "tries="); ok {
			tries = atoiOrFail(t, v)
		}
	}
	if tries < 3 || tries > 20 {
		t.Errorf("tries = %d in 400ms; want a backed-off handful (3..20), not a 5 ms poll (~80)", tries)
	}
}

func atoiOrFail(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			t.Fatalf("not a number: %q", s)
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// The stats seam writes one OK record per take with the hold and the epoch the
// lock was got, only when both env vars are set, and only to the fixed file next
// to the lock: a path in the environment is never used.
func TestLockStatsSeam(t *testing.T) {
	dir := t.TempDir()
	stats := filepath.Join(dir, ".supervisor-lock-stats")
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	lock := filepath.Join(dir, "a.lock")

	t.Setenv("YAKOS_TEST_LOCK_STATS", "1")
	rel, ok := acquireLockAs(lock, "x", time.Second)
	if !ok {
		t.Fatal("lock not taken")
	}
	rel()
	if _, err := os.Stat(stats); err == nil {
		t.Fatal("stats written without YAKOS_TEST_SEAMS=1")
	}
	t.Setenv("YAKOS_TEST_LOCK_STATS", elsewhere) // a path is not a switch
	t.Setenv("YAKOS_TEST_SEAMS", "1")
	rel, ok = acquireLockAs(lock, "x", time.Second)
	if !ok {
		t.Fatal("lock not taken")
	}
	rel()
	for _, p := range []string{stats, elsewhere} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("a stats file was written for a path-valued YAKOS_TEST_LOCK_STATS: %s", p)
		}
	}
	t.Setenv("YAKOS_TEST_LOCK_STATS", "1")

	rel, ok = acquireLockAs(lock, "x", time.Second)
	if !ok {
		t.Fatal("lock not taken")
	}
	time.Sleep(20 * time.Millisecond)
	rel()
	b, err := os.ReadFile(stats)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(b))
	if !strings.Contains(line, " x OK wait_us=") || !strings.Contains(line, " hold_us=") || !strings.Contains(line, " got_us=") {
		t.Fatalf("record = %q", line)
	}
	var hold int
	for _, f := range strings.Fields(line) {
		if v, ok := strings.CutPrefix(f, "hold_us="); ok {
			hold = atoiOrFail(t, v)
		}
	}
	if hold < 20000 {
		t.Errorf("hold_us = %d, want >= 20000 (the lock was held 20ms)", hold)
	}
}
