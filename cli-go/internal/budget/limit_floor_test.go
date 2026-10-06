package budget

// limit_floor_test.go: the floor on positive dollar limits, and the bounds on a project's
// agent_budgets values (K-136, spec item 7 of #330). A positive dollar limit below one cent
// is out of range like a negative one: a tiny number in range, 5e-324, divides spend to an
// infinite share, and an infinite or huge value from a project file, accepted for an agent
// the operator left unlimited, gives an infinite stop. Either one makes the status
// unencodable, so `budget check --json` prints no JSON and the bash supervisor gate fails
// open. The status stays finite and marshals in every case below.

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
)

func writePolicyYAML(t *testing.T, dir, yml string) {
	t.Helper()
	if err := os.WriteFile(PolicyPath(dir), []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
}

func hasWarningWith(st Status, parts ...string) bool {
	for _, w := range st.Warnings {
		ok := true
		for _, p := range parts {
			ok = ok && strings.Contains(w, p)
		}
		if ok {
			return true
		}
	}
	return false
}

// finiteStatus fails the test unless every number the status carries is finite and the
// status marshals: the line `budget check --json` prints.
func finiteStatus(t *testing.T, label string, st Status) {
	t.Helper()
	for k, v := range map[string]float64{
		"limit_usd": st.LimitUSD, "stop_usd": st.StopUSD, "spent_usd": st.SpentUSD, "pct": st.Pct, "tokens_pct": st.TokensPct,
	} {
		if math.IsInf(v, 0) || math.IsNaN(v) {
			t.Errorf("%s: %s is %v, not finite", label, k, v)
		}
	}
	if _, err := json.Marshal(st); err != nil {
		t.Errorf("%s: the status must marshal (budget check --json prints it): %v", label, err)
	}
}

// In the user-level policy a positive limit below the floor is ignored with a warning and the
// built-in (or nothing, for an agent without one) stays; exactly the floor is accepted, and 0
// still turns a limit off.
func TestLimitFloor_PolicyValuesBelowTheFloorAreIgnored(t *testing.T) {
	for _, agent := range []string{"supervisor", "librarian", "backend"} {
		wantUSD, wantStop := 0.0, 0.0
		switch agent {
		case "supervisor":
			wantUSD, wantStop = 100, 200
		case "librarian":
			wantUSD, wantStop = 40, 40
		}
		for _, v := range []string{"0.009", "0.001", "1e-9", "1e-300", "1e-310", "5e-324"} {
			dir := t.TempDir()
			writePolicyYAML(t, dir, "agents:\n  "+agent+":\n    limit_usd: "+v+"\n")
			appendLog(t, dir, ledgerLine(agent, octMid, "api", 0, 0, 0, 0, 12345.5))
			st := mustEval(t, agent, Options{StateDir: dir, Now: clock(octMid)})
			label := agent + " limit_usd " + v
			finiteStatus(t, label, st)
			if st.LimitUSD != wantUSD || st.StopUSD != wantStop {
				t.Errorf("%s: the built-in must stay (limit %v stop %v): %+v", label, wantUSD, wantStop, st)
			}
			if !hasWarningWith(st, "limit_usd for "+agent+" ignored") {
				t.Errorf("%s: the ignored value must be reported: %v", label, st.Warnings)
			}
		}
	}

	// exactly the floor is a limit, with its stop
	dir := t.TempDir()
	writePolicyYAML(t, dir, "agents:\n  backend:\n    limit_usd: 0.01\n  supervisor:\n    limit_usd: 0.01\n")
	if st := mustEval(t, "backend", Options{StateDir: dir, Now: clock(octMid)}); st.LimitUSD != 0.01 || st.StopUSD != 0.01 || hasWarningWith(st, "ignored") {
		t.Errorf("exactly one cent is accepted: %+v", st)
	}
	if st := mustEval(t, "supervisor", Options{StateDir: dir, Now: clock(octMid)}); st.LimitUSD != 0.01 || st.StopUSD != 0.02 || hasWarningWith(st, "ignored") {
		t.Errorf("exactly one cent is accepted for the supervisor, with its stop of twice: %+v", st)
	}

	// 0 still turns the dollar limit off, on purpose and without a warning
	dir = t.TempDir()
	writePolicyYAML(t, dir, "agents:\n  supervisor:\n    limit_usd: 0\n")
	if st := mustEval(t, "supervisor", Options{StateDir: dir, Now: clock(octMid)}); st.LimitUSD != 0 || st.StopUSD != 0 || st.LimitTokens != 33_000_000 || hasWarningWith(st, "ignored") {
		t.Errorf("0 turns the dollar limit off and leaves the built-in token limit: %+v", st)
	}

	// the global default is held to the same floor
	dir = t.TempDir()
	writePolicyYAML(t, dir, "default:\n  limit_usd: 5e-324\n")
	st := mustEval(t, "backend", Options{StateDir: dir, Now: clock(octMid)})
	finiteStatus(t, "default limit_usd 5e-324", st)
	if st.LimitUSD != 0 || !hasWarningWith(st, "limit_usd for default ignored") {
		t.Errorf("a sub-floor default is ignored with a warning: %+v", st)
	}
}

// `budget set` refuses a sub-floor positive limit and writes nothing; the floor, 0 and the
// largest limit are accepted.
func TestLimitFloor_SetLimitRefusesBelowTheFloor(t *testing.T) {
	for _, v := range []float64{0.009, 0.001, 1e-9, 1e-300, 5e-324} {
		dir := t.TempDir()
		err := SetLimit(dir, "backend", v, Monthly)
		if err == nil || !strings.Contains(err.Error(), "0.01") {
			t.Errorf("SetLimit(%v) must be refused and name the floor: %v", v, err)
		}
		if pol, _ := LoadPolicy(dir); len(pol.Agents) != 0 {
			t.Errorf("a refused set must not write a policy: %+v", pol.Agents)
		}
	}
	for _, v := range []float64{0, minLimitUSD, 5, maxLimitUSD} {
		if err := SetLimit(t.TempDir(), "backend", v, Monthly); err != nil {
			t.Errorf("SetLimit(%v) must be accepted: %v", v, err)
		}
	}
	for _, v := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1), maxLimitUSD * 10} {
		if err := SetLimit(t.TempDir(), "backend", v, Monthly); err == nil {
			t.Errorf("SetLimit(%v) must be refused", v)
		}
	}
}

