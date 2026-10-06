package budget

// supervisor_alias_tighten_test.go: the agent a project names as its supervisor is
// budgeted at the STRICTER of its own limit and the supervisor's (K-136, security and
// code review of #330). A project file is the attacker in this model, so naming an agent
// the supervisor may add limits to an agent that has none, and may never raise anything:
// per unit the smaller amount, per unit the smaller absolute stop, the window lifetime if
// either side is lifetime, and an off or unlimited unit counts as infinite. The adopted
// reviewer probes (alias_tighten_only_*_test.go, policy_out_of_range_sec330_test.go) sweep
// hundreds of policies and project files for a loosening; these tests pin the rule case by
// case, with the values it must give.

import (
	"os"
	"strings"
	"testing"
)

// lim is the limit tuple a Status reports.
type lim struct {
	usd, stopUSD float64
	tok, stopTok int64
	window       Window
}

// unlimited is what an agent with no limit of either kind reports.
var unlimited = lim{window: Monthly}

func tupleOf(st Status) lim {
	return lim{usd: st.LimitUSD, stopUSD: st.StopUSD, tok: st.LimitTokens, stopTok: st.StopTokens, window: st.Window}
}

// evalAs evaluates agent under pol, with no project (named false) or with a project that
// names it as its supervisor.
func evalAs(t *testing.T, pol Policy, agent string, named bool) lim {
	t.Helper()
	dir := t.TempDir()
	if err := SavePolicy(dir, pol); err != nil {
		t.Fatal(err)
	}
	o := Options{StateDir: dir, Now: clock(octMid)}
	if named {
		o.Project = projectWith(t, "supervisor:\n  agent: "+agent+"\n")
	}
	return tupleOf(mustEval(t, agent, o))
}

