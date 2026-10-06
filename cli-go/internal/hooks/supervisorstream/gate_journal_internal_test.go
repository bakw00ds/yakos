package supervisorstream

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/statepath"
)

// K-128 gate tests that need the unexported launchGate, to hold the lock between
// the counter and the gate.

func internalGateHook(t *testing.T) (*Hook, *int32, string, string) {
	t.Helper()
	t.Setenv("YAKOS_DISPATCH_LOG", t.TempDir())
	work := t.TempDir()
	launches := new(int32)
	h := &Hook{
		WorkCurrentDir: work, ProjectDir: t.TempDir(), NowFn: time.Now,
		Launch: func(LaunchSpec) error { atomic.AddInt32(launches, 1); return nil },
	}
	return h, launches, work, filepath.Join(work, "logs", "supervisor-stream.ndjson")
}

func gateIn() hooktype.HookInput { return hooktype.HookInput{Env: map[string]string{}} }

func readFileT(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func stateKV(t *testing.T, work, key string) string {
	t.Helper()
	for _, l := range strings.Split(readFileT(t, filepath.Join(work, ".supervisor-run.nosession")), "\n") {
		if v, ok := strings.CutPrefix(l, key+"="); ok {
			return v
		}
	}
	return ""
}

// A gate that cannot take the lock journals its trigger, and the next gate
// holder of the session folds it into pending/high and the pending file.
func TestGateLockBusyJournalsTriggerAndNextHolderFolds(t *testing.T) {
	old := lockBudget
	lockBudget = 150 * time.Millisecond
	defer func() { lockBudget = old }()
	h, _, work, logFile := internalGateHook(t)
	lock := filepath.Join(work, ".supervisor-counter.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var out hooktype.HookOutput
	h.launchGate(&out, gateIn(), nil, logFile, gateCall{high: true, event: map[string]any{"n": 1}, model: "haiku"})
	recs := journalFiles(filepath.Join(work, ".supervisor-run.nosession"))
	if len(recs) != 1 {
		t.Fatalf("gate records = %v, want 1", recs)
	}
	if b, _ := os.ReadFile(recs[0]); string(b) != "high=1\n{\"n\":1}\n" {
		t.Errorf("record = %q", b)
	}
	if _, err := os.Stat(filepath.Join(work, ".supervisor-run.nosession")); err == nil {
		t.Error("state written without the lock")
	}
	if logs := readFileT(t, logFile); !strings.Contains(logs, "launch-state lock busy or unremovable; trigger journaled for the next lock holder") {
		t.Errorf("no WARN:\n%s", logs)
	}

	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	h.launchGate(&out, gateIn(), nil, logFile, gateCall{high: true, event: map[string]any{"n": 2}, model: "haiku"})
	if got := stateKV(t, work, "pending"); got != "2" {
		t.Errorf("pending = %s, want 2 (1 folded + own)", got)
	}
	if got := stateKV(t, work, "high"); got != "2" {
		t.Errorf("high = %s, want 2", got)
	}
	pend := readFileT(t, filepath.Join(work, ".supervisor-pending.nosession"))
	if strings.Count(pend, "\n") != 2 || !strings.Contains(pend, `"n":1`) || !strings.Contains(pend, `"n":2`) {
		t.Errorf("pending previews = %q", pend)
	}
	if left := journalFiles(filepath.Join(work, ".supervisor-run.nosession")); len(left) != 0 {
		t.Errorf("records left after the fold: %v", left)
	}
	if logs := readFileT(t, logFile); !strings.Contains(logs, `"high_risk":true,"pending":2`) {
		t.Errorf("no record with pending 2:\n%s", logs)
	}
}

// pausedGate is a launch gate parked by the pause seam (YAKOS_TEST_SEAMS plus a
// .supervisor-test-pause file in the work directory) after its budget peek and
// read and before it takes the lock. The test changes the world while it is
// parked, then release()s it: no sleeping, no race with the hook.
type pausedGate struct {
	t        *testing.T
	work     string
	logFile  string
	launches *int32
	done     chan struct{}
	reads    *int32 // budget reads so far
	under    *int32 // ... of which ran while the gate lock was held
}

func startPausedGate(t *testing.T, setup func(work string)) *pausedGate {
	t.Helper()
	h, launches, work, logFile := internalGateHook(t)
	t.Setenv("YAKOS_TEST_SEAMS", "1")
	if setup != nil {
		setup(work)
	}
	if err := os.WriteFile(filepath.Join(work, ".supervisor-test-pause"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	p := &pausedGate{t: t, work: work, logFile: logFile, launches: launches, done: make(chan struct{}), reads: new(int32), under: new(int32)}
	budgetReadHook = func() {
		atomic.AddInt32(p.reads, 1)
		if _, err := os.Stat(filepath.Join(work, ".supervisor-counter.lock")); err == nil {
			atomic.AddInt32(p.under, 1)
		}
	}
	t.Cleanup(func() { budgetReadHook = nil })
	go func() {
		defer close(p.done)
		var out hooktype.HookOutput
		h.launchGate(&out, gateIn(), nil, logFile, gateCall{crossed: true, event: map[string]any{"n": 1}, model: "haiku", agent: "supervisor"})
	}()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(work, ".supervisor-test-reached")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the gate never reached the pause")
		}
	}
	if _, err := os.Stat(filepath.Join(work, ".supervisor-counter.lock")); err == nil {
		t.Error("the gate holds the lock while parked: its budget read must come before the lock")
	}
	return p
}

func (p *pausedGate) release() {
	p.t.Helper()
	if err := os.Remove(filepath.Join(p.work, ".supervisor-test-pause")); err != nil {
		p.t.Fatal(err)
	}
	select {
	case <-p.done:
	case <-time.After(10 * time.Second):
		p.t.Fatal("the gate never finished")
	}
}

func (p *pausedGate) statePath() string { return filepath.Join(p.work, ".supervisor-run.nosession") }

func (p *pausedGate) launched() int { return int(atomic.LoadInt32(p.launches)) }

// wantBudgetReads pins how often the budget was read and how often under the lock.
func (p *pausedGate) wantBudgetReads(reads, underLock int) {
	p.t.Helper()
	if r, u := int(atomic.LoadInt32(p.reads)), int(atomic.LoadInt32(p.under)); r != reads || u != underLock {
		p.t.Errorf("budget reads = %d (%d under the lock), want %d (%d under the lock)", r, u, reads, underLock)
	}
}

// ledgerEvent appends one event to the spend ledger, as the dispatch pipeline does.
func ledgerEvent(t *testing.T, typ string, usd float64) {
	t.Helper()
	line := fmt.Sprintf(`{"type":%q,"ts":%q,"agent":"supervisor","usage":{"total_cost_usd":%g}}`+"\n", typ, time.Now().UTC().Format(time.RFC3339), usd)
	f, err := os.OpenFile(statepath.DispatchLog(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
}

func (p *pausedGate) wantRefusedAtTheHardStop() {
	p.t.Helper()
	if n := p.launched(); n != 0 {
		p.t.Errorf("launched %d times at the hard stop: the budget the decision used was stale", n)
	}
	if logs := readFileT(p.t, p.logFile); !strings.Contains(logs, "supervisor budget exhausted; skipping this routine") {
		p.t.Errorf("no budget refusal:\n%s", logs)
	}
}

// K-119 must still hold when the run that made the peek see "in flight" ends
// before the lock is taken: the launch decision then needs the budget, so the
// gate reads it (outside the lock) and retries rather than skipping the check.
func TestGateReadsBudgetWhenTheRunEndsBeforeTheLock(t *testing.T) {
	p := startPausedGate(t, func(work string) {
		ledgerEvent(t, "dispatch_finished", 100) // the limit is spent already
		nowS := strconv.FormatInt(time.Now().Unix(), 10)
		if err := os.WriteFile(filepath.Join(work, ".supervisor-run.nosession"), []byte("start="+nowS+"\nlaunches=1\nlast="+nowS+"\npending=0\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	// a run is in flight at the peek, so no budget was read before the lock; it ends while the gate waits
	if err := os.WriteFile(p.statePath(), []byte("start=\nlaunches=1\nlast=0\npending=0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p.release()
	p.wantRefusedAtTheHardStop()
	p.wantBudgetReads(1, 0) // the gate drops the lock, reads, and retries: the read is outside the lock
}

// B1 (K-128 review): the budget is read before the lock, and a run that starts,
// spends the rest of the limit and ends while the gate reads and waits leaves
// the state showing nothing in flight, so the re-check under the lock must not
// trust the read. The ledger grew, so the gate reads again under the lock.
// A run of ANOTHER session leaves this session's state untouched: only the
// ledger can tell.
func TestGateRereadsTheBudgetWhenAnotherSessionSpendsWhileItWaits(t *testing.T) {
	p := startPausedGate(t, nil) // the pre-lock read saw an empty ledger: budget ok
	ledgerEvent(t, "dispatch_finished", 100)
	p.release()
	p.wantRefusedAtTheHardStop()
	p.wantBudgetReads(2, 1) // the read before the lock, and the one under it
}

// ... and a run of THIS session also leaves its launch counts in the state.
func TestGateRereadsTheBudgetWhenItsOwnSessionSpendsWhileItWaits(t *testing.T) {
	p := startPausedGate(t, nil)
	if err := os.WriteFile(p.statePath(), []byte("start=\nlaunches=1\nlast=0\npending=0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ledgerEvent(t, "dispatch_finished", 100)
	p.release()
	p.wantRefusedAtTheHardStop()
	p.wantBudgetReads(2, 1)
}

// The re-read is a re-read, not a refusal: a ledger that grew without spending
// the limit (a dispatch started, say) still lets the launch through, and so does
// one that did not change at all.
func TestGateLaunchesWhenTheLedgerGrewButTheLimitIsNotSpent(t *testing.T) {
	for _, tc := range []struct {
		name  string
		grow  func(*testing.T)
		reads int // the log grew: one read before the lock and one under it; unchanged: just the first
		under int
	}{
		{"nothing changed", func(*testing.T) {}, 1, 0},
		{"a dispatch started", func(t *testing.T) { ledgerEvent(t, "dispatch_started", 0) }, 2, 1},
		{"a cheap run finished", func(t *testing.T) { ledgerEvent(t, "dispatch_finished", 5) }, 2, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := startPausedGate(t, nil)
			tc.grow(t)
			p.release()
			p.wantBudgetReads(tc.reads, tc.under)
			if n := p.launched(); n != 1 {
				t.Errorf("launched %d times, want 1:\n%s", n, readFileT(t, p.logFile))
			}
			if logs := readFileT(t, p.logFile); strings.Contains(logs, "budget exhausted") {
				t.Errorf("refused although the limit is not spent:\n%s", logs)
			}
		})
	}
}

// The pause seam is a test aid: without YAKOS_TEST_SEAMS a pause file is ignored.
func TestGatePauseSeamNeedsTheToggle(t *testing.T) {
	h, launches, work, logFile := internalGateHook(t)
	t.Setenv("YAKOS_TEST_SEAMS", "")
	if err := os.WriteFile(filepath.Join(work, ".supervisor-test-pause"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	var out hooktype.HookOutput
	h.launchGate(&out, gateIn(), nil, logFile, gateCall{crossed: true, event: map[string]any{"n": 1}, model: "haiku", agent: "supervisor"})
	if el := time.Since(start); el > 5*time.Second {
		t.Errorf("the gate waited %v for a pause file without the toggle", el)
	}
	if _, err := os.Stat(filepath.Join(work, ".supervisor-test-reached")); err == nil {
		t.Error("the seam announced itself without the toggle")
	}
	if n := atomic.LoadInt32(launches); n != 1 {
		t.Errorf("launched %d times, want 1", n)
	}
}

// The trigger records a gate folded are removed only after the state they were
// folded into is on disk (at-least-once): when the state cannot be saved they stay
// for the next holder. A directory in the state's place makes the save fail.
func TestGateKeepsJournalRecordsWhenTheStateCannotBeSaved(t *testing.T) {
	h, _, work, logFile := internalGateHook(t)
	statePath := filepath.Join(work, ".supervisor-run.nosession")
	if err := os.Mkdir(statePath, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := statePath + ".add.9.1"
	if err := os.WriteFile(rec, []byte("high=1\n{\"e\":7}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out hooktype.HookOutput
	h.launchGate(&out, gateIn(), nil, logFile, gateCall{high: true, event: map[string]any{"n": 1}, model: "haiku"})
	if _, err := os.Stat(rec); err != nil {
		t.Errorf("the record was removed although the state it was folded into was never saved: %v", err)
	}
}

// The decision is taken at the time the lock is held: the budget read and the lock
// wait can take seconds, so the clock read at the hook's start would record the
// launch (and judge the interval) against the past.
func TestGateClockIsReadAfterTheLock(t *testing.T) {
	h, _, work, logFile := internalGateHook(t)
	base := time.Unix(1_700_000_000, 0)
	var calls int32
	h.NowFn = func() time.Time {
		if atomic.AddInt32(&calls, 1) == 1 {
			return base // the hook's start
		}
		return base.Add(7 * time.Second) // once the lock is held
	}
	var out hooktype.HookOutput
	h.launchGate(&out, gateIn(), nil, logFile, gateCall{crossed: true, event: map[string]any{"n": 1}, model: "haiku", agent: "supervisor"})
	if got, want := stateKV(t, work, "last"), strconv.FormatInt(base.Add(7*time.Second).Unix(), 10); got != want {
		t.Errorf("last = %s, want %s (the clock read after the lock)", got, want)
	}
}

// A hook that dies while holding the lock must not leave it for a minute: the
// critical section releases through a defer. A Launch that panics stands in for
// any panic between taking the lock and dropping it.
func TestGateReleasesTheLockWhenLaunchPanics(t *testing.T) {
	h, _, work, logFile := internalGateHook(t)
	h.Launch = func(LaunchSpec) error { panic("boom") }
	var out hooktype.HookOutput
	func() {
		defer func() { _ = recover() }()
		h.launchGate(&out, gateIn(), nil, logFile, gateCall{crossed: true, event: map[string]any{"n": 1}, model: "haiku", agent: "supervisor"})
	}()
	if _, err := os.Stat(filepath.Join(work, ".supervisor-counter.lock")); err == nil {
		t.Error("the lock was left behind after a panic")
	}
}

// K-128 review finding 9: the ledger is stamped BEFORE the budget is read. A spend that
// lands while the read is in progress (here: right after the evaluation has read the
// ledger) is invisible to that read, so only a stamp taken before it can notice, under
// the lock, that the read is stale. A stamp taken after the read would include the spend,
// compare equal, and the launch would go ahead on the stale "ok", past the hard stop. The
// pause seam cannot see this (it parks the gate after the read). Bash twin: the stream
// suite's (k19), whose fake CLI appends the spend after it has answered.
func TestGateStampsTheLedgerBeforeTheBudgetRead(t *testing.T) {
	h, launches, _, logFile := internalGateHook(t)
	reads := 0
	budgetEvaluatedHook = func() {
		reads++
		if reads == 1 {
			ledgerEvent(t, "dispatch_finished", 100) // the whole limit is spent just after the first read looked
		}
	}
	t.Cleanup(func() { budgetEvaluatedHook = nil })
	var out hooktype.HookOutput
	h.launchGate(&out, gateIn(), nil, logFile, gateCall{crossed: true, event: map[string]any{"n": 1}, model: "haiku", agent: "supervisor"})
	if n := atomic.LoadInt32(launches); n != 0 {
		t.Errorf("launched %d times at the hard stop: the decision used the stale read (the ledger was stamped after it)", n)
	}
	logs := readFileT(t, logFile)
	if !strings.Contains(logs, "supervisor budget exhausted; skipping this routine") {
		t.Errorf("no budget refusal:\n%s", logs)
	}
	if reads != 2 {
		t.Errorf("budget reads = %d, want 2 (the one before the lock and the re-read under it)", reads)
	}
}