// A project's agent_budgets value is validated exactly like a policy value: negative, NaN,
// infinite, above the bound or below the floor is ignored with a warning and not applied, not
// even to an agent the operator left unlimited (then nothing else bounds it). A valid value
// still fills the gap (off becomes limited) and still only lowers a limit.
func TestProjectValue_IsValidatedLikeAPolicyValue(t *testing.T) {
	bad := []string{".inf", "-.inf", ".nan", "1e308", "1e30", "1000000001", "0.009", "0.000001", "1e-300", "5e-324", "-5", "0"}
	for _, v := range bad {
		dir := t.TempDir()
		proj := projectWith(t, "agent_budgets:\n  backend: "+v+"\n")
		appendLog(t, dir, ledgerLine("backend", octMid, "api", 0, 0, 0, 0, 12345.5))
		st := mustEval(t, "backend", Options{StateDir: dir, Project: proj, Now: clock(octMid)})
		finiteStatus(t, "project agent_budgets.backend="+v, st)
		if st.LimitUSD != 0 || st.StopUSD != 0 || st.State != StateOff {
			t.Errorf("project agent_budgets.backend=%s must not be applied to an agent the operator left unlimited: %+v", v, st)
		}
		if !hasWarningWith(st, "project agent_budgets.backend=", "ignored") {
			t.Errorf("project agent_budgets.backend=%s must be ignored WITH a warning: %v", v, st.Warnings)
		}
		for _, w := range st.Warnings {
			if strings.Contains(w, proj) || strings.Contains(w, "agent_budgets:") {
				t.Errorf("the warning echoes the project's path or text: %q", w)
			}
		}
	}

	// valid values fill the gap, up to the bound and down to the floor
	for v, want := range map[string]float64{"0.01": 0.01, "5": 5, "1000000000": 1e9} {
		proj := projectWith(t, "agent_budgets:\n  backend: "+v+"\n")
		st := mustEval(t, "backend", Options{StateDir: t.TempDir(), Project: proj, Now: clock(octMid)})
		finiteStatus(t, "project agent_budgets.backend="+v, st)
		if st.LimitUSD != want || st.StopUSD != want || st.Source != "project" || hasWarningWith(st, "ignored") {
			t.Errorf("a valid project value %s fills the gap: %+v", v, st)
		}
	}

	// beside an operator limit a project only lowers: lower applies, higher is ignored, and an
	// invalid value is ignored with the range warning, not the "cannot raise" one
	dir := t.TempDir()
	writePolicyYAML(t, dir, "agents:\n  backend:\n    limit_usd: 50\n")
	for v, want := range map[string]float64{"3": 3, "60": 50, "1e308": 50, ".inf": 50, "0.009": 50} {
		proj := projectWith(t, "agent_budgets:\n  backend: "+v+"\n")
		st := mustEval(t, "backend", Options{StateDir: dir, Project: proj, Now: clock(octMid)})
		finiteStatus(t, "operator $50, project "+v, st)
		if st.LimitUSD != want || st.StopUSD != want {
			t.Errorf("operator $50, project %s: limit %v stop %v, want %v: %+v", v, st.LimitUSD, st.StopUSD, want, st)
		}
	}
	proj := projectWith(t, "agent_budgets:\n  backend: 1e308\n")
	if st := mustEval(t, "backend", Options{StateDir: dir, Project: proj, Now: clock(octMid)}); !hasWarningWith(st, "ignored: it is not a finite number of dollars") {
		t.Errorf("an out-of-range project value says so: %v", st.Warnings)
	}
}

