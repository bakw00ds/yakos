package dispatch

// budget_preflight_alias_test.go: the console's and the dispatcher's pre-flight hold
// the agent a project names as its supervisor to the supervisor's budget (K-136,
// security review of #330, finding 8).

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/budget"
)

func TestPreflightBudget_RenamedSupervisorIsHeldToTheSupervisorsBudget(t *testing.T) {
	logDir := isolatedLogDir(t)
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, ".yakos.yml"), []byte("supervisor:\n  agent: watchdog\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 70M subscription tokens and no dollars: past 2x the supervisor's 33M, where dispatch refuses.
	writeFinishedLine(t, logDir, fmt.Sprintf(`{"type":"dispatch_finished","ts":%q,"agent":"watchdog","runtime":"claude","billing":"subscription","usage":{"input_tokens":70000000,"output_tokens":0,"total_cost_usd":0}}`,
		time.Now().UTC().Format(time.RFC3339)))

	if err := PreflightBudget("watchdog", proj); !budget.IsRefused(err) {
		t.Fatalf("a renamed supervisor past 2x its token budget must be refused, got %v", err)
	}
	// The control: with no project naming it, or another project, it is an unbudgeted agent.
	if err := PreflightBudget("watchdog", t.TempDir()); err != nil {
		t.Fatalf("an agent no project names as its supervisor is not refused: %v", err)
	}
	if err := PreflightBudget("watchdog", ""); err != nil {
		t.Fatalf("no project, no supervisor alias: %v", err)
	}
}
