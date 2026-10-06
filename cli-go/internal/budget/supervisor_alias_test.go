package budget

// supervisor_alias_test.go: the agent a project names as its supervisor keeps the
// supervisor's budget (K-136, security review of #330, finding 8). The supervisor hook
// launches `yakos dispatch <name>` with the name from the project's .yakos.yml, and the
// budget is keyed on that name, so before this a project that wrote `supervisor: agent:
// watchdog` ran its supervisor with no budget at all and lifted the built-in limits a
// committed file has no right to lift. The user-level file is still the only place to
// raise or turn off a limit. The agent is budgeted at the stricter of its own limits and
// the supervisor's, never looser than either: the rule is pinned case by case in
// supervisor_alias_tighten_test.go.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// projectWith writes a project directory whose .yakos.yml holds yml.
func projectWith(t *testing.T, yml string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".yakos.yml"), []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

const renamedSupervisor = "supervisor:\n  runtime: claude\n  agent: watchdog\n"

// A subscription supervisor that the project calls watchdog, at 70M tokens and $0: the
// dollar built-in cannot see it, the token built-in must. It is at its hard stop at 33M
// and refused outright past 2x (66M), exactly as the supervisor itself is.
func TestRenamedSupervisor_KeepsTheBuiltinTokenLimitAndStopFactor(t *testing.T) {
	dir := t.TempDir()
	proj := projectWith(t, renamedSupervisor)
	appendLog(t, dir, ledgerLine("watchdog", octMid, "subscription", 70_000_000, 0, 0, 0, 0))
	o := Options{StateDir: dir, Project: proj, Now: clock(octMid)}

	st := mustEval(t, "watchdog", o)
	if st.State != StateHardStop || st.LimitTokens != 33_000_000 || st.StopTokens != 66_000_000 || st.LimitUSD != 100 || st.StopUSD != 200 {
		t.Fatalf("the renamed supervisor has the supervisor's budget: %+v", st)
	}
	if st.Source != "builtin" {
		t.Errorf("source = %q", st.Source)
	}
	if _, err := Enforce("watchdog", o); !IsRefused(err) {
		t.Fatalf("70M tokens is past 2x the supervisor's 33M: dispatch must be refused, got %v", err)
	}

	// Between 1x and 2x it is a hard stop that dispatch still allows, as for the supervisor.
	dir2 := t.TempDir()
	appendLog(t, dir2, ledgerLine("watchdog", octMid, "subscription", 40_000_000, 0, 0, 0, 0))
	o2 := Options{StateDir: dir2, Project: proj, Now: clock(octMid)}
	if st := mustEval(t, "watchdog", o2); st.State != StateHardStop {
		t.Fatalf("40M tokens is past the 33M limit: %+v", st)
	}
	if _, err := Enforce("watchdog", o2); err != nil {
		t.Fatalf("between 1x and 2x the supervisor's stop factor lets dispatch through: %v", err)
	}
}

// The control: the same ledger with no project config naming it, or a project naming
// another agent, leaves watchdog unbudgeted, as any agent with no limit is.
func TestRenamedSupervisor_OnlyTheNamedAgentIsAffected(t *testing.T) {
	dir := t.TempDir()
	appendLog(t, dir, ledgerLine("watchdog", octMid, "subscription", 70_000_000, 0, 0, 0, 0))
	for name, o := range map[string]Options{
		"no project":                {StateDir: dir, Now: clock(octMid)},
		"a project with no config":  {StateDir: dir, Project: t.TempDir(), Now: clock(octMid)},
		"a project naming another":  {StateDir: dir, Project: projectWith(t, "supervisor:\n  agent: sentinel\n"), Now: clock(octMid)},
		"a project with no section": {StateDir: dir, Project: projectWith(t, "agent_budgets:\n  backend: 5\n"), Now: clock(octMid)},
	} {
		if st := mustEval(t, "watchdog", o); st.State != StateOff || st.LimitTokens != 0 {
			t.Errorf("%s: watchdog has no budget: %+v", name, st)
		}
	}
	// And the supervisor itself is unchanged by a project that renames it.
	appendLog(t, dir, ledgerLine("supervisor", octMid, "subscription", 70_000_000, 0, 0, 0, 0))
	if st := mustEval(t, "supervisor", Options{StateDir: dir, Project: projectWith(t, renamedSupervisor), Now: clock(octMid)}); st.State != StateHardStop || st.LimitTokens != 33_000_000 {
		t.Fatalf("the supervisor keeps its budget: %+v", st)
	}
}

