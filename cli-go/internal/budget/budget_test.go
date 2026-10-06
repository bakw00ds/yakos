package budget

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func clock(s string) func() time.Time {
	return func() time.Time {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			panic(err)
		}
		return t
	}
}

// finished renders one dispatch_finished line.
func finished(agent, ts string, usd float64) string {
	return fmt.Sprintf(`{"type":"dispatch_finished","ts":%q,"agent":%q,"runtime":"claude","exit_code":0,"usage":{"total_cost_usd":%v}}`, ts, agent, usd)
}

func appendLog(t testing.TB, dir string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, logFileName), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, l := range lines {
		if _, err := f.WriteString(l + "\n"); err != nil {
			t.Fatal(err)
		}
	}
}

func setLimit(t testing.TB, dir, agent string, usd float64, w Window) {
	t.Helper()
	if err := SetLimit(dir, agent, usd, w); err != nil {
		t.Fatal(err)
	}
}

func mustEval(t testing.TB, agent string, o Options) Status {
	t.Helper()
	st, err := Evaluate(agent, o)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

const octMid = "2026-10-15T12:00:00Z"

func TestOffByDefault(t *testing.T) {
	dir := t.TempDir()
	appendLog(t, dir, finished("backend", octMid, 9999))
	st := mustEval(t, "backend", Options{StateDir: dir, Now: clock(octMid)})
	if st.State != StateOff || st.Refused() {
		t.Fatalf("want off, got %+v", st)
	}
	if _, err := Enforce("backend", Options{StateDir: dir}); err != nil {
		t.Fatal(err)
	}
}

func TestBuiltinDefaults(t *testing.T) {
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	if st := mustEval(t, "supervisor", o); st.LimitUSD != 100 || st.Window != Monthly || st.Source != "builtin" {
		t.Fatalf("supervisor default: %+v", st)
	}
	if st := mustEval(t, "librarian", o); st.LimitUSD != 40 {
		t.Fatalf("librarian default: %+v", st)
	}
	appendLog(t, dir, finished("supervisor", octMid, 100.5))
	if st := mustEval(t, "supervisor", o); st.State != StateHardStop || st.StopUSD != 200 {
		t.Fatalf("supervisor over its default is hard_stop with a 2x dispatch stop: %+v", st)
	}
	// Dispatch itself refuses the supervisor only at 2x: the hook refuses
	// routine launches at 1x and must still be able to launch high-risk ones.
	if _, err := Enforce("supervisor", o); err != nil {
		t.Fatalf("supervisor between 1x and 2x must still dispatch: %v", err)
	}
	appendLog(t, dir, finished("supervisor", octMid, 100))
	_, err := Enforce("supervisor", o)
	if !IsRefused(err) {
		t.Fatalf("supervisor at 2x must be refused, got %v", err)
	}
	if !strings.Contains(err.Error(), "exceeded 2x the $100.00 monthly supervisor budget (high-risk exemption ceiling") || strings.Contains(err.Error(), "of its $100.00") {
		t.Fatalf("2x refusal message: %v", err)
	}
	// The operator can turn the default off explicitly.
	setLimit(t, dir, "supervisor", 0, Monthly)
	if st := mustEval(t, "supervisor", o); st.State != StateOff {
		t.Fatalf("limit 0 must turn the built-in off: %+v", st)
	}
}

func TestWarningThreshold(t *testing.T) {
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	setLimit(t, dir, "backend", 100, Monthly)
	appendLog(t, dir, finished("backend", octMid, 79.99))
	if st := mustEval(t, "backend", o); st.State != StateOK {
		t.Fatalf("79.99%% must be ok: %+v", st)
	}
	appendLog(t, dir, finished("backend", octMid, 0.01))
	st := mustEval(t, "backend", o)
	if st.State != StateWarning || st.Refused() {
		t.Fatalf("80%% must warn, not stop: %+v", st)
	}
	if _, err := Enforce("backend", o); err != nil {
		t.Fatalf("warning must not refuse: %v", err)
	}
	// Custom threshold from the policy file.
	p, _ := LoadPolicy(dir)
	a := p.Agents["backend"]
	a.WarnPct = 95
	p.Agents["backend"] = a
	if err := SavePolicy(dir, p); err != nil {
		t.Fatal(err)
	}
	if st := mustEval(t, "backend", o); st.State != StateOK {
		t.Fatalf("warn_pct 95 at 80%% must be ok: %+v", st)
	}
}

func TestHardStopRefusal(t *testing.T) {
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	setLimit(t, dir, "backend", 10, Monthly)
	appendLog(t, dir, finished("backend", octMid, 9.99))
	if _, err := Enforce("backend", o); err != nil {
		t.Fatalf("below 100%% must pass: %v", err)
	}
	appendLog(t, dir, finished("backend", octMid, 0.01))
	st, err := Enforce("backend", o)
	if !IsRefused(err) || st.State != StateHardStop {
		t.Fatalf("exactly 100%% must refuse: %v %+v", err, st)
	}
	for _, want := range []string{`"backend"`, "$10.00", "yakos budget set backend", "yakos budget reset backend", "2026-11-01"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal message missing %q: %s", want, err)
		}
	}
	// Another agent is unaffected: the pause is per agent.
	setLimit(t, dir, "frontend", 10, Monthly)
	if _, err := Enforce("frontend", o); err != nil {
		t.Fatal(err)
	}
	// Raising the limit lifts the pause.
	setLimit(t, dir, "backend", 50, Monthly)
	if _, err := Enforce("backend", o); err != nil {
		t.Fatalf("raising the limit must resume the agent: %v", err)
	}
}

