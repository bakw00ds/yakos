package budget

// Reviewer probes (PR 330, final round), scratch only: spec item 7 of
// work/current/briefs/k136-final-push.md (two fail-open paths into `budget check --json`)
// and the lead's window ruling (only a side with a limit in at least one unit contributes
// its window). Through the public API only.

import (
	"encoding/json"
	"math"
	"testing"
)

func zzFiniteStatus(t *testing.T, label string, st Status) {
	t.Helper()
	for k, v := range map[string]float64{
		"limit_usd": st.LimitUSD, "stop_usd": st.StopUSD, "spent_usd": st.SpentUSD, "pct": st.Pct, "tokens_pct": st.TokensPct,
	} {
		if math.IsInf(v, 0) || math.IsNaN(v) {
			t.Errorf("%s: %s is %v, not finite", label, k, v)
		}
	}
	if _, err := json.Marshal(st); err != nil {
		t.Errorf("%s: the status must marshal, `budget check --json` prints nothing otherwise: %v", label, err)
	}
}

// A tiny positive trusted dollar limit must not push pct to +Inf (spent / limit * 100).
func TestZZR330_TinyPositivePolicyDollarLimitKeepsStatusFinite(t *testing.T) {
	for _, tiny := range []float64{1e-300, 1e-305, 5e-324, 1e-9, 0.001, 0.009} {
		dir := t.TempDir()
		if err := SavePolicy(dir, Policy{Agents: map[string]AgentLimit{"backend": {LimitUSD: zzF(tiny)}}}); err != nil {
			t.Fatal(err)
		}
		appendLog(t, dir, ledgerLine("backend", octMid, "api", 0, 0, 0, 0, 12345.5))
		st, _ := Evaluate("backend", Options{StateDir: dir, Now: clock(octMid)})
		zzFiniteStatus(t, "policy limit_usd "+json.Number(formatFloat(tiny)).String(), st)
	}
}

func formatFloat(v float64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// A project's agent_budgets value is validated like a policy value: an infinite, NaN, huge or
// absurdly small value is ignored with a warning, also when the operator's dollar limit is off
// (then nothing else bounds it), and a valid value still tightens an unlimited agent.
func TestZZR330_ProjectBudgetValuesAreValidatedLikePolicyValues(t *testing.T) {
	bad := []string{".inf", "-.inf", ".nan", "1e308", "1e30", "1000000001", "0.000001", "1e-300", "-5", "0"}
	for _, v := range bad {
		dir := t.TempDir()
		proj := projectWith(t, "agent_budgets:\n  backend: "+v+"\n")
		appendLog(t, dir, ledgerLine("backend", octMid, "api", 0, 0, 0, 0, 12345.5))
		st, _ := Evaluate("backend", Options{StateDir: dir, Project: proj, Now: clock(octMid)})
		zzFiniteStatus(t, "project agent_budgets.backend="+v, st)
		if st.LimitUSD != 0 {
			t.Errorf("project agent_budgets.backend=%s must be ignored for an agent the operator left unlimited: %+v", v, st)
		}
		if len(st.Warnings) == 0 {
			t.Errorf("project agent_budgets.backend=%s must be ignored WITH a warning", v)
		}
	}
	dir := t.TempDir()
	proj := projectWith(t, "agent_budgets:\n  backend: 5\n")
	if st, _ := Evaluate("backend", Options{StateDir: dir, Project: proj, Now: clock(octMid)}); st.LimitUSD != 5 || st.StopUSD != 5 {
		t.Errorf("a valid project value tightens an unlimited agent: %+v", st)
	}
}

// Not adopted: TestZZR330_UnlimitedAgentUnderALifetimeDefaultGetsTheSupervisorsMonthlyWindow. It
// expects monthly for an unlimited agent named as the supervisor under a lifetime DEFAULT window with
// no supervisor entry, but the supervisor itself is lifetime there (the default's window applies to
// every agent, built-ins included: plain Evaluate gives the supervisor lifetime, $100 and 33M), so
// monthly would make the renamed agent looser than the supervisor, which a project must not be able
// to do. window_ruling_test.go pins the ruling where it changes the outcome: the supervisor made
// monthly by an entry, beside an unlimited agent whose lifetime window comes only from the default.
