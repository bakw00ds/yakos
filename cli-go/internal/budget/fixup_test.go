package budget

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func finishedP(agent, ts, project string, usd float64) string {
	return fmt.Sprintf(`{"type":"dispatch_finished","ts":%q,"agent":%q,"project":%q,"usage":{"total_cost_usd":%v}}`, ts, agent, project, usd)
}

func TestReasonCodes(t *testing.T) {
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	if st := mustEval(t, "backend", o); st.Reason != ReasonOff {
		t.Fatalf("%+v", st)
	}
	setLimit(t, dir, "backend", 10, Monthly)
	if st := mustEval(t, "backend", o); st.Reason != ReasonOK {
		t.Fatalf("%+v", st)
	}
	appendLog(t, dir, finished("backend", octMid, 8.5))
	if st := mustEval(t, "backend", o); st.Reason != ReasonWarning {
		t.Fatalf("%+v", st)
	}
	appendLog(t, dir, finished("backend", octMid, 2))
	if st := mustEval(t, "backend", o); st.Reason != ReasonExhausted {
		t.Fatalf("%+v", st)
	}
}

func TestPerProjectSpend(t *testing.T) {
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	setLimit(t, dir, "supervisor", 100, Monthly)
	appendLog(t, dir,
		finishedP("supervisor", octMid, "/repo/a", 3), finishedP("supervisor", octMid, "/repo/b", 5),
		finishedP("supervisor", octMid, "/repo/a", 4), finishedP("supervisor", "2026-09-10T12:00:00Z", "/repo/a", 50))
	st := mustEval(t, "supervisor", o)
	if len(st.Projects) != 2 || st.Projects[0].Project != "/repo/a" || st.Projects[0].SpentUSD != 7 || st.Projects[1].SpentUSD != 5 {
		t.Fatalf("per-project spend for the window: %+v", st.Projects)
	}
	if st.SpentUSD != 12 {
		t.Fatalf("%+v", st)
	}
}

func TestClampModel(t *testing.T) {
	dir := t.TempDir()
	o := Options{StateDir: dir}
	if m, n := ClampModel("backend", "opus", o); m != "opus" || n != "" {
		t.Fatalf("no ceiling must not clamp: %s %s", m, n)
	}
	// Built-in: the supervisor is capped at sonnet with no config at all.
	if m, _ := ClampModel("supervisor", "opus", o); m != "sonnet" {
		t.Fatalf("built-in supervisor ceiling: %s", m)
	}
	if m, _ := ClampModel("supervisor", "haiku", o); m != "haiku" {
		t.Fatalf("below the ceiling must pass: %s", m)
	}
	// The user can lift or tighten it; a project cannot (it is not read here).
	if err := SetMaxModel(dir, "supervisor", "fable"); err != nil {
		t.Fatal(err)
	}
	if m, _ := ClampModel("supervisor", "opus", o); m != "opus" {
		t.Fatalf("user-level override must lift the built-in: %s", m)
	}
	if err := SetMaxModel(dir, "supervisor", ""); err != nil {
		t.Fatal(err)
	}
	if err := SetMaxModel(dir, "supervisor", "haiku"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ in, want string }{{"opus", "haiku"}, {"sonnet", "haiku"}, {"haiku", "haiku"}, {"claude-opus-9", "claude-opus-9"}} {
		if m, _ := ClampModel("supervisor", c.in, o); m != c.want {
			t.Errorf("clamp %s = %s, want %s", c.in, m, c.want)
		}
	}
	if m, _ := ClampModel("backend", "opus", o); m != "opus" {
		t.Error("ceiling is per agent")
	}
	if err := SetMaxModel(dir, "supervisor", "gpt"); err == nil {
		t.Error("bad tier must fail")
	}
	// A ceiling survives a later limit change.
	setLimit(t, dir, "supervisor", 30, Monthly)
	if m, _ := ClampModel("supervisor", "opus", o); m != "haiku" {
		t.Error("ceiling lost by set")
	}
}

