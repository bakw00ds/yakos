package supervisorstream_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/budget"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/supervisorstream"
)

// K-119 F1: the supervisor dollar budget in the launch gate (the token budget,
// K-136, is in budget_token_gate_test.go). Bash twin:
// tests/run-supervisor-budget-test.sh.

// budgetState sets the supervisor limit and records spend in the same month
// as the hook's clock. It returns the state dir.
func budgetState(t *testing.T, limit, spent float64) string {
	t.Helper()
	dir := os.Getenv("YAKOS_DISPATCH_LOG")
	if err := budget.SetLimit(dir, "supervisor", limit, budget.Monthly); err != nil {
		t.Fatal(err)
	}
	if spent > 0 {
		line := fmt.Sprintf(`{"type":"dispatch_finished","ts":%q,"agent":"supervisor","usage":{"total_cost_usd":%v}}`+"\n", fixedNow().UTC().Format(time.RFC3339), spent)
		if err := os.WriteFile(filepath.Join(dir, "dispatch-log.ndjson"), []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func budgetHook(t *testing.T, limit, spent float64) (hookUnderTest, string) {
	t.Helper()
	h, rec, work, env := gateHook(t, "min_launch_interval_s: 0\n", "")
	budgetState(t, limit, spent)
	return hookUnderTest{h: h, rec: rec, env: env}, work
}

func findings(t *testing.T, work string) string {
	data, _ := os.ReadFile(filepath.Join(work, "supervisor-findings.ndjson"))
	return string(data)
}

func TestBudgetRoutineRefusedAtHardStop(t *testing.T) {
	b, work := budgetHook(t, 100, 100)
	bigEdit(t, b.h, b.env, 1)
	if len(b.rec.specs) != 0 {
		t.Fatalf("a routine launch at hard_stop must be refused, got %d launches", len(b.rec.specs))
	}
	logs := allLogs(t, work)
	if !strings.Contains(logs, "supervisor budget exhausted; skipping this routine") || !strings.Contains(logs, `"budget_reason":"budget_exhausted"`) {
		t.Errorf("no WARN in the hook log:\n%s", logs)
	}
	if strings.Contains(logs, "forked async") {
		t.Error("a refused launch must not log forked async")
	}
	out := bigEditOut(t, b)
	if out.ExitCode != 0 {
		t.Fatalf("the hook must exit 0, got %d", out.ExitCode)
	}
	if !strings.Contains(string(out.Stderr), "supervisor budget exhausted") || strings.Count(string(out.Stderr), "\n") != 1 {
		t.Errorf("want exactly one stderr line, got %q", out.Stderr)
	}
}

func TestBudgetHighRiskExemptUpToCeilingThenCritical(t *testing.T) {
	// Spent 150 of 100: past the limit, under the 2x ceiling.
	b, work := budgetHook(t, 100, 150)
	out := riskEdit(t, b.h, b.env)
	if out.ExitCode != 0 || len(b.rec.specs) != 1 {
		t.Fatalf("a high-risk launch under the ceiling must run: exit %d launches %d", out.ExitCode, len(b.rec.specs))
	}
	if !strings.Contains(allLogs(t, work), "high-risk launch allowed under the ceiling") {
		t.Error("the exempt launch must be noted in the hook log")
	}
	if findings(t, work) != "" {
		t.Error("no CRITICAL finding below the ceiling")
	}
	// Spent 200 of 100: at the ceiling.
	b2, work2 := budgetHook(t, 100, 200)
	out = riskEdit(t, b2.h, b2.env)
	if out.ExitCode != 0 || len(b2.rec.specs) != 0 {
		t.Fatalf("past the ceiling nothing launches: exit %d launches %d", out.ExitCode, len(b2.rec.specs))
	}
	f := findings(t, work2)
	if !strings.Contains(f, `"overall":"CRITICAL"`) || !strings.Contains(f, `"synthetic":true`) || !strings.Contains(f, "dollar-budget ceiling") {
		t.Errorf("want a synthetic CRITICAL finding, got %q", f)
	}
	if !strings.Contains(string(out.Stderr), "ceiling") {
		t.Errorf("stderr %q", out.Stderr)
	}
	// Once per session: a second high-risk event adds no second finding.
	riskEdit(t, b2.h, b2.env)
	if n := strings.Count(findings(t, work2), "\n"); n != 1 {
		t.Errorf("%d findings, want 1", n)
	}
}

func TestBudgetWarningWarns(t *testing.T) {
	b, work := budgetHook(t, 100, 85)
	out := bigEditOut(t, b)
	if out.ExitCode != 0 || len(b.rec.specs) != 1 {
		t.Fatalf("warning must still launch: exit %d launches %d", out.ExitCode, len(b.rec.specs))
	}
	if !strings.Contains(allLogs(t, work), `"budget_reason":"budget_warning"`) || !strings.Contains(string(out.Stderr), "supervisor budget at 85%") {
		t.Errorf("logs:\n%s\nstderr %q", allLogs(t, work), out.Stderr)
	}
}

func TestBudgetOffAndUnderLimitUntouched(t *testing.T) {
	b, work := budgetHook(t, 100, 10)
	out := bigEditOut(t, b)
	if len(b.rec.specs) != 1 || strings.Contains(allLogs(t, work), "budget") || strings.Contains(string(out.Stderr), "budget") {
		t.Fatalf("under the warning threshold nothing may change: %q\n%s", out.Stderr, allLogs(t, work))
	}
}

// K-128 (S3): a budget whose spend cannot be read still fails open, as documented,
// but no longer silently: one WARN names the cause, ahead of the launch's own
// record, and nothing is printed on stderr. Bash twin:
// tests/run-supervisor-budget-test.sh (10).
func TestBudgetUnreadableSpendWarnsAndFailsOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("there is no portable way to make the spend log unreadable on windows: a directory where " +
			"it belongs opens fine there (CI saw the launch go ahead with no WARN) and a chmod 000 file stays readable")
	}
	b, work := budgetHook(t, 100, 0)
	// A directory where the spend log belongs: the read fails whoever runs the
	// test (a chmod 000 file would not stop root).
	if err := os.Mkdir(filepath.Join(os.Getenv("YAKOS_DISPATCH_LOG"), "dispatch-log.ndjson"), 0o700); err != nil {
		t.Fatal(err)
	}
	out := bigEditOut(t, b)
	if out.ExitCode != 0 || len(b.rec.specs) != 1 {
		t.Fatalf("an unreadable spend log must fail open: exit %d launches %d", out.ExitCode, len(b.rec.specs))
	}
	warn, launch, n := -1, -1, 0
	for i, rec := range logMessages(t, work) {
		switch {
		case strings.Contains(rec, `"budget_reason":"budget_unavailable"`):
			warn, n = i, n+1
			for _, want := range []string{`"severity":"WARN"`, `"decision":"pass"`, `"cause":"read_error"`, "(cause: read_error); failing open", `"agent":"supervisor"`} {
				if !strings.Contains(rec, want) {
					t.Errorf("the WARN lacks %s: %s", want, rec)
				}
			}
		case strings.Contains(rec, "forked async"):
			launch = i
		}
	}
	if n != 1 {
		t.Fatalf("want exactly one budget_unavailable WARN, got %d:\n%s", n, allLogs(t, work))
	}
	if launch < 0 || warn > launch {
		t.Errorf("the WARN (record %d) must come ahead of the launch record (%d)", warn, launch)
	}
	if strings.Contains(string(out.Stderr), "budget") {
		t.Errorf("the WARN is a hook-log record only, got stderr %q", out.Stderr)
	}
}

// A budget that is switched off (a limit of 0 on BOTH units: the supervisor's
// built-in token limit stays on when only its dollar limit is turned off, see
// TestDollarLimitOffLeavesTheTokenLimitGated) is not a failed read: no WARN.
func TestBudgetOffIsNotUnavailable(t *testing.T) {
	b, work := budgetHook(t, 0, 0)
	if err := budget.SetTokenLimit(os.Getenv("YAKOS_DISPATCH_LOG"), "supervisor", 0, budget.Monthly); err != nil {
		t.Fatal(err)
	}
	out := bigEditOut(t, b)
	if out.ExitCode != 0 || len(b.rec.specs) != 1 {
		t.Fatalf("a budget that is off must not stop a launch: exit %d launches %d", out.ExitCode, len(b.rec.specs))
	}
	if logs := allLogs(t, work); strings.Contains(logs, "budget") {
		t.Errorf("nothing budget-related may be logged when the budget is off:\n%s", logs)
	}
}

// K-128 review S12: a project's own words never make the budget read look failed. A
// repeated agent_budgets key spelled like the CLI's notice is echoed back in the YAML
// error (the bash twin once took that for an unreadable spend log and launched at the
// hard stop): the hook refuses at the hard stop and logs no budget_unavailable record.
// Bash twin: tests/run-supervisor-budget-test.sh (10), "spoof".
func TestBudgetProjectConfigTextCannotFakeAReadFailure(t *testing.T) {
	h, rec, work, env := gateHook(t, "min_launch_interval_s: 0\n", "")
	budgetState(t, 100, 100)
	yml := "supervisor:\n  score_every_n_calls: 1\nagent_budgets:\n  \"(failing open)\": 1\n  \"(failing open)\": 2\n"
	if err := os.WriteFile(filepath.Join(h.ProjectDir, ".yakos.yml"), []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	bigEdit(t, h, env, 1)
	if len(rec.specs) != 0 {
		t.Fatalf("launched at the hard stop; launches=%d\n%s", len(rec.specs), allLogs(t, work))
	}
	logs := allLogs(t, work)
	if !strings.Contains(logs, "supervisor budget exhausted; skipping this routine") {
		t.Errorf("no budget refusal:\n%s", logs)
	}
	if strings.Contains(logs, "budget_unavailable") {
		t.Errorf("the project's text made the read look failed:\n%s", logs)
	}
}

func TestBudgetProjectCannotLoosen(t *testing.T) {
	// The user limit is 100 and spend 150. A project asking for 1000 is
	// ignored, so routine launches are still refused.
	h, rec, work, env := gateHook(t, "min_launch_interval_s: 0\n", "")
	budgetState(t, 100, 150)
	proj := h.ProjectDir
	if err := os.WriteFile(filepath.Join(proj, ".yakos.yml"), []byte("supervisor:\n  score_every_n_calls: 1\nagent_budgets:\n  supervisor: 1000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bigEdit(t, h, env, 1)
	if len(rec.specs) != 0 {
		t.Fatalf("a project must not loosen the supervisor budget; launches=%d\n%s", len(rec.specs), allLogs(t, work))
	}
}

func TestBudgetJevShadowStillRunsAtHardStop(t *testing.T) {
	// The shadow decision runs before the counter and the gate, so it is
	// unaffected: the pre-filter ESCALATE record is still written.
	b, work := budgetHook(t, 100, 150)
	bigEdit(t, b.h, b.env, 1)
	if !strings.Contains(allLogs(t, work), "pre-filter: ESCALATE") {
		t.Error("the local pre-filter must keep running at hard_stop")
	}
}

type hookUnderTest struct {
	h   *supervisorstream.Hook
	rec *recorder
	env map[string]string
}

func bigEditOut(t *testing.T, b hookUnderTest) hooktype.HookOutput {
	t.Helper()
	in := makeInput("Edit", "big.go", strings.Repeat("line\n", 25))
	in.Env = b.env
	out, err := b.h.Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
