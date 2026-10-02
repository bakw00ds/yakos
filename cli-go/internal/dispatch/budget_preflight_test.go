package dispatch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/budget"
)

// A hard-stopped agent is refused before any roster compose or runtime exec.
func TestRunRefusesAgentInHardStop(t *testing.T) {
	state := t.TempDir()
	t.Setenv("YAKOS_DISPATCH_LOG", state)
	if err := budget.SetLimit(state, "backend", 5, budget.Monthly); err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf(`{"type":"dispatch_finished","ts":%q,"agent":"backend","usage":{"total_cost_usd":5.5}}`+"\n", time.Now().UTC().Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(state, "dispatch-log.ndjson"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := Run(context.Background(), Request{AgentName: "backend", Task: "t", Project: t.TempDir(), YakosRoot: t.TempDir()})
	if !budget.IsRefused(err) {
		t.Fatalf("want a budget refusal, got %v", err)
	}
	// The refusal must not have written a dispatch_started event.
	data, _ := os.ReadFile(filepath.Join(state, "dispatch-log.ndjson"))
	if string(data) != line {
		t.Fatalf("a refused dispatch must not touch the log:\n%s", data)
	}
}
