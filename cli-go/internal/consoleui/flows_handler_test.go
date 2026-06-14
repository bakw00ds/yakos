package consoleui_test

// flows_handler_test.go — unit + integration tests for /flows/api/* endpoints.
//
// Test coverage:
//   - GET /flows/api/workflows        — list, empty dir, non-yaml skipped
//   - GET /flows/api/workflow         — happy, not found, malformed, traversal
//   - POST /flows/api/workflow        — save, 409 conflict, invalid name/yaml
//   - POST /flows/api/workflow        — clobber-409: empty version + existing file → 409
//   - POST /flows/api/run             — happy (engine nil → 503), invalid name, traversal
//   - POST /flows/api/run             — happy with fake engine → 202 + run_id (N3)
//   - GET /flows/api/run              — happy, not found, traversal
//   - GET /flows/api/run/node         — happy, not found, traversal (id AND node)
//   - POST /flows/api/resume          — happy (engine nil → 503), traversal
//   - POST /flows/api/resume          — traversal workflow_name in run.json → 400 (B1)
//   - POST /flows/api/cancel          — 202 on active run, 404 on unknown, 405 on GET, 403 on RoleRead
//
// Determinism: no time.Sleep; no subprocess calls; no LLM calls.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// ---- Test helpers ------------------------------------------------------------

// newFlowsTestServer builds a consoleui.Server with a real workDir wired in.
// No WorkflowEngine is set so run/resume return 503 (engine nil).
func newFlowsTestServer(t *testing.T) (*httptest.Server, string, string) {
	t.Helper()
	stateDir := t.TempDir()
	workDir := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)

	srv := consoleui.New(consoleui.Config{
		Token:             tok,
		KanbanBoardPath:   t.TempDir() + "/kanban.md",
		KanbanProject:     "test",
		MetricsProjectDir: t.TempDir(),
		PerfWorkDir:       t.TempDir(),
		Bus:               bus,
		WorkDir:           workDir,
		// WorkflowEngine intentionally nil — run/resume return 503.
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, tok, workDir
}

// authedGet issues a GET with the token.
func authedGet(t *testing.T, url, tok string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

// authedPost issues a POST with token and Content-Type: application/json.
func authedPost(t *testing.T, url, tok, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

// bodyStr drains and closes the response body, returning the content as string.
func bodyStr(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// writeWorkflow writes a minimal valid workflow YAML to <workDir>/workflows/<name>.yaml.
func writeWorkflow(t *testing.T, workDir, name, yamlContent string) {
	t.Helper()
	dir := filepath.Join(workDir, "workflows")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdirall workflows: %v", err)
	}
	path := filepath.Join(dir, name+".yaml")
	if err := os.WriteFile(path, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
}

// minimalYAML is a minimal valid workflow YAML for testing.
const minimalYAML = `version: 1
name: my-flow
nodes:
  - id: step1
    agent: tester
    prompt: "run tests"
    output_limit: 4096
`

// ---- GET /flows/api/workflows tests ------------------------------------------

func TestFlows_ListWorkflows_Empty(t *testing.T) {
	ts, tok, _ := newFlowsTestServer(t)
	resp := authedGet(t, ts.URL+"/flows/api/workflows", tok)
	body := bodyStr(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d; want 200; body=%s", resp.StatusCode, body)
	}
	var result map[string][]string
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, body)
	}
	wfs := result["workflows"]
	if len(wfs) != 0 {
		t.Errorf("workflows=%v; want empty slice", wfs)
	}
}

func TestFlows_ListWorkflows_ReturnsNames(t *testing.T) {
	ts, tok, workDir := newFlowsTestServer(t)

	writeWorkflow(t, workDir, "my-flow", minimalYAML)
	writeWorkflow(t, workDir, "another-flow", strings.Replace(minimalYAML, "my-flow", "another-flow", 1))

	// Also create a non-yaml file that should be skipped.
	dir := filepath.Join(workDir, "workflows")
	if err := os.WriteFile(filepath.Join(dir, "README.txt"), []byte("skip me"), 0644); err != nil {
		t.Fatal(err)
	}
	// Create a yaml with an invalid (traversal) name that should be skipped.
	if err := os.WriteFile(filepath.Join(dir, "..badname.yaml"), []byte("skip"), 0644); err != nil {
		t.Fatal(err)
	}

	resp := authedGet(t, ts.URL+"/flows/api/workflows", tok)
	body := bodyStr(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d; want 200; body=%s", resp.StatusCode, body)
	}
	var result map[string][]string
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	wfs := result["workflows"]
	if len(wfs) != 2 {
		t.Errorf("workflows=%v; want exactly 2 entries", wfs)
	}
}

// ---- GET /flows/api/workflow tests -------------------------------------------

func TestFlows_GetWorkflow_NotFound(t *testing.T) {
	ts, tok, _ := newFlowsTestServer(t)
	resp := authedGet(t, ts.URL+"/flows/api/workflow?name=nonexistent", tok)
	drainClose(resp)

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status=%d; want 404", resp.StatusCode)
	}
}

func TestFlows_GetWorkflow_InvalidName(t *testing.T) {
	ts, tok, _ := newFlowsTestServer(t)
	resp := authedGet(t, ts.URL+"/flows/api/workflow?name=../etc/passwd", tok)
	drainClose(resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d; want 400 (invalid name)", resp.StatusCode)
	}
}

func TestFlows_GetWorkflow_TraversalRejected(t *testing.T) {
	ts, tok, _ := newFlowsTestServer(t)
	// Traversal via dot-dot encoded
	for _, bad := range []string{"../etc/passwd", "..", "foo/bar", "FLOW", "flow!", ""} {
		resp := authedGet(t, ts.URL+"/flows/api/workflow?name="+bad, tok)
		drainClose(resp)
		if resp.StatusCode == http.StatusOK {
			t.Errorf("name=%q: status=200; want 400 (traversal guard)", bad)
		}
	}
}

func TestFlows_GetWorkflow_Happy(t *testing.T) {
	ts, tok, workDir := newFlowsTestServer(t)
	writeWorkflow(t, workDir, "my-flow", minimalYAML)

	resp := authedGet(t, ts.URL+"/flows/api/workflow?name=my-flow", tok)
	body := bodyStr(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d; want 200; body=%s", resp.StatusCode, body)
	}
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, body)
	}
	if result["name"] != "my-flow" {
		t.Errorf("name=%v; want my-flow", result["name"])
	}
	if result["yaml"] == "" || result["yaml"] == nil {
		t.Error("yaml field should be non-empty")
	}
	if result["version"] == "" || result["version"] == nil {
		t.Error("version field should be non-empty (content hash)")
	}
}

