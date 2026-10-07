package workflow_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/workflow"
)

const cronWF = `version: 1
name: nightly
triggers:
  cron: "0 9 * * *"
nodes:
  - id: a
    agent: reviewer
    prompt: "review"
    output_limit: 1000
`

// schedHome points HOME at a private dir holding ~/.yakos-state/schedules and
// returns the path of slug's file (not yet written).
func schedHome(t *testing.T, slug string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".yakos-state", "schedules")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{filepath.Join(home, ".yakos-state"), dir} {
		_ = os.Chmod(d, 0o700)
	}
	return filepath.Join(dir, slug+".yaml")
}

func writeSched(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(path, mode)
}

func writeWF(t *testing.T, workDir, name, body string) {
	t.Helper()
	dir := filepath.Join(workDir, "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const enabledSched = "version: 1\ntimezone: UTC\nworkflows:\n  nightly:\n    cron: true\n"

// fakeClock is a manual clock: After registers a waiter that Advance releases.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []fakeWaiter
}
type fakeWaiter struct {
	at time.Time
	ch chan time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.waiters = append(c.waiters, fakeWaiter{c.now.Add(d), ch})
	return ch
}
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var keep []fakeWaiter
	for _, w := range c.waiters {
		if !c.now.Before(w.at) {
			w.ch <- c.now
		} else {
			keep = append(keep, w)
		}
	}
	c.waiters = keep
	c.mu.Unlock()
}

func runCount(t *testing.T, workDir string) int {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join(workDir, "workflows", "runs"))
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range ents {
		if e.IsDir() {
			n++
		}
	}
	return n
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func newSched(t *testing.T, fn workflow.EngineRunFn, now time.Time) (*workflow.Scheduler, string) {
	t.Helper()
	eng, workDir := newTestEngine(t, fn)
	return &workflow.Scheduler{
		Engine:    eng,
		Load:      func() (workflow.Schedules, error) { return workflow.LoadSchedules("proj") },
		OwnerOpID: "op-sched",
	}, workDir
}

func ok(_ context.Context, _ dispatch.Params) ([]byte, dispatch.Result, error) {
	return []byte("done"), dispatch.Result{}, nil
}

func TestScheduler_FiresWhenDue(t *testing.T) {
	path := schedHome(t, "proj")
	writeSched(t, path, enabledSched, 0o600)
	s, workDir := newSched(t, ok, time.Time{})
	writeWF(t, workDir, "nightly", cronWF)
	ctx := context.Background()

	t0 := time.Date(2026, 6, 1, 8, 59, 0, 0, time.UTC)
	s.Tick(ctx, t0) // arms: next fire is 09:00
	if n := runCount(t, workDir); n != 0 {
		t.Fatalf("armed tick started %d runs", n)
	}
	s.Tick(ctx, t0.Add(time.Minute)) // 09:00 due
	waitFor(t, "the cron run", func() bool { return runCount(t, workDir) == 1 })

	ents, _ := os.ReadDir(filepath.Join(workDir, "workflows", "runs"))
	if !strings.HasPrefix(ents[0].Name(), "trg-") {
		t.Fatalf("run id %q lacks the trigger prefix", ents[0].Name())
	}
	rs, err := workflow.LoadRunState(filepath.Join(workDir, "workflows", "runs", ents[0].Name()))
	if err != nil || rs.OwnerOpID != "op-sched" {
		t.Fatalf("run owner = %v, err %v", rs, err)
	}
	// The same minute again must not start another run.
	s.Tick(ctx, t0.Add(time.Minute))
	s.Tick(ctx, t0.Add(90*time.Second))
	time.Sleep(50 * time.Millisecond)
	if n := runCount(t, workDir); n != 1 {
		t.Fatalf("re-ticking the same fire time started %d runs, want 1", n)
	}
}

func TestScheduler_RunLoopOnFakeClock(t *testing.T) {
	path := schedHome(t, "proj")
	writeSched(t, path, enabledSched, 0o600)
	s, workDir := newSched(t, ok, time.Time{})
	writeWF(t, workDir, "nightly", cronWF)
	clk := &fakeClock{now: time.Date(2026, 6, 1, 8, 58, 30, 0, time.UTC)}
	s.Clock = clk
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	step := func(d time.Duration) {
		// Wait for the loop to park on the clock before advancing it.
		waitFor(t, "scheduler to wait on the clock", func() bool {
			clk.mu.Lock()
			defer clk.mu.Unlock()
			return len(clk.waiters) == 1
		})
		clk.Advance(d)
	}
	step(30 * time.Second) // 08:59:00 arms
	step(time.Minute)      // 09:00:00 fires
	waitFor(t, "the cron run", func() bool { return runCount(t, workDir) == 1 })
}