// The renamed supervisor is budgeted at the supervisor's limits where it has none of its
// own, so the operator's entry for the supervisor (raised, or turned off) is what it
// gets, and an entry for its own name, which only the operator can write, holds beside it
// when it is the stricter (a looser one does not loosen the supervisor's: see
// supervisor_alias_tighten_test.go).
func TestRenamedSupervisor_FollowsTheOperatorsSupervisorEntry(t *testing.T) {
	proj := projectWith(t, renamedSupervisor)
	rows := ledgerLine("watchdog", octMid, "subscription", 70_000_000, 0, 0, 0, 0)

	// The operator raised the supervisor to 100M tokens: watchdog gets 100M.
	dir := t.TempDir()
	appendLog(t, dir, rows)
	setTokenLimit(t, dir, "supervisor", 100_000_000, Monthly)
	if st := mustEval(t, "watchdog", Options{StateDir: dir, Project: proj, Now: clock(octMid)}); st.LimitTokens != 100_000_000 || st.State != StateOK {
		t.Fatalf("the operator's supervisor entry applies to the renamed supervisor: %+v", st)
	}

	// The operator turned the supervisor's budget off (both limits): so it is for watchdog.
	dir = t.TempDir()
	appendLog(t, dir, rows)
	setLimit(t, dir, "supervisor", 0, Monthly)
	setTokenLimit(t, dir, "supervisor", 0, Monthly)
	if st := mustEval(t, "watchdog", Options{StateDir: dir, Project: proj, Now: clock(octMid)}); st.State != StateOff {
		t.Fatalf("a user-level opt-out of the supervisor budget covers the renamed supervisor: %+v", st)
	}

	// An entry under watchdog's own name that is stricter than the supervisor's holds.
	dir = t.TempDir()
	appendLog(t, dir, rows)
	setTokenLimit(t, dir, "supervisor", 100_000_000, Monthly)
	setTokenLimit(t, dir, "watchdog", 1_000, Monthly)
	st := mustEval(t, "watchdog", Options{StateDir: dir, Project: proj, Now: clock(octMid)})
	if st.LimitTokens != 1_000 || st.State != StateHardStop || st.StopTokens != 1_000 || st.LimitUSD != 100 || st.StopUSD != 200 {
		t.Fatalf("its own stricter entry holds for the limit it sets and for that unit's stop (1000, not the supervisor's factor of 2), and the supervisor's limit and stop stand for the dollars it has no entry for: %+v", st)
	}
}

// A project's own agent_budgets for the renamed agent can still only lower the dollar
// limit, as for any agent.
func TestRenamedSupervisor_ProjectDollarsOnlyLower(t *testing.T) {
	dir := t.TempDir()
	proj := projectWith(t, renamedSupervisor+"agent_budgets:\n  watchdog: 5\n")
	if st := mustEval(t, "watchdog", Options{StateDir: dir, Project: proj, Now: clock(octMid)}); st.LimitUSD != 5 || st.LimitTokens != 33_000_000 {
		t.Fatalf("a project can lower the dollar limit and not the token built-in: %+v", st)
	}
	proj = projectWith(t, renamedSupervisor+"agent_budgets:\n  watchdog: 5000\n")
	st := mustEval(t, "watchdog", Options{StateDir: dir, Project: proj, Now: clock(octMid)})
	if st.LimitUSD != 100 || st.StopUSD != 200 {
		t.Fatalf("a project cannot raise it past the supervisor's limit: %+v", st)
	}
}

