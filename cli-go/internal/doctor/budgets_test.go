package doctor

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

func TestDoctorReportsBudgetStates(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, ".yakos-state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := budget.SetLimit(state, "supervisor", 10, budget.Monthly); err != nil {
		t.Fatal(err)
	}
	if err := budget.SetLimit(state, "librarian", 10, budget.Monthly); err != nil {
		t.Fatal(err)
	}
	ts := time.Now().UTC().Format(time.RFC3339)
	line := func(a string, usd float64) string {
		return fmt.Sprintf(`{"type":"dispatch_finished","ts":%q,"agent":%q,"usage":{"total_cost_usd":%v}}`+"\n", ts, a, usd)
	}
	if err := os.WriteFile(filepath.Join(state, "dispatch-log.ndjson"), []byte(line("supervisor", 12)+line("librarian", 8.5)), 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	r := &runner{cfg: Config{Writer: &buf}, w: &buf, home: home, env: func(string) string { return "" }, report: &Report{}}
	r.checkAgentBudgets()
	out := buf.String()
	if !strings.Contains(out, "librarian: warning") {
		t.Fatalf("doctor output:\n%s", out)
	}
	if !strings.Contains(out, "LLM supervision disabled: supervisor budget exhausted") {
		t.Fatalf("supervisor hard stop must say LLM supervision is disabled:\n%s", out)
	}
	// Supervisor hard stop is an ERROR; the librarian warning stays a warning.
	if r.report.Warnings != 1 || r.report.Errors != 1 {
		t.Fatalf("want 1 warning, 1 error: %+v", r.report)
	}
}

func TestDoctorBudgetsSilentWhenHealthy(t *testing.T) {
	home := t.TempDir()
	var buf bytes.Buffer
	r := &runner{cfg: Config{Writer: &buf}, w: &buf, home: home, env: func(string) string { return "" }, report: &Report{}}
	r.checkAgentBudgets()
	if buf.Len() != 0 {
		t.Fatalf("healthy install must print nothing, got %q", buf.String())
	}
}

func TestDoctorSupervisorWarningIsWarning(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, ".yakos-state")
	_ = os.MkdirAll(state, 0o700)
	_ = budget.SetLimit(state, "supervisor", 10, budget.Monthly)
	ts := time.Now().UTC().Format(time.RFC3339)
	_ = os.WriteFile(filepath.Join(state, "dispatch-log.ndjson"), []byte(fmt.Sprintf(`{"type":"dispatch_finished","ts":%q,"agent":"supervisor","usage":{"total_cost_usd":8.5}}`+"\n", ts)), 0o600)
	var buf bytes.Buffer
	r := &runner{cfg: Config{Writer: &buf}, w: &buf, home: home, env: func(string) string { return "" }, report: &Report{}}
	r.checkAgentBudgets()
	if r.report.Errors != 0 || r.report.Warnings != 1 || !strings.Contains(buf.String(), "LLM supervision budget at 85%") {
		t.Fatalf("%+v\n%s", r.report, buf.String())
	}
}

func TestDoctorListsProjectOnlyBudgetAgent(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	state := filepath.Join(home, ".yakos-state")
	_ = os.MkdirAll(state, 0o700)
	_ = os.WriteFile(filepath.Join(proj, ".yakos.yml"), []byte("agent_budgets:\n  backend: 5\n"), 0o600)
	ts := time.Now().UTC().Format(time.RFC3339)
	_ = os.WriteFile(filepath.Join(state, "dispatch-log.ndjson"), []byte(fmt.Sprintf(`{"type":"dispatch_finished","ts":%q,"agent":"backend","usage":{"total_cost_usd":6}}`+"\n", ts)), 0o600)
	var buf bytes.Buffer
	r := &runner{cfg: Config{Writer: &buf, ProjectPath: proj}, w: &buf, home: home, env: func(string) string { return "" }, report: &Report{}}
	r.checkAgentBudgets()
	if !strings.Contains(buf.String(), "backend: hard_stop") {
		t.Fatalf("project-limited agent missing:\n%s", buf.String())
	}
}
