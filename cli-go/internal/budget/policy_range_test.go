package budget

// policy_range_test.go: a limit in the policy file that is out of range is ignored with a
// warning and the limit it would have replaced stays (K-136, security review of #330). A
// value that cannot be a limit is a typo or a corrupt edit, not the operator turning the
// budget off; before this it resolved to 0, which switched the supervisor's and
// librarian's built-in limits off. 0 is how a limit is turned off, on purpose.

import (
	"math"
	"strings"
	"testing"
)

func i64(v int64) *int64 { return &v }
func hasWarning(st Status, sub string) bool {
	for _, w := range st.Warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

// Every out-of-range token limit keeps the built-in 33M, and the budget still trips on it.
func TestResolve_OutOfRangeTokenLimitKeepsTheBuiltin(t *testing.T) {
	for name, bad := range map[string]int64{
		"negative":           -5,
		"minus one":          -1,
		"one past the bound": maxTokenLimit + 1,
		"far past the bound": math.MaxInt64,
		"most negative":      math.MinInt64,
	} {
		dir := t.TempDir()
		if err := SavePolicy(dir, Policy{Agents: map[string]AgentLimit{"supervisor": {LimitTokens: i64(bad)}}}); err != nil {
			t.Fatal(err)
		}
		appendLog(t, dir, ledgerLine("supervisor", octMid, "subscription", 40_000_000, 0, 0, 0, 0))
		st := mustEval(t, "supervisor", Options{StateDir: dir, Now: clock(octMid)})
		if st.LimitTokens != 33_000_000 || st.Source != "builtin" || st.State != StateHardStop {
			t.Errorf("%s: an out-of-range limit_tokens must not switch the built-in off: %+v", name, st)
		}
		if !hasWarning(st, "limit_tokens for supervisor ignored") {
			t.Errorf("%s: the ignored value is reported: %v", name, st.Warnings)
		}
	}
}

// The same for the dollar limit: a negative, NaN or infinite value keeps the built-in $100.
func TestResolve_OutOfRangeDollarLimitKeepsTheBuiltin(t *testing.T) {
	for name, bad := range map[string]float64{
		"negative": -1, "NaN": math.NaN(), "+Inf": math.Inf(1), "-Inf": math.Inf(-1),
	} {
		dir := t.TempDir()
		if err := SavePolicy(dir, Policy{Agents: map[string]AgentLimit{"librarian": {LimitUSD: f64(bad)}}}); err != nil {
			t.Fatal(err)
		}
		appendLog(t, dir, `{"type":"dispatch_finished","ts":"`+octMid+`","agent":"librarian","runtime":"claude","usage":{"total_cost_usd":45}}`)
		st := mustEval(t, "librarian", Options{StateDir: dir, Now: clock(octMid)})
		if st.LimitUSD != 40 || st.Source != "builtin" || st.State != StateHardStop {
			t.Errorf("%s: an out-of-range limit_usd must not switch the built-in off: %+v", name, st)
		}
		if !hasWarning(st, "limit_usd for librarian ignored") {
			t.Errorf("%s: the ignored value is reported: %v", name, st.Warnings)
		}
	}
}

// An agent with no built-in stays unlimited, with the warning; a valid value in the same
// entry still applies; and the on-purpose off, 0, still works.
func TestResolve_OutOfRangeValuesAreIgnoredNotApplied(t *testing.T) {
	dir := t.TempDir()
	pol := Policy{Agents: map[string]AgentLimit{
		"backend":    {LimitUSD: f64(-1), LimitTokens: i64(maxTokenLimit + 1)},
		"frontend":   {LimitUSD: f64(25), LimitTokens: i64(-9)},
		"supervisor": {LimitTokens: i64(0)},
		"backup":     {LimitUSD: f64(maxLimitUSD), LimitTokens: i64(maxTokenLimit)},
	}}
	if err := SavePolicy(dir, pol); err != nil {
		t.Fatal(err)
	}
	o := Options{StateDir: dir, Now: clock(octMid)}
	if st := mustEval(t, "backend", o); st.State != StateOff || st.LimitUSD != 0 || st.LimitTokens != 0 ||
		!hasWarning(st, "limit_usd for backend ignored") || !hasWarning(st, "limit_tokens for backend ignored") {
		t.Errorf("an unlimited agent stays unlimited, and says why: %+v", st)
	}
	if st := mustEval(t, "frontend", o); st.LimitUSD != 25 || st.LimitTokens != 0 || !hasWarning(st, "limit_tokens for frontend ignored") {
		t.Errorf("the valid half of an entry applies: %+v", st)
	}
	if st := mustEval(t, "backup", o); st.LimitUSD != maxLimitUSD || st.LimitTokens != maxTokenLimit || st.StopUSD != maxLimitUSD || hasWarning(st, "ignored") {
		t.Errorf("the largest values allowed apply, with no warning: %+v", st)
	}
	if st := mustEval(t, "supervisor", o); st.LimitTokens != 0 || st.LimitUSD != 100 || hasWarning(st, "ignored") {
		t.Errorf("limit_tokens 0 is the on-purpose off for the built-in token limit, with no warning: %+v", st)
	}
}

// The global default's entry gets the same treatment, named as the default.
func TestResolve_OutOfRangeGlobalDefaultIsIgnored(t *testing.T) {
	dir := t.TempDir()
	if err := SavePolicy(dir, Policy{Default: AgentLimit{LimitTokens: i64(-1), LimitUSD: f64(math.NaN())}}); err != nil {
		t.Fatal(err)
	}
	st := mustEval(t, "backend", Options{StateDir: dir, Now: clock(octMid)})
	if st.State != StateOff || !hasWarning(st, "limit_tokens for default ignored") || !hasWarning(st, "limit_usd for default ignored") {
		t.Errorf("%+v", st)
	}
}
