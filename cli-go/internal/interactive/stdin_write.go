package interactive

// stdin_write.go — shared bounded-write result resolution for Session and
// SDKEngine's stdin writes.
//
// Both engines write a frame to the child process's stdin from a background
// goroutine (so a stuck pipe can't hang the caller) and then select on three
// signals: the write's own completion, an engine-closed signal, and a timeout.
//
// # The race this closes
//
// The write's completion arrives on a buffered (capacity 1) channel filled by
// a separate goroutine. If the engine closes (or the timeout fires) at
// nearly the same instant the write actually completes, BOTH the "done" case
// and the "closed"/"timeout" case can be ready when the select statement
// evaluates. Go's select picks uniformly at random among simultaneously
// ready cases — so a write that genuinely succeeded (or failed with its own,
// more specific error) could be silently discarded and reported as a
// generic "closed during write" or "timed out" error instead, even though
// the real outcome was already known.
//
// awaitStdinWrite avoids that by re-checking done (non-blocking) on the
// closed/timeout branches before reporting them, so a write result that is
// already available is always authoritative.

import "time"

// awaitStdinWrite blocks until the write behind done completes, closedC
// fires, or timeoutC fires — whichever the runtime observes first — but
// always prefers an already-available write result over a coincidentally
// simultaneous close/timeout signal.
//
// done must be buffered with capacity >= 1 (the writer goroutine must never
// block sending its result). Exactly one of the three return groups is
// meaningful: (err, false, false) — the write completed (err may be nil);
// (_, true, false) — timeoutC fired and no write result was available;
// (_, false, true) — closedC fired and no write result was available.
func awaitStdinWrite(done <-chan error, closedC <-chan struct{}, timeoutC <-chan time.Time) (err error, timedOut, closed bool) {
	select {
	case err := <-done:
		return err, false, false
	case <-timeoutC:
		if err, ok := peekWriteResult(done); ok {
			return err, false, false
		}
		return nil, true, false
	case <-closedC:
		if err, ok := peekWriteResult(done); ok {
			return err, false, false
		}
		return nil, false, true
	}
}

// peekWriteResult does a non-blocking read of done, reporting ok=false when
// no result is available yet.
func peekWriteResult(done <-chan error) (err error, ok bool) {
	select {
	case err := <-done:
		return err, true
	default:
		return nil, false
	}
}