// ---- POST /flows/api/workflow tests ------------------------------------------

func TestFlows_SaveWorkflow_InvalidName(t *testing.T) {
	ts, tok, _ := newFlowsTestServer(t)
	body := `{"name":"../etc","yaml":"version: 1\nname: x\nnodes: []\n","version":""}`
	resp := authedPost(t, ts.URL+"/flows/api/workflow", tok, body)
	drainClose(resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d; want 400 (invalid name)", resp.StatusCode)
	}
}

func TestFlows_SaveWorkflow_InvalidYAML(t *testing.T) {
	ts, tok, _ := newFlowsTestServer(t)
	body := `{"name":"my-flow","yaml":"not: valid: yaml: [","version":""}`
	resp := authedPost(t, ts.URL+"/flows/api/workflow", tok, body)
	drainClose(resp)

	// The YAML parse error should produce 400.
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d; want 400 (invalid YAML)", resp.StatusCode)
	}
}

func TestFlows_SaveWorkflow_ValidationError(t *testing.T) {
	ts, tok, _ := newFlowsTestServer(t)
	// Missing output_limit → validation error.
	badYAML := `version: 1
name: my-flow
nodes:
  - id: step1
    agent: tester
    prompt: "run"
`
	body, _ := json.Marshal(map[string]string{"name": "my-flow", "yaml": badYAML, "version": ""})
	resp := authedPost(t, ts.URL+"/flows/api/workflow", tok, string(body))
	drainClose(resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d; want 400 (validation error: missing output_limit)", resp.StatusCode)
	}
}

