package supervisorstream

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/budget"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
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

// K-119 must still hold when the run that made the peek see "in flight" ends
// before the lock is taken: the launch decision then needs the budget, so the
// gate reads it (outside the lock) and retries rather than skipping the check.
func TestGateReadsBudgetWhenTheRunEndsBeforeTheLock(t *testing.T) {
	h, launches, work, logFile := internalGateHook(t)
	stateDir := os.Getenv("YAKOS_DISPATCH_LOG")
	if err := budget.SetLimit(stateDir, "supervisor", 100, budget.Monthly); err != nil {
		t.Fatal(err)
	}
	spend := `{"type":"dispatch_finished","ts":"` + time.Now().UTC().Format(time.RFC3339) + `","agent":"supervisor","usage":{"total_cost_usd":100}}` + "\n"
	if err := os.WriteFile(filepath.Join(stateDir, "dispatch-log.ndjson"), []byte(spend), 0o600); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(work, ".supervisor-run.nosession")
	nowS := strconv.FormatInt(time.Now().Unix(), 10)
	if err := os.WriteFile(statePath, []byte("start="+nowS+"\nlaunches=1\nlast="+nowS+"\npending=0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(work, ".supervisor-counter.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var out hooktype.HookOutput
	go func() {
		defer close(done)
		h.launchGate(&out, gateIn(), nil, logFile, gateCall{crossed: true, event: map[string]any{"n": 1}, model: "haiku", agent: "supervisor"})
	}()
	time.Sleep(250 * time.Millisecond) // the gate peeked (a run is in flight), then waits for the lock
	if err := os.WriteFile(statePath, []byte("start=\nlaunches=1\nlast=0\npending=0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the gate never finished")
	}
	if n := atomic.LoadInt32(launches); n != 0 {
		t.Errorf("launched %d times at the hard stop: the budget check was skipped", n)
	}
	if logs := readFileT(t, logFile); !strings.Contains(logs, "supervisor budget exhausted; skipping this routine") {
		t.Errorf("no budget refusal:\n%s", logs)
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
