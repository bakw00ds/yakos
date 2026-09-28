// runstate_internal_test.go — white-box tests for the persistNow rename
// retry and error-surfacing added for K-88 (Windows CI: run.json stuck at
// "pending" forever because a rename failure was silently discarded; see
// work/current/reports/h1-flakes-ci-diag-2026-09-28.md).
//
// This is package workflow (not workflow_test) because it exercises osRename
// (the test seam), renameWithRetry, persistNow, and lastPersistErr directly
// — all unexported. It injects rename failures via the osRename function
// var rather than requiring a real Windows sharing violation, so the retry
// and error-surfacing behavior is proven deterministically on every OS/CI
// runner, not just intermittently on Windows.
package workflow

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// withOSRename temporarily replaces osRename for the duration of the test,
// restoring the original on cleanup. Not safe for parallel tests (osRename
// is a single package-level var) — none of these tests call t.Parallel().
func withOSRename(t *testing.T, fn func(oldpath, newpath string) error) {
	t.Helper()
	orig := osRename
	osRename = fn
	t.Cleanup(func() { osRename = orig })
}

// withShortPersistRenameMaxWait shrinks persistRenameMaxWait for the
// duration of the test so the "gives up after maxWait" path doesn't cost
// real seconds, restoring the original on cleanup.
func withShortPersistRenameMaxWait(t *testing.T, d time.Duration) {
	t.Helper()
	orig := persistRenameMaxWait
	persistRenameMaxWait = d
	t.Cleanup(func() { persistRenameMaxWait = orig })
}

func newTestRunState(t *testing.T) *RunState {
	t.Helper()
	runDir := filepath.Join(t.TempDir(), "run-1")
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatalf("mkdir run dir: %v", err)
	}
	return newRunState("run-1", "my-flow", "hash", "alice", runDir, []Node{{ID: "step1"}})
}

// TestRenameWithRetry_SucceedsAfterTransientFailures proves the retry
// mechanism itself: a rename that fails a few times and then succeeds must
// be reported as success, not as the transient failure.
func TestRenameWithRetry_SucceedsAfterTransientFailures(t *testing.T) {
	var calls atomic.Int32
	const failCount = 3
	withOSRename(t, func(oldpath, newpath string) error {
		n := calls.Add(1)
		if n <= failCount {
			// Simulate a Windows sharing violation: the classic
			// os.LinkError-wrapped errno shape os.Rename actually
			// returns, so any errors.As-style unwrapping in real
			// callers is exercised too.
			return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: errors.New("sharing violation")}
		}
		return os.Rename(oldpath, newpath)
	})

	dir := t.TempDir()
	oldpath := filepath.Join(dir, "a.tmp")
	newpath := filepath.Join(dir, "a")
	if err := os.WriteFile(oldpath, []byte("data"), 0644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if err := renameWithRetry(oldpath, newpath, 2*time.Second); err != nil {
		t.Fatalf("renameWithRetry: expected success after %d transient failures, got: %v", failCount, err)
	}
	if got := calls.Load(); got != failCount+1 {
		t.Errorf("expected exactly %d calls (failCount+1 success), got %d", failCount+1, got)
	}
	if _, err := os.Stat(newpath); err != nil {
		t.Errorf("expected %s to exist after successful retry: %v", newpath, err)
	}
}

// TestRenameWithRetry_GivesUpAfterMaxWait proves the retry is bounded: a
// rename that ALWAYS fails must eventually surface the error rather than
// retry forever, and must do so within roughly maxWait (not instantly, and
// not far beyond it).
func TestRenameWithRetry_GivesUpAfterMaxWait(t *testing.T) {
	wantErr := errors.New("permanent failure")
	withOSRename(t, func(_, _ string) error { return wantErr })

	const maxWait = 40 * time.Millisecond
	start := time.Now()
	err := renameWithRetry("old", "new", maxWait)
	elapsed := time.Since(start)

	if !errors.Is(err, wantErr) {
		t.Fatalf("expected the permanent error to be returned, got: %v", err)
	}
	if elapsed < maxWait {
		t.Errorf("renameWithRetry returned before maxWait elapsed (%v < %v) — did it retry at all?", elapsed, maxWait)
	}
	if elapsed > maxWait+500*time.Millisecond {
		t.Errorf("renameWithRetry took %v, way beyond maxWait=%v — backoff cap not working?", elapsed, maxWait)
	}
}