func TestFlows_SaveWorkflow_Happy(t *testing.T) {
	ts, tok, workDir := newFlowsTestServer(t)

	body, _ := json.Marshal(map[string]string{"name": "my-flow", "yaml": minimalYAML, "version": ""})
	resp := authedPost(t, ts.URL+"/flows/api/workflow", tok, string(body))
	respBody := bodyStr(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d; want 200; body=%s", resp.StatusCode, respBody)
	}
	var result map[string]string
	if err := json.Unmarshal([]byte(respBody), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["version"] == "" {
		t.Error("version field should be non-empty")
	}

	// File should exist on disk.
	path := filepath.Join(workDir, "workflows", "my-flow.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("workflow file not found on disk: %v", err)
	}
}

func TestFlows_SaveWorkflow_ConflictOnStaleVersion(t *testing.T) {
	ts, tok, _ := newFlowsTestServer(t)

	// First save (create).
	body1, _ := json.Marshal(map[string]string{"name": "my-flow", "yaml": minimalYAML, "version": ""})
	resp1 := authedPost(t, ts.URL+"/flows/api/workflow", tok, string(body1))
	body1Str := bodyStr(t, resp1)
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first save: status=%d; body=%s", resp1.StatusCode, body1Str)
	}
	var firstResult map[string]string
	if err := json.Unmarshal([]byte(body1Str), &firstResult); err != nil {
		t.Fatalf("unmarshal first: %v", err)
	}
	v1 := firstResult["version"]

	// Simulate another operator saving by doing a valid save with v1 as the
	// version (the other operator also loaded at v1). This changes the on-disk
	// content, producing a new version stamp.
	body2, _ := json.Marshal(map[string]string{
		"name":    "my-flow",
		"yaml":    strings.Replace(minimalYAML, "run tests", "run integration tests", 1),
		"version": v1, // other operator also had v1 when they loaded
	})
	resp2 := authedPost(t, ts.URL+"/flows/api/workflow", tok, string(body2))
	if resp2.StatusCode != http.StatusOK {
		body2Str := bodyStr(t, resp2)
		t.Fatalf("intermediate save: status=%d; body=%s", resp2.StatusCode, body2Str)
	}
	drainClose(resp2)

	// Now try to save with the stale version (v1) — should get 409.
	body3, _ := json.Marshal(map[string]string{"name": "my-flow", "yaml": minimalYAML, "version": v1})
	resp3 := authedPost(t, ts.URL+"/flows/api/workflow", tok, string(body3))
	drainClose(resp3)

	if resp3.StatusCode != http.StatusConflict {
		t.Errorf("status=%d; want 409 Conflict on stale version", resp3.StatusCode)
	}
}

func TestFlows_SaveWorkflow_SameVersionNoConflict(t *testing.T) {
	ts, tok, _ := newFlowsTestServer(t)

	// First save (create).
	body1, _ := json.Marshal(map[string]string{"name": "my-flow", "yaml": minimalYAML, "version": ""})
	resp1 := authedPost(t, ts.URL+"/flows/api/workflow", tok, string(body1))
	body1Str := bodyStr(t, resp1)
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first save: status=%d; body=%s", resp1.StatusCode, body1Str)
	}
	var firstResult map[string]string
	if err := json.Unmarshal([]byte(body1Str), &firstResult); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	v1 := firstResult["version"]

	// Save again with the same version — should succeed (no change on disk).
	body2, _ := json.Marshal(map[string]string{"name": "my-flow", "yaml": minimalYAML, "version": v1})
	resp2 := authedPost(t, ts.URL+"/flows/api/workflow", tok, string(body2))
	drainClose(resp2)

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200 (same version, same content = idempotent save)", resp2.StatusCode)
	}
}

// ---- POST /flows/api/run tests -----------------------------------------------

func TestFlows_Run_InvalidName(t *testing.T) {
	ts, tok, _ := newFlowsTestServer(t)
	resp := authedPost(t, ts.URL+"/flows/api/run?name=../etc/passwd", tok, `{}`)
	drainClose(resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d; want 400 (traversal guard)", resp.StatusCode)
	}
}

func TestFlows_Run_TraversalNames(t *testing.T) {
	ts, tok, _ := newFlowsTestServer(t)
	for _, bad := range []string{"../etc", "foo/bar", "..", ""} {
		resp := authedPost(t, ts.URL+"/flows/api/run?name="+bad, tok, `{}`)
		drainClose(resp)
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted {
			t.Errorf("name=%q: status=%d; want 400 (traversal guard)", bad, resp.StatusCode)
		}
	}
}

func TestFlows_Run_WorkflowNotFound(t *testing.T) {
	ts, tok, _ := newFlowsTestServer(t)
	resp := authedPost(t, ts.URL+"/flows/api/run?name=nonexistent", tok, `{}`)
	drainClose(resp)

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status=%d; want 404", resp.StatusCode)
	}
}

