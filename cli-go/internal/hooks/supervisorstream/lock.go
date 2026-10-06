package supervisorstream

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// The lock (K-128).
//
// One lock, <counter>.lock, serialises the escalation counter, the per-session
// run state and the detached wrapper, and is shared with supervisor-stream.sh.
// Holding it means the path exists: it is created exclusively (an O_EXCL file
// create here and in current bash hooks, mkdir in older ones; both are atomic and
// exclude each other) and removed by whoever created it. It protects ONLY the
// read-modify-write of those files. Whatever else a hook does (the budget read,
// the log records, the synthetic findings) happens before the lock is taken or
// after it is dropped, so a hold is two small file writes and a rename.
//
// A waiter backs off exponentially (5 ms doubling to 160 ms, +-50 % jitter) so
// many hooks neither retry in lockstep nor starve the holder, and gives up after
// lockBudget (the hard ceiling). A hook that gives up does not drop its tick: it
// leaves a journal record for the next lock holder (journal.go). A lock older
// than counterLockStale is reaped. Bash twin: _ss_lock_take.
const (
	// counterLockStale is how old an abandoned lock must be before it is reaped
	// (bash: find -mmin +1).
	counterLockStale = time.Minute

	lockBackoffMin = 5 * time.Millisecond
	lockBackoffMax = 160 * time.Millisecond

	// reapEvery, reapAt: the age check runs on the fifth miss and every 8th after
	// it, not on every retry (bash: one find fork, and a lock created a moment ago
	// is essentially never stale).
	reapEvery = 8
	reapAt    = 4
)

// lockBudget is the most a hook ever waits for the lock: the hard ceiling. It is
// a variable only so tests can shorten it.
var lockBudget = 3 * time.Second

// acquireLock takes the lock for the detached wrapper (whose callers retry it).
func acquireLock(lock string) (release func(), ok bool) {
	return acquireLockAs(lock, "wrapper", lockBudget)
}

// acquireLockAs takes the lock within budget; label names the caller in the
// lock-stats seam. release removes the lock.
func acquireLockAs(lock, label string, budget time.Duration) (release func(), ok bool) {
	t0 := time.Now()
	_ = os.MkdirAll(filepath.Dir(lock), 0o755) //nolint:gosec
	deadline := t0.Add(budget)
	bo := lockBackoffMin
	for tries := 0; ; tries++ {
		if tryLock(lock) {
			got := time.Now()
			return func() {
				_ = os.Remove(lock)
				lockStat(lock, label, true, got.Sub(t0), tries, time.Since(got), got)
			}, true
		}
		if !time.Now().Before(deadline) {
			lockStat(lock, label, false, time.Since(t0), tries, 0, time.Time{})
			return nil, false
		}
		// Every retry sleeps and is bounded by the deadline, reaping included,
		// so a stale lock that cannot be removed never spins this loop.
		if tries%reapEvery == reapAt {
			reapStaleLock(lock)
		}
		d := bo/2 + time.Duration(rand.Int63n(int64(bo)+1)) //nolint:gosec // jitter, not security
		if rem := time.Until(deadline); d > rem {
			d = rem
		}
		time.Sleep(d)
		if bo *= 2; bo > lockBackoffMax {
			bo = lockBackoffMax
		}
	}
}

