package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func decodeObj(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", b, err)
	}
	return m
}

func TestApplyPersistFailure_NonTerminalBecomesFailed(t *testing.T) {
	for _, st := range []string{"pending", "running", "", "bogus"} {
		in := fmt.Sprintf(`{"run_id":"r","status":%q,"workflow_name":"w","nodes":{}}`, st)
		m := decodeObj(t, ApplyPersistFailure([]byte(in), "disk full"))
		if m["status"] != "failed" {
			t.Errorf("status %q: got %v; want failed", st, m["status"])
		}
		if m["error"] != "persist: disk full" || m["persist_error"] != "disk full" {
			t.Errorf("status %q: error=%v persist_error=%v", st, m["error"], m["persist_error"])
		}
		if m["workflow_name"] != "w" || m["run_id"] != "r" {
			t.Errorf("status %q: existing fields not preserved: %v", st, m)
		}
	}
}

// Adversarial: the failure arrives while the run is already terminal on disk.
// Precedence: the terminal on-disk status wins (it did reach disk); the
// failure is still reported additively via persist_error.
func TestApplyPersistFailure_TerminalStatusPreserved(t *testing.T) {
	for _, st := range []string{"completed", "failed", "interrupted"} {
		in := fmt.Sprintf(`{"run_id":"r","status":%q,"nodes":{}}`, st)
		m := decodeObj(t, ApplyPersistFailure([]byte(in), "disk full"))
		if m["status"] != st {
			t.Errorf("terminal %q rewritten to %v", st, m["status"])
		}
		if _, has := m["error"]; has {
			t.Errorf("terminal %q: unexpected error field %v", st, m["error"])
		}
		if m["persist_error"] != "disk full" {
			t.Errorf("terminal %q: persist_error=%v", st, m["persist_error"])
		}
	}
}

func TestApplyPersistFailure_InvalidJSONUnchanged(t *testing.T) {
	for _, in := range []string{"not json", "null", ""} {
		if got := string(ApplyPersistFailure([]byte(in), "x")); got != in {
			t.Errorf("ApplyPersistFailure(%q) = %q; want unchanged", in, got)
		}
	}
}

func TestPersistFailureReason_DropsPaths(t *testing.T) {
	err := fmt.Errorf("workflow: rename run.json: %w", &os.LinkError{Op: "rename", Old: "/secret/a.tmp", New: "/secret/run.json", Err: errors.New("disk full")})
	if got := PersistFailureReason(err); got != "disk full" {
		t.Errorf("got %q", got)
	}
	err = &fs.PathError{Op: "open", Path: "/secret/x", Err: errors.New("permission denied")}
	if got := PersistFailureReason(err); strings.Contains(got, "/secret") {
		t.Errorf("path leaked: %q", got)
	}
	if got := PersistFailureReason(errors.New("/secret/raw")); strings.Contains(got, "/secret") {
		t.Errorf("path leaked from opaque error: %q", got)
	}
}

func TestEngine_PersistError_PermanentFinishedRun(t *testing.T) {
	e := &Engine{}
	rs := newTestRunState(t)
	e.persist.track(rs)
	rs.recordPersistErr(&os.LinkError{Op: "rename", Err: errors.New("disk full")})
	if r, ok := e.PersistError("run-1"); !ok || r != "disk full" {
		t.Fatalf("in-flight: got (%q,%v)", r, ok)
	}
	e.persist.finish(rs, rs.LastPersistError())
	if r, ok := e.PersistError("run-1"); !ok || r != "disk full" {
		t.Fatalf("finished: got (%q,%v)", r, ok)
	}
}

// A transient failure that a later flush repairs must not read as failed,
// both while the run is live and after it finishes.
func TestEngine_PersistError_TransientClears(t *testing.T) {
	e := &Engine{}
	rs := newTestRunState(t)
	e.persist.track(rs)
	rs.recordPersistErr(errors.New("boom"))
	if _, ok := e.PersistError("run-1"); !ok {
		t.Fatal("expected failure while last attempt failed")
	}
	rs.recordPersistErr(nil)
	if r, ok := e.PersistError("run-1"); ok {
		t.Fatalf("live run reported failed after later success: %q", r)
	}
	e.persist.finish(rs, rs.LastPersistError())
	if r, ok := e.PersistError("run-1"); ok {
		t.Fatalf("finished run reported failed after later success: %q", r)
	}
}

func TestEngine_PersistError_FailedSetIsBounded(t *testing.T) {
	e := &Engine{}
	for i := 0; i < maxFailedPersistRuns+10; i++ {
		rs := &RunState{RunID: fmt.Sprintf("r%d", i)}
		e.persist.finish(rs, errors.New("x"))
	}
	if _, ok := e.PersistError("r0"); ok {
		t.Error("oldest entry not evicted")
	}
	if _, ok := e.PersistError(fmt.Sprintf("r%d", maxFailedPersistRuns+9)); !ok {
		t.Error("newest entry missing")
	}
	if len(e.persist.failed) != maxFailedPersistRuns {
		t.Errorf("failed map size %d; want %d", len(e.persist.failed), maxFailedPersistRuns)
	}
}

// Run with -race: persists (flipping between failing and succeeding) run
// concurrently with PersistError readers and the debounce goroutine.
func TestEngine_PersistError_ConcurrentPersistAndRead(t *testing.T) {
	withShortPersistRenameMaxWait(t, time.Millisecond)
	var mu sync.Mutex
	fail := true
	withOSRename(t, func(o, n string) error {
		mu.Lock()
		f := fail
		mu.Unlock()
		if f {
			return errors.New("disk full")
		}
		return os.Rename(o, n)
	})
	e := &Engine{}
	rs := newTestRunState(t)
	e.persist.track(rs)
	rs.startDebounce(t.Context())

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					e.PersistError("run-1")
					rs.LastPersistError()
				}
			}
		}()
	}
	for i := 0; i < 50; i++ {
		mu.Lock()
		fail = i%2 == 0
		mu.Unlock()
		rs.markNodeRunning("step1")
		rs.recordPersistErr(rs.persistNow())
	}
	close(stop)
	wg.Wait()
	mu.Lock()
	fail = false
	mu.Unlock()
	err := rs.stopDebounce()
	e.persist.finish(rs, err)
	if r, ok := e.PersistError("run-1"); ok {
		t.Errorf("final flush succeeded but reported failed: %q", r)
	}
}
