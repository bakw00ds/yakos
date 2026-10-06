package doctor

// budgets_tokens_test.go: `yakos doctor` at a token stop (K-136). The built-in supervisor
// and librarian budgets carry a token limit, which is what a subscription operator hits
// first (their runs carry no dollars), so the doctor lines must say tokens first, say
// which limit was reached, and point at the flag that raises it. Before, they printed
// "$0.00 of $100.00" and recommended the dollar form, which lifts nothing.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/budget"
)

// tokenDoctor runs the budget check against a state directory holding one subscription
// row of tokens for each agent given, and returns its output and report.
func tokenDoctor(t *testing.T, project string, tokens map[string]int64, setup func(state string)) (string, *Report) {
	t.Helper()
	home := t.TempDir()
	state := filepath.Join(home, ".yakos-state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	if setup != nil {
		setup(state)
	}
	ts := time.Now().UTC().Format(time.RFC3339)
	var lines strings.Builder
	for agent, n := range tokens {
		fmt.Fprintf(&lines, `{"type":"dispatch_finished","ts":%q,"agent":%q,"runtime":"claude","billing":"subscription","usage":{"input_tokens":%d,"output_tokens":0,"total_cost_usd":0}}`+"\n", ts, agent, n)
	}
	if err := os.WriteFile(filepath.Join(state, "dispatch-log.ndjson"), []byte(lines.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	r := &runner{cfg: Config{Writer: &buf, ProjectPath: project}, w: &buf, home: home, env: func(string) string { return "" }, report: &Report{}}
	r.checkAgentBudgets()
	return buf.String(), r.report
}

// A subscription supervisor past its built-in token limit, with $0 spent: the error says
// tokens, says the token limit was reached, and recommends the token flag.
func TestDoctorSupervisorTokenStop(t *testing.T) {
	out, rep := tokenDoctor(t, "", map[string]int64{"supervisor": 34_000_000}, nil)
	for _, want := range []string{
		"LLM supervision disabled: supervisor budget exhausted",
		"token limit reached: 34,000,000 of 33,000,000 tokens and $0.00 of $100.00, monthly",
		"`yakos budget set supervisor --tokens <n>`",
		"`yakos budget reset supervisor`",
		"(reason=budget_exhausted)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "set supervisor <usd>`") {
		t.Errorf("the dollar form lifts nothing at a token stop and must not be recommended:\n%s", out)
	}
	if rep.Errors != 1 || rep.Warnings != 0 {
		t.Errorf("a supervisor stop is one error: %+v", rep)
	}
}

// Tokens come first in the description, whichever limit tripped.
func TestDoctorLibrarianTokenStop(t *testing.T) {
	out, rep := tokenDoctor(t, "", map[string]int64{"librarian": 14_000_000}, nil)
	want := "librarian: hard_stop, token limit reached (14,000,000 of 13,000,000 tokens and $0.00 of $40.00, monthly); new dispatches are refused. `yakos budget set librarian --tokens <n>` or `yakos budget reset librarian`"
	if !strings.Contains(out, want) {
		t.Errorf("want %q in:\n%s", want, out)
	}
	if strings.Contains(out, "hard_stop at $") {
		t.Errorf("no dollar-first line for a token stop:\n%s", out)
	}
	if rep.Warnings != 1 || rep.Errors != 0 {
		t.Errorf("%+v", rep)
	}
}

// A dollar stop on an agent that also has a token limit blames the dollar limit and
// recommends the dollar form; both limits reached says both.
func TestDoctorSaysWhichLimitTripped(t *testing.T) {
	// The built-in supervisor token limit is 33M and the dollar limit is lowered to $10; the
	// dollars come from an api row, so only the dollar limit is reached.
	home := t.TempDir()
	state := filepath.Join(home, ".yakos-state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := budget.SetLimit(state, "librarian", 10, budget.Monthly); err != nil {
		t.Fatal(err)
	}
	ts := time.Now().UTC().Format(time.RFC3339)
	row := func(billing string, in int64, usd float64) string {
		return fmt.Sprintf(`{"type":"dispatch_finished","ts":%q,"agent":"librarian","runtime":"claude","billing":%q,"usage":{"input_tokens":%d,"output_tokens":0,"total_cost_usd":%v}}`+"\n", ts, billing, in, usd)
	}
	if err := os.WriteFile(filepath.Join(state, "dispatch-log.ndjson"), []byte(row("api", 1_000, 12)), 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	r := &runner{cfg: Config{Writer: &buf}, w: &buf, home: home, env: func(string) string { return "" }, report: &Report{}}
	r.checkAgentBudgets()
	out := buf.String()
	if !strings.Contains(out, "dollar limit reached") || !strings.Contains(out, "`yakos budget set librarian <usd>`") || strings.Contains(out, "--tokens <n>`") {
		t.Errorf("a dollar stop names the dollar limit and the dollar form:\n%s", out)
	}

	// Both reached.
	buf.Reset()
	if err := os.WriteFile(filepath.Join(state, "dispatch-log.ndjson"), []byte(row("api", 14_000_000, 12)), 0o600); err != nil {
		t.Fatal(err)
	}
	r = &runner{cfg: Config{Writer: &buf}, w: &buf, home: home, env: func(string) string { return "" }, report: &Report{}}
	r.checkAgentBudgets()
	if out := buf.String(); !strings.Contains(out, "token and dollar limits reached") || !strings.Contains(out, "`yakos budget set librarian <usd> --tokens <n>`") {
		t.Errorf("both reached:\n%s", out)
	}
}

// The warning level reads in tokens too: 85% of 33M is a warning, not an error.
func TestDoctorSupervisorTokenWarning(t *testing.T) {
	out, rep := tokenDoctor(t, "", map[string]int64{"supervisor": 28_050_000}, nil)
	if !strings.Contains(out, "LLM supervision budget at 85% (28,050,000 of 33,000,000 tokens and $0.00 of $100.00, monthly); at 100% routine supervisor launches stop") {
		t.Errorf("%s", out)
	}
	if rep.Errors != 0 || rep.Warnings != 1 {
		t.Errorf("%+v", rep)
	}
	out, _ = tokenDoctor(t, "", map[string]int64{"librarian": 11_050_000}, nil)
	if !strings.Contains(out, "librarian: warning, 85% (11,050,000 of 13,000,000 tokens and $0.00 of $40.00, monthly) spent") {
		t.Errorf("%s", out)
	}
}

// The agent a project names as its supervisor gets the supervisor's budget and wording.
func TestDoctorRenamedSupervisor(t *testing.T) {
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, ".yakos.yml"), []byte("supervisor:\n  agent: watchdog\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, rep := tokenDoctor(t, proj, map[string]int64{"watchdog": 34_000_000}, nil)
	if !strings.Contains(out, "LLM supervision disabled: watchdog budget exhausted (token limit reached: 34,000,000 of 33,000,000 tokens") ||
		!strings.Contains(out, "`yakos budget set watchdog --tokens <n>`") {
		t.Errorf("%s", out)
	}
	if rep.Errors != 1 {
		t.Errorf("%+v", rep)
	}
	out, _ = tokenDoctor(t, proj, map[string]int64{"watchdog": 28_050_000}, nil)
	if !strings.Contains(out, "LLM supervision budget (watchdog) at 85% (28,050,000 of 33,000,000 tokens") {
		t.Errorf("%s", out)
	}
	// No project naming it: no budget, nothing printed.
	if out, rep := tokenDoctor(t, "", map[string]int64{"watchdog": 34_000_000}, nil); out != "" || rep.Errors != 0 {
		t.Errorf("an unbudgeted agent is silent: %q %+v", out, rep)
	}
}

// A dollar-only agent (no token limit) reads exactly as it did.
func TestDoctorDollarOnlyAgentsReadAsBefore(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, ".yakos-state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := budget.SetLimit(state, "backend", 5, budget.Monthly); err != nil {
		t.Fatal(err)
	}
	ts := time.Now().UTC().Format(time.RFC3339)
	line := func(usd float64) string {
		return fmt.Sprintf(`{"type":"dispatch_finished","ts":%q,"agent":"backend","usage":{"total_cost_usd":%v}}`+"\n", ts, usd)
	}
	write := func(usd float64) string {
		if err := os.WriteFile(filepath.Join(state, "dispatch-log.ndjson"), []byte(line(usd)), 0o600); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		r := &runner{cfg: Config{Writer: &buf}, w: &buf, home: home, env: func(string) string { return "" }, report: &Report{}}
		r.checkAgentBudgets()
		return buf.String()
	}
	if out := write(6); !strings.Contains(out, "backend: hard_stop at $6.00 of $5.00 (monthly); new dispatches are refused. `yakos budget set backend <usd>` or `yakos budget reset backend`") {
		t.Errorf("%s", out)
	}
	if out := write(4.5); !strings.Contains(out, "backend: warning, 90% of $5.00 (monthly) spent") {
		t.Errorf("%s", out)
	}
}
