package workflow

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"sync"
)

// maxFailedPersistRuns bounds the memory the engine spends remembering
// finished runs whose final persist failed. Oldest entries are evicted first.
const maxFailedPersistRuns = 256

// persistTracker lets API consumers see a persist failure that, by
// definition, never reached run.json: it holds in-flight runs (so the live
// LastPersistError can be consulted) and a bounded set of finished runs whose
// persistence permanently failed. The zero value is ready to use.
type persistTracker struct {
	mu     sync.Mutex
	live   map[string]*RunState
	failed map[string]string // runID -> sanitized reason
	order  []string          // insertion order of failed, for eviction
}

func (t *persistTracker) track(rs *RunState) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.live == nil {
		t.live = make(map[string]*RunState)
	}
	t.live[rs.RunID] = rs
}

// finish stops tracking rs as in-flight. finalErr is the outcome of the
// final flush (see PersistFailureReason for the "permanent" definition).
func (t *persistTracker) finish(rs *RunState, finalErr error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.live, rs.RunID)
	if finalErr == nil {
		delete(t.failed, rs.RunID)
		return
	}
	if t.failed == nil {
		t.failed = make(map[string]string)
	}
	if _, ok := t.failed[rs.RunID]; !ok {
		t.order = append(t.order, rs.RunID)
		if len(t.order) > maxFailedPersistRuns {
			delete(t.failed, t.order[0])
			t.order = t.order[1:]
		}
	}
	t.failed[rs.RunID] = PersistFailureReason(finalErr)
}

func (t *persistTracker) lookup(runID string) (string, bool) {
	t.mu.Lock()
	rs := t.live[runID]
	reason, ok := t.failed[runID]
	t.mu.Unlock()
	if rs != nil {
		// Live state wins: a later successful flush clears the failure.
		if err := rs.LastPersistError(); err != nil {
			return PersistFailureReason(err), true
		}
		return "", false
	}
	return reason, ok
}

// PersistError reports whether run runID's persistence has permanently
// failed, with a client-safe reason.
//
// "Permanently failed" means: the most recent persist attempt failed (after
// persistNow's own bounded rename retry, see persistRenameMaxWait) and no
// later attempt has succeeded. For an in-flight run that is
// RunState.LastPersistError() != nil at the moment of the call; a transient
// failure that a later flush repairs is therefore never reported. For a
// finished run it is the outcome of the final flush. Persist is
// debounce-driven and does not retry on its own, so a mid-run failure stays
// reported until the next successful flush (any state change, or the final
// flush at run end).
//
// Only runs executed by this Engine instance since process start are known;
// after a daemon restart run.json (or crash reconciliation) is the only
// record. Finished-run failures are kept for the newest
// maxFailedPersistRuns runs.
func (e *Engine) PersistError(runID string) (reason string, failed bool) {
	return e.persist.lookup(runID)
}

// PersistFailureReason renders err as a short reason safe to return to API
// consumers. Filesystem paths embedded in *fs.PathError / *os.LinkError are
// dropped, leaving only the underlying OS error text.
func PersistFailureReason(err error) string {
	if err == nil {
		return ""
	}
	var le *os.LinkError
	if errors.As(err, &le) && le.Err != nil {
		return le.Err.Error()
	}
	var pe *fs.PathError
	if errors.As(err, &pe) && pe.Err != nil {
		return pe.Err.Error()
	}
	return "run state could not be saved"
}

// ApplyPersistFailure overlays a permanent persist failure onto a run.json
// blob for API output. It is additive and backwards compatible:
//
//   - persist_error is always set to reason.
//   - If the on-disk status is non-terminal (anything but completed, failed
//     or interrupted — typically the stale "pending"/"running" left behind by
//     the failed write), status becomes "failed" and error becomes
//     "persist: <reason>".
//   - A terminal on-disk status is preserved: that state did reach disk, so
//     it is the truth, and rewriting "completed" to "failed" would mislead.
//
// Invalid JSON is returned unchanged.
func ApplyPersistFailure(runJSON []byte, reason string) []byte {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(runJSON, &m); err != nil || m == nil {
		return runJSON
	}
	var status string
	if raw, ok := m["status"]; ok {
		_ = json.Unmarshal(raw, &status)
	}
	set := func(k, v string) { b, _ := json.Marshal(v); m[k] = b }
	set("persist_error", reason)
	switch RunStatus(status) {
	case RunCompleted, RunFailed, RunInterrupted:
	default:
		set("status", string(RunFailed))
		set("error", "persist: "+reason)
	}
	out, err := json.Marshal(m)
	if err != nil {
		return runJSON
	}
	return out
}