func TestWindowRollover(t *testing.T) {
	dir := t.TempDir()
	setLimit(t, dir, "backend", 50, Monthly)
	appendLog(t, dir, finished("backend", "2026-09-15T12:00:00Z", 60), finished("backend", octMid, 10))
	if st := mustEval(t, "backend", Options{StateDir: dir, Now: clock("2026-09-20T12:00:00Z")}); st.State != StateHardStop {
		t.Fatalf("September spend 60 of 50: %+v", st)
	}
	st := mustEval(t, "backend", Options{StateDir: dir, Now: clock(octMid)})
	if st.State != StateOK || st.SpentUSD != 10 {
		t.Fatalf("October must start over: %+v", st)
	}
	if st := mustEval(t, "backend", Options{StateDir: dir, Now: clock("2026-11-02T12:00:00Z")}); st.SpentUSD != 0 {
		t.Fatalf("November must start at zero: %+v", st)
	}
}

func TestLifetimeWindow(t *testing.T) {
	dir := t.TempDir()
	setLimit(t, dir, "backend", 50, Lifetime)
	appendLog(t, dir, finished("backend", "2026-01-15T12:00:00Z", 30), finished("backend", octMid, 30))
	if st := mustEval(t, "backend", Options{StateDir: dir, Now: clock("2027-03-01T12:00:00Z")}); st.State != StateHardStop || st.RollsOver != "" {
		t.Fatalf("lifetime must never roll over: %+v", st)
	}
}

func TestResetStartsWindowOver(t *testing.T) {
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	setLimit(t, dir, "backend", 10, Monthly)
	appendLog(t, dir, finished("backend", octMid, 12))
	if _, err := Enforce("backend", o); !IsRefused(err) {
		t.Fatal("expected hard stop")
	}
	st, err := Reset("backend", o)
	if err != nil || st.State != StateOK || st.SpentUSD != 0 {
		t.Fatalf("reset: %v %+v", err, st)
	}
	appendLog(t, dir, finished("backend", octMid, 4))
	if st := mustEval(t, "backend", o); st.SpentUSD != 4 {
		t.Fatalf("spend after reset must count only new runs: %+v", st)
	}
	appendLog(t, dir, finished("backend", octMid, 6))
	if _, err := Enforce("backend", o); !IsRefused(err) {
		t.Fatal("expected hard stop again after reset window fills")
	}
	// A reset in October does not carry into November.
	if st := mustEval(t, "backend", Options{StateDir: dir, Now: clock("2026-11-03T12:00:00Z")}); st.SpentUSD != 0 {
		t.Fatalf("%+v", st)
	}
}

func f64(v float64) *float64 { return &v }