func TestScheduler_SkipsWhileRunning(t *testing.T) {
	path := schedHome(t, "proj")
	writeSched(t, path, enabledSched, 0o600)
	release := make(chan struct{})
	var calls atomic.Int32
	blocking := func(ctx context.Context, p dispatch.Params) ([]byte, dispatch.Result, error) {
		calls.Add(1)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return []byte("x"), dispatch.Result{}, nil
	}
	s, workDir := newSched(t, blocking, time.Time{})
	writeWF(t, workDir, "nightly", cronWF)
	ctx := context.Background()
	defer close(release)

	day := time.Date(2026, 6, 1, 8, 59, 0, 0, time.UTC)
	s.Tick(ctx, day)
	s.Tick(ctx, day.Add(time.Minute)) // first fire
	waitFor(t, "node dispatch", func() bool { return calls.Load() == 1 })
	s.Tick(ctx, day.Add(24*time.Hour+time.Minute)) // next day's fire while the first still runs

	if n := runCount(t, workDir); n != 1 {
		t.Fatalf("second trigger started a run while one was active: %d runs", n)
	}
	ledger, err := os.ReadFile(filepath.Join(workDir, "workflows", "triggers.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ledger), `"outcome":"skipped"`) || !strings.Contains(string(ledger), "already active") {
		t.Fatalf("ledger lacks the skip note:\n%s", ledger)
	}
}

func TestScheduler_IgnoresDisabledAndUntrusted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits")
	}
	cases := []struct {
		name string
		body string // "" = no file
		mode os.FileMode
	}{
		{"no file", "", 0},
		{"cron not enabled", "version: 1\ntimezone: UTC\nworkflows:\n  nightly:\n    cron: false\n", 0o600},
		{"other workflow enabled", "version: 1\ntimezone: UTC\nworkflows:\n  other:\n    cron: true\n", 0o600},
		{"group-readable file", enabledSched, 0o640},
		{"world-writable file", enabledSched, 0o666},
		{"malformed", "version: 1\nworkflows: [", 0o600},
		{"unknown field", enabledSched + "  extra: 1\n", 0o600},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := schedHome(t, "proj")
			if tc.body != "" {
				writeSched(t, path, tc.body, tc.mode)
			}
			s, workDir := newSched(t, ok, time.Time{})
			writeWF(t, workDir, "nightly", cronWF)
			ctx := context.Background()
			t0 := time.Date(2026, 6, 1, 8, 59, 0, 0, time.UTC)
			s.Tick(ctx, t0)
			s.Tick(ctx, t0.Add(time.Minute))
			s.Tick(ctx, t0.Add(25*time.Hour))
			time.Sleep(50 * time.Millisecond)
			if n := runCount(t, workDir); n != 0 {
				t.Fatalf("%s: %d runs started, want 0", tc.name, n)
			}
		})
	}
}

func TestScheduler_SymlinkedFileIgnored(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	path := schedHome(t, "proj")
	real := filepath.Join(t.TempDir(), "real.yaml")
	writeSched(t, real, enabledSched, 0o600)
	if err := os.Symlink(real, path); err != nil {
		t.Fatal(err)
	}
	s, workDir := newSched(t, ok, time.Time{})
	writeWF(t, workDir, "nightly", cronWF)
	t0 := time.Date(2026, 6, 1, 8, 59, 0, 0, time.UTC)
	s.Tick(context.Background(), t0)
	s.Tick(context.Background(), t0.Add(time.Minute))
	time.Sleep(50 * time.Millisecond)
	if n := runCount(t, workDir); n != 0 {
		t.Fatalf("a symlinked schedules file enabled %d runs", n)
	}
}

func TestScheduler_RevokedBetweenTicksStopsFiring(t *testing.T) {
	path := schedHome(t, "proj")
	writeSched(t, path, enabledSched, 0o600)
	s, workDir := newSched(t, ok, time.Time{})
	writeWF(t, workDir, "nightly", cronWF)
	ctx := context.Background()
	t0 := time.Date(2026, 6, 1, 8, 59, 0, 0, time.UTC)
	s.Tick(ctx, t0)
	writeSched(t, path, strings.Replace(enabledSched, "true", "false", 1), 0o600)
	s.Tick(ctx, t0.Add(time.Minute))
	time.Sleep(50 * time.Millisecond)
	if n := runCount(t, workDir); n != 0 {
		t.Fatalf("revoked trigger still fired: %d runs", n)
	}
}

func TestLoadSchedules_ErrorsNameNoPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits")
	}
	path := schedHome(t, "proj")
	writeSched(t, path, enabledSched, 0o644)
	_, err := workflow.LoadSchedules("proj")
	if err == nil {
		t.Fatal("0644 file accepted")
	}
	if strings.Contains(err.Error(), filepath.Dir(path)) {
		t.Fatalf("error leaks the path: %v", err)
	}
}

func TestProjectSlug(t *testing.T) {
	for in, want := range map[string]string{
		"/home/u/My Project": "my-project", "/x/yakOS": "yakos", "/x/a_b.c": "a-b-c", "/": "",
	} {
		if got := workflow.ProjectSlug(in); got != want {
			t.Errorf("ProjectSlug(%q) = %q, want %q", in, got, want)
		}
	}
}