func TestTightenOnly_TheRequiredCases(t *testing.T) {
	const m33, m66, m13 = int64(33_000_000), int64(66_000_000), int64(13_000_000)
	for _, c := range []struct {
		name         string
		pol          Policy
		agent        string
		plain, named lim
	}{
		{ // (a) the operator's default is not raised by the supervisor's built-in
			name: "(a) a default limit_usd of 5 stays 5, stop 5", pol: Policy{Default: AgentLimit{LimitUSD: f64(5)}}, agent: "backend",
			plain: lim{5, 5, 0, 0, Monthly}, named: lim{5, 5, m33, m66, Monthly},
		},
		{ // (b) the librarian does not inherit the supervisor's 2x stop
			name: "(b) the librarian's stop is not doubled", pol: Policy{}, agent: "librarian",
			plain: lim{40, 40, m13, m13, Monthly}, named: lim{40, 40, m13, m13, Monthly},
		},
		{ // (c) control: an unlimited agent gains the supervisor's limits and stop
			name: "(c) an unlimited agent gains the supervisor's limits", pol: Policy{}, agent: "watchdog",
			plain: unlimited, named: lim{100, 200, m33, m66, Monthly},
		},
		{ // (d) an explicit off counts as infinite, not as a limit of zero
			name: "(d) a librarian the operator switched off is budgeted at the supervisor's",
			pol:  Policy{Agents: map[string]AgentLimit{"librarian": {LimitUSD: f64(0), LimitTokens: i64(0)}}}, agent: "librarian",
			plain: unlimited, named: lim{100, 200, m33, m66, Monthly},
		},
		{ // (e) the operator's 500M tokens become the supervisor's 33M with its 66M stop
			name: "(e) 500M tokens becomes 33M with a 66M stop",
			pol:  Policy{Agents: map[string]AgentLimit{"backend": {LimitTokens: i64(500_000_000)}}}, agent: "backend",
			plain: lim{0, 0, 500_000_000, 500_000_000, Monthly}, named: lim{100, 200, m33, m66, Monthly},
		},
		{ // (f) the supervisor entry's monthly window does not flip a lifetime default
			name: "(f) a lifetime default window stays lifetime",
			pol: Policy{Default: AgentLimit{Window: "lifetime"}, Agents: map[string]AgentLimit{
				"supervisor": {Window: "monthly"}, "backend": {LimitUSD: f64(5)}}}, agent: "backend",
			plain: lim{5, 5, 0, 0, Lifetime}, named: lim{5, 5, m33, m66, Lifetime},
		},
		{ // (g) the stop is the smaller ABSOLUTE stop: 50M, never the supervisor's 66M
			name: "(g) the librarian's own 50M, named, is 33M with a 50M stop",
			pol:  Policy{Agents: map[string]AgentLimit{"librarian": {LimitTokens: i64(50_000_000)}}}, agent: "librarian",
			plain: lim{40, 40, 50_000_000, 50_000_000, Monthly}, named: lim{40, 40, m33, 50_000_000, Monthly},
		},
		{ // an own $200 lifetime limit beside the supervisor's $100 monthly one: $100 lifetime
			name: "own $200 lifetime and supervisor $100 monthly is $100 lifetime",
			pol: Policy{Agents: map[string]AgentLimit{
				"backend": {LimitUSD: f64(200), Window: "lifetime"}, "supervisor": {LimitUSD: f64(100), Window: "monthly"}}}, agent: "backend",
			plain: lim{200, 200, 0, 0, Lifetime}, named: lim{100, 200, m33, m66, Lifetime},
		},
		{ // the supervisor's window is lifetime and the agent's own is monthly: lifetime
			name: "the supervisor's lifetime window wins over the agent's monthly one",
			pol: Policy{Agents: map[string]AgentLimit{
				"backend": {LimitUSD: f64(50)}, "supervisor": {Window: "lifetime"}}}, agent: "backend",
			plain: lim{50, 50, 0, 0, Monthly}, named: lim{50, 50, m33, m66, Lifetime},
		},
		{ // the operator raised the supervisor: an unlimited agent gets that, a librarian is not raised
			name: "the operator's raised supervisor entry applies to an unlimited agent",
			pol:  Policy{Agents: map[string]AgentLimit{"supervisor": {LimitUSD: f64(1000), LimitTokens: i64(100_000_000)}}}, agent: "watchdog",
			plain: unlimited, named: lim{1000, 2000, 100_000_000, 200_000_000, Monthly},
		},
		{
			name: "a raised supervisor entry does not raise the librarian",
			pol:  Policy{Agents: map[string]AgentLimit{"supervisor": {LimitUSD: f64(1000), LimitTokens: i64(100_000_000)}}}, agent: "librarian",
			plain: lim{40, 40, m13, m13, Monthly}, named: lim{40, 40, m13, m13, Monthly},
		},
		{ // the operator switched the supervisor off: an unlimited agent stays unlimited
			name: "a supervisor the operator switched off leaves an unlimited agent unlimited",
			pol:  Policy{Agents: map[string]AgentLimit{"supervisor": {LimitUSD: f64(0), LimitTokens: i64(0)}}}, agent: "watchdog",
			plain: unlimited, named: unlimited,
		},
		{
			name: "a supervisor switched off does not loosen the librarian",
			pol:  Policy{Agents: map[string]AgentLimit{"supervisor": {LimitUSD: f64(0), LimitTokens: i64(0)}}}, agent: "librarian",
			plain: lim{40, 40, m13, m13, Monthly}, named: lim{40, 40, m13, m13, Monthly},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := evalAs(t, c.pol, c.agent, false); got != c.plain {
				t.Errorf("control, no project: %+v, want %+v", got, c.plain)
			}
			if got := evalAs(t, c.pol, c.agent, true); got != c.named {
				t.Errorf("named as the supervisor: %+v, want %+v", got, c.named)
			}
		})
	}
}

