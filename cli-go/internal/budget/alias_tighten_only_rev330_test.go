package budget

// Reviewer probe (PR 330, round 4, head a09a9c51), scratch only. The code comments and
// docs say the renamed-supervisor alias "can only tighten". Test that against the two
// ways it could loosen something the operator set.

import "testing"

func zzf(v float64) *float64 { return &v }

// An operator's global default limit applies to an agent that has no entry of its own.
// A project that names that agent its supervisor must not raise it.
func TestZZR330_AliasMustNotRaiseTheOperatorsGlobalDefault(t *testing.T) {
	dir := t.TempDir()
	if err := SavePolicy(dir, Policy{Default: AgentLimit{LimitUSD: zzf(5)}}); err != nil {
		t.Fatal(err)
	}
	proj := projectWith(t, "supervisor:\n  agent: backend\n")
	o := Options{StateDir: dir, Now: clock(octMid)}

	plain := mustEval(t, "backend", o) // no project: the operator's default applies
	o.Project = proj
	named := mustEval(t, "backend", o) // a project calls backend its supervisor
	t.Logf("plain:  limit_usd=%v limit_tokens=%v stop_usd=%v source=%s", plain.LimitUSD, plain.LimitTokens, plain.StopUSD, plain.Source)
	t.Logf("named:  limit_usd=%v limit_tokens=%v stop_usd=%v source=%s", named.LimitUSD, named.LimitTokens, named.StopUSD, named.Source)
	if named.LimitUSD > plain.LimitUSD || named.StopUSD > plain.StopUSD {
		t.Errorf("the alias loosened the operator's default: plain $%v (stop $%v) became $%v (stop $%v)",
			plain.LimitUSD, plain.StopUSD, named.LimitUSD, named.StopUSD)
	}
}

// An agent with its own built-in budget (the librarian) must not inherit the
// supervisor's 2x dispatch stop from a project's say-so.
func TestZZR330_AliasMustNotGiveTheLibrarianTheSupervisorsStopFactor(t *testing.T) {
	dir := t.TempDir()
	proj := projectWith(t, "supervisor:\n  agent: librarian\n")
	o := Options{StateDir: dir, Now: clock(octMid)}

	plain := mustEval(t, "librarian", o)
	o.Project = proj
	named := mustEval(t, "librarian", o)
	t.Logf("plain:  limit_usd=%v stop_usd=%v limit_tokens=%v stop_tokens=%v", plain.LimitUSD, plain.StopUSD, plain.LimitTokens, plain.StopTokens)
	t.Logf("named:  limit_usd=%v stop_usd=%v limit_tokens=%v stop_tokens=%v", named.LimitUSD, named.StopUSD, named.LimitTokens, named.StopTokens)
	if named.StopUSD > plain.StopUSD || named.StopTokens > plain.StopTokens {
		t.Errorf("the alias loosened the librarian's dispatch stop: $%v/%d tokens became $%v/%d tokens",
			plain.StopUSD, plain.StopTokens, named.StopUSD, named.StopTokens)
	}
}

// Control: an agent with no limit at all gains the supervisor's, which is a tightening.
func TestZZR330_AliasTightensAnUnlimitedAgent(t *testing.T) {
	dir := t.TempDir()
	proj := projectWith(t, "supervisor:\n  agent: watchdog\n")
	st := mustEval(t, "watchdog", Options{StateDir: dir, Project: proj, Now: clock(octMid)})
	if st.LimitUSD != 100 || st.LimitTokens != 33_000_000 {
		t.Errorf("control: %+v", st)
	}
}
