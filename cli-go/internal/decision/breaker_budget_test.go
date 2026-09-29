package decision

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBreaker_OpensAtThresholdExactly(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	b := &Breaker{Path: filepath.Join(t.TempDir(), "b.json"), Now: func() time.Time { return now }}
	for i := 1; i <= 4; i++ {
		b.Failure(ClassHTTP5xx)
		if err := b.Allow(); err != nil {
			t.Fatalf("after %d failures breaker must be closed: %v", i, err)
		}
	}
	b.Failure(ClassHTTP5xx)
	if ErrorClass(b.Allow()) != ClassBreakerOpen {
		t.Fatal("5th consecutive failure must open the breaker")
	}
}

func TestBreaker_CooldownThenHalfOpenReopensOnOneFailure(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	b := &Breaker{Path: filepath.Join(t.TempDir(), "b.json"), Now: func() time.Time { return now }}
	for i := 0; i < 5; i++ {
		b.Failure(ClassTimeout)
	}
	now = now.Add(DefaultBreakerCooldown - time.Second)
	if b.Allow() == nil {
		t.Fatal("still inside cooldown")
	}
	now = now.Add(2 * time.Second)
	if err := b.Allow(); err != nil {
		t.Fatalf("after cooldown a probe call is allowed: %v", err)
	}
	b.Failure(ClassTimeout) // half-open probe fails
	if b.Allow() == nil {
		t.Fatal("a single failure while half-open must re-open")
	}
}

func TestBreaker_SuccessResets(t *testing.T) {
	b := &Breaker{Path: filepath.Join(t.TempDir(), "b.json")}
	for i := 0; i < 4; i++ {
		b.Failure(ClassTimeout)
	}
	b.Success()
	for i := 0; i < 4; i++ {
		b.Failure(ClassTimeout)
	}
	if err := b.Allow(); err != nil {
		t.Fatalf("success must reset the count: %v", err)
	}
}

func TestBreaker_PersistsAcrossInstances(t *testing.T) {
	p := filepath.Join(t.TempDir(), "b.json")
	a := &Breaker{Path: p}
	for i := 0; i < 5; i++ {
		a.Failure(ClassTimeout)
	}
	if ErrorClass((&Breaker{Path: p}).Allow()) != ClassBreakerOpen {
		t.Fatal("state must be shared between processes via the file")
	}
}

func TestBreaker_CorruptStateFailsClosed(t *testing.T) {
	p := filepath.Join(t.TempDir(), "b.json")
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ErrorClass((&Breaker{Path: p}).Allow()) != ClassBreakerOpen {
		t.Fatal("unreadable breaker state must not allow calls")
	}
}

func TestBreaker_FileIs0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix modes")
	}
	p := filepath.Join(t.TempDir(), "sub", "b.json")
	(&Breaker{Path: p}).Failure(ClassTimeout)
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}
}

func TestBudget_SessionCapAndIsolation(t *testing.T) {
	b := NewBudget(filepath.Join(t.TempDir(), "bud.json"), 3, 1)
	for i := 0; i < 3; i++ {
		if err := b.Reserve("a"); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
	if ErrorClass(b.Reserve("a")) != ClassBudget {
		t.Fatal("4th call in session a must be refused")
	}
	if err := b.Reserve("b"); err != nil {
		t.Fatalf("session b has its own allowance: %v", err)
	}
}

func TestBudget_DailyUSDCap(t *testing.T) {
	b := NewBudget(filepath.Join(t.TempDir(), "bud.json"), 1000, 0.10)
	if err := b.Reserve("a"); err != nil {
		t.Fatal(err)
	}
	b.AddUSD(0.09)
	if err := b.Reserve("a"); err != nil {
		t.Fatalf("under cap: %v", err)
	}
	b.AddUSD(0.01)
	if ErrorClass(b.Reserve("a")) != ClassBudget {
		t.Fatal("at $0.10 the day is exhausted")
	}
}

func TestBudget_DayRolloverResets(t *testing.T) {
	now := time.Date(2026, 9, 29, 23, 59, 0, 0, time.UTC)
	b := NewBudget(filepath.Join(t.TempDir(), "bud.json"), 1, 1)
	b.Now = func() time.Time { return now }
	if err := b.Reserve("a"); err != nil {
		t.Fatal(err)
	}
	if b.Reserve("a") == nil {
		t.Fatal("cap reached")
	}
	now = now.Add(2 * time.Minute) // next UTC day
	if err := b.Reserve("a"); err != nil {
		t.Fatalf("new day must reset: %v", err)
	}
}

func TestBudget_Defaults(t *testing.T) {
	b := NewBudget(filepath.Join(t.TempDir(), "x.json"), 0, 0)
	if b.MaxCallsPerSession != 2000 || b.MaxUSDPerDay != 1.00 {
		t.Fatalf("defaults = %d / %v", b.MaxCallsPerSession, b.MaxUSDPerDay)
	}
}

func TestBudget_CorruptLedgerFailsClosed(t *testing.T) {
	b := NewBudget(filepath.Join(t.TempDir(), "bud.json"), 10, 1)
	if err := os.WriteFile(b.LedgerPath(), []byte("garbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ErrorClass(b.Reserve("a")) != ClassBudget {
		t.Fatal("corrupt ledger must refuse spend")
	}
	if ErrorClass(b.Check("a")) != ClassBudget {
		t.Fatal("corrupt ledger must fail Check")
	}
	_ = os.WriteFile(b.LedgerPath(), []byte(`{"s":"a","c":-5}`+"\n"), 0o600)
	if _, err := b.Load(); err == nil {
		t.Fatal("negative counts are corrupt")
	}
}

// Review F13: a mutation making save fail OPEN survived. If the reservation
// cannot be recorded the call must be refused.
func TestBudget_UnwritableFailsClosed(t *testing.T) {
	dir := t.TempDir()
	// Under a regular file: cannot create the state dir.
	f := filepath.Join(dir, "file")
	_ = os.WriteFile(f, []byte("x"), 0o600)
	if ErrorClass(NewBudget(filepath.Join(f, "bud.json"), 10, 1).Reserve("a")) != ClassBudget {
		t.Fatal("state dir not creatable: must refuse")
	}
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("read-only directory semantics need a non-root posix user")
	}
	ro := filepath.Join(dir, "ro")
	if err := os.MkdirAll(ro, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(ro, 0o700)
	b := NewBudget(filepath.Join(ro, "bud.json"), 10, 1)
	if ErrorClass(b.Reserve("a")) != ClassBudget {
		t.Fatal("read-only state dir: the reservation cannot be recorded, so the call must be refused (fail closed)")
	}
}

// Review F5: a read-modify-write file recorded 96 of 800 concurrent calls.
// Independent Budget values (own mutex) on one path stand in for processes.
func TestBudget_ConcurrentWritersLoseNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bud.json")
	const writers, each = 16, 50
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := NewBudget(path, 1_000_000, 1000)
			for i := 0; i < each; i++ {
				if err := b.Reserve("s"); err != nil {
					t.Error(err)
					return
				}
				b.AddUSD(0.001)
			}
		}()
	}
	wg.Wait()
	st, err := NewBudget(path, 1_000_000, 1000).Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Sessions["s"] != writers*each {
		t.Fatalf("recorded %d calls, want %d (lost updates)", st.Sessions["s"], writers*each)
	}
	if got, want := st.USD, float64(writers*each)*0.001; got < want-1e-6 || got > want+1e-6 {
		t.Fatalf("recorded $%v, want $%v", got, want)
	}
}