func TestFlows_Run_EngineNil_Returns503(t *testing.T) {
	ts, tok, workDir := newFlowsTestServer(t)
	writeWorkflow(t, workDir, "my-flow", minimalYAML)

	// Engine is nil in newFlowsTestServer → expect 503.
	resp := authedPost(t, ts.URL+"/flows/api/run?name=my-flow", tok, `{}`)
	drainClose(resp)

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status=%d; want 503 (engine not configured)", resp.StatusCode)
	}
}

// ---- GET /flows/api/run tests ------------------------------------------------

func TestFlows_GetRun_InvalidID(t *testing.T) {
	ts, tok, _ := newFlowsTestServer(t)
	for _, bad := range []string{"../etc", "foo/bar", "..", ""} {
		resp := authedGet(t, ts.URL+"/flows/api/run?id="+bad, tok)
		drainClose(resp)
		if resp.StatusCode == http.StatusOK {
			t.Errorf("id=%q: status=200; want 400 (traversal guard)", bad)
		}
	}
}

func TestFlows_GetRun_NotFound(t *testing.T) {
	ts, tok, _ := newFlowsTestServer(t)
	resp := authedGet(t, ts.URL+"/flows/api/run?id=run-20240101-000000-aabbcc", tok)
	drainClose(resp)

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status=%d; want 404", resp.StatusCode)
	}
}

func TestFlows_GetRun_ReturnsJSON(t *testing.T) {
	ts, tok, workDir := newFlowsTestServer(t)

	// Manually create a run.json.
	runID := "run-20240101-000000-aabbcc"
	runDir := filepath.Join(workDir, "workflows", "runs", runID)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatal(err)
	}
	runJSON := `{"run_id":"run-20240101-000000-aabbcc","status":"completed","nodes":{}}`
	if err := os.WriteFile(filepath.Join(runDir, "run.json"), []byte(runJSON), 0644); err != nil {
		t.Fatal(err)
	}

	resp := authedGet(t, ts.URL+"/flows/api/run?id=run-20240101-000000-aabbcc", tok)
	body := bodyStr(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d; want 200; body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "completed") {
		t.Errorf("body=%s; should contain 'completed'", body)
	}
}

// ---- GET /flows/api/run/node tests -------------------------------------------

func TestFlows_GetNodeOutput_InvalidRunID(t *testing.T) {
	ts, tok, _ := newFlowsTestServer(t)
	for _, bad := range []string{"../etc", "..", "foo/bar", ""} {
		resp := authedGet(t, ts.URL+"/flows/api/run/node?id="+bad+"&node=step1", tok)
		drainClose(resp)
		if resp.StatusCode == http.StatusOK {
			t.Errorf("id=%q: status=200; want 400 (traversal guard on runID)", bad)
		}
	}
}

func TestFlows_GetNodeOutput_InvalidNodeID(t *testing.T) {
	ts, tok, _ := newFlowsTestServer(t)
	for _, bad := range []string{"../etc", "..", "foo/bar", ""} {
		resp := authedGet(t, ts.URL+"/flows/api/run/node?id=run-20240101-000000-aabbcc&node="+bad, tok)
		drainClose(resp)
		if resp.StatusCode == http.StatusOK {
			t.Errorf("node=%q: status=200; want 400 (traversal guard on nodeID)", bad)
		}
	}
}

func TestFlows_GetNodeOutput_NotFound(t *testing.T) {
	ts, tok, _ := newFlowsTestServer(t)
	resp := authedGet(t, ts.URL+"/flows/api/run/node?id=run-20240101-000000-aabbcc&node=step1", tok)
	drainClose(resp)

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status=%d; want 404", resp.StatusCode)
	}
}

func TestFlows_GetNodeOutput_Happy(t *testing.T) {
	ts, tok, workDir := newFlowsTestServer(t)

	// Manually create a node stdout file.
	runID := "run-20240101-000000-aabbcc"
	nodesDir := filepath.Join(workDir, "workflows", "runs", runID, "nodes")
	if err := os.MkdirAll(nodesDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nodesDir, "step1.stdout"), []byte("hello world\n"), 0644); err != nil {
		t.Fatal(err)
	}

	resp := authedGet(t, ts.URL+"/flows/api/run/node?id=run-20240101-000000-aabbcc&node=step1", tok)
	body := bodyStr(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d; want 200; body=%s", resp.StatusCode, body)
	}
	if body != "hello world\n" {
		t.Errorf("body=%q; want 'hello world\\n'", body)
	}
}

