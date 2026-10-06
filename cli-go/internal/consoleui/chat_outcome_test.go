package consoleui

// chat_outcome_test.go: dispatchOutcome is written by the chunk callback on an
// interactive engine's goroutine and by the dispatch goroutine, which also reads it for
// fleet.finished (K-136). Its rules are tested directly, and so is its lock: the
// concurrent test fails under `go test -race` the moment an access is unlocked, which is
// how the race was first found, in CI, as a write from the engine's read loop against
// the dispatch goroutine's deferred read.

import (
	"sync"
	"testing"

	"github.com/bakw00ds/yakos/internal/dispatch"
)

func TestDispatchOutcome_Rules(t *testing.T) {
	o := newDispatchOutcome()
	if st, code := o.get(); st != dispatch.StatusFinished || code != 0 {
		t.Fatalf("a new outcome is finished with code 0: %v %d", st, code)
	}
	o.summary(0)
	if st, code := o.get(); st != dispatch.StatusFinished || code != 0 {
		t.Fatalf("a successful summary keeps it finished: %v %d", st, code)
	}
	o.summary(3)
	if st, code := o.get(); st != dispatch.StatusFailed || code != 3 {
		t.Fatalf("a non-zero summary fails it with that code: %v %d", st, code)
	}
	o.summary(0)
	if st, code := o.get(); st != dispatch.StatusFailed || code != 0 {
		t.Fatalf("a later successful turn takes the code but not the failure back: %v %d", st, code)
	}
	o.fail(-1)
	if st, code := o.get(); st != dispatch.StatusFailed || code != -1 {
		t.Fatalf("an error path fails it with -1: %v %d", st, code)
	}
	p := newDispatchOutcome()
	p.fail(-1)
	if st, code := p.get(); st != dispatch.StatusFailed || code != -1 {
		t.Fatalf("%v %d", st, code)
	}
}

// The callback's goroutine and the dispatch goroutine use it at the same time.
func TestDispatchOutcome_IsSafeAcrossGoroutines(t *testing.T) {
	o := newDispatchOutcome()
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { // the engine's read loop delivering summaries
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			o.summary(i % 3)
		}
	}()
	go func() { // the dispatch goroutine on an error path
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			o.fail(-1)
		}
	}()
	go func() { // the dispatch goroutine's deferred fleet.finished
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			_, _ = o.get()
		}
	}()
	wg.Wait()
	if st, _ := o.get(); st != dispatch.StatusFailed {
		t.Fatalf("a failure is never taken back: %v", st)
	}
}