// TestRunState_PersistNow_RetriesTransientRenameFailure proves persistNow
// itself (not just the low-level helper) survives a transient rename
// failure and still produces a correct, complete run.json.
func TestRunState_PersistNow_RetriesTransientRenameFailure(t *testing.T) {
	rs := newTestRunState(t)
	rs.markRunStarted()

	failedOnce := false
	withOSRename(t, func(oldpath, newpath string) error {
		if !failedOnce {
			failedOnce = true
			return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: errors.New("sharing violation")}
		}
		return os.Rename(oldpath, newpath)
	})

	if err := rs.persistNow(); err != nil {
		t.Fatalf("persistNow: expected the transient failure to be retried away, got: %v", err)
	}
	if !failedOnce {
		t.Fatal("test bug: osRename was never called")
	}

	data, err := os.ReadFile(rs.runJSONPath())
	if err != nil {
		t.Fatalf("read run.json after persistNow: %v", err)
	}
	var onDisk RunState
	if err := json.Unmarshal(data, &onDisk); err != nil {
		t.Fatalf("unmarshal run.json: %v", err)
	}
	if onDisk.Status != RunRunning {
		t.Errorf("run.json status=%q; want %q", onDisk.Status, RunRunning)
	}
}

// TestRunState_PersistNow_SurfacesPersistentRenameFailure is the other
// half of the K-88 fix: when the rename NEVER succeeds (not just a
// transient collision), persistNow must return an error rather than
// silently pretending to have written the file — this is what
// recordPersistErr/LastPersistError depend on to make a genuinely stuck
// persist distinguishable from a merely slow one.
func TestRunState_PersistNow_SurfacesPersistentRenameFailure(t *testing.T) {
	withShortPersistRenameMaxWait(t, 20*time.Millisecond)
	wantErr := errors.New("disk full")
	withOSRename(t, func(_, _ string) error { return wantErr })

	rs := newTestRunState(t)
	err := rs.persistNow()
	if err == nil {
		t.Fatal("persistNow: expected an error when rename always fails, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("persistNow error does not wrap the underlying rename error: %v", err)
	}
}

// TestRunState_Debounce_SurfacesFinalFlushFailure is the end-to-end proof
// of the actual K-88 symptom's fix: a run whose FINAL flush (on
// stopDebounce) fails must be observable via LastPersistError/the
// stopDebounce return value, not indistinguishable from an ordinary
// still-pending run.
func TestRunState_Debounce_SurfacesFinalFlushFailure(t *testing.T) {
	withShortPersistRenameMaxWait(t, 20*time.Millisecond)
	wantErr := errors.New("disk full")
	withOSRename(t, func(_, _ string) error { return wantErr })

	rs := newTestRunState(t)
	rs.startDebounce(t.Context())
	rs.markRunDone(true) // sets dirty=true

	err := rs.stopDebounce()
	if err == nil {
		t.Fatal("stopDebounce: expected the final flush's persistent failure to be returned, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("stopDebounce error does not wrap the underlying rename error: %v", err)
	}
	if got := rs.LastPersistError(); got == nil {
		t.Error("LastPersistError() == nil after a failed final flush; want the failure to persist as observable state")
	}
}

// TestRunState_Debounce_ClearsErrorOnLaterSuccess proves LastPersistError
// is not "sticky" past a subsequent successful persist — a transient
// failure that later resolves must not leave a stale error visible forever.
func TestRunState_Debounce_ClearsErrorOnLaterSuccess(t *testing.T) {
	rs := newTestRunState(t)

	// First flush fails outright (no retry involved here — testing
	// recordPersistErr's bookkeeping, not renameWithRetry).
	rs.recordPersistErr(errors.New("boom"))
	if rs.LastPersistError() == nil {
		t.Fatal("test setup: expected LastPersistError to be non-nil after recordPersistErr(err)")
	}

	rs.recordPersistErr(nil)
	if got := rs.LastPersistError(); got != nil {
		t.Errorf("LastPersistError() = %v; want nil after a later successful persist", got)
	}
}
