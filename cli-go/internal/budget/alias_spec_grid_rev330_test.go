package budget

// Reviewer oracle (PR 330, final round), scratch only. It encodes the combine rule of
// work/current/briefs/k136-final-push.md item 3 over the PUBLIC API only (Evaluate and
// Reset), so it checks any implementation of it:
//
//	for the agent a project names as its supervisor, with own = the agent's limits as
//	Evaluate reports them without the project and sup = the supervisor's:
//	  per unit the smaller amount (an off or unlimited unit is infinite);
//	  per unit the smaller absolute stop (the stop of a unit that is off is infinite);
//	  window lifetime if either side that has a limit is lifetime, else monthly.
//
// It runs the rule over a grid of own and supervisor policy shapes for a plain agent
// and for the librarian (which has built-ins of its own), and checks that the Status
// still marshals to JSON (an infinite stop would break `budget check --json`).

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"
)

func zzF(v float64) *float64 { return &v }
func zzI(v int64) *int64     { return &v }

type zzUnit struct{ amt, stop float64 } // amt 0: off or unlimited

func zzMin(a, b float64) float64 {
	switch {
	case a == 0:
		return b
	case b == 0:
		return a
	}
	return math.Min(a, b)
}

func zzCombine(own, sup zzUnit) zzUnit {
	// amount: smaller nonzero. stop: smaller stop of the sides that have a limit.
	return zzUnit{amt: zzMin(own.amt, sup.amt), stop: zzMin(own.stop, sup.stop)}
}

func zzUsd(s Status) zzUnit {
	if s.LimitUSD <= 0 {
		return zzUnit{}
	}
	return zzUnit{amt: s.LimitUSD, stop: s.StopUSD}
}
func zzTok(s Status) zzUnit {
	if s.LimitTokens <= 0 {
		return zzUnit{}
	}
	return zzUnit{amt: float64(s.LimitTokens), stop: float64(s.StopTokens)}
}

type zzPolicyShape struct {
	name string
	// def, agent and sup are the policy's default entry, the agent's entry and the
	// supervisor's entry (nil: none).
	def, agent, sup *AgentLimit
}

func TestZZR330_AliasGridMatchesTheSpecFormula(t *testing.T) {
	shapes := []zzPolicyShape{
		{name: "no policy"},
		{name: "default $5", def: &AgentLimit{LimitUSD: zzF(5)}},
		{name: "default 20M tokens", def: &AgentLimit{LimitTokens: zzI(20_000_000)}},
		{name: "default lifetime $7", def: &AgentLimit{LimitUSD: zzF(7), Window: "lifetime"}},
		{name: "agent $500 and 500M", agent: &AgentLimit{LimitUSD: zzF(500), LimitTokens: zzI(500_000_000)}},
		{name: "agent explicitly off", agent: &AgentLimit{LimitUSD: zzF(0), LimitTokens: zzI(0)}},
		{name: "agent 50M tokens only", agent: &AgentLimit{LimitTokens: zzI(50_000_000)}},
		{name: "agent $200 lifetime", agent: &AgentLimit{LimitUSD: zzF(200), Window: "lifetime"}},
		{name: "agent $30 and 10M", agent: &AgentLimit{LimitUSD: zzF(30), LimitTokens: zzI(10_000_000)}},
		{name: "supervisor raised $500 and 100M", sup: &AgentLimit{LimitUSD: zzF(500), LimitTokens: zzI(100_000_000)}},
		{name: "supervisor lifetime", sup: &AgentLimit{Window: "lifetime"}},
		{name: "supervisor off", sup: &AgentLimit{LimitUSD: zzF(0), LimitTokens: zzI(0)}},
		{name: "default $5 and supervisor $500", def: &AgentLimit{LimitUSD: zzF(5)}, sup: &AgentLimit{LimitUSD: zzF(500)}},
		{name: "agent 500M and supervisor lifetime", agent: &AgentLimit{LimitTokens: zzI(500_000_000)}, sup: &AgentLimit{Window: "lifetime"}},
	}
	for _, agentName := range []string{"backend", "librarian"} {
		for _, sh := range shapes {
			name := fmt.Sprintf("%s/%s", agentName, sh.name)
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				pol := Policy{Agents: map[string]AgentLimit{}}
				if sh.def != nil {
					pol.Default = *sh.def
				}
				if sh.agent != nil {
					pol.Agents[agentName] = *sh.agent
				}
				if sh.sup != nil {
					pol.Agents["supervisor"] = *sh.sup
				}
				if err := SavePolicy(dir, pol); err != nil {
					t.Fatal(err)
				}
				proj := projectWith(t, "supervisor:\n  agent: "+agentName+"\n")
				o := Options{StateDir: dir, Now: clock(octMid)}

				own := mustEval(t, agentName, o)
				sup := mustEval(t, "supervisor", o)
				o.Project = proj
				got := mustEval(t, agentName, o)

				wantUsd, wantTok := zzCombine(zzUsd(own), zzUsd(sup)), zzCombine(zzTok(own), zzTok(sup))
				gotUsd, gotTok := zzUsd(got), zzTok(got)
				if gotUsd != wantUsd {
					t.Errorf("dollars: want amount %v stop %v, got %v (own %+v, supervisor %+v)", wantUsd.amt, wantUsd.stop, gotUsd, zzUsd(own), zzUsd(sup))
				}
				if gotTok != wantTok {
					t.Errorf("tokens: want amount %v stop %v, got %v (own %+v, supervisor %+v)", wantTok.amt, wantTok.stop, gotTok, zzTok(own), zzTok(sup))
				}
				if wantUsd.amt > 0 || wantTok.amt > 0 {
					wantWin := Monthly
					if (own.HasLimit() && own.Window == Lifetime) || (sup.HasLimit() && sup.Window == Lifetime) {
						wantWin = Lifetime
					}
					if got.Window != wantWin {
						t.Errorf("window: want %s, got %s", wantWin, got.Window)
					}
				}
				if _, err := json.Marshal(got); err != nil {
					t.Errorf("the combined status must marshal (no infinite stop): %v", err)
				}
				// And never looser than checking both sides separately, in absolute terms.
				for _, u := range []struct {
					n          string
					g, o, s    zzUnit
					haveSomeOf bool
				}{{"usd", gotUsd, zzUsd(own), zzUsd(sup), true}, {"tokens", gotTok, zzTok(own), zzTok(sup), true}} {
					if u.o.amt > 0 && (u.g.amt == 0 || u.g.amt > u.o.amt || u.g.stop > u.o.stop) {
						t.Errorf("%s is looser than the agent's own: own %+v got %+v", u.n, u.o, u.g)
					}
					if u.s.amt > 0 && (u.g.amt == 0 || u.g.amt > u.s.amt || u.g.stop > u.s.stop) {
						t.Errorf("%s is looser than the supervisor's: supervisor %+v got %+v", u.n, u.s, u.g)
					}
				}
			})
		}
	}
}

