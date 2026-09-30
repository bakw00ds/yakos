package supervisorstream

import (
	"os"
	"path/filepath"
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
			v, ok := incrementCounter(counter)
			if !ok {
				t.Errorf("caller %d: lock not acquired", i)
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
		t.Error("lock dir left behind")
	}
}

// A lock abandoned by a crashed holder is reaped once stale.
func TestIncrementCounterReapsStaleLock(t *testing.T) {
	counter := filepath.Join(t.TempDir(), ".supervisor-counter")
	lock := counter + ".lock"
	if err := os.Mkdir(lock, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * counterLockStale)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	if v, ok := incrementCounter(counter); !ok || v != 1 {
		t.Fatalf("incrementCounter = %d,%v; want 1,true", v, ok)
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
	if v, ok := incrementCounter(counter); !ok || v != 1 {
		t.Fatalf("incrementCounter = %d,%v; want 1,true", v, ok)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("recovery took %v", time.Since(start))
	}
}

// A fresh lock held by someone else is never stolen; the caller gives up
// after the bounded wait instead of spinning, and the lock is left intact.
func TestIncrementCounterBoundedWaitOnHeldLock(t *testing.T) {
	counter := filepath.Join(t.TempDir(), ".supervisor-counter")
	lock := counter + ".lock"
	if err := os.Mkdir(lock, 0o700); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, ok := incrementCounter(counter); ok {
		t.Fatal("took a lock that is held")
	}
	if d := time.Since(start); d < 2*time.Second || d > 6*time.Second {
		t.Fatalf("waited %v, want about 3s", d)
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("held lock was removed: %v", err)
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