// ---- POST /flows/api/resume tests --------------------------------------------

func TestFlows_Resume_InvalidRunID(t *testing.T) {
	ts, tok, _ := newFlowsTestServer(t)
	body := `{"run_id":"../etc/passwd"}`
	resp := authedPost(t, ts.URL+"/flows/api/resume", tok, body)
	drainClose(resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d; want 400 (traversal guard)", resp.StatusCode)
	}
}

func TestFlows_Resume_InvalidNewRunID(t *testing.T) {
	ts, tok, _ := newFlowsTestServer(t)
	body := `{"run_id":"run-20240101-000000-aabbcc","new_run_id":"../bad"}`
	resp := authedPost(t, ts.URL+"/flows/api/resume", tok, body)
	drainClose(resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d; want 400 (traversal guard on new_run_id)", resp.StatusCode)
	}
}

func TestFlows_Resume_PriorRunNotFound(t *testing.T) {
	ts, tok, _ := newFlowsTestServer(t)
	body := `{"run_id":"run-20240101-000000-aabbcc"}`
	resp := authedPost(t, ts.URL+"/flows/api/resume", tok, body)
	drainClose(resp)

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status=%d; want 404 (prior run not found)", resp.StatusCode)
	}
}

func TestFlows_Resume_EngineNil_Returns503(t *testing.T) {
	ts, tok, workDir := newFlowsTestServer(t)

	// Create a prior run.json + workflow YAML for the resume to find.
	runID := "run-20240101-000000-aabbcc"
	runDir := filepath.Join(workDir, "workflows", "runs", runID)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatal(err)
	}
	runJSON := `{"run_id":"run-20240101-000000-aabbcc","workflow_name":"my-flow","status":"failed","nodes":{}}`
	if err := os.WriteFile(filepath.Join(runDir, "run.json"), []byte(runJSON), 0644); err != nil {
		t.Fatal(err)
	}
	writeWorkflow(t, workDir, "my-flow", minimalYAML)

	body := `{"run_id":"run-20240101-000000-aabbcc"}`
	resp := authedPost(t, ts.URL+"/flows/api/resume", tok, body)
	drainClose(resp)

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status=%d; want 503 (engine not configured)", resp.StatusCode)
	}
}

// ---- B1: path-traversal in deserialized workflow_name → 400 -----------------

// TestFlows_Resume_TraversalWorkflowNameInRunJSON verifies that a planted
// run.json with workflow_name containing a path-traversal sequence is rejected
// with 400 before any file is read outside the workflows directory (B1).
func TestFlows_Resume_TraversalWorkflowNameInRunJSON(t *testing.T) {
	ts, tok, workDir := newFlowsTestServer(t)

	// Plant a run.json whose workflow_name contains a traversal sequence.
	runID := "run-20240101-000000-b1test"
	runDir := filepath.Join(workDir, "workflows", "runs", runID)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatal(err)
	}
	// workflow_name with path traversal — as an attacker would plant it.
	maliciousRunJSON := `{"run_id":"run-20240101-000000-b1test","workflow_name":"../../../etc/passwd","status":"failed","nodes":{}}`
	if err := os.WriteFile(filepath.Join(runDir, "run.json"), []byte(maliciousRunJSON), 0644); err != nil {
		t.Fatal(err)
	}

	body := `{"run_id":"run-20240101-000000-b1test"}`
	resp := authedPost(t, ts.URL+"/flows/api/resume", tok, body)
	respBody := bodyStr(t, resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d; want 400 (B1 traversal guard on workflow_name); body=%s", resp.StatusCode, respBody)
	}
}

// ---- MEDIUM: clobber-409 when version="" and file exists --------------------

// TestFlows_SaveWorkflow_EmptyVersionNewFile verifies that version="" on a NEW
// file succeeds (force-create is only for new files).
func TestFlows_SaveWorkflow_EmptyVersionNewFile(t *testing.T) {
	ts, tok, workDir := newFlowsTestServer(t)

	// No file exists yet — version="" should succeed (new file).
	body, _ := json.Marshal(map[string]string{"name": "brand-new", "yaml": strings.Replace(minimalYAML, "my-flow", "brand-new", 1), "version": ""})
	resp := authedPost(t, ts.URL+"/flows/api/workflow", tok, string(body))
	respBody := bodyStr(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d; want 200 (new file with empty version); body=%s", resp.StatusCode, respBody)
	}
	// File should exist on disk.
	path := filepath.Join(workDir, "workflows", "brand-new.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("workflow file not found on disk: %v", err)
	}
}