// A project's own dollar limit for the supervisor lowers the renamed agent's too: it
// must not run looser than the supervisor it is.
func TestTightenOnly_ALoweredSupervisorLowersTheRenamedAgent(t *testing.T) {
	dir := t.TempDir()
	proj := projectWith(t, "supervisor:\n  agent: watchdog\nagent_budgets:\n  supervisor: 10\n")
	got := tupleOf(mustEval(t, "watchdog", Options{StateDir: dir, Project: proj, Now: clock(octMid)}))
	if want := (lim{10, 20, 33_000_000, 66_000_000, Monthly}); got != want {
		t.Fatalf("%+v, want %+v", got, want)
	}
}

// Reset records the reset in the window the combined limit counts in: lifetime when
// either side is lifetime, here the agent's own default while the supervisor's is monthly.
func TestTightenOnly_ResetUsesTheCombinedWindow(t *testing.T) {
	dir := t.TempDir()
	pol := Policy{Default: AgentLimit{Window: "lifetime"}, Agents: map[string]AgentLimit{
		"supervisor": {Window: "monthly"}, "backend": {LimitUSD: f64(5)}}}
	if err := SavePolicy(dir, pol); err != nil {
		t.Fatal(err)
	}
	appendLog(t, dir, `{"type":"dispatch_finished","ts":"`+octMid+`","agent":"backend","runtime":"claude","usage":{"total_cost_usd":9}}`)
	o := Options{StateDir: dir, Project: projectWith(t, "supervisor:\n  agent: backend\n"), Now: clock(octMid)}
	if st := mustEval(t, "backend", o); st.State != StateHardStop || st.Window != Lifetime {
		t.Fatalf("$9 of $5, lifetime: %+v", st)
	}
	if _, err := Reset("backend", o); err != nil {
		t.Fatal(err)
	}
	if st := mustEval(t, "backend", o); st.State != StateOK {
		t.Fatalf("a reset in the combined (lifetime) window lifts the stop: %+v", st)
	}
}