// tryLock creates the lock exclusively. Any existing path (a file, or the
// directory an older hook creates) makes it fail.
func tryLock(lock string) bool {
	f, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// reapStaleLock removes a lock older than counterLockStale. It renames first
// (atomic, one winner) and re-checks the age of what it moved, so a waiter
// never deletes a lock another hook has just created. Mirrors the bash hook.
func reapStaleLock(lock string) {
	fi, err := os.Lstat(lock)
	if err != nil || time.Since(fi.ModTime()) <= counterLockStale {
		return
	}
	moved := fmt.Sprintf("%s.reap.%d", lock, os.Getpid())
	if os.Rename(lock, moved) != nil {
		return
	}
	if mfi, merr := os.Lstat(moved); merr == nil && time.Since(mfi.ModTime()) <= counterLockStale {
		if os.Rename(moved, lock) == nil {
			return // was fresh after all; put it back
		}
	}
	_ = os.RemoveAll(moved)
}

// lockStat is a test seam: with YAKOS_TEST_SEAMS=1 and YAKOS_TEST_LOCK_STATS=1 it
// appends one record per lock take (label, wait, tries, hold and the epoch
// microsecond the lock was got) to .supervisor-lock-stats next to the lock: a
// fixed file in the directory the hook already writes, never a path taken from the
// environment. It reads the process environment only (a project .yakos.yml cannot
// set it) and is a no-op otherwise. Bash twin: the _ss_stats seam.
func lockStat(lock, label string, ok bool, wait time.Duration, tries int, hold time.Duration, got time.Time) {
	if os.Getenv("YAKOS_TEST_SEAMS") != "1" || os.Getenv("YAKOS_TEST_LOCK_STATS") != "1" {
		return
	}
	path := filepath.Join(filepath.Dir(lock), ".supervisor-lock-stats")
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return // a planted link is never followed
	}
	line := fmt.Sprintf("H %d %s FAIL wait_us=%d tries=%d\n", os.Getpid(), label, wait.Microseconds(), tries)
	if ok {
		line = fmt.Sprintf("H %d %s OK wait_us=%d tries=%d hold_us=%d got_us=%d\n",
			os.Getpid(), label, wait.Microseconds(), tries, hold.Microseconds(), got.UnixMicro())
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec
	if err != nil {
		return
	}
	_, _ = f.WriteString(line)
	_ = f.Close()
}

// ---- counter -----------------------------------------------------------------

func readCounter(counterFile string) int {
	data, err := os.ReadFile(counterFile) //nolint:gosec
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// writeCounter stores n through a temp file and a rename, so a reader never
// sees a torn counter. If that cannot be done (a failed temp file or rename) a
// direct write still beats losing the tick; a directory in the counter's place
// fails both. Bash twin: the counter section.
func writeCounter(counterFile string, n int) error {
	_ = os.MkdirAll(filepath.Dir(counterFile), 0o755) //nolint:gosec
	data := []byte(strconv.Itoa(n) + "\n")
	tmp := counterFile + ".tmp." + strconv.Itoa(os.Getpid())
	err := os.WriteFile(tmp, data, 0o644) //nolint:gosec
	if err == nil {
		if err = renameReplace(tmp, counterFile); err == nil {
			return nil
		}
	}
	_ = os.Remove(tmp)
	return os.WriteFile(counterFile, data, 0o644) //nolint:gosec
}

// renameReplace renames tmp over dst. On Windows a reader that has dst open (the
// lock-free peek of the run state, say) makes the rename fail for a moment, so it
// is retried briefly there; POSIX never needs the retry.
func renameReplace(tmp, dst string) error {
	err := os.Rename(tmp, dst)
	if err == nil || runtime.GOOS != "windows" {
		return err
	}
	for i := 1; i <= 8 && err != nil; i++ {
		time.Sleep(time.Duration(i) * 2 * time.Millisecond)
		err = os.Rename(tmp, dst)
	}
	return err
}

// counterResult says how incrementCounter ended.
type counterResult int

const (
	counterOK counterResult = iota
	// counterBusy: the lock was not taken within lockBudget. The increment was
	// journaled for the next lock holder, not dropped.
	counterBusy
	// counterBusyUnjournaled: the same, and the record could not be written (the
	// journal is full or the directory is not writable): the tick is dropped.
	counterBusyUnjournaled
	// counterWriteFailed: the counter could not be written. Bash exits silently
	// here, so Run does too.
	counterWriteFailed
)

// incrementCounter is the K-110 atomic read-increment-write. It takes the same
// lock as supervisor-stream.sh (<counter>.lock), so concurrent bash and Go hooks
// serialise too: every caller gets a unique value and at most one crosses a
// score-every multiple. Increments that other hooks journaled (journal.go) are
// folded in: folded counts them, and the caller covers a crossing whenever a
// multiple of score-every lies in (cur-1-folded, cur].
func incrementCounter(counterFile string) (cur, folded int, res counterResult) {
	release, ok := acquireLockAs(counterFile+".lock", "counter", lockBudget)
	if !ok {
		if !journalTick(counterFile) {
			return 0, 0, counterBusyUnjournaled
		}
		return 0, 0, counterBusy
	}
	defer release()
	adds := foldTicks(counterFile)
	cur = readCounter(counterFile) + len(adds) + 1
	if err := writeCounter(counterFile, cur); err != nil {
		return cur, 0, counterWriteFailed
	}
	removeFiles(adds)
	return cur, len(adds), counterOK
}