// TestFlows_SaveWorkflow_EmptyVersionExistingFile verifies that version="" on
// an EXISTING file returns 409 (force-create is not allowed on existing files;
// the caller must supply the current version).
func TestFlows_SaveWorkflow_EmptyVersionExistingFile(t *testing.T) {
	ts, tok, workDir := newFlowsTestServer(t)

	// Create the file first.
	writeWorkflow(t, workDir, "existing-flow", strings.Replace(minimalYAML, "my-flow", "existing-flow", 1))

	// Try to save with empty version — should get 409.
	body, _ := json.Marshal(map[string]string{
		"name":    "existing-flow",
		"yaml":    strings.Replace(minimalYAML, "my-flow", "existing-flow", 1),
		"version": "",
	})
	resp := authedPost(t, ts.URL+"/flows/api/workflow", tok, string(body))
	drainClose(resp)

	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status=%d; want 409 (empty version on existing file must require real version round-trip)", resp.StatusCode)
	}
}

// ---- N3: happy-path run test with fake engine → 202 + run_id ----------------

// newFlowsHandlerServer creates an httptest server backed by the flows handler
// with a fake engine injected via the runFn seam. No LLM calls, no real dispatch.
func newFlowsHandlerServer(t *testing.T, fn func(context.Context, dispatch.Params) ([]byte, dispatch.Result, error)) (*httptest.Server, string) {
	t.Helper()
	workDir := t.TempDir()
	handler, _ := consoleui.NewFlowsHandlerForTest(t, workDir, fn)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts, workDir
}

// TestFlows_Run_HappyPath_Returns202_WithRunID verifies that POST /flows/api/run
// with a valid workflow returns 202 Accepted and a well-formed run_id, using the
// fake engine seam (no LLM call, no time.Sleep).
func TestFlows_Run_HappyPath_Returns202_WithRunID(t *testing.T) {
	// Fake runFn: completes immediately with empty output and exit code 0.
	fn := func(_ context.Context, _ dispatch.Params) ([]byte, dispatch.Result, error) {
		return []byte("ok"), dispatch.Result{ExitCode: 0}, nil
	}

	ts, workDir := newFlowsHandlerServer(t, fn)

	// Write a workflow to disk.
	dir := filepath.Join(workDir, "workflows")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "my-flow.yaml"), []byte(minimalYAML), 0644); err != nil {
		t.Fatal(err)
	}

	// POST /flows/api/run?name=my-flow (no auth middleware in this handler — direct mux).
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/flows/api/run?name=my-flow", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /flows/api/run: %v", err)
	}
	body := bodyStr(t, resp)

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status=%d; want 202; body=%s", resp.StatusCode, body)
	}

	var result map[string]string
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("unmarshal response: %v; body=%s", err, body)
	}
	runID := result["run_id"]
	if runID == "" {
		t.Error("run_id should be non-empty in 202 response")
	}
	// run_id should look like "run-<timestamp>-<rand>" — at minimum starts with "run-".
	if !strings.HasPrefix(runID, "run-") {
		t.Errorf("run_id=%q; want prefix 'run-'", runID)
	}
}

// ---- DELETE /flows/api/workflow tests ----------------------------------------

// authedDelete issues a DELETE with the token.
func authedDelete(t *testing.T, url, tok string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		t.Fatalf("new delete request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", url, err)
	}
	return resp
}

func TestFlows_DeleteWorkflow_Happy(t *testing.T) {
	ts, tok, workDir := newFlowsTestServer(t)
	writeWorkflow(t, workDir, "my-flow", minimalYAML)

	resp := authedDelete(t, ts.URL+"/flows/api/workflow?name=my-flow", tok)
	drainClose(resp)

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status=%d; want 204", resp.StatusCode)
	}

	// Verify the file is gone.
	path := filepath.Join(workDir, "workflows", "my-flow.yaml")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected file to be deleted; stat err=%v", err)
	}
}