func TestProjectCanOnlyLower(t *testing.T) {
	pol := Policy{Agents: map[string]AgentLimit{"backend": {LimitUSD: f64(50)}}}
	cases := []struct {
		name    string
		project float64
		want    float64
		warn    bool
	}{
		{"lower applies", 20, 20, false},
		{"equal is a no-op", 50, 50, false},
		{"raise ignored", 500, 50, true},
		{"disable ignored", 0, 50, true},
		{"negative ignored", -1, 50, true},
	}
	for _, c := range cases {
		l := Resolve("backend", pol, f64(c.project))
		if l.USD != c.want || (len(l.Warnings) > 0) != c.warn {
			t.Errorf("%s: got %v warnings=%v", c.name, l.USD, l.Warnings)
		}
	}
	// Off at user level: a project may introduce a limit (tightening).
	if l := Resolve("backend", Policy{}, f64(7)); l.USD != 7 || l.Source != "project" {
		t.Errorf("project limit on an unlimited agent: %+v", l)
	}
	// A project cannot lift the built-in supervisor limit.
	if l := Resolve("supervisor", Policy{}, f64(1000)); l.USD != 100 || len(l.Warnings) == 0 {
		t.Errorf("project raising builtin: %+v", l)
	}
}

func TestProjectYAMLEndToEnd(t *testing.T) {
	dir, proj := t.TempDir(), t.TempDir()
	setLimit(t, dir, "backend", 50, Monthly)
	if err := os.WriteFile(filepath.Join(proj, ".yakos.yml"), []byte("budget:\n  enabled: true\nagent_budgets:\n  backend: 500\n  frontend: 5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	appendLog(t, dir, finished("backend", octMid, 60), finished("frontend", octMid, 6))
	o := Options{StateDir: dir, Project: proj, Now: clock(octMid)}
	st := mustEval(t, "backend", o)
	if st.LimitUSD != 50 || st.State != StateHardStop || len(st.Warnings) == 0 {
		t.Fatalf("project raise must be ignored with a warning: %+v", st)
	}
	if st := mustEval(t, "frontend", o); st.LimitUSD != 5 || st.State != StateHardStop {
		t.Fatalf("project limit on an unlimited agent: %+v", st)
	}
}

func TestUntrustedPolicyIgnoredBuiltinsRemain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	dir := t.TempDir()
	if err := os.WriteFile(PolicyPath(dir), []byte("agents:\n  supervisor:\n    limit_usd: 0\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(PolicyPath(dir), 0o666)
	st := mustEval(t, "supervisor", Options{StateDir: dir, Now: clock(octMid)})
	if st.LimitUSD != 100 || len(st.Warnings) == 0 {
		t.Fatalf("a world-writable policy must be ignored (builtin kept) with a warning: %+v", st)
	}
	if err := SetLimit(dir, "x", 1, Monthly); err == nil {
		t.Fatal("SetLimit must refuse to rewrite an untrusted policy")
	}
}

func TestCorruptAggregateRebuiltFromLog(t *testing.T) {
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	setLimit(t, dir, "backend", 100, Monthly)
	appendLog(t, dir, finished("backend", octMid, 30), finished("backend", octMid, 20))
	if st := mustEval(t, "backend", o); st.SpentUSD != 50 {
		t.Fatalf("%+v", st)
	}
	for _, junk := range []string{"{not json", `{"version":99}`, `{"version":1,"agents":null}`, ""} {
		if err := os.WriteFile(filepath.Join(dir, aggregateFileName), []byte(junk), 0o600); err != nil {
			t.Fatal(err)
		}
		if st := mustEval(t, "backend", o); st.SpentUSD != 50 {
			t.Fatalf("corrupt aggregate %q must be rebuilt from the log: %+v", junk, st)
		}
		if readAggregate(dir) == nil {
			t.Fatalf("aggregate not rewritten after rebuild of %q", junk)
		}
	}
	// A poisoned aggregate that parses but lies about its offset is caught by the head check.
	a := readAggregate(dir)
	a.Head = "deadbeef"
	_ = writeAggregate(dir, a)
	appendLog(t, dir, finished("backend", octMid, 1))
	if st := mustEval(t, "backend", o); st.SpentUSD != 51 {
		t.Fatalf("head mismatch must trigger a rebuild: %+v", st)
	}
}

func TestIncrementalAndRotation(t *testing.T) {
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	setLimit(t, dir, "backend", 1000, Monthly)
	appendLog(t, dir, finished("backend", octMid, 1))
	mustEval(t, "backend", o)
	off1 := readAggregate(dir).Offset
	appendLog(t, dir, finished("backend", octMid, 2), `{"type":"dispatch_started","ts":"x","agent":"backend"}`)
	if st := mustEval(t, "backend", o); st.SpentUSD != 3 {
		t.Fatalf("incremental: %+v", st)
	}
	if off2 := readAggregate(dir).Offset; off2 <= off1 {
		t.Fatalf("offset must advance: %d -> %d", off1, off2)
	}
	// A partial trailing line (writer mid-append) is not consumed.
	f, _ := os.OpenFile(filepath.Join(dir, logFileName), os.O_WRONLY|os.O_APPEND, 0o600)
	_, _ = f.WriteString(`{"type":"dispatch_finished","ts":"` + octMid + `","agent":"backend","usage":{"total_cost_usd":100`)
	_ = f.Close()
	if st := mustEval(t, "backend", o); st.SpentUSD != 3 {
		t.Fatalf("partial line must wait: %+v", st)
	}
	f, _ = os.OpenFile(filepath.Join(dir, logFileName), os.O_WRONLY|os.O_APPEND, 0o600)
	_, _ = f.WriteString("}}\n")
	_ = f.Close()
	if st := mustEval(t, "backend", o); st.SpentUSD != 103 {
		t.Fatalf("completed line must count: %+v", st)
	}
	// Rotation: current becomes an archive, a fresh log starts.
	if err := os.Rename(filepath.Join(dir, logFileName), filepath.Join(dir, "dispatch-log-2026-10-15.ndjson")); err != nil {
		t.Fatal(err)
	}
	appendLog(t, dir, finished("backend", octMid, 7))
	if st := mustEval(t, "backend", o); st.SpentUSD != 110 {
		t.Fatalf("rotation must rebuild across archives: %+v", st)
	}
	// Truncation.
	if err := os.WriteFile(filepath.Join(dir, logFileName), []byte(finished("backend", octMid, 1)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if st := mustEval(t, "backend", o); st.SpentUSD != 104 {
		t.Fatalf("truncated log must rebuild: %+v", st)
	}
}

func TestIgnoresBadCostFields(t *testing.T) {
	dir := t.TempDir()
	setLimit(t, dir, "backend", 100, Monthly)
	appendLog(t, dir,
		finished("backend", octMid, -5),
		`{"type":"dispatch_finished","ts":"`+octMid+`","agent":"backend"}`,
		`{"type":"dispatch_finished","ts":"`+octMid+`","agent":"backend","usage":{"total_cost_usd":null}}`,
		`garbage`,
		finished("backend", octMid, 2),
	)
	if st := mustEval(t, "backend", Options{StateDir: dir, Now: clock(octMid)}); st.SpentUSD != 2 {
		t.Fatalf("%+v", st)
	}
}

func TestFilesAre0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	dir := t.TempDir()
	setLimit(t, dir, "backend", 10, Monthly)
	appendLog(t, dir, finished("backend", octMid, 1))
	mustEval(t, "backend", Options{StateDir: dir, Now: clock(octMid)})
	if _, err := Reset("backend", Options{StateDir: dir, Now: clock(octMid)}); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{PolicyFileName, aggregateFileName, resetsFileName} {
		fi, err := os.Stat(filepath.Join(dir, n))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %o, want 0600", n, fi.Mode().Perm())
		}
	}
	if _, err := os.Stat(filepath.Join(dir, lockFileName)); !os.IsNotExist(err) {
		t.Error("lock file must be released")
	}
}

// Two writers appending to the log while several dispatches evaluate: the
// cache must never double count, drop an event, or get corrupted.
func TestConcurrentEvaluateAndAppend(t *testing.T) {
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	setLimit(t, dir, "backend", 1e9, Monthly)
	const writers, perWriter = 4, 50
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				appendLog(t, dir, finished("backend", octMid, 1))
				if _, err := Evaluate("backend", o); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	if st := mustEval(t, "backend", o); st.SpentUSD != writers*perWriter {
		t.Fatalf("spend %v, want %d", st.SpentUSD, writers*perWriter)
	}
	// And the cache agrees with a from-scratch rebuild.
	cached := readAggregate(dir)
	fresh, err := advance(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cached.Agents["backend"].Lifetime != fresh.Agents["backend"].Lifetime {
		t.Fatalf("cache %v != rebuild %v", cached.Agents["backend"].Lifetime, fresh.Agents["backend"].Lifetime)
	}
}

// Two dispatches near the limit: both pass the pre-flight (spend is only known
// when a run finishes; an in-flight run is never killed), then the next
// dispatch after both finish is refused.
func TestConcurrentDispatchesNearLimit(t *testing.T) {
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	setLimit(t, dir, "backend", 10, Monthly)
	appendLog(t, dir, finished("backend", octMid, 9))
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = Enforce("backend", o)
		}(i)
	}
	wg.Wait()
	for i, e := range errs {
		if e != nil {
			t.Fatalf("dispatch %d below the limit must pass: %v", i, e)
		}
	}
	appendLog(t, dir, finished("backend", octMid, 0.6), finished("backend", octMid, 0.6))
	if _, err := Enforce("backend", o); !IsRefused(err) {
		t.Fatalf("after both finish ($10.20 of $10) the next dispatch must be refused: %v", err)
	}
}

func TestConcurrentResetsAndEvaluations(t *testing.T) {
	dir := t.TempDir()
	o := Options{StateDir: dir, Now: clock(octMid)}
	setLimit(t, dir, "backend", 100, Monthly)
	appendLog(t, dir, finished("backend", octMid, 5))
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				if _, err := Reset("backend", o); err != nil {
					t.Error(err)
				}
			} else if _, err := Evaluate("backend", o); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if st := mustEval(t, "backend", o); st.SpentUSD != 0 {
		t.Fatalf("%+v", st)
	}
}

func TestSetLimitValidation(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []struct {
		agent string
		usd   float64
		w     Window
	}{{"", 1, Monthly}, {"a b", 1, Monthly}, {"../x", 1, Monthly}, {"a", -1, Monthly}, {"a", 1, "weekly"}} {
		if err := SetLimit(dir, bad.agent, bad.usd, bad.w); err == nil {
			t.Errorf("SetLimit(%+v) must fail", bad)
		}
	}
	setLimit(t, dir, "a", 5, Lifetime)
	p, err := LoadPolicy(dir)
	if err != nil || *p.Agents["a"].LimitUSD != 5 || p.Agents["a"].Window != "lifetime" {
		t.Fatalf("%v %+v", err, p)
	}
	names := AgentNames(p)
	if !sort.StringsAreSorted(names) || len(names) != 3 {
		t.Fatalf("%v", names)
	}
}

func TestStatusJSONShape(t *testing.T) {
	st := Status{Agent: "a", State: StateHardStop}
	b, _ := json.Marshal(st)
	if !strings.Contains(string(b), `"state":"hard_stop"`) {
		t.Fatal(string(b))
	}
}

// --- latency ---------------------------------------------------------------

func bigLog(t testing.TB, dir string, n int) {
	t.Helper()
	var sb strings.Builder
	agents := []string{"supervisor", "librarian", "backend", "code-reviewer", "architect"}
	for i := 0; i < n; i++ {
		a := agents[i%len(agents)]
		sb.WriteString(finished(a, fmt.Sprintf("2026-10-%02dT12:00:00Z", 1+i%28), 0.05))
		sb.WriteString("\n")
		sb.WriteString(`{"type":"dispatch_started","ts":"2026-10-01T12:00:00Z","agent":"` + a + `","runtime":"claude","project":"/x","task_preview":"` + strings.Repeat("t", 150) + `"}` + "\n")
	}
	if err := os.WriteFile(filepath.Join(dir, logFileName), []byte(sb.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkPreflightSteadyState(b *testing.B) {
	dir := b.TempDir()
	bigLog(b, dir, 10000) // about 4.5 MB, the size of the real log
	o := Options{StateDir: dir, Now: clock(octMid)}
	if err := SetLimit(dir, "supervisor", 1e6, Monthly); err != nil {
		b.Fatal(err)
	}
	if _, err := Enforce("supervisor", o); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Enforce("supervisor", o); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPreflightAfterAppend(b *testing.B) {
	dir := b.TempDir()
	bigLog(b, dir, 10000)
	o := Options{StateDir: dir, Now: clock(octMid)}
	_ = SetLimit(dir, "supervisor", 1e6, Monthly)
	_, _ = Enforce("supervisor", o)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		appendLog(b, dir, finished("supervisor", octMid, 0.1))
		if _, err := Enforce("supervisor", o); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRebuildFromLog(b *testing.B) {
	dir := b.TempDir()
	bigLog(b, dir, 10000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := advance(dir, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// TestPreflightLatencyTarget guards the under-10ms target on a log the size
// of the real one. The bound is loose (CI runners vary); the benchmark
// reports the real number.
func TestPreflightLatencyTarget(t *testing.T) {
	dir := t.TempDir()
	bigLog(t, dir, 10000)
	o := Options{StateDir: dir, Now: clock(octMid)}
	setLimit(t, dir, "supervisor", 1e6, Monthly)
	if _, err := Enforce("supervisor", o); err != nil { // cold: builds the cache
		t.Fatal(err)
	}
	const n = 50
	d := make([]time.Duration, n)
	for i := range d {
		s := time.Now()
		if _, err := Enforce("supervisor", o); err != nil {
			t.Fatal(err)
		}
		d[i] = time.Since(s)
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	t.Logf("steady-state pre-flight: median %v, p95 %v", d[n/2], d[n*95/100])
	if d[n/2] > 25*time.Millisecond {
		t.Fatalf("median pre-flight %v exceeds the budget", d[n/2])
	}
}

// The CLI says explicitly when it could not read the spend (K-128 review S12): the
// field is set from the error itself, never from text. A directory where the log
// belongs is a read error on unix; windows opens it without one.
func TestEvaluateReadFailedWhenTheSpendLogCannotBeRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a directory where the log belongs is not a read error on windows")
	}
	dir := t.TempDir()
	setLimit(t, dir, "backend", 10, Monthly)
	if err := os.Mkdir(filepath.Join(dir, logFileName), 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := Evaluate("backend", Options{StateDir: dir, Now: clock(octMid)})
	if err == nil || !st.ReadFailed || st.State != StateOK || st.SpentUSD != 0 {
		t.Fatalf("an unreadable log: err=%v status=%+v", err, st)
	}
	if b, _ := json.Marshal(st); !strings.Contains(string(b), `"read_failed":true`) {
		t.Errorf("read_failed is not in the JSON: %s", b)
	}
	// A read that works never carries the field.
	good := t.TempDir()
	setLimit(t, good, "backend", 10, Monthly)
	st = mustEval(t, "backend", Options{StateDir: good, Now: clock(octMid)})
	if b, _ := json.Marshal(st); st.ReadFailed || strings.Contains(string(b), "read_failed") {
		t.Errorf("a good read carries read_failed: %s", b)
	}
}

// Text a project controls reaches Warnings (and the CLI's stderr): a repeated
// agent_budgets key is echoed back in the YAML error. It must not look like a read
// failure to anything: the status is the real one, the hard stop, and read_failed
// stays false.
func TestProjectConfigTextCannotFakeAReadFailure(t *testing.T) {
	dir, proj := t.TempDir(), t.TempDir()
	setLimit(t, dir, "backend", 10, Monthly)
	appendLog(t, dir, finished("backend", octMid, 10))
	yml := "agent_budgets:\n  \"(failing open)\": 1\n  \"(failing open)\": 2\n"
	if err := os.WriteFile(filepath.Join(proj, ".yakos.yml"), []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Evaluate("backend", Options{StateDir: dir, Project: proj, Now: clock(octMid)})
	if err != nil {
		t.Fatal(err)
	}
	if st.ReadFailed || st.State != StateHardStop {
		t.Errorf("project text must not change the status: %+v", st)
	}
	if !strings.Contains(strings.Join(st.Warnings, "\n"), "(failing open)") {
		t.Errorf("the project's text no longer reaches Warnings, so this test proves nothing: %v", st.Warnings)
	}
}