func TestTamperedAggregateRebuilt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits and symlinks")
	}
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	setLimit(t, dir, "backend", 10, Monthly)
	appendLog(t, dir, finished("backend", octMid, 12))
	mustEval(t, "backend", o)
	agg := filepath.Join(dir, aggregateFileName)
	good, _ := os.ReadFile(agg)

	// 1. group/world-writable cache with zeroed spend: not trusted, rebuilt.
	a := readAggregate(dir)
	a.Agents["backend"].Monthly["2026-10"] = 0
	a.Agents["backend"].Lifetime = 0
	_ = writeAggregate(dir, a)
	_ = os.Chmod(agg, 0o666)
	if st := mustEval(t, "backend", o); st.State != StateHardStop {
		t.Fatalf("world-writable aggregate must be rebuilt, got %+v", st)
	}
	// 2. symlinked cache: not followed.
	target := filepath.Join(t.TempDir(), "lie.json")
	_ = os.WriteFile(target, []byte(`{"version":2,"offset":0,"agents":{}}`), 0o600)
	_ = os.Remove(agg)
	if err := os.Symlink(target, agg); err != nil {
		t.Fatal(err)
	}
	if st := mustEval(t, "backend", o); st.State != StateHardStop {
		t.Fatalf("symlinked aggregate must be rebuilt, got %+v", st)
	}
	if b, _ := os.ReadFile(target); string(b) != `{"version":2,"offset":0,"agents":{}}` {
		t.Fatal("the symlink target must be left untouched")
	}
	_ = good
}

func TestTamperedResetsIgnored(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlinks")
	}
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	setLimit(t, dir, "backend", 10, Monthly)
	appendLog(t, dir, finished("backend", octMid, 12))
	lie := filepath.Join(t.TempDir(), "r.json")
	_ = os.WriteFile(lie, []byte(`{"backend":{"window":"monthly","key":"2026-10","usd":1000}}`), 0o600)
	if err := os.Symlink(lie, filepath.Join(dir, resetsFileName)); err != nil {
		t.Fatal(err)
	}
	st := mustEval(t, "backend", o)
	if st.State != StateHardStop || len(st.Warnings) == 0 {
		t.Fatalf("a symlinked resets file must not lift the stop, and must warn: %+v", st)
	}
	// An operator reset replaces the planted link with a real file.
	if _, err := Reset("backend", o); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(filepath.Join(dir, resetsFileName)); fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("reset must replace the symlink")
	}
	if st := mustEval(t, "backend", o); st.State != StateOK {
		t.Fatalf("%+v", st)
	}
}

func TestDanglingLockSymlinkDoesNotStall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlinks")
	}
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(dir, "nowhere"), filepath.Join(dir, lockFileName)); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	unlock, err := acquire(dir, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("a dangling lock symlink stalled acquire for %v", d)
	}
}

func TestParallelSetsLoseNothing(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := SetLimit(dir, fmt.Sprintf("agent-%d", i), float64(i+1), Monthly); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	p, err := LoadPolicy(dir)
	if err != nil || len(p.Agents) != 10 {
		t.Fatalf("10 parallel sets must all land, got %d (%v)", len(p.Agents), err)
	}
}

func TestTimezoneChangeRebuilds(t *testing.T) {
	dir := t.TempDir()
	setLimit(t, dir, "backend", 100, Monthly)
	// 2026-11-01T03:00Z is October in Los Angeles and November in UTC.
	appendLog(t, dir, finished("backend", "2026-11-01T03:00:00Z", 5))
	o := Options{StateDir: dir, Now: clock("2026-11-15T12:00:00Z")}
	saved := time.Local
	defer func() { time.Local = saved }()
	time.Local = time.UTC
	if st := mustEval(t, "backend", o); st.SpentUSD != 5 {
		t.Fatalf("UTC: %+v", st)
	}
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skip("no tzdata")
	}
	time.Local = la
	if st := mustEval(t, "backend", o); st.SpentUSD != 0 {
		t.Fatalf("after a TZ change the event belongs to October, got %+v", st)
	}
}

func TestProjectOnlyAgentsListed(t *testing.T) {
	names := AgentNamesWith(Policy{}, map[string]float64{"backend": 10, "../bad": 1})
	want := map[string]bool{"backend": true, "supervisor": true, "librarian": true}
	if len(names) != 3 {
		t.Fatalf("%v", names)
	}
	for _, n := range names {
		if !want[n] {
			t.Fatalf("%v", names)
		}
	}
}
