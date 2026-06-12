package consoleui_test

// flows_handler_test.go — unit + integration tests for /flows/api/* endpoints.
//
// Test coverage:
//   - GET /flows/api/workflows        — list, empty dir, non-yaml skipped
//   - GET /flows/api/workflow         — happy, not found, malformed, traversal
//   - POST /flows/api/workflow        — save, 409 conflict, invalid name/yaml
//   - POST /flows/api/run             — happy (engine nil → 503), invalid name, traversal
//   - GET /flows/api/run              — happy, not found, traversal
//   - GET /flows/api/run/node         — happy, not found, traversal (id AND node)
//   - POST /flows/api/resume          — happy (engine nil → 503), traversal
//
// Determinism: no time.Sleep; no subprocess calls; no LLM calls.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/consoleui"
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

	// Modify the file on disk to simulate another operator saving.
	// We do this by saving again with an empty version to get a new version.
	body2, _ := json.Marshal(map[string]string{
		"name":    "my-flow",
		"yaml":    strings.Replace(minimalYAML, "run tests", "run integration tests", 1),
		"version": "",
	})
	resp2 := authedPost(t, ts.URL+"/flows/api/workflow", tok, string(body2))
	drainClose(resp2)
	// This succeeds (empty version = force-create).

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