func TestBudget_CapNeverExceededUnderConcurrency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bud.json")
	const cap, writers, each = 100, 16, 50
	var ok int64
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := NewBudget(path, cap, 1000)
			for i := 0; i < each; i++ {
				if b.Reserve("s") == nil {
					atomic.AddInt64(&ok, 1)
				}
			}
		}()
	}
	wg.Wait()
	// Append-then-sum never over-admits. It may refuse a call or two near the
	// cap when later appends land before its own read (at most writers-1).
	if ok > cap || ok < cap-writers {
		t.Fatalf("%d calls allowed with a cap of %d", ok, cap)
	}
}

// Real processes (the re-exec'd test binary) appending to one ledger.
func TestBudget_ConcurrentProcesses(t *testing.T) {
	if os.Getenv("YAKOS_BUDGET_HELPER") != "" {
		return
	}
	path := filepath.Join(t.TempDir(), "bud.json")
	const procs, each = 8, 25
	var cmds []*exec.Cmd
	for i := 0; i < procs; i++ {
		c := exec.Command(os.Args[0], "-test.run=^TestBudgetHelperProcess$") //nolint:gosec
		c.Env = append(os.Environ(), "YAKOS_BUDGET_HELPER="+path, fmt.Sprintf("YAKOS_BUDGET_N=%d", each))
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		cmds = append(cmds, c)
	}
	for _, c := range cmds {
		if err := c.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	st, err := NewBudget(path, 1_000_000, 1000).Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Sessions["p"] != procs*each {
		t.Fatalf("recorded %d calls from %d processes, want %d", st.Sessions["p"], procs, procs*each)
	}
}

func TestBudgetHelperProcess(t *testing.T) {
	path := os.Getenv("YAKOS_BUDGET_HELPER")
	if path == "" {
		t.Skip("helper only")
	}
	n, _ := strconv.Atoi(os.Getenv("YAKOS_BUDGET_N"))
	b := NewBudget(path, 1_000_000, 1000)
	for i := 0; i < n; i++ {
		if err := b.Reserve("p"); err != nil {
			os.Exit(1)
		}
	}
}

func TestBudget_StaleLedgersRemoved(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	b := NewBudget(filepath.Join(dir, "bud.json"), 10, 1)
	b.Now = func() time.Time { return now }
	old := filepath.Join(dir, "bud-2026-09-20.ndjson")
	recent := filepath.Join(dir, "bud-2026-09-28.ndjson")
	_ = os.WriteFile(old, []byte("{}\n"), 0o600)
	_ = os.WriteFile(recent, []byte("{}\n"), 0o600)
	if err := b.Reserve("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); err == nil {
		t.Error("old ledger must be removed")
	}
	if _, err := os.Stat(recent); err != nil {
		t.Error("yesterday's ledger must stay")
	}
}

func TestBudget_LedgerFileMode0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix modes")
	}
	b := NewBudget(filepath.Join(t.TempDir(), "s", "bud.json"), 10, 1)
	_ = b.Reserve("a")
	fi, err := os.Stat(b.LedgerPath())
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", fi, err)
	}
}

func TestCostUSD_MatchesDocumentedPrice(t *testing.T) {
	got := CostUSD(Usage{InputTokens: 1_000_000, OutputTokens: 999})
	if got != 0.042 {
		t.Fatalf("1M input tokens = $%v, want $0.042 (output is free)", got)
	}
}
