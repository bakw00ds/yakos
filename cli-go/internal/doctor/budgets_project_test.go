package doctor

// budgets_project_test.go: `yakos doctor --project` at the package level (K-136). The Agent
// budgets section reads the project's .yakos.yml from Config.BudgetProject (the entry point sets
// it from --project, else the positional path, else the working directory) so an agent the
// project names as its supervisor is listed with its budget. Three rules are pinned here:
//
//   - the working directory default is NOT a project path: BudgetProject alone must never switch
//     on the four project-wide checks (hook drift, hook binaries, the pre-push gate, the project
//     rules), which are gated on ProjectPath, or a plain `yakos doctor` would start running them
//     in whatever directory it was started from;
//   - the section prints only validated agent names and limit numbers: no path and no text taken
//     from the project file, so a malformed .yakos.yml cannot echo itself into the report;
//   - the project is only a place to read that one file from, never a state path (K-129).

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// budgetProjectDir writes a project directory holding the given .yakos.yml.
func budgetProjectDir(t *testing.T, yml string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".yakos.yml"), []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// budgetHome is a HOME whose state directory holds, in the current month, one subscription run
// of that many tokens (and no dollars) for each agent in tokens, and one API run of that many
// dollars for each agent in apiUSD.
func budgetHome(t *testing.T, tokens map[string]int64, apiUSD ...map[string]float64) string {
	t.Helper()
	home := makeTmpHome(t)
	state := filepath.Join(home, ".yakos-state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	ts := time.Now().UTC().Format(time.RFC3339)
	var lines strings.Builder
	for agent, n := range tokens {
		fmt.Fprintf(&lines, `{"type":"dispatch_finished","ts":%q,"agent":%q,"runtime":"claude","billing":"subscription","usage":{"input_tokens":%d,"output_tokens":0,"total_cost_usd":0}}`+"\n", ts, agent, n)
	}
	for _, m := range apiUSD {
		for agent, usd := range m {
			fmt.Fprintf(&lines, `{"type":"dispatch_finished","ts":%q,"agent":%q,"runtime":"claude","billing":"api","usage":{"input_tokens":1,"output_tokens":0,"total_cost_usd":%v}}`+"\n", ts, agent, usd)
		}
	}
	if err := os.WriteFile(filepath.Join(state, "dispatch-log.ndjson"), []byte(lines.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// budgetsSection runs only the Agent budgets check for cfg over home and returns its output.
func budgetsSection(t *testing.T, home string, cfg Config) (string, *Report) {
	t.Helper()
	var buf bytes.Buffer
	cfg.Writer = &buf
	r := &runner{cfg: cfg, w: &buf, home: home, env: func(string) string { return "" }, report: &Report{}}
	r.checkAgentBudgets()
	return buf.String(), r.report
}

// fullDoctor runs the whole report for cfg over home.
func fullDoctor(t *testing.T, home string, cfg Config) string {
	t.Helper()
	var buf bytes.Buffer
	cfg.Writer = &buf
	cfg.HomeDir = home
	cfg.LookPath = noLookPath
	cfg.Environ = func(string) string { return "" }
	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return buf.String()
}

const renamedSupervisorYML = "supervisor:\n  agent: watchdog\n"

// BudgetProject alone names the project: the renamed supervisor is listed with its (the
// supervisor's) budget, and with no project at all the same spend is an unbudgeted agent.
func TestBudgetProject_NamesTheProjectTheSectionReads(t *testing.T) {
	proj := budgetProjectDir(t, renamedSupervisorYML)
	home := budgetHome(t, map[string]int64{"watchdog": 34_000_000})

	out, rep := budgetsSection(t, home, Config{BudgetProject: proj})
	if !strings.Contains(out, "LLM supervision disabled: watchdog budget exhausted (token limit reached: 34,000,000 of 33,000,000 tokens") ||
		!strings.Contains(out, "`yakos budget set watchdog --tokens <n>`") {
		t.Errorf("BudgetProject must make the section read the project:\n%s", out)
	}
	if rep.Errors != 1 {
		t.Errorf("a supervisor stop is one error: %+v", rep)
	}
	if out, rep := budgetsSection(t, home, Config{}); out != "" || rep.Errors != 0 {
		t.Errorf("no project named: an unbudgeted agent is silent: %q %+v", out, rep)
	}
}

// ProjectPath still works for library callers that never set BudgetProject, and BudgetProject
// wins when both name a project (the entry point sets it from --project).
func TestBudgetProject_FallsBackToProjectPathAndBudgetProjectWins(t *testing.T) {
	alpha := budgetProjectDir(t, "supervisor:\n  agent: alpha\n")
	watch := budgetProjectDir(t, renamedSupervisorYML)
	home := budgetHome(t, map[string]int64{"alpha": 34_000_000, "watchdog": 34_000_000})

	out, _ := budgetsSection(t, home, Config{ProjectPath: alpha})
	if !strings.Contains(out, "alpha budget exhausted") || strings.Contains(out, "watchdog") {
		t.Errorf("with no BudgetProject the section reads ProjectPath:\n%s", out)
	}
	out, _ = budgetsSection(t, home, Config{ProjectPath: alpha, BudgetProject: watch})
	if !strings.Contains(out, "watchdog budget exhausted") || strings.Contains(out, "alpha") {
		t.Errorf("BudgetProject wins over ProjectPath:\n%s", out)
	}
}

// The working directory default lives in BudgetProject, never in ProjectPath: with only
// BudgetProject set, the four project-wide checks do not run (they print their headings and the
// project path), the report outside the budgets section is byte for byte the no-project report,
// and ProjectPath alone, as the control, does run them.
func TestBudgetProject_DoesNotSwitchOnTheProjectChecks(t *testing.T) {
	proj := budgetProjectDir(t, "name: plain\n")
	home := budgetHome(t, nil)

	none := fullDoctor(t, home, Config{})
	budgetOnly := fullDoctor(t, home, Config{BudgetProject: proj})
	if budgetOnly != none {
		t.Errorf("BudgetProject alone changed the report outside the budgets section:\n--- none\n%s\n--- BudgetProject\n%s", none, budgetOnly)
	}
	withPath := fullDoctor(t, home, Config{ProjectPath: proj})
	for _, heading := range []string{"Project hook drift", "Pre-push version gate", "Project rules"} {
		if strings.Contains(none, heading) || strings.Contains(budgetOnly, heading) {
			t.Errorf("%q is a project-wide check and must not run on BudgetProject alone", heading)
		}
		if !strings.Contains(withPath, heading) {
			t.Errorf("control: ProjectPath must still run %q:\n%s", heading, withPath)
		}
	}
}

// Nothing taken from the project file reaches the report. A malformed .yakos.yml makes the YAML
// error echo its own keys (budget.Status.Warnings carries it): the section must print none of it,
// and still list the supervisor name the bash hook's line scan reads from that same file.
func TestBudgetProject_PrintsNothingTakenFromAMalformedProjectFile(t *testing.T) {
	proj := budgetProjectDir(t, "supervisor:\n  agent: watchdog\nagent_budgets:\n  SENTINELKEY: 1\n  SENTINELKEY: 2\n")
	home := budgetHome(t, map[string]int64{"supervisor": 34_000_000, "watchdog": 34_000_000})

	out, rep := budgetsSection(t, home, Config{BudgetProject: proj})
	if !strings.Contains(out, "supervisor budget exhausted") || !strings.Contains(out, "watchdog budget exhausted") || rep.Errors != 2 {
		t.Fatalf("both supervisors' stops must be reported, the name from the line scan included:\n%s", out)
	}
	for _, leak := range []string{"SENTINEL", "already defined", "yaml", "unmarshal", ".yakos.yml", proj, "line "} {
		if strings.Contains(out, leak) {
			t.Errorf("the budgets section printed project-sourced text (%q):\n%s", leak, out)
		}
	}
}

// A name that is not a valid agent name is never printed, whether it is a supervisor name or an
// agent_budgets key, even when its spend is far over its limit (an API run of $5 against a $1
// project limit is a hard stop that would be listed by name if it were evaluated at all).
func TestBudgetProject_NamesThatFailValidationAreNotPrinted(t *testing.T) {
	proj := budgetProjectDir(t, "supervisor:\n  agent: \"bad name SENTINELNAME\"\n"+
		"agent_budgets:\n  \"bad key SENTINELBAD\": 1\n  \"x\\nSENTINELNL\": 1\n  goodname: 1\n")
	home := budgetHome(t, map[string]int64{"supervisor": 34_000_000}, map[string]float64{
		"bad name SENTINELNAME": 5, "bad key SENTINELBAD": 5, "x\nSENTINELNL": 5, "goodname": 5})

	out, _ := budgetsSection(t, home, Config{BudgetProject: proj})
	if strings.Contains(out, "SENTINEL") {
		t.Errorf("an invalid agent name reached the report:\n%s", out)
	}
	// The control: a valid name from the same file is listed, so the section did read the project.
	if !strings.Contains(out, "goodname: hard_stop at $5.00 of $1.00") {
		t.Errorf("a valid project-limited agent is listed:\n%s", out)
	}
}

// The path of the project is not printed by the section for any outcome, including when the
// project holds no .yakos.yml at all.
func TestBudgetProject_NeverPrintsItsPath(t *testing.T) {
	empty := t.TempDir()
	home := budgetHome(t, map[string]int64{"supervisor": 34_000_000})
	out, _ := budgetsSection(t, home, Config{BudgetProject: empty})
	if !strings.Contains(out, "supervisor budget exhausted") || strings.Contains(out, empty) {
		t.Errorf("%s", out)
	}
}
