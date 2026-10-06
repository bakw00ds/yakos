package supervisorstream_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/budget"
)

// K-136, spec item 7 of #330 (rev-327's finding): a project's agent_budgets value cannot
// switch the supervisor gate off. With the operator's supervisor dollar limit off (limit_usd
// 0, the token-only budget a subscription operator has) a project value of .inf or 1e308, or
// one below the one-cent floor, used to be applied as the dollar limit, and its stop of twice
// it overflowed to +Inf: json could not encode the status, `budget check --json` printed no
// JSON, and the bash gate, which reads that line alone, launched at a token hard stop while
// this twin refused. The value is now ignored with a warning, the status marshals, and both
// twins refuse the routine launch at 40M of 33M tokens with the token message. Bash twin:
// tests/run-supervisor-budget-test.sh (14).

// projectValueBudget gives the supervisor a token-only budget (dollars off, the built-in 33M
// tokens), a project whose agent_budgets asks for value, and one run of 40M tokens in the hook's
// month. It returns the hook under test, its work directory and the project directory.
func projectValueBudget(t *testing.T, value string) (hookUnderTest, string, string) {
	t.Helper()
	h, rec, work, env := gateHook(t, "min_launch_interval_s: 0\n", "agent_budgets:\n  supervisor: "+value+"\n")
	dir := os.Getenv("YAKOS_DISPATCH_LOG")
	if err := budget.SetLimit(dir, "supervisor", 0, budget.Monthly); err != nil {
		t.Fatal(err)
	}
	if err := budget.SetTokenLimit(dir, "supervisor", 33_000_000, budget.Monthly); err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf(`{"type":"dispatch_finished","ts":%q,"agent":"supervisor","usage":{"input_tokens":40000000,"output_tokens":0,"total_cost_usd":0}}`+"\n",
		fixedNow().UTC().Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(dir, "dispatch-log.ndjson"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	return hookUnderTest{h: h, rec: rec, env: env}, work, h.ProjectDir
}

func TestProjectBudgetValueCannotSwitchTheGateOff(t *testing.T) {
	for _, v := range []string{".inf", "1e308", "1e30", "5e-324", "0.009"} {
		t.Run(v, func(t *testing.T) {
			b, work, proj := projectValueBudget(t, v)

			// the status is what budget check --json prints: it marshals, and it is the token hard stop
			st, err := budget.Evaluate("supervisor", budget.Options{StateDir: os.Getenv("YAKOS_DISPATCH_LOG"), Project: proj, Now: fixedNow})
			if err != nil {
				t.Fatal(err)
			}
			raw, merr := json.Marshal(st)
			if merr != nil {
				t.Fatalf("the status must marshal (budget check --json prints it): %v", merr)
			}
			if st.State != budget.StateHardStop || st.LimitUSD != 0 || st.StopUSD != 0 || st.LimitTokens != 33_000_000 || st.SpentTokens != 40_000_000 {
				t.Fatalf("a token hard stop with no dollar limit, whatever the project asks for: %s", raw)
			}

			// and the Go twin refuses the routine launch, in tokens
			out := bigEditOut(t, b)
			if len(b.rec.specs) != 0 || out.ExitCode != 0 {
				t.Fatalf("a project value of %s must not let a routine launch through a token hard stop: launches %d, exit %d", v, len(b.rec.specs), out.ExitCode)
			}
			rec := recordWith(t, work, "skipping this routine")
			if want := `"spent_tokens":40000000,"limit_tokens":33000000,"budget_reason":"budget_exhausted","kind":"routine"`; !strings.Contains(rec, want) || strings.Contains(rec, "_usd") {
				t.Errorf("the record should be the token message (%s): %s", want, rec)
			}
			if !strings.Contains(string(out.Stderr), "(40000000 of 33000000 tokens)") {
				t.Errorf("stderr = %q", out.Stderr)
			}
			if strings.Contains(allLogs(t, work), "budget_unavailable") {
				t.Errorf("the budget was readable: no unavailable WARN:\n%s", allLogs(t, work))
			}
		})
	}
}