// Both hook twins read the name, in different ways, and every name either of them can
// arrive at is the supervisor: the Go twin reads supervisor.agent as YAML, the bash twin
// takes the first agent: line within 20 lines after a supervisor: line.
func TestProjectSupervisorAgents_ReadsTheNameTheWayBothTwinsDo(t *testing.T) {
	for name, tc := range map[string]struct {
		yml  string
		want []string
	}{
		"the usual shape":               {"supervisor:\n  runtime: claude\n  agent: watchdog\n", []string{"watchdog"}},
		"agent before runtime":          {"supervisor:\n  agent: watchdog\n  runtime: claude\n", []string{"watchdog"}},
		"no supervisor section":         {"agent_budgets:\n  backend: 5\n", nil},
		"no agent in the section":       {"supervisor:\n  runtime: claude\n", nil},
		"an empty name":                 {"supervisor:\n  agent:\n", nil},
		"a quoted name (YAML only)":     {"supervisor:\n  agent: \"watchdog\"\n", []string{"watchdog"}},
		"an invalid name is dropped":    {"supervisor:\n  agent: \"bad name!\"\n", nil},
		"a nested agent: the bash scan": {"supervisor:\n  notes:\n    agent: sneaky\n", []string{"sneaky"}},
		"both readings":                 {"supervisor:\n  agent: watchdog\n  notes:\n    agent: sneaky\n", []string{"watchdog"}},
		"the scan only looks 20 lines":  {"supervisor:\n" + strings.Repeat("  x: 1\n", 21) + "  agent: toofar\n", nil},
		"the scan sees 20 lines":        {"supervisor:\n" + strings.Repeat("  x: 1\n", 19) + "  agent: justin\n", []string{"justin"}},
		"supervisor is a scalar":        {"supervisor: true\nagent_budgets:\n  backend: 5\n", nil},
		"CRLF line endings":             {"supervisor:\r\n  agent: watchdog\r\n", []string{"watchdog"}},
		"a colon in the value (both)":   {"supervisor:\n  agent: watchdog:extra\n", []string{"watchdog:extra", "watchdog"}},
	} {
		got := ProjectSupervisorAgents(projectWith(t, tc.yml))
		if !reflect.DeepEqual(got, tc.want) && !(len(got) == 0 && len(tc.want) == 0) {
			t.Errorf("%s: names = %v, want %v", name, got, tc.want)
		}
	}
	// The two readings that differ are both returned (YAML first, then the scan).
	both := ProjectSupervisorAgents(projectWith(t, "supervisor:\n  notes:\n    agent: sneaky\n  agent: watchdog\n"))
	if !reflect.DeepEqual(both, []string{"watchdog", "sneaky"}) && !reflect.DeepEqual(both, []string{"sneaky", "watchdog"}) {
		t.Errorf("a file the twins disagree about yields both names: %v", both)
	}
	if got := ProjectSupervisorAgents(""); len(got) != 0 {
		t.Errorf("no project, no names: %v", got)
	}
}

// Reading the supervisor name must not cost a project its agent_budgets: `supervisor:`
// written as something other than a section used to be ignored and still is.
func TestProjectLimits_SurviveAnOddSupervisorKey(t *testing.T) {
	limits, warn := ProjectLimits(projectWith(t, "supervisor: true\nagent_budgets:\n  backend: 5\n"))
	if warn != "" || limits["backend"] != 5 {
		t.Fatalf("limits = %v, warn = %q", limits, warn)
	}
	limits, warn = ProjectLimits(projectWith(t, "agent_budgets: [not, a, map]\n"))
	if warn == "" || limits != nil {
		t.Fatalf("a malformed agent_budgets is still reported: %v %q", limits, warn)
	}
}

// status and doctor list the renamed supervisor, so an operator sees its hard stop.
func TestAgentNamesForProject_IncludesTheRenamedSupervisor(t *testing.T) {
	names := AgentNamesForProject(Policy{}, projectWith(t, renamedSupervisor+"agent_budgets:\n  backend: 5\n"))
	want := []string{"backend", "librarian", "supervisor", "watchdog"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	if names := AgentNamesForProject(Policy{}, ""); !reflect.DeepEqual(names, []string{"librarian", "supervisor"}) {
		t.Fatalf("no project: %v", names)
	}
}

// A reset lifts the renamed supervisor's hard stop like any other agent's.
func TestRenamedSupervisor_ResetStartsItsWindowOver(t *testing.T) {
	dir := t.TempDir()
	proj := projectWith(t, renamedSupervisor)
	appendLog(t, dir, ledgerLine("watchdog", octMid, "subscription", 40_000_000, 0, 0, 0, 0))
	o := Options{StateDir: dir, Project: proj, Now: clock(octMid)}
	if st := mustEval(t, "watchdog", o); st.State != StateHardStop {
		t.Fatalf("%+v", st)
	}
	if _, err := Reset("watchdog", o); err != nil {
		t.Fatal(err)
	}
	if st := mustEval(t, "watchdog", o); st.State != StateOK || st.SpentTokens != 0 {
		t.Fatalf("a reset starts the window over: %+v", st)
	}

	// The window it is reset in is the one it is counted in: the operator's supervisor
	// entry gave it a lifetime window, and a reset recorded in another window would not
	// lift the stop.
	dir = t.TempDir()
	appendLog(t, dir, ledgerLine("watchdog", octMid, "subscription", 40_000_000, 0, 0, 0, 0))
	setTokenLimit(t, dir, "supervisor", 33_000_000, Lifetime)
	o = Options{StateDir: dir, Project: proj, Now: clock(octMid)}
	if st := mustEval(t, "watchdog", o); st.State != StateHardStop || st.Window != Lifetime {
		t.Fatalf("%+v", st)
	}
	if _, err := Reset("watchdog", o); err != nil {
		t.Fatal(err)
	}
	if st := mustEval(t, "watchdog", o); st.State != StateOK {
		t.Fatalf("a reset in the renamed supervisor's own window lifts the stop: %+v", st)
	}
}