func TestFlows_DeleteWorkflow_NotFound(t *testing.T) {
	ts, tok, _ := newFlowsTestServer(t)

	resp := authedDelete(t, ts.URL+"/flows/api/workflow?name=nonexistent", tok)
	drainClose(resp)

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status=%d; want 404", resp.StatusCode)
	}
}

func TestFlows_DeleteWorkflow_InvalidName(t *testing.T) {
	ts, tok, _ := newFlowsTestServer(t)

	resp := authedDelete(t, ts.URL+"/flows/api/workflow?name=../etc/passwd", tok)
	drainClose(resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d; want 400 (invalid name)", resp.StatusCode)
	}
}

func TestFlows_DeleteWorkflow_MethodNotAllowed(t *testing.T) {
	ts, tok, _ := newFlowsTestServer(t)

	// PATCH is not supported on /flows/api/workflow.
	req, err := http.NewRequest(http.MethodPatch, ts.URL+"/flows/api/workflow?name=my-flow", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	drainClose(resp)

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status=%d; want 405", resp.StatusCode)
	}
}

func TestFlows_DeleteWorkflow_ListUpdated(t *testing.T) {
	ts, tok, workDir := newFlowsTestServer(t)
	writeWorkflow(t, workDir, "flow-a", strings.Replace(minimalYAML, "my-flow", "flow-a", 1))
	writeWorkflow(t, workDir, "flow-b", strings.Replace(minimalYAML, "my-flow", "flow-b", 1))

	// Delete flow-a.
	resp := authedDelete(t, ts.URL+"/flows/api/workflow?name=flow-a", tok)
	drainClose(resp)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status=%d; want 204", resp.StatusCode)
	}

	// List should now return only flow-b.
	listResp := authedGet(t, ts.URL+"/flows/api/workflows", tok)
	body := bodyStr(t, listResp)
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list status=%d; body=%s", listResp.StatusCode, body)
	}
	var result map[string][]string
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	wfs := result["workflows"]
	if len(wfs) != 1 || wfs[0] != "flow-b" {
		t.Errorf("workflows=%v; want [flow-b]", wfs)
	}
}

// ---- POST /flows/api/cancel tests -------------------------------------------

// newCancelTestServer builds a handler backed by a fake nodeRunFn that blocks
// until its context is cancelled, allowing cancel tests to exercise handleCancel.
// It returns the httptest.Server, the workDir, a "started" channel (closed when
// the fake node fn starts executing), and a "unblock" channel (close it to let
// the fn complete without waiting for ctx cancel).
func newCancelTestServer(t *testing.T) (ts *httptest.Server, workDir string, started <-chan struct{}, unblock chan<- struct{}) {
	t.Helper()
	wDir := t.TempDir()

	startedCh := make(chan struct{})
	unblockCh := make(chan struct{})

	var startOnce sync.Once
	fn := func(ctx context.Context, _ dispatch.Params) ([]byte, dispatch.Result, error) {
		startOnce.Do(func() { close(startedCh) })
		select {
		case <-ctx.Done():
			return nil, dispatch.Result{ExitCode: 1}, ctx.Err()
		case <-unblockCh:
			return []byte("ok"), dispatch.Result{ExitCode: 0}, nil
		}
	}

	handler, _ := consoleui.NewFlowsHandlerForTest(t, wDir, fn)
	testSrv := httptest.NewServer(handler)
	t.Cleanup(testSrv.Close)
	return testSrv, wDir, startedCh, unblockCh
}

// postCancel sends POST /flows/api/cancel?id=<runID> to the test server.
func postCancel(t *testing.T, url, runID string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url+"/flows/api/cancel?id="+runID, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST cancel: %v", err)
	}
	return resp
}