// tighter, unit by unit: 0 is "off", which is infinite.
func TestTighter_PerUnitAmountsAndStops(t *testing.T) {
	own := Limit{USD: 50, StopUSD: 50, Tokens: 50_000_000, StopTokens: 50_000_000, Window: Monthly, WarnPct: 80, StopFactor: 1, Source: "policy"}
	sup := Limit{USD: 100, StopUSD: 200, Tokens: 33_000_000, StopTokens: 66_000_000, Window: Monthly, WarnPct: 90, StopFactor: 2, Source: "builtin"}
	got := tighter(own, sup)
	if got.USD != 50 || got.StopUSD != 50 || got.Tokens != 33_000_000 || got.StopTokens != 50_000_000 {
		t.Errorf("each unit keeps its smaller amount and its smaller stop: %+v", got)
	}
	if got.WarnPct != 80 || got.Source != "policy" || got.StopFactor != 1 {
		t.Errorf("warn at the earlier threshold, source of the agent's own limit: %+v", got)
	}
	// The earlier warning wins from either side.
	earlier := sup
	earlier.WarnPct = 70
	if g := tighter(own, earlier); g.WarnPct != 70 {
		t.Errorf("the supervisor's earlier warning applies: %+v", g)
	}
	// An off unit on one side is infinite there.
	if g := tighter(Limit{Window: Monthly, WarnPct: 80, StopFactor: 1, Source: "none"}, sup); g.USD != 100 || g.StopUSD != 200 || g.Tokens != 33_000_000 || g.Source != "builtin" {
		t.Errorf("an unlimited agent takes the supervisor's: %+v", g)
	}
	if g := tighter(own, Limit{Window: Monthly, WarnPct: 80, StopFactor: 1, Source: "none"}); g.USD != 50 || g.Tokens != 50_000_000 || g.StopTokens != 50_000_000 {
		t.Errorf("an unlimited supervisor leaves the agent's own: %+v", g)
	}
	// Window: lifetime if a side that has a limit, in either unit, is lifetime. A side with no
	// limit at all counts no window, and when neither side has one the agent's own is kept.
	limited := func(w Window) Limit {
		return Limit{USD: 10, StopUSD: 10, Window: w, WarnPct: 80, StopFactor: 1}
	}
	tokensOnly := func(w Window) Limit {
		return Limit{Tokens: 5, StopTokens: 5, Window: w, WarnPct: 80, StopFactor: 1}
	}
	unlimited := func(w Window) Limit { return Limit{Window: w, WarnPct: 80, StopFactor: 1} }
	for name, c := range map[string]struct {
		own, sup Limit
		want     Window
	}{
		"limited monthly, limited monthly":    {limited(Monthly), limited(Monthly), Monthly},
		"limited lifetime, limited monthly":   {limited(Lifetime), limited(Monthly), Lifetime},
		"limited monthly, limited lifetime":   {limited(Monthly), limited(Lifetime), Lifetime},
		"limited lifetime, limited lifetime":  {limited(Lifetime), limited(Lifetime), Lifetime},
		"unlimited lifetime, limited monthly": {unlimited(Lifetime), limited(Monthly), Monthly},
		"limited monthly, unlimited lifetime": {limited(Monthly), unlimited(Lifetime), Monthly},
		"unlimited monthly, limited lifetime": {unlimited(Monthly), limited(Lifetime), Lifetime},
		"limited lifetime, unlimited monthly": {limited(Lifetime), unlimited(Monthly), Lifetime},
		"tokens alone count as a limit":       {tokensOnly(Lifetime), limited(Monthly), Lifetime},
		"tokens alone, the other unlimited":   {unlimited(Lifetime), tokensOnly(Monthly), Monthly},
		"neither limited keeps own, lifetime": {unlimited(Lifetime), unlimited(Monthly), Lifetime},
		"neither limited keeps own, monthly":  {unlimited(Monthly), unlimited(Lifetime), Monthly},
	} {
		if g := tighter(c.own, c.sup); g.Window != c.want {
			t.Errorf("%s: %s, want %s", name, g.Window, c.want)
		}
	}
	if g := tighter(unlimited(Monthly), unlimited(Lifetime)); g.USD != 0 || g.Tokens != 0 || g.StopUSD != 0 || g.StopTokens != 0 {
		t.Errorf("both unlimited stays unlimited: %+v", g)
	}
	// The result is never looser than either side, whichever way round they are given.
	for _, pair := range [][2]Limit{{own, sup}, {sup, own}} {
		g := tighter(pair[0], pair[1])
		for _, side := range pair {
			if side.USD > 0 && (g.USD > side.USD || g.StopUSD > side.StopUSD) || side.Tokens > 0 && (g.Tokens > side.Tokens || g.StopTokens > side.StopTokens) {
				t.Errorf("%+v is looser than %+v", g, side)
			}
		}
	}
}

// The supervisor's own policy warnings (an ignored out-of-range value) reach the agent
// named as its supervisor: it is budgeted by that entry, so the operator must see it.
func TestTightenOnly_TheSupervisorsWarningsReachTheRenamedAgent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(PolicyPath(dir), []byte("agents:\n  supervisor:\n    limit_tokens: 1500000.5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := mustEval(t, "watchdog", Options{StateDir: dir, Project: projectWith(t, renamedSupervisor), Now: clock(octMid)})
	if st.LimitTokens != 33_000_000 || !hasWarning(st, "limit_tokens for supervisor ignored") {
		t.Fatalf("%+v", st)
	}
	if n := countWarnings(st, "limit_tokens for supervisor ignored"); n != 1 {
		t.Errorf("the warning is reported once, not once per side: %d in %v", n, st.Warnings)
	}
}

func countWarnings(st Status, sub string) int {
	n := 0
	for _, w := range st.Warnings {
		if strings.Contains(w, sub) {
			n++
		}
	}
	return n
}
