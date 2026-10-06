package supervisorstream

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// K-110: concurrent increments are serialized: every caller gets a unique
// value 1..N (so at most one crosses a score-every multiple) and none is lost.
func TestIncrementCounterConcurrent(t *testing.T) {
	counter := filepath.Join(t.TempDir(), ".supervisor-counter")
	const n = 40
	var wg sync.WaitGroup
	got := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, folded, res := incrementCounter(counter)
			if res != counterOK || folded != 0 {
				t.Errorf("caller %d: result %v folded %d", i, res, folded)
			}
			got[i] = v
		}(i)
	}
	wg.Wait()
	seen := map[int]bool{}
	for _, v := range got {
		if v < 1 || v > n || seen[v] {
			t.Fatalf("duplicate or out-of-range value %d in %v", v, got)
		}
		seen[v] = true
	}
	if final := readCounter(counter); final != n {
		t.Errorf("final counter = %d, want %d", final, n)
	}
	if _, err := os.Stat(counter + ".lock"); err == nil {
		t.Error("lock left behind")
	}
	if left, _ := filepath.Glob(counter + ".tmp.*"); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

// A lock abandoned by a crashed holder is reaped once stale. Older hooks made it
// a directory, current ones a file; both are recovered.
func TestIncrementCounterReapsStaleLock(t *testing.T) {
	for _, kind := range []string{"dir", "file"} {
		t.Run(kind, func(t *testing.T) {
			counter := filepath.Join(t.TempDir(), ".supervisor-counter")
			lock := counter + ".lock"
			if kind == "dir" {
				if err := os.Mkdir(lock, 0o700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(lock, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-2 * counterLockStale)
			if err := os.Chtimes(lock, old, old); err != nil {
				t.Fatal(err)
			}
			if v, _, res := incrementCounter(counter); res != counterOK || v != 1 {
				t.Fatalf("incrementCounter = %d,%v; want 1,ok", v, res)
			}
		})
	}
}

// A stale lock that is a NON-EMPTY directory (a killed holder plus debris) is
// reaped, the counter proceeds, and nothing spins.
func TestIncrementCounterReapsStaleNonEmptyLock(t *testing.T) {
	counter := filepath.Join(t.TempDir(), ".supervisor-counter")
	lock := counter + ".lock"
	if err := os.MkdirAll(filepath.Join(lock, "debris"), 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * counterLockStale)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if v, _, res := incrementCounter(counter); res != counterOK || v != 1 {
		t.Fatalf("incrementCounter = %d,%v; want 1,ok", v, res)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("recovery took %v", time.Since(start))
	}
}

// A fresh lock held by someone else is never stolen; the caller gives up
// after the bounded wait instead of spinning, the lock is left intact, and the
// increment is journaled, not dropped.
func TestIncrementCounterBoundedWaitOnHeldLock(t *testing.T) {
	counter := filepath.Join(t.TempDir(), ".supervisor-counter")
	lock := counter + ".lock"
	if err := os.Mkdir(lock, 0o700); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, _, res := incrementCounter(counter); res != counterBusy {
		t.Fatalf("took a lock that is held: %v", res)
	}
	if d := time.Since(start); d < 2*time.Second || d > 6*time.Second {
		t.Fatalf("waited %v, want about 3s", d)
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("held lock was removed: %v", err)
	}
	if _, err := os.Stat(counter); err == nil {
		t.Error("the counter was written without the lock")
	}
	recs := journalFiles(counter)
	if len(recs) != 1 {
		t.Fatalf("journal records = %v, want exactly one", recs)
	}
	if b, _ := os.ReadFile(recs[0]); string(b) != "1\n" {
		t.Errorf("journal record = %q, want \"1\\n\"", b)
	}
	if fi, _ := os.Stat(recs[0]); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 { // Windows has no mode bits
		t.Errorf("journal record mode %v, want 0600", fi.Mode().Perm())
	}
}

// The next lock holder folds the journaled increments: none is lost, and the
// caller learns how many it folded so it can cover a crossing.
func TestIncrementCounterFoldsJournaledTicks(t *testing.T) {
	counter := filepath.Join(t.TempDir(), ".supervisor-counter")
	if err := os.WriteFile(counter, []byte("5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".add.1.1", ".add.2.1", ".add.3.9"} {
		if err := os.WriteFile(counter+name, []byte("1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	bad := counter + ".add.9.9"
	if err := os.WriteFile(bad, []byte("2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cur, folded, res := incrementCounter(counter)
	if res != counterOK || cur != 9 || folded != 3 {
		t.Fatalf("cur=%d folded=%d res=%v; want 9, 3, ok (5 + 3 folded + own)", cur, folded, res)
	}
	if got := readCounter(counter); got != 9 {
		t.Errorf("counter file = %d, want 9", got)
	}
	left := journalFiles(counter)
	if len(left) != 1 || left[0] != bad {
		t.Errorf("records left = %v, want only the malformed one", left)
	}
}

// A counter that cannot be written keeps the journaled records (at least once),
// and releases the lock.
func TestIncrementCounterKeepsRecordsWhenWriteFails(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(dir, ".supervisor-counter")
	if err := os.Mkdir(counter, 0o755); err != nil { // the counter path is a directory
		t.Fatal(err)
	}
	rec := counter + ".add.1.1"
	if err := os.WriteFile(rec, []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, res := incrementCounter(counter); res != counterWriteFailed {
		t.Fatalf("result %v, want counterWriteFailed", res)
	}
	if _, err := os.Stat(rec); err != nil {
		t.Errorf("record removed although the counter was not written: %v", err)
	}
	if _, err := os.Stat(counter + ".lock"); err == nil {
		t.Error("lock left behind after a failed write")
	}
	if left, _ := filepath.Glob(counter + ".tmp.*"); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

// reapStaleLock puts back a lock that turned out to be fresh.
func TestReapStaleLockKeepsFreshLock(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "x.lock")
	if err := os.Mkdir(lock, 0o700); err != nil {
		t.Fatal(err)
	}
	reapStaleLock(lock)
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("fresh lock removed: %v", err)
	}
}

// The counter file is replaced by rename, so a concurrent reader only ever sees a
// complete number.
func TestWriteCounterIsAtomic(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a reader that has the file open makes the rename fail there; renameReplace retries it, but this loop reads without any retry")
	}
	counter := filepath.Join(t.TempDir(), ".supervisor-counter")
	if err := writeCounter(counter, 1); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 2; ; i++ {
			select {
			case <-stop:
				return
			default:
				_ = writeCounter(counter, i)
			}
		}
	}()
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(counter)
		if err != nil || !strings.HasSuffix(string(b), "\n") || len(b) < 2 {
			close(stop)
			wg.Wait()
			t.Fatalf("a reader saw a torn counter: %q (err %v)", b, err)
		}
	}
	close(stop)
	wg.Wait()
}

// If the temp file cannot be made (here its name is taken by a directory) a direct
// write still stores the counter: a failed rename must not lose the tick.
func TestWriteCounterFallsBackToADirectWrite(t *testing.T) {
	counter := filepath.Join(t.TempDir(), ".supervisor-counter")
	if err := os.Mkdir(counter+".tmp."+strconv.Itoa(os.Getpid()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeCounter(counter, 5); err != nil {
		t.Fatalf("writeCounter: %v", err)
	}
	if got := readCounter(counter); got != 5 {
		t.Errorf("counter = %d, want 5", got)
	}
}

// A lock check at the fifth miss and every 8th after, not on the first: a lock
// created a moment ago is essentially never stale, and the first reap check would
// be a wasted syscall pair per waiter. A stale lock is still reaped well inside
// the budget.
func TestStaleLockIsReapedAfterTheFifthMiss(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "x.lock")
	if err := os.Mkdir(lock, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * counterLockStale)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	rel, ok := acquireLockAs(lock, "t", time.Second)
	if !ok {
		t.Fatal("a stale lock was not recovered")
	}
	rel()
	if d := time.Since(start); d > 600*time.Millisecond {
		t.Errorf("recovery took %v", d)
	}
}
