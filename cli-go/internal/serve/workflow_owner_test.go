package serve

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/loopbackowner"
)

// K-86 item 3 (k82-security-review-2026-09-23.md, PR #262's REST fix applied
// to JSON-RPC): yakos.workflow.run / .resume took the run's owner from a
// self-asserted "operator_id" param. The owner must be the daemon's own
// loopback operator ID, derived server-side (same source as yakos.term.create
// and the console's loopback identity), and any client-supplied value ignored.

const wfOwnerTestYAML = `version: 1
name: my-flow
nodes:
  - id: step1
    agent: no-such-agent-k86
    prompt: "x"
    output_limit: 4096
`

func newWorkflowOwnerCfg(t *testing.T) (Config, string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YAKOS_DISPATCH_LOG", t.TempDir())
	ws := t.TempDir()
	stateDir := filepath.Join(t.TempDir(), "state")
	svc := dispatch.NewService(dispatch.ServiceConfig{YakosRoot: ws, WorkspaceRoot: ws})
	cfg := Config{
		DispatchService: svc,
		WorkspaceRoot:   ws,
		YakosRoot:       ws,
		RESTStateDir:    stateDir,
		ServerCtx:       context.Background(),
	}
	wfDir := filepath.Join(workflowWorkDir(ws), "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, "my-flow.yaml"), []byte(wfOwnerTestYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfg, ws, stateDir
}

// runJSONFields waits for the run to reach a terminal status and returns
// (owner_operator_id, status).
func waitRunTerminal(t *testing.T, ws, runID string) string {
	t.Helper()
	path := filepath.Join(workflowWorkDir(ws), "workflows", "runs", runID, "run.json")
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			var probe struct {
				Owner  string `json:"owner_operator_id"`
				Status string `json:"status"`
			}
			if json.Unmarshal(data, &probe) == nil && (probe.Status == "failed" || probe.Status == "completed") {
				return probe.Owner
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %s never reached a terminal status", runID)
	return ""
}

func TestWorkflowRun_OwnerIsServerDerived_NotClientSupplied(t *testing.T) {
	cfg, ws, stateDir := newWorkflowOwnerCfg(t)
	want := loopbackowner.LoadOrCreate(stateDir)

	params := json.RawMessage(`{"name":"my-flow","run_id":"run-k86-owner-1","operator_id":"alice"}`)
	if _, err := handleWorkflowRun(cfg)(context.Background(), params); err != nil {
		t.Fatalf("workflow.run: %v", err)
	}
	got := waitRunTerminal(t, ws, "run-k86-owner-1")
	if got == "alice" {
		t.Fatal("SECURITY: run owner is the client-supplied operator_id")
	}
	if got != want {
		t.Fatalf("owner_operator_id=%q; want the server-derived loopback owner %q", got, want)
	}
}

func TestWorkflowResume_OwnerIsServerDerived_NotClientSupplied(t *testing.T) {
	cfg, ws, stateDir := newWorkflowOwnerCfg(t)
	want := loopbackowner.LoadOrCreate(stateDir)

	if _, err := handleWorkflowRun(cfg)(context.Background(), json.RawMessage(`{"name":"my-flow","run_id":"run-k86-prior"}`)); err != nil {
		t.Fatalf("workflow.run: %v", err)
	}
	waitRunTerminal(t, ws, "run-k86-prior")

	params := json.RawMessage(`{"name":"my-flow","prior_run_id":"run-k86-prior","new_run_id":"run-k86-resumed","operator_id":"alice"}`)
	if _, err := handleWorkflowResume(cfg)(context.Background(), params); err != nil {
		t.Fatalf("workflow.resume: %v", err)
	}
	got := waitRunTerminal(t, ws, "run-k86-resumed")
	if got == "alice" {
		t.Fatal("SECURITY: resumed run owner is the client-supplied operator_id")
	}
	if got != want {
		t.Fatalf("owner_operator_id=%q; want the server-derived loopback owner %q", got, want)
	}
}

// The param structs must not even carry an owner field any more.
func TestWorkflowParams_HaveNoOperatorField(t *testing.T) {
	for name, v := range map[string]interface{}{"run": workflowRunParams{}, "resume": workflowResumeParams{}} {
		b, _ := json.Marshal(v)
		var m map[string]interface{}
		_ = json.Unmarshal(b, &m)
		if _, ok := m["operator_id"]; ok {
			t.Errorf("workflow.%s params still expose operator_id", name)
		}
	}
}

// K-166: yakos.workflow.status returned any run's state to any socket caller.
// A run owned by another operator must read exactly like a missing run.
func TestWorkflowStatus_OwnerCheck(t *testing.T) {
	cfg, ws, stateDir := newWorkflowOwnerCfg(t)
	self := loopbackowner.LoadOrCreate(stateDir)
	runsDir := filepath.Join(workflowWorkDir(ws), "workflows", "runs")
	for id, owner := range map[string]string{
		"run-mine": self, "run-legacy": "", "run-theirs": "someone-else",
	} {
		dir := filepath.Join(runsDir, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(map[string]any{"run_id": id, "workflow_name": "my-flow", "status": "completed", "owner_operator_id": owner})
		if err := os.WriteFile(filepath.Join(dir, "run.json"), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h := handleWorkflowStatus(cfg)
	call := func(id string) (any, error) {
		p, _ := json.Marshal(map[string]string{"run_id": id})
		return h(context.Background(), p)
	}
	for _, id := range []string{"run-mine", "run-legacy"} {
		if _, err := call(id); err != nil {
			t.Errorf("%s: %v, want readable", id, err)
		}
	}
	_, errTheirs := call("run-theirs")
	_, errMissing := call("run-nope")
	if errTheirs == nil || errMissing == nil {
		t.Fatalf("foreign run readable (%v) or missing run readable (%v)", errTheirs, errMissing)
	}
	norm := func(e error, id string) string { return strings.ReplaceAll(e.Error(), id, "ID") }
	if norm(errTheirs, "run-theirs") != norm(errMissing, "run-nope") {
		t.Errorf("foreign run is distinguishable from a missing one:\n  %v\n  %v", errTheirs, errMissing)
	}
}
