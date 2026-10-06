package supervisorstream_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/budget"
)

// K-136: a project that names its supervisor (supervisor: agent: watchdog) makes the
// hook launch, and budget, THAT agent. The Go twin reads the name as YAML; the
// budget package reads the same file and combines the agent's own limits with the
// supervisor's into one tuple on the agent's one spend counter (budget.tighter): per
// unit the smaller amount, per unit the smaller absolute stop, lifetime if a side that
// has a limit is lifetime. A renamed supervisor is never budgeted more loosely than the
// supervisor is. These tests drive the launch gate over that, in-process; the bash
// twin reads the name with a line scan and the combined tuple through
// `yakos budget check --json`: tests/run-supervisor-budget-test.sh (13).

const renamedAgent = "watchdog"

// renamedBudget names renamedAgent as the project's supervisor, gives it the
// operator's token limit in window w when tokLimit >= 0 (the supervisor keeps its
// built-in 100 dollars and 33,000,000 tokens, with a stop of twice each), and
// records one run of that agent that spent tokens tokens at ts. The row has no
// billing field, so it counts like a legacy row. It returns the hook under test
// and its work directory.
func renamedBudget(t *testing.T, tokLimit int64, w budget.Window, tokens int64, ts time.Time) (hookUnderTest, string) {
	t.Helper()
	h, rec, work, env := gateHook(t, "min_launch_interval_s: 0\n", "  agent: "+renamedAgent+"\n")
	dir := os.Getenv("YAKOS_DISPATCH_LOG")
	if tokLimit >= 0 {
		if err := budget.SetTokenLimit(dir, renamedAgent, tokLimit, w); err != nil {
			t.Fatal(err)
		}
	}
	if tokens > 0 {
		line := fmt.Sprintf(`{"type":"dispatch_finished","ts":%q,"agent":%q,"usage":{"input_tokens":%d,"output_tokens":0,"total_cost_usd":0}}`+"\n",
			ts.UTC().Format(time.RFC3339), renamedAgent, tokens)
		if err := os.WriteFile(filepath.Join(dir, "dispatch-log.ndjson"), []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return hookUnderTest{h: h, rec: rec, env: env}, work
}

// renamedRecord is recordWith for a record that must also name the renamed agent: the
// hook writes the budget agent's name as the record's agent field.
func renamedRecord(t *testing.T, work, sub string) string {
	t.Helper()
	rec := recordWith(t, work, sub)
	if !strings.Contains(rec, `"agent":"`+renamedAgent+`"`) {
		t.Errorf("the record must name the renamed agent %q: %s", renamedAgent, rec)
	}
	return rec
}

// An agent with no limits of its own, named as the supervisor, is budgeted at the
// supervisor's: 40M of its 33M tokens refuses a routine launch, in the renamed
// agent's name; the supervisor's stop (66M) is the ceiling a high-risk launch runs
// under, and at it the one CRITICAL names that ceiling.
func TestRenamedSupervisorGainsTheSupervisorsTokenLimit(t *testing.T) {
	b, work := renamedBudget(t, -1, budget.Monthly, 40_000_000, fixedNow())
	out := bigEditOut(t, b)
	if len(b.rec.specs) != 0 || out.ExitCode != 0 {
		t.Fatalf("a routine launch of the renamed supervisor past the supervisor's token limit must be refused (exit 0): launches %d, exit %d", len(b.rec.specs), out.ExitCode)
	}
	rec := renamedRecord(t, work, "skipping this routine")
	if want := `"spent_tokens":40000000,"limit_tokens":33000000,"budget_reason":"budget_exhausted","kind":"routine"`; !strings.Contains(rec, want) || strings.Contains(rec, "_usd") {
		t.Errorf("the record should name the renamed agent and the supervisor's 33M limit, in tokens only (%s): %s", want, rec)
	}
	if !strings.Contains(string(out.Stderr), "(40000000 of 33000000 tokens)") {
		t.Errorf("stderr = %q", out.Stderr)
	}

	b2, work2 := renamedBudget(t, -1, budget.Monthly, 40_000_000, fixedNow())
	if out := riskEdit(t, b2.h, b2.env); out.ExitCode != 0 || len(b2.rec.specs) != 1 {
		t.Fatalf("a high-risk launch under the supervisor's stop must run: exit %d launches %d", out.ExitCode, len(b2.rec.specs))
	}
	rec = renamedRecord(t, work2, "high-risk launch allowed under the ceiling")
	if want := `"spent_tokens":40000000,"limit_tokens":33000000,"ceiling_tokens":66000000,"budget_reason":"budget_exhausted"`; !strings.Contains(rec, want) {
		t.Errorf("the exempt note should carry the supervisor's 66M stop (%s): %s", want, rec)
	}

	b3, work3 := renamedBudget(t, -1, budget.Monthly, 66_000_000, fixedNow())
	riskEdit(t, b3.h, b3.env)
	if len(b3.rec.specs) != 0 {
		t.Fatalf("at the supervisor's stop nothing launches, got %d launches", len(b3.rec.specs))
	}
	if f := findings(t, work3); !strings.Contains(f, "Supervisor token-budget ceiling (66000000 tokens) reached") {
		t.Errorf("the CRITICAL should name the 66M ceiling: %s", f)
	}
}

// An operator limit LOOSER than the supervisor's does not loosen it: naming the
// agent adds the supervisor's limit to it (the smaller amount per unit), so 40M of
// the agent's own 500M tokens still refuses at the supervisor's 33M, and the stop
// stays the supervisor's 66M (the smaller absolute stop).
func TestRenamedSupervisorAnOwnLimitLooserThanTheSupervisorsDoesNotLoosenIt(t *testing.T) {
	b, work := renamedBudget(t, 500_000_000, budget.Monthly, 40_000_000, fixedNow())
	bigEditOut(t, b)
	if len(b.rec.specs) != 0 {
		t.Fatalf("40M tokens is past the supervisor's 33M: a routine launch must be refused, got %d launches", len(b.rec.specs))
	}
	if rec := renamedRecord(t, work, "skipping this routine"); !strings.Contains(rec, `"spent_tokens":40000000,"limit_tokens":33000000,`) {
		t.Errorf("the limit is the supervisor's 33M, not the agent's own 500M: %s", rec)
	}
	b2, work2 := renamedBudget(t, 500_000_000, budget.Monthly, 40_000_000, fixedNow())
	riskEdit(t, b2.h, b2.env)
	if rec := renamedRecord(t, work2, "high-risk launch allowed under the ceiling"); !strings.Contains(rec, `"limit_tokens":33000000,"ceiling_tokens":66000000,`) {
		t.Errorf("the stop is the supervisor's 66M: %s", rec)
	}
}

// The other direction: an own limit stricter than the supervisor's holds, because
// naming the agent a supervisor never raises it. 1500 of its own 1000 tokens refuses,
// where the supervisor's 33M alone would not.
func TestRenamedSupervisorKeepsItsOwnStricterTokenLimit(t *testing.T) {
	b, work := renamedBudget(t, 1000, budget.Monthly, 1500, fixedNow())
	bigEditOut(t, b)
	if len(b.rec.specs) != 0 {
		t.Fatalf("1500 of the agent's own 1000 tokens must refuse a routine launch, got %d launches", len(b.rec.specs))
	}
	if rec := renamedRecord(t, work, "skipping this routine"); !strings.Contains(rec, `"spent_tokens":1500,"limit_tokens":1000,"budget_reason":"budget_exhausted","kind":"routine"`) {
		t.Errorf("the limit is the agent's own 1000: %s", rec)
	}
}

// Each unit's stop is the smaller of the two sides' ABSOLUTE stops, not the stop of
// whichever side has the smaller amount. Own 50M (stop 50M, factor 1) beside the
// supervisor's 33M (stop 66M) is a 33M limit with a 50M stop: a high-risk launch at
// 45M runs under a 50M ceiling, and at 55M, past it, nothing launches and the one
// CRITICAL names 50M. (Taking the stop from the 33M side gives 66M, which would let
// the 55M launch through.)
func TestRenamedSupervisorStopIsTheSmallerAbsoluteStop(t *testing.T) {
	b, work := renamedBudget(t, 50_000_000, budget.Monthly, 45_000_000, fixedNow())
	if out := riskEdit(t, b.h, b.env); out.ExitCode != 0 || len(b.rec.specs) != 1 {
		t.Fatalf("45M is under the 50M stop: the high-risk launch must run: exit %d launches %d", out.ExitCode, len(b.rec.specs))
	}
	if rec := renamedRecord(t, work, "high-risk launch allowed under the ceiling"); !strings.Contains(rec, `"spent_tokens":45000000,"limit_tokens":33000000,"ceiling_tokens":50000000,"budget_reason":"budget_exhausted"`) {
		t.Errorf("the ceiling is the agent's own 50M stop, not the supervisor's 66M: %s", rec)
	}

	b2, work2 := renamedBudget(t, 50_000_000, budget.Monthly, 55_000_000, fixedNow())
	riskEdit(t, b2.h, b2.env)
	if len(b2.rec.specs) != 0 {
		t.Fatalf("55M is past the 50M stop: nothing may launch, got %d launches", len(b2.rec.specs))
	}
	f := findings(t, work2)
	if !strings.Contains(f, "Supervisor token-budget ceiling (50000000 tokens) reached") || strings.Contains(f, "66000000") {
		t.Errorf("the CRITICAL should name the 50M ceiling: %s", f)
	}
}

// The window is lifetime when either side is: an own lifetime limit with a run from
// months ago counts it (40M of the supervisor's 33M, in a lifetime window), where the
// same run beside a monthly own limit is outside the month and does not.
func TestRenamedSupervisorWindowIsLifetimeIfEitherSideIs(t *testing.T) {
	old := fixedNow().AddDate(0, -6, 0)
	b, work := renamedBudget(t, 500_000_000, budget.Lifetime, 40_000_000, old)
	bigEditOut(t, b)
	if len(b.rec.specs) != 0 {
		t.Fatalf("a lifetime own window makes the combined window lifetime: the old run counts and refuses, got %d launches", len(b.rec.specs))
	}
	if rec := renamedRecord(t, work, "skipping this routine"); !strings.Contains(rec, `"spent_tokens":40000000,"limit_tokens":33000000,`) {
		t.Errorf("the old run should count in the lifetime window: %s", rec)
	}

	b2, work2 := renamedBudget(t, 500_000_000, budget.Monthly, 40_000_000, old)
	bigEditOut(t, b2)
	if len(b2.rec.specs) != 1 {
		t.Fatalf("both sides monthly: a run from six months ago is outside the month and must not count, got %d launches\n%s", len(b2.rec.specs), allLogs(t, work2))
	}
}
