package consoleui

// chat_outcome.go: how a chat dispatch ended, for the fleet.finished event.
//
// Who writes it. The dispatch goroutine writes it where a dispatch fails, and the
// chunk callback writes it from every summary. A one-shot turn calls the callback
// from RunStream, which has returned before the dispatch goroutine reads the
// outcome. An interactive engine calls it from its own read loop instead, and that
// loop can still be handing over a final summary while the session closes and the
// dispatch goroutine, woken by the close, returns and publishes fleet.finished
// (neither engine waits for its read loop before it signals the close). The two
// goroutines have no other ordering between them, so the outcome has a lock of its
// own: before it did, `go test -race` found the callback's write against the
// deferred read of exitCode and exitStatus (K-136).

import (
	"sync"

	"github.com/bakw00ds/yakos/internal/dispatch"
)

// dispatchOutcome is the status and exit code a dispatch ends with. The zero value is
// not usable; use newDispatchOutcome. It is safe for concurrent use.
type dispatchOutcome struct {
	mu     sync.Mutex
	status dispatch.SessionStatus
	code   int
}

// newDispatchOutcome is a dispatch that has finished with exit code 0, until something
// says otherwise.
func newDispatchOutcome() *dispatchOutcome {
	return &dispatchOutcome{status: dispatch.StatusFinished}
}

// fail records that the dispatch failed with code (-1 for an error with no exit code of
// the harness's own).
func (o *dispatchOutcome) fail(code int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.status = dispatch.StatusFailed
	o.code = code
}

// summary records a turn's exit code from its summary chunk. The code is the latest
// turn's, and a non-zero one marks the dispatch failed for good: a later successful
// turn does not take the failure back.
func (o *dispatchOutcome) summary(code int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.code = code
	if code != 0 {
		o.status = dispatch.StatusFailed
	}
}

// get returns the status and exit code as they stand.
func (o *dispatchOutcome) get() (dispatch.SessionStatus, int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.status, o.code
}
