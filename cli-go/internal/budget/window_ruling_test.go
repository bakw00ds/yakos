package budget

// window_ruling_test.go: the window of the agent a project names as its supervisor (K-136,
// spec item 7 of #330, rev-330's ruling). Only a side that has a limit, in either unit,
// contributes its window: the combined window is lifetime if such a side is lifetime, and a
// side with no limit at all counts none. When neither side has a limit the agent's own window
// is kept. Reset keys its window the same way.

import "testing"

// The distinguishing case: an agent with no limit of its own, whose lifetime window comes only
// from the policy default, named as the supervisor beside a supervisor that an entry makes
// monthly. The agent has nothing to count in a lifetime window, so it gets the supervisor's
// limits in the supervisor's monthly window. (The old rule, lifetime if either side is, made it
// lifetime.)
func TestWindowRuling_AnUnlimitedAgentsDefaultWindowDoesNotCount(t *testing.T) {
	dir := t.TempDir()
	if err := SavePolicy(dir, Policy{Default: AgentLimit{Window: "lifetime"}, Agents: map[string]AgentLimit{"supervisor": {Window: "monthly"}}}); err != nil {
		t.Fatal(err)
	}
	proj := projectWith(t, "supervisor:\n  agent: backend\n")
	o := Options{StateDir: dir, Now: clock(octMid)}
	plain := mustEval(t, "backend", o)
	if plain.Window != Lifetime || plain.HasLimit() {
		t.Fatalf("the setup: backend is unlimited with a lifetime window from the default: %+v", plain)
	}
	o.Project = proj
	named := mustEval(t, "backend", o)
	if named.LimitUSD != 100 || named.StopUSD != 200 || named.LimitTokens != 33_000_000 || named.StopTokens != 66_000_000 {
		t.Fatalf("the unlimited agent gains the supervisor's limits and stop: %+v", named)
	}
	if named.Window != Monthly {
		t.Errorf("window = %s, want the supervisor's monthly: the agent had no limit, so its lifetime default does not count", named.Window)
	}
}

// Without that entry the supervisor itself is lifetime (the default's window applies to every
// agent, built-ins included), so an unlimited agent named as the supervisor gets exactly the
// supervisor's tuple, window included: lifetime. Anything else would make the renamed agent
// looser than the supervisor, which is the thing a project must not be able to do.
func TestWindowRuling_AnUnlimitedAgentGetsExactlyTheSupervisorsTuple(t *testing.T) {
	dir := t.TempDir()
	if err := SavePolicy(dir, Policy{Default: AgentLimit{Window: "lifetime"}}); err != nil {
		t.Fatal(err)
	}
	o := Options{StateDir: dir, Now: clock(octMid)}
	sup := mustEval(t, "supervisor", o)
	o.Project = projectWith(t, "supervisor:\n  agent: backend\n")
	named := mustEval(t, "backend", o)
	if sup.Window != Lifetime {
		t.Fatalf("the setup: the supervisor under a lifetime default is lifetime: %+v", sup)
	}
	if named.Window != sup.Window || named.LimitUSD != sup.LimitUSD || named.StopUSD != sup.StopUSD || named.LimitTokens != sup.LimitTokens || named.StopTokens != sup.StopTokens {
		t.Errorf("named %+v is not the supervisor's %+v", named, sup)
	}
}

// A side that has a limit in tokens only counts too.
func TestWindowRuling_ATokenOnlyLimitCountsItsWindow(t *testing.T) {
	dir := t.TempDir()
	setTokenLimit(t, dir, "backend", 1_000_000, Lifetime) // own: tokens only, lifetime
	setLimit(t, dir, "supervisor", 100, Monthly)
	o := Options{StateDir: dir, Now: clock(octMid), Project: projectWith(t, "supervisor:\n  agent: backend\n")}
	if st := mustEval(t, "backend", o); st.Window != Lifetime || st.LimitTokens != 1_000_000 {
		t.Errorf("an own token limit in a lifetime window keeps the lifetime window: %+v", st)
	}
}

// When neither side has a limit the status is off and the agent's own window is kept.
func TestWindowRuling_NeitherSideLimitedKeepsTheAgentsWindow(t *testing.T) {
	dir := t.TempDir()
	if err := SavePolicy(dir, Policy{Default: AgentLimit{Window: "lifetime"}, Agents: map[string]AgentLimit{
		"supervisor": {LimitUSD: f64(0), LimitTokens: i64(0), Window: "monthly"}}}); err != nil {
		t.Fatal(err)
	}
	o := Options{StateDir: dir, Now: clock(octMid), Project: projectWith(t, "supervisor:\n  agent: backend\n")}
	if st := mustEval(t, "backend", o); st.State != StateOff || st.HasLimit() || st.Window != Lifetime {
		t.Errorf("both sides off: the status is off in the agent's own window: %+v", st)
	}
}

// Reset keys its window the same way: with the monthly window the ruling gives, a reset lifts
// the hard stop and the status counts from the reset.
func TestWindowRuling_ResetUsesTheRulingsWindow(t *testing.T) {
	dir := t.TempDir()
	if err := SavePolicy(dir, Policy{Default: AgentLimit{Window: "lifetime"}, Agents: map[string]AgentLimit{"supervisor": {Window: "monthly"}}}); err != nil {
		t.Fatal(err)
	}
	appendLog(t, dir,
		ledgerLine("backend", "2026-08-15T12:00:00Z", "api", 0, 0, 0, 0, 90),
		ledgerLine("backend", octMid, "api", 0, 0, 0, 0, 150))
	o := Options{StateDir: dir, Now: clock(octMid), Project: projectWith(t, "supervisor:\n  agent: backend\n")}
	st := mustEval(t, "backend", o)
	if st.Window != Monthly || st.State != StateHardStop || st.SpentUSD != 150 {
		t.Fatalf("monthly: this month's $150 is past $100, last August's $90 does not count: %+v", st)
	}
	if _, err := Reset("backend", o); err != nil {
		t.Fatal(err)
	}
	if st := mustEval(t, "backend", o); st.State == StateHardStop || st.SpentUSD != 0 || st.Window != Monthly {
		t.Errorf("a reset in the ruling's monthly window lifts the stop: %+v", st)
	}
}
