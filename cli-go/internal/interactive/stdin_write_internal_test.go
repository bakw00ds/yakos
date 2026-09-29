// stdin_write_internal_test.go — white-box proof for the write/close(-or-
// timeout) priority race described in stdin_write.go and fixed by
// awaitStdinWrite.
//
// This is in-package (not interactive_test) because awaitStdinWrite is
// unexported.
//
// # Why this is deterministic, not a stress test
//
// The underlying flake (K-88; see
// work/current/reports/h1-flakes-2026-09-28.md) is a CPU-contention-
// triggered scheduling race in CI that could not be reproduced locally even
// under heavy artificial load (14x busy-loop oversubscription,
// -race -count=200, GOMAXPROCS variations). Rather than rely on timing to
// hit the race window, these tests construct the exact state select() sees
// when the race fires — done already holding a result AND closedC/timeoutC
// already signaled before awaitStdinWrite is even called — so both cases are
// unambiguously ready from the first instant. Go's select picks uniformly at
// random among simultaneously-ready cases (language spec), so without the
// fix roughly half of many repeated trials would report the wrong outcome;
// with the fix, every trial must prefer the write's real result.
package interactive

import (
	"errors"
	"testing"
	"time"
)

// TestAwaitStdinWrite_ClosedRacingCompletedWrite proves that when the write
// has already succeeded AND the engine is already closed by the time
// awaitStdinWrite runs (both cases ready simultaneously), the write's real
// result always wins — never the generic "closed" outcome.
func TestAwaitStdinWrite_ClosedRacingCompletedWrite(t *testing.T) {
	const iterations = 200
	for i := 0; i < iterations; i++ {
		done := make(chan error, 1)
		done <- nil // the write already completed successfully

		closedC := make(chan struct{})
		close(closedC) // the engine closed at (apparently) the same instant

		timeoutC := make(chan time.Time) // never fires

		err, timedOut, closed := awaitStdinWrite(done, closedC, timeoutC)
		if timedOut || closed {
			t.Fatalf("iteration %d: expected the completed write to win; got err=%v timedOut=%v closed=%v",
				i, err, timedOut, closed)
		}
		if err != nil {
			t.Fatalf("iteration %d: expected nil error from the completed write, got %v", i, err)
		}
	}
}

// TestAwaitStdinWrite_ClosedRacingFailedWrite is the same race, but the write
// itself failed. The write's own (more specific) error must still win over
// the generic "closed" outcome.
func TestAwaitStdinWrite_ClosedRacingFailedWrite(t *testing.T) {
	wantErr := errors.New("boom")
	const iterations = 200
	for i := 0; i < iterations; i++ {
		done := make(chan error, 1)
		done <- wantErr

		closedC := make(chan struct{})
		close(closedC)

		timeoutC := make(chan time.Time)

		err, timedOut, closed := awaitStdinWrite(done, closedC, timeoutC)
		if timedOut || closed {
			t.Fatalf("iteration %d: expected the failed write's own result to win; got err=%v timedOut=%v closed=%v",
				i, err, timedOut, closed)
		}
		if !errors.Is(err, wantErr) {
			t.Fatalf("iteration %d: expected %v, got %v", i, wantErr, err)
		}
	}
}

// TestAwaitStdinWrite_TimeoutRacingCompletedWrite is the same race for the
// timeout branch instead of the closed branch.
func TestAwaitStdinWrite_TimeoutRacingCompletedWrite(t *testing.T) {
	const iterations = 200
	for i := 0; i < iterations; i++ {
		done := make(chan error, 1)
		done <- nil

		closedC := make(chan struct{}) // never closes

		timeoutC := make(chan time.Time, 1)
		timeoutC <- time.Now() // the timeout also "fired" at the same instant

		err, timedOut, closed := awaitStdinWrite(done, closedC, timeoutC)
		if timedOut || closed {
			t.Fatalf("iteration %d: expected the completed write to win; got err=%v timedOut=%v closed=%v",
				i, err, timedOut, closed)
		}
		if err != nil {
			t.Fatalf("iteration %d: expected nil error, got %v", i, err)
		}
	}
}

// TestAwaitStdinWrite_GenuineCloseNoResultYet is the control case: when the
// write goroutine genuinely has not produced a result yet, closed must still
// be reported (the fix must not suppress real closed/timeout outcomes).
func TestAwaitStdinWrite_GenuineCloseNoResultYet(t *testing.T) {
	done := make(chan error, 1) // never written to
	closedC := make(chan struct{})
	close(closedC)
	timeoutC := make(chan time.Time)

	err, timedOut, closed := awaitStdinWrite(done, closedC, timeoutC)
	if err != nil || timedOut || !closed {
		t.Fatalf("expected closed=true with no write result; got err=%v timedOut=%v closed=%v", err, timedOut, closed)
	}
}

// TestAwaitStdinWrite_GenuineTimeoutNoResultYet mirrors the above for the
// timeout branch.
func TestAwaitStdinWrite_GenuineTimeoutNoResultYet(t *testing.T) {
	done := make(chan error, 1)
	closedC := make(chan struct{})
	timeoutC := make(chan time.Time, 1)
	timeoutC <- time.Now()

	err, timedOut, closed := awaitStdinWrite(done, closedC, timeoutC)
	if err != nil || closed || !timedOut {
		t.Fatalf("expected timedOut=true with no write result; got err=%v timedOut=%v closed=%v", err, timedOut, closed)
	}
}

// TestAwaitStdinWrite_PlainSuccess is the ordinary, uncontended path: done
// fires first, nothing else is ready.
func TestAwaitStdinWrite_PlainSuccess(t *testing.T) {
	done := make(chan error, 1)
	done <- nil
	closedC := make(chan struct{})
	timeoutC := make(chan time.Time)

	err, timedOut, closed := awaitStdinWrite(done, closedC, timeoutC)
	if err != nil || timedOut || closed {
		t.Fatalf("expected plain success; got err=%v timedOut=%v closed=%v", err, timedOut, closed)
	}
}

// TestAwaitStdinWrite_WriteCompletesJustAfterClose models the CI flake: the
// child consumed the frame and exited, so closed fired, and only then does
// the (descheduled) writer goroutine report its successful write. The write
// must win.
func TestAwaitStdinWrite_WriteCompletesJustAfterClose(t *testing.T) {
	done := make(chan error, 1)
	closedC := make(chan struct{})
	close(closedC)
	go func() {
		time.Sleep(20 * time.Millisecond)
		done <- nil
	}()

	err, timedOut, closed := awaitStdinWrite(done, closedC, make(chan time.Time))
	if err != nil || timedOut || closed {
		t.Fatalf("expected the late write result to win; got err=%v timedOut=%v closed=%v", err, timedOut, closed)
	}
}