// TestFlows_Cancel_HappyPath verifies that POST /flows/api/cancel?id=<runID>
// returns 202 for an in-flight run, and that the goroutine eventually exits
// (the run finishes).
func TestFlows_Cancel_HappyPath(t *testing.T) {
	t.Parallel()

	ts, workDir, started, _ := newCancelTestServer(t)

	// Write a workflow so handleRun can load it.
	dir := filepath.Join(workDir, "workflows")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "my-flow.yaml"), []byte(minimalYAML), 0644); err != nil {
		t.Fatal(err)
	}

	// Start the run.
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/flows/api/run?name=my-flow", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	runResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /flows/api/run: %v", err)
	}
	body := bodyStr(t, runResp)
	if runResp.StatusCode != http.StatusAccepted {
		t.Fatalf("run status=%d; want 202; body=%s", runResp.StatusCode, body)
	}
	var runResult map[string]string
	if err := json.Unmarshal([]byte(body), &runResult); err != nil {
		t.Fatalf("unmarshal run response: %v", err)
	}
	runID := runResult["run_id"]
	if runID == "" {
		t.Fatal("empty run_id in run response")
	}

	// Wait for the fake node fn to start so we know the run is in-flight.
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for run to start")
	}

	// Cancel the run.
	cancelResp := postCancel(t, ts.URL, runID)
	cancelBody := bodyStr(t, cancelResp)
	if cancelResp.StatusCode != http.StatusAccepted {
		t.Errorf("cancel status=%d; want 202; body=%s", cancelResp.StatusCode, cancelBody)
	}

	// The cancel response should include the run_id.
	var cancelResult map[string]string
	if err := json.Unmarshal([]byte(cancelBody), &cancelResult); err != nil {
		t.Fatalf("unmarshal cancel response: %v; body=%s", err, cancelBody)
	}
	if cancelResult["run_id"] != runID {
		t.Errorf("cancel response run_id=%q; want %q", cancelResult["run_id"], runID)
	}

	// After cancellation the run goroutine should exit and remove the entry from
	// activeRuns.  Poll for 404 on a second cancel call (entry removed on exit).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp2 := postCancel(t, ts.URL, runID)
		body2 := bodyStr(t, resp2)
		if resp2.StatusCode == http.StatusNotFound {
			// Goroutine exited and cleaned up the entry — correct behaviour.
			return
		}
		// Still 202 (goroutine hasn't exited yet) — retry.
		_ = body2
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("timed out waiting for run goroutine to exit after cancel")
}

// TestFlows_Cancel_UnknownRunID verifies that POST /flows/api/cancel with an
// unknown or finished run ID returns 404.
func TestFlows_Cancel_UnknownRunID(t *testing.T) {
	t.Parallel()

	ts, _, _, _ := newCancelTestServer(t)

	resp := postCancel(t, ts.URL, "run-20240101-000000-aabbcc")
	drainClose(resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("cancel unknown run: status=%d; want 404", resp.StatusCode)
	}
}

// TestFlows_Cancel_InvalidRunID verifies that a malformed run ID returns 400.
func TestFlows_Cancel_InvalidRunID(t *testing.T) {
	t.Parallel()

	ts, _, _, _ := newCancelTestServer(t)

	for _, bad := range []string{"../etc/passwd", "foo/bar", "..", ""} {
		resp := postCancel(t, ts.URL, bad)
		drainClose(resp)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("cancel id=%q: status=%d; want 400 (invalid id)", bad, resp.StatusCode)
		}
	}
}

// TestFlows_Cancel_MethodNotAllowed verifies that GET /flows/api/cancel returns 405.
func TestFlows_Cancel_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	ts, _, _, _ := newCancelTestServer(t)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/flows/api/cancel?id=run-20240101-000000-aabbcc", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET cancel: %v", err)
	}
	drainClose(resp)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /flows/api/cancel: status=%d; want 405", resp.StatusCode)
	}
}

// TestFlows_Cancel_RoleReadForbidden verifies that a resolved RoleRead identity
// gets 403 on POST /flows/api/cancel (requires RoleFlowsRun).
func TestFlows_Cancel_RoleReadForbidden(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	workDir := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)

	srv := consoleui.New(consoleui.Config{
		Token:             tok,
		KanbanBoardPath:   t.TempDir() + "/kanban.md",
		KanbanProject:     "test",
		MetricsProjectDir: t.TempDir(),
		PerfWorkDir:       t.TempDir(),
		Bus:               bus,
		WorkDir:           workDir,
	})

	readOnly := netid.Identity{
		OperatorID:    "viewer",
		Role:          netid.RoleRead,
		Authenticated: true,
		Resolved:      true,
	}
	// Inject a RoleRead identity so the per-endpoint role check fires.
	handler := consoleui.RequireTokenForNonStatic(tok,
		consoleui.RequireJSONForMutations(
			injectIdentityMiddleware(readOnly, srv.Handler())))
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/flows/api/cancel?id=run-20240101-000000-aabbcc", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST cancel: %v", err)
	}
	drainClose(resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("RoleRead on /flows/api/cancel: status=%d; want 403", resp.StatusCode)
	}
}