// The case rev-327 found, at the status: with the operator's supervisor dollar limit off (the
// documented token-only setup) a project value of .inf or 1e308, or one below the floor, used
// to be applied as the dollar limit, with a stop of twice it. The status must keep the token
// limit, a dollar limit of 0 and a stop of 0, marshal, and be the token hard stop. Also under a
// project that names another agent as its supervisor.
func TestProjectValue_CannotMakeATokenOnlyStatusUnencodable(t *testing.T) {
	for _, v := range []string{".inf", "1e308", "1e30", "5e-324", "0.009"} {
		for _, c := range []struct{ agent, yml string }{
			{"supervisor", "agent_budgets:\n  supervisor: " + v + "\n"},
			{"backend", "supervisor:\n  agent: backend\nagent_budgets:\n  supervisor: " + v + "\n"},
			{"librarian", "supervisor:\n  agent: librarian\nagent_budgets:\n  supervisor: " + v + "\n"},
			{"watchdog", "supervisor:\n  agent: watchdog\nagent_budgets:\n  supervisor: " + v + "\n  watchdog: " + v + "\n"},
		} {
			dir := t.TempDir()
			writePolicyYAML(t, dir, "agents:\n  supervisor:\n    limit_usd: 0\n")
			appendLog(t, dir, ledgerLine(c.agent, octMid, "subscription", 40_000_000, 0, 0, 0, 0))
			proj := projectWith(t, c.yml)
			st := mustEval(t, c.agent, Options{StateDir: dir, Project: proj, Now: clock(octMid)})
			label := c.agent + " with agent_budgets " + v
			finiteStatus(t, label, st)
			if st.State != StateHardStop || st.SpentTokens != 40_000_000 {
				t.Errorf("%s: 40M tokens is a hard stop: %+v", label, st)
			}
			if c.agent == "supervisor" || c.agent == "watchdog" {
				// the agent's tokens are the supervisor's built-in 33M; its dollars are off
				if st.LimitTokens != 33_000_000 || st.LimitUSD != 0 || st.StopUSD != 0 {
					t.Errorf("%s: want 33M tokens and no dollar limit: %+v", label, st)
				}
			}
		}
	}
}