// The real supervisor, and a project that names "supervisor" as its own supervisor, are
// unchanged by the rule.
func TestZZR330_AliasLeavesTheRealSupervisorAlone(t *testing.T) {
	dir := t.TempDir()
	proj := projectWith(t, "supervisor:\n  agent: supervisor\n")
	plain := mustEval(t, "supervisor", Options{StateDir: dir, Now: clock(octMid)})
	named := mustEval(t, "supervisor", Options{StateDir: dir, Project: proj, Now: clock(octMid)})
	if plain.LimitUSD != named.LimitUSD || plain.StopUSD != named.StopUSD || plain.LimitTokens != named.LimitTokens || plain.StopTokens != named.StopTokens || plain.Window != named.Window {
		t.Errorf("plain %+v vs named %+v", plain, named)
	}
	if named.LimitUSD != 100 || named.StopUSD != 200 || named.LimitTokens != 33_000_000 || named.StopTokens != 66_000_000 {
		t.Errorf("the supervisor's own tuple moved: %+v", named)
	}
}

// Reset must use the window the status counts in: own $200 lifetime plus the supervisor's
// $100 monthly is $100 lifetime, so a lifetime spend of $150 is a hard stop, and a reset
// of the agent with the project lifts it.
func TestZZR330_ResetUsesTheCombinedWindow(t *testing.T) {
	dir := t.TempDir()
	if err := SavePolicy(dir, Policy{Agents: map[string]AgentLimit{"backend": {LimitUSD: zzF(200), Window: "lifetime"}}}); err != nil {
		t.Fatal(err)
	}
	appendLog(t, dir,
		ledgerLine("backend", "2026-08-15T12:00:00Z", "", 0, 0, 0, 0, 50),
		ledgerLine("backend", "2026-09-15T12:00:00Z", "", 0, 0, 0, 0, 50),
		ledgerLine("backend", octMid, "", 0, 0, 0, 0, 50))
	proj := projectWith(t, "supervisor:\n  agent: backend\n")
	o := Options{StateDir: dir, Project: proj, Now: clock(octMid)}

	st := mustEval(t, "backend", o)
	t.Logf("before reset: window=%s limit=$%v spent=$%v state=%s", st.Window, st.LimitUSD, st.SpentUSD, st.State)
	if st.Window != Lifetime || st.LimitUSD != 100 || st.State != StateHardStop {
		t.Fatalf("the combined tuple is $100 lifetime and $150 spent is a hard stop: %+v", st)
	}
	if _, err := Reset("backend", o); err != nil {
		t.Fatal(err)
	}
	st = mustEval(t, "backend", o)
	t.Logf("after reset:  window=%s limit=$%v spent=$%v state=%s", st.Window, st.LimitUSD, st.SpentUSD, st.State)
	if st.State == StateHardStop || st.SpentUSD != 0 {
		t.Errorf("Reset must start the window the status counts in over: %+v", st)
	}
}
