package decision

import (
	"os"
	"path/filepath"
	"runtime"
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

func TestBudget_CorruptAndUnwritableFailClosed(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bud.json")
	_ = os.WriteFile(p, []byte("garbage"), 0o600)
	if ErrorClass(NewBudget(p, 10, 1).Reserve("a")) != ClassBudget {
		t.Fatal("corrupt budget file must refuse spend")
	}
	// Unwritable: path under a regular file.
	f := filepath.Join(dir, "file")
	_ = os.WriteFile(f, []byte("x"), 0o600)
	if ErrorClass(NewBudget(filepath.Join(f, "bud.json"), 10, 1).Reserve("a")) != ClassBudget {
		t.Fatal("if spend cannot be recorded, do not spend")
	}
}

func TestCostUSD_MatchesDocumentedPrice(t *testing.T) {
	got := CostUSD(Usage{InputTokens: 1_000_000, OutputTokens: 999})
	if got != 0.042 {
		t.Fatalf("1M input tokens = $%v, want $0.042 (output is free)", got)
	}
}
