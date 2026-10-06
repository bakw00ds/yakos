package supervisorstream_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/supervisorstream"
)

// K-128: the lock budget, the journal records and the concurrency timing. Bash
// twin: the (k128) section of tests/run-supervisor-stream-test.sh.

func journalCount(t *testing.T, work, prefix string) int {
	t.Helper()
	entries, err := os.ReadDir(work)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix+".add.") {
			n++
		}
	}
	return n
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Ten hooks cross at once, every one holding the gate for 20 ms (the K-117
// seam): the lock must never run out of budget and every hold must be short.
// This is the 10-concurrent-hooks timing target; before K-128 the bash twin's
// holds were 100-300 ms of forks each and the last hooks gave up.
func TestLockTenConcurrentHooksStayWithinTarget(t *testing.T) {
	h, rec, work, env := gateHook(t, "min_launch_interval_s: 0\n", "")
	stats := filepath.Join(work, ".supervisor-lock-stats")
	t.Setenv("YAKOS_TEST_SEAMS", "1")
	t.Setenv("YAKOS_TEST_GATE_HOLD_MS", "20")
	t.Setenv("YAKOS_TEST_LOCK_STATS", "1")
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			in := makeInput("Edit", "big.go", strings.Repeat("line\n", 25))
			in.Env = env
			if _, err := h.Run(context.Background(), in); err != nil { // t.Error, never t.Fatal, off the test goroutine
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	// Generous: the race detector and a shared runner slow this several times over,
	// and the lock stats below are the real check. The ceiling the hooks used to
	// exhaust was 3 s.
	if el := time.Since(start); el > 4*time.Second {
		t.Errorf("10 concurrent hooks took %v, want well under the lock ceiling's reach", el)
	}
	all := allLogs(t, work)
	if strings.Contains(all, "lock busy") {
		t.Errorf("a hook ran out of lock budget:\n%s", all)
	}
	if len(rec.specs) != 1 || strings.Count(all, `"coalesced":true`) != 9 {
		t.Errorf("launches=%d coalesced=%d, want 1 and 9", len(rec.specs), strings.Count(all, `"coalesced":true`))
	}
	data, err := os.ReadFile(stats)
	if err != nil {
		t.Fatal(err)
	}
	var oks, fails int
	holds := map[string][]int{} // hold_us by lock label
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.Contains(line, " FAIL ") {
			fails++
			continue
		}
		oks++
		fields := strings.Fields(line)
		for _, kv := range fields[4:] {
			k, v, _ := strings.Cut(kv, "=")
			n := 0
			for _, c := range v {
				n = n*10 + int(c-'0')
			}
			if k == "hold_us" {
				holds[fields[2]] = append(holds[fields[2]], n)
				if n > 1_500_000 {
					t.Errorf("a hold took %d us: %s", n, line)
				}
			}
		}
	}
	if fails != 0 || oks != 20 {
		t.Errorf("lock takes: %d ok, %d failed; want 20 (10 counter + 10 gate) and 0", oks, fails)
	}
	// Nothing slow runs under a lock (the Go twin forks nothing). A single hold can be
	// stretched by a descheduled holder on a shared runner, but the MEDIAN of ten
	// cannot, so it is what pins the property: a gate hold is the 20 ms seam plus a
	// state write, a counter hold a counter write. 60 ms of extra work under either
	// lock moves the median past these bounds (the measured medians are 21 ms and 0.4 ms).
	t.Logf("lock holds (us) by label: %v", holds)
	for label, limitUS := range map[string]int{"gate": 80_000, "counter": 40_000} {
		hs := holds[label]
		if len(hs) != 10 {
			t.Errorf("%s holds recorded: %d, want 10", label, len(hs))
			continue
		}
		sort.Ints(hs)
		if med := hs[len(hs)/2]; med > limitUS {
			t.Errorf("median %s lock hold is %d us, want <= %d: work is running under the lock (%v)", label, med, limitUS, hs)
		}
	}
}

// A hook that cannot take the lock does not drop its increment: the next holder
// folds it in and, because two increments were owed, covers the crossing the
// plain modulo would have missed (cur 3 is not a multiple of 2, but 2 was passed).
func TestCounterLockBusyJournalsAndNextHookCoversCrossing(t *testing.T) {
	defer supervisorstream.SetLockBudgetForTest(200 * time.Millisecond)()
	work, proj := t.TempDir(), t.TempDir()
	t.Setenv("YAKOS_DISPATCH_LOG", t.TempDir())
	writeYAML(t, proj, "supervisor:\n  score_every_n_calls: 2\n")
	rec := &recorder{}
	h := newHook(work, proj)
	h.Launch = rec.launch
	env := map[string]string{"YAKOS_CLI": "/fake/yakos"}

	lock := filepath.Join(work, ".supervisor-counter.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	bigEdit(t, h, env, 2) // both give up and journal
	if n := journalCount(t, work, ".supervisor-counter"); n != 2 {
		t.Fatalf("journal records = %d, want 2", n)
	}
	if _, err := os.Stat(filepath.Join(work, ".supervisor-counter")); err == nil {
		t.Error("the counter was written without the lock")
	}
	if len(rec.specs) != 0 {
		t.Errorf("launched %d times without the lock", len(rec.specs))
	}
	if !strings.Contains(allLogs(t, work), "increment journaled for the next lock holder") {
		t.Error("no WARN for the journaled increments")
	}

	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	bigEdit(t, h, env, 1)
	if got := readFile(t, filepath.Join(work, ".supervisor-counter")); strings.TrimSpace(got) != "3" {
		t.Errorf("counter = %q, want 3 (2 folded + own)", got)
	}
	if n := journalCount(t, work, ".supervisor-counter"); n != 0 {
		t.Errorf("%d records left after the fold", n)
	}
	if len(rec.specs) != 1 {
		t.Errorf("launches = %d, want 1: the folded increments crossed the threshold", len(rec.specs))
	}
	if !strings.Contains(allLogs(t, work), `"counter":3,"score_every":2,"will_score":true`) {
		t.Errorf("no threshold record for counter 3:\n%s", allLogs(t, work))
	}
}

// A high-risk trigger that misses the counter lock also owes the session's run
// state a record, so it is still covered by the next supervisor run.
func TestCounterLockBusyHighRiskAlsoJournalsTheGate(t *testing.T) {
	defer supervisorstream.SetLockBudgetForTest(150 * time.Millisecond)()
	h, _, work, env := gateHook(t, "min_launch_interval_s: 0\n", "")
	if err := os.WriteFile(filepath.Join(work, ".supervisor-counter.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	riskEdit(t, h, env)
	if n := journalCount(t, work, ".supervisor-counter"); n != 1 {
		t.Errorf("counter records = %d, want 1", n)
	}
	if n := journalCount(t, work, ".supervisor-run.nosession"); n != 1 {
		t.Fatalf("gate records = %d, want 1", n)
	}
	entries, _ := os.ReadDir(work)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".supervisor-run.nosession.add.") {
			body := readFile(t, filepath.Join(work, e.Name()))
			if !strings.HasPrefix(body, "high=1\n{") {
				t.Errorf("gate record = %q, want high=1 and the event", body)
			}
		}
	}
}

// A trigger a hook journaled because it could not take the lock is folded by the
// wrapper too: when it is there at the claim it joins the claim of the run.
func TestWrapperFoldsJournaledTriggersAtTheClaim(t *testing.T) {
	dir := t.TempDir()
	c := wrapCfg(dir)
	runs := filepath.Join(dir, "runs")
	seedState(t, c, "start=1\nlaunches=1\nlast=1\npending=0\nhigh=0\n")
	rec := c.State + ".add.9.1"
	if err := os.WriteFile(rec, []byte("high=1\n{\"e\":1}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	helperRun(t, c, "run", 10, map[string]string{"SS_RUNS": runs})
	if n := countLines(runs); n != 1 {
		t.Fatalf("runs = %d, want 1 (the claim covers the folded trigger)", n)
	}
	if !strings.Contains(readFile(t, runs), "Coalesced events: 1") {
		t.Errorf("task lacks the folded trigger:\n%s", readFile(t, runs))
	}
	if !strings.Contains(readFile(t, c.Pending+".run"), `"e":1`) {
		t.Errorf("the claimed file lacks the preview:\n%s", readFile(t, c.Pending+".run"))
	}
	if _, err := os.Stat(rec); err == nil {
		t.Error("the record was not removed after the claim")
	}
	if st := readState(t, c); !strings.Contains(st, "pending=0") || !strings.Contains(st, "start=\n") {
		t.Errorf("state after the drain:\n%s", st)
	}
}

// ... and when it lands while the run is in flight, the end-of-run decision folds
// it, so the trigger still gets its follow-up run.
func TestWrapperFollowUpForATriggerJournaledMidRun(t *testing.T) {
	dir := t.TempDir()
	c := wrapCfg(dir)
	runs := filepath.Join(dir, "runs")
	seedState(t, c, "start=1\nlaunches=1\nlast=1\npending=0\nhigh=0\n")
	rec := c.State + ".add.9.1"
	go func() {
		time.Sleep(250 * time.Millisecond)
		_ = os.WriteFile(rec, []byte("high=0\n{\"e\":2}\n"), 0o600)
	}()
	helperRun(t, c, "run", 800, map[string]string{"SS_RUNS": runs})
	if n := countLines(runs); n != 2 {
		t.Errorf("runs = %d, want 2 (the run, then one follow-up for the journaled trigger)", n)
	}
	if _, err := os.Stat(rec); err == nil {
		t.Error("the record was not removed after the fold")
	}
}

// A counter that cannot be written (a directory in its place) must not make the
// hook drop its tick without a word (K-128 security review S2), and a high-risk
// trigger still reaches the session's run state. Bash twin: (k13).
func TestCounterNotWritableWarnsAndStillRecordsAHighRiskTrigger(t *testing.T) {
	h, _, work, env := gateHook(t, "min_launch_interval_s: 0\n", "")
	if err := os.Mkdir(filepath.Join(work, ".supervisor-counter"), 0o755); err != nil {
		t.Fatal(err)
	}
	riskEdit(t, h, env)
	if all := allLogs(t, work); !strings.Contains(all, "counter not writable; skipping this escalation tick") {
		t.Errorf("no WARN for the unwritable counter:\n%s", all)
	}
	if got := stateField(t, work, "pending"); got != "1" {
		t.Errorf("pending = %q, want 1: the high-risk trigger was not recorded", got)
	}
	if got := stateField(t, work, "high"); got != "1" {
		t.Errorf("high = %q, want 1", got)
	}
}
