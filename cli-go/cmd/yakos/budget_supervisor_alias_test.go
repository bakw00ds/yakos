package main

// budget_supervisor_alias_test.go: the agent a project names as its supervisor keeps
// the supervisor's budget (K-136, security review of #330, finding 8). The hook launches
// `yakos dispatch <name>` and asks `budget check <name> --project <dir>`, so the CLI is
// the surface that must see it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/budget"
)

func TestBudgetCheck_RenamedSupervisorKeepsTheBuiltinBudget(t *testing.T) {
	state := t.TempDir()
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, ".yakos.yml"), []byte("supervisor:\n  runtime: claude\n  agent: watchdog\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTokenLog(t, state, "watchdog", 70_000_000, 0)

	// 70M subscription tokens and $0: the project's supervisor is at its hard stop.
	code, out := runYakos(t, state, nil, "budget", "check", "watchdog", "--project", proj, "--json")
	if code != budget.ExitHardStop || !strings.Contains(out, `"state":"hard_stop"`) ||
		!strings.Contains(out, `"limit_tokens":33000000`) || !strings.Contains(out, `"stop_tokens":66000000`) || !strings.Contains(out, `"spent_usd":0`) {
		t.Fatalf("a renamed supervisor at 70M tokens: exit %d (want %d): %s", code, budget.ExitHardStop, out)
	}

	// The control: a project that does not name it leaves it with no budget.
	code, out = runYakos(t, state, nil, "budget", "check", "watchdog", "--project", t.TempDir(), "--json")
	if code != 0 || !strings.Contains(out, `"state":"off"`) {
		t.Fatalf("an agent no project names as its supervisor has no budget: exit %d: %s", code, out)
	}

	// status lists it, so an operator sees the stop.
	code, out = runYakos(t, state, nil, "budget", "status", "--project", proj)
	if code != 0 || !strings.Contains(out, "watchdog") || !strings.Contains(out, "hard_stop") {
		t.Fatalf("status lists the renamed supervisor: exit %d:\n%s", code, out)
	}
}

// A reset lifts the renamed supervisor's stop too, in the window it is counted in: the
// operator gave the supervisor a lifetime window, so the renamed one counts in that
// window, and the reset must be recorded in it (reset takes --project, as status does).
func TestBudgetReset_RenamedSupervisorUsesTheSupervisorsWindow(t *testing.T) {
	state := t.TempDir()
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, ".yakos.yml"), []byte("supervisor:\n  agent: watchdog\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := budget.SetTokenLimit(state, "supervisor", 33_000_000, budget.Lifetime); err != nil {
		t.Fatal(err)
	}
	writeTokenLog(t, state, "watchdog", 40_000_000, 0)
	if code, out := runYakos(t, state, nil, "budget", "check", "watchdog", "--project", proj, "--json"); code != budget.ExitHardStop || !strings.Contains(out, `"window":"lifetime"`) {
		t.Fatalf("setup: exit %d: %s", code, out)
	}
	code, out := runYakos(t, state, nil, "budget", "reset", "watchdog", "--project", proj)
	if code != 0 || !strings.Contains(out, "restarted") || strings.Contains(out, "no budget") {
		t.Fatalf("reset: exit %d: %s", code, out)
	}
	if code, out := runYakos(t, state, nil, "budget", "check", "watchdog", "--project", proj, "--json"); code != 0 || !strings.Contains(out, `"state":"ok"`) || strings.Contains(out, "spent_tokens") {
		t.Fatalf("after the reset the stop is lifted: exit %d: %s", code, out)
	}
}
