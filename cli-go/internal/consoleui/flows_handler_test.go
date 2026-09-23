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
	"github.com/bakw00ds/yakos/internal/workflow"
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

	srv := consoleui.MustNew(t, consoleui.Config{
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

// newOwnerScopeTestServer builds a real consoleui.Server (MustNew) with a
// live WorkDir and returns a helper that issues a GET/POST wrapped with a
// given identity injected into the request context (simulating the resolver
// having run with that identity — see injectIdentityMiddleware). Used by the
// R10 (round-1 security review) owner-scoping regression tests below, which
// need per-request identity control that newFlowsTestServer's shared
// unauthenticated srv.Handler() cannot provide.
func newOwnerScopeTestServer(t *testing.T) (tok, workDir string, doAs func(id netid.Identity, method, path, body string) *http.Response) {
	t.Helper()
	stateDir := t.TempDir()
	wDir := t.TempDir()
	tk, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)

	srv := consoleui.MustNew(t, consoleui.Config{
		Token:             tk,
		KanbanBoardPath:   t.TempDir() + "/kanban.md",
		KanbanProject:     "test",
		MetricsProjectDir: t.TempDir(),
		PerfWorkDir:       t.TempDir(),
		Bus:               bus,
		WorkDir:           wDir,
	})

	doAs = func(id netid.Identity, method, path, reqBody string) *http.Response {
		t.Helper()
		handler := consoleui.RequireTokenForNonStatic(tk,
			consoleui.RequireJSONForMutations(
				injectIdentityMiddleware(id, srv.Handler())))
		ts := httptest.NewServer(handler)
		defer ts.Close()

		var bodyReader io.Reader
		if reqBody != "" {
			bodyReader = strings.NewReader(reqBody)
		}
		req, err := http.NewRequest(method, ts.URL+path, bodyReader)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+tk)
		if reqBody != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		return resp
	}
	return tk, wDir, doAs
}

// writeOwnedRun seeds run.json for runID with owner ownerOpID directly on
// disk, matching the shape engine.go's newRunState/persistNow produces.
func writeOwnedRun(t *testing.T, workDir, runID, ownerOpID string) {
	t.Helper()
	runDir := filepath.Join(workDir, "workflows", "runs", runID)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatal(err)
	}
	runJSON, err := json.Marshal(map[string]any{
		"run_id":            runID,
		"workflow_name":     "my-flow",
		"workflow_hash":     "x",
		"owner_operator_id": ownerOpID,
		"status":            "completed",
		"nodes":             map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "run.json"), runJSON, 0644); err != nil {
		t.Fatal(err)
	}
}

// newProductionEngineTestServer builds a real consoleui.Server (MustNew)
// wired with a REAL *workflow.Engine (workflow.NewEngineForTest) whose
// per-node dispatch is fn — no LLM calls, no live dispatch, but unlike
// newFlowsHandlerServer/NewFlowsHandlerForTest (which set flowsHandlers'
// handler-level nodeRunFn shortcut and never touch the engine at all), this
// exercises handleRun/handleResume's REAL production code path.
//
// K2 (k82-security-review-2026-09-23.md): every other flows test in this
// file uses the nodeRunFn shortcut, which causes handleRun/handleResume to
// return BEFORE reaching their production operator-attribution branch (the
// one that calls resolveRunOperatorID and 403s an unresolved identity, or
// that a mutation could revert to the pre-R10 self-asserted body-field
// fallback) — reverting that branch left the whole package green because no
// test ever reached it. Tests built on this helper drive that exact branch.
//
// Returns workDir (for on-disk run.json assertions) and a doAs helper
// identical in shape to newOwnerScopeTestServer's.
func newProductionEngineTestServer(t *testing.T, fn workflow.EngineRunFn) (workDir string, doAs func(id netid.Identity, method, path, body string) *http.Response) {
	t.Helper()
	stateDir := t.TempDir()
	wDir := t.TempDir()
	tk, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)

	eng := workflow.NewEngineForTest(workflow.EngineConfig{
		Bus:     bus,
		WorkDir: wDir,
	}, fn)

	srv := consoleui.MustNew(t, consoleui.Config{
		Token:             tk,
		KanbanBoardPath:   t.TempDir() + "/kanban.md",
		KanbanProject:     "test",
		MetricsProjectDir: t.TempDir(),
		PerfWorkDir:       t.TempDir(),
		Bus:               bus,
		WorkDir:           wDir,
		WorkflowEngine:    eng,
	})

	doAs = func(id netid.Identity, method, path, reqBody string) *http.Response {
		t.Helper()
		handler := consoleui.RequireTokenForNonStatic(tk,
			consoleui.RequireJSONForMutations(
				injectIdentityMiddleware(id, srv.Handler())))
		ts := httptest.NewServer(handler)
		defer ts.Close()

		var bodyReader io.Reader
		if reqBody != "" {
			bodyReader = strings.NewReader(reqBody)
		}
		req, err := http.NewRequest(method, ts.URL+path, bodyReader)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+tk)
		if reqBody != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		return resp
	}
	return wDir, doAs
}

// runJSONOwner reads <workDir>/workflows/runs/<runID>/run.json and returns
// its owner_operator_id field.
func runJSONOwner(t *testing.T, workDir, runID string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(workDir, "workflows", "runs", runID, "run.json"))
	if err != nil {
		t.Fatalf("read run.json for %s: %v", runID, err)
	}
	var probe struct {
		OwnerOpID string `json:"owner_operator_id"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Fatalf("unmarshal run.json for %s: %v", runID, err)
	}
	return probe.OwnerOpID
}

// waitForRunStatus polls <workDir>/workflows/runs/<runID>/run.json until its
// status field equals want (or 5s elapses), and returns the final bytes.
// Used so a "byte-identical run.json" assertion snapshots a run only once
// it has settled into its terminal state — the fake node fns in this file
// always succeed synchronously, so "completed" is reached almost
// immediately, but the run.json write for status/started_at/ended_at/nodes
// happens strictly after the waitForDispatch() signal (which fires from
// inside the node dispatch call, before the run is marked done), so a plain
// read right after that signal would race the debounce writer.
func waitForRunStatus(t *testing.T, workDir, runID, want string) []byte {
	t.Helper()
	path := filepath.Join(workDir, "workflows", "runs", runID, "run.json")
	deadline := time.Now().Add(5 * time.Second)
	var last []byte
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			last = data
			var probe struct {
				Status string `json:"status"`
			}
			if json.Unmarshal(data, &probe) == nil && probe.Status == want {
				return data
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for run %s status=%q; last=%s", runID, want, last)
	return nil
}

// TestFlows_Run_ProductionPath_AttributionIgnoresBodyOperatorID proves K2's
// fix on the REAL production path (not the nodeRunFn shortcut): mallory
// POSTs /flows/api/run with a self-asserted {"operator_id":"alice"} body
// field. The run's recorded owner must be mallory (the resolved identity),
// never alice (the self-asserted body field).
//
// Two identity shapes are tried because the review's mutation
// (k82-security-review-2026-09-23.md, K2) is:
//
//	if resolvedID.Authenticated { operatorID = resolvedID.OperatorID
//	} else                       { operatorID = req.OperatorID }
//
// — an authenticated identity's OperatorID is unaffected by that mutation
// (both branches agree), so an authenticated-only test would pass even
// against the mutant. The loopback shape (Authenticated=false, a stable
// server-stamped OperatorID already present — see resolveRunOperatorID's
// doc comment) is the one the mutation actually flips to the body field,
// and is the shape the review calls out by name: "on the loopback path
// (Authenticated == false) the owner reverts to the browser-minted
// operator_id token, which is precisely the R10 bug."
func TestFlows_Run_ProductionPath_AttributionIgnoresBodyOperatorID(t *testing.T) {
	cases := []struct {
		name    string
		mallory netid.Identity
	}{
		{"authenticated identity", netid.Identity{OperatorID: "mallory", Role: netid.RoleAdmin, Authenticated: true, Resolved: true}},
		{"loopback stamped identity", netid.Identity{OperatorID: "mallory", Role: netid.RoleAdmin, Authenticated: false, Resolved: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := make(chan struct{}, 16)
			fn := func(ctx context.Context, _ dispatch.Params) ([]byte, dispatch.Result, error) {
				calls <- struct{}{}
				return []byte("ok"), dispatch.Result{ExitCode: 0}, nil
			}
			workDir, doAs := newProductionEngineTestServer(t, fn)
			writeWorkflow(t, workDir, "my-flow", minimalYAML)

			resp := doAs(tc.mallory, http.MethodPost, "/flows/api/run?name=my-flow", `{"operator_id":"alice"}`)
			body := bodyStr(t, resp)
			if resp.StatusCode != http.StatusAccepted {
				t.Fatalf("status=%d; want 202; body=%s", resp.StatusCode, body)
			}
			var runResult map[string]string
			if err := json.Unmarshal([]byte(body), &runResult); err != nil {
				t.Fatalf("unmarshal run response: %v; body=%s", err, body)
			}
			runID := runResult["run_id"]
			if runID == "" {
				t.Fatal("empty run_id in run response")
			}

			select {
			case <-calls:
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for node dispatch to start")
			}

			if got := runJSONOwner(t, workDir, runID); got != "mallory" {
				t.Errorf("SECURITY: run.json owner_operator_id=%q; want %q (resolved identity, not the self-asserted operator_id body field)", got, "mallory")
			}

			// Wait for the run's background goroutine to fully settle
			// before the test (and its t.TempDir() cleanup) returns —
			// otherwise the goroutine's still-in-flight debounce/final
			// persistNow writes can race t.TempDir()'s RemoveAll.
			waitForRunStatus(t, workDir, runID, "completed")
		})
	}
}

// TestFlows_Run_ProductionPath_UnresolvedIdentity_FailsClosed proves K2's
// fix's other half: on the REAL production path, a request that never
// resolved any identity at all must be refused (403) rather than starting a
// run under an empty/legacy owner. fn asserts it is never invoked.
func TestFlows_Run_ProductionPath_UnresolvedIdentity_FailsClosed(t *testing.T) {
	fn := func(ctx context.Context, _ dispatch.Params) ([]byte, dispatch.Result, error) {
		t.Error("node dispatch must not run for a request with no resolvable operator identity")
		return nil, dispatch.Result{}, nil
	}
	workDir, doAs := newProductionEngineTestServer(t, fn)
	writeWorkflow(t, workDir, "my-flow", minimalYAML)

	unresolved := netid.Identity{} // Resolved=false: resolver never ran / no identity at all.
	resp := doAs(unresolved, http.MethodPost, "/flows/api/run?name=my-flow", `{}`)
	body := bodyStr(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status=%d; want 403 (unresolved identity must fail closed rather than start a run); body=%s", resp.StatusCode, body)
	}

	runsDir := filepath.Join(workDir, "workflows", "runs")
	entries, _ := os.ReadDir(runsDir)
	if len(entries) != 0 {
		t.Errorf("a run directory was created for an unresolved identity: %v", entries)
	}
}

// TestFlows_Resume_ProductionPath_AttributionIgnoresBodyOperatorID is
// TestFlows_Run_ProductionPath_AttributionIgnoresBodyOperatorID's
// handleResume counterpart (K2 / mutation M8). mallory owns the prior run
// (created through the same production path, so its workflow_hash matches);
// she then resumes it with a self-asserted {"operator_id":"alice"}. The
// resumed run's owner must still be mallory. Both identity shapes are
// exercised for the same reason as the handleRun test: the mutation only
// flips the loopback (Authenticated=false) branch to the body field.
func TestFlows_Resume_ProductionPath_AttributionIgnoresBodyOperatorID(t *testing.T) {
	cases := []struct {
		name    string
		mallory netid.Identity
	}{
		{"authenticated identity", netid.Identity{OperatorID: "mallory", Role: netid.RoleAdmin, Authenticated: true, Resolved: true}},
		{"loopback stamped identity", netid.Identity{OperatorID: "mallory", Role: netid.RoleAdmin, Authenticated: false, Resolved: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := make(chan struct{}, 16)
			fn := func(ctx context.Context, _ dispatch.Params) ([]byte, dispatch.Result, error) {
				calls <- struct{}{}
				return []byte("ok"), dispatch.Result{ExitCode: 0}, nil
			}
			waitForDispatch := func() {
				t.Helper()
				select {
				case <-calls:
				case <-time.After(5 * time.Second):
					t.Fatal("timed out waiting for node dispatch to start")
				}
			}

			workDir, doAs := newProductionEngineTestServer(t, fn)
			writeWorkflow(t, workDir, "my-flow", minimalYAML)

			// mallory creates a prior run through the real production path
			// (so its workflow_hash matches minimalYAML — engine.Resume
			// pins the hash).
			priorResp := doAs(tc.mallory, http.MethodPost, "/flows/api/run?name=my-flow", `{}`)
			priorBody := bodyStr(t, priorResp)
			if priorResp.StatusCode != http.StatusAccepted {
				t.Fatalf("prior run status=%d; want 202; body=%s", priorResp.StatusCode, priorBody)
			}
			var priorResult map[string]string
			if err := json.Unmarshal([]byte(priorBody), &priorResult); err != nil {
				t.Fatalf("unmarshal prior run response: %v; body=%s", err, priorBody)
			}
			priorRunID := priorResult["run_id"]
			waitForDispatch()
			// Wait for the prior run to fully settle (not just dispatch-
			// started) before resuming it: this both guarantees run.json
			// (incl. the hash Resume pins against) is fully written, and
			// avoids racing this run's own goroutine against a later
			// t.TempDir() cleanup.
			waitForRunStatus(t, workDir, priorRunID, "completed")

			resumeBody := `{"run_id":"` + priorRunID + `","operator_id":"alice"}`
			resumeResp := doAs(tc.mallory, http.MethodPost, "/flows/api/resume", resumeBody)
			respBody := bodyStr(t, resumeResp)
			if resumeResp.StatusCode != http.StatusAccepted {
				t.Fatalf("resume status=%d; want 202; body=%s", resumeResp.StatusCode, respBody)
			}
			var resumeResult map[string]string
			if err := json.Unmarshal([]byte(respBody), &resumeResult); err != nil {
				t.Fatalf("unmarshal resume response: %v; body=%s", err, respBody)
			}
			newRunID := resumeResult["new_run_id"]
			if newRunID == "" {
				t.Fatal("empty new_run_id in resume response")
			}
			// The prior run's single node is already NodeCompleted by the
			// time Resume loads it (pinned-output resume), so the resumed
			// run has no node left to dispatch — waitForDispatch's channel
			// never fires. Wait on the resumed run.json reaching its
			// terminal state instead, which persistNow writes unconditionally
			// regardless of whether any node re-executes.
			waitForRunStatus(t, workDir, newRunID, "completed")

			if got := runJSONOwner(t, workDir, newRunID); got != "mallory" {
				t.Errorf("SECURITY: resumed run.json owner_operator_id=%q; want %q (resolved identity, not the self-asserted operator_id body field)", got, "mallory")
			}
		})
	}
}

// TestFlows_Resume_ProductionPath_UnresolvedIdentity_FailsClosed is
// TestFlows_Run_ProductionPath_UnresolvedIdentity_FailsClosed's
// handleResume counterpart (K2). The prior run is legacy/unowned (owner
// "") so checkRunOwnership's first gate lets any caller past it — isolating
// the assertion to resolveRunOperatorID's own fail-closed behaviour on the
// NEW run's attribution, not the prior-run ownership gate.
func TestFlows_Resume_ProductionPath_UnresolvedIdentity_FailsClosed(t *testing.T) {
	fn := func(ctx context.Context, _ dispatch.Params) ([]byte, dispatch.Result, error) {
		t.Error("node dispatch must not run for a resume with no resolvable operator identity")
		return nil, dispatch.Result{}, nil
	}
	workDir, doAs := newProductionEngineTestServer(t, fn)
	writeWorkflow(t, workDir, "my-flow", minimalYAML)

	priorRunID := "run-20260101-000000-legacyxx"
	writeOwnedRun(t, workDir, priorRunID, "") // legacy/unowned: open to any caller

	unresolved := netid.Identity{}
	resp := doAs(unresolved, http.MethodPost, "/flows/api/resume", `{"run_id":"`+priorRunID+`"}`)
	body := bodyStr(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status=%d; want 403 (unresolved identity must fail closed rather than resume); body=%s", resp.StatusCode, body)
	}
}

// TestFlows_Resume_K1_NewRunIDCannotTargetAnotherOperatorsRun is the
// regression test for K1 (k82-security-review-2026-09-23.md), reproducing
// the review's exact scenario end-to-end against the real production path:
// mallory owns her own run; she POSTs /flows/api/resume asking the server
// to write the resumed run into alice's EXISTING run ID via new_run_id.
// This must be refused — in the fixed code, refused structurally, because
// new_run_id is never consulted at all: the server always mints its own ID,
// alice's run.json is untouched byte-for-byte, alice can still read her own
// run, and mallory still cannot read alice's run directly.
func TestFlows_Resume_K1_NewRunIDCannotTargetAnotherOperatorsRun(t *testing.T) {
	calls := make(chan struct{}, 16)
	fn := func(ctx context.Context, _ dispatch.Params) ([]byte, dispatch.Result, error) {
		calls <- struct{}{}
		return []byte("ok"), dispatch.Result{ExitCode: 0}, nil
	}
	waitForDispatch := func() {
		t.Helper()
		select {
		case <-calls:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for node dispatch to start")
		}
	}

	workDir, doAs := newProductionEngineTestServer(t, fn)
	writeWorkflow(t, workDir, "my-flow", minimalYAML)

	alice := netid.Identity{OperatorID: "alice", Role: netid.RoleAdmin, Authenticated: true, Resolved: true}
	mallory := netid.Identity{OperatorID: "mallory", Role: netid.RoleAdmin, Authenticated: true, Resolved: true}

	// alice creates her own run — the attack's target.
	aliceResp := doAs(alice, http.MethodPost, "/flows/api/run?name=my-flow", `{}`)
	aliceBody := bodyStr(t, aliceResp)
	if aliceResp.StatusCode != http.StatusAccepted {
		t.Fatalf("alice's run status=%d; want 202; body=%s", aliceResp.StatusCode, aliceBody)
	}
	var aliceRun map[string]string
	if err := json.Unmarshal([]byte(aliceBody), &aliceRun); err != nil {
		t.Fatalf("unmarshal alice's run response: %v; body=%s", err, aliceBody)
	}
	aliceRunID := aliceRun["run_id"]
	waitForDispatch()
	// Snapshot only once alice's run has settled into its terminal state —
	// the fake fn succeeds synchronously, so this is immediate, but a plain
	// read right after waitForDispatch would race the run's own
	// status/started_at/ended_at finalization (unrelated to the attack).
	before := waitForRunStatus(t, workDir, aliceRunID, "completed")
	aliceRunJSONPath := filepath.Join(workDir, "workflows", "runs", aliceRunID, "run.json")

	// mallory creates her own run — a legitimate resume source she owns.
	malloryResp := doAs(mallory, http.MethodPost, "/flows/api/run?name=my-flow", `{}`)
	malloryBody := bodyStr(t, malloryResp)
	if malloryResp.StatusCode != http.StatusAccepted {
		t.Fatalf("mallory's run status=%d; want 202; body=%s", malloryResp.StatusCode, malloryBody)
	}
	var malloryRun map[string]string
	if err := json.Unmarshal([]byte(malloryBody), &malloryRun); err != nil {
		t.Fatalf("unmarshal mallory's run response: %v; body=%s", err, malloryBody)
	}
	malloryRunID := malloryRun["run_id"]
	waitForDispatch()
	// Wait for full settlement (not just dispatch-started), same reasoning
	// as the resume-attribution test above: guarantees run.json is fully
	// written before resuming it, and avoids racing this run's own
	// goroutine against t.TempDir()'s later cleanup.
	waitForRunStatus(t, workDir, malloryRunID, "completed")

	// K1 attack: mallory resumes her OWN run (so checkRunOwnership's gate on
	// the prior run passes) but asks the server to write the result into
	// alice's EXISTING run ID.
	attackBody := `{"run_id":"` + malloryRunID + `","new_run_id":"` + aliceRunID + `"}`
	attackResp := doAs(mallory, http.MethodPost, "/flows/api/resume", attackBody)
	attackRespBody := bodyStr(t, attackResp)
	if attackResp.StatusCode != http.StatusAccepted {
		t.Fatalf("resume status=%d; want 202; body=%s", attackResp.StatusCode, attackRespBody)
	}
	var resumeResult map[string]string
	if err := json.Unmarshal([]byte(attackRespBody), &resumeResult); err != nil {
		t.Fatalf("unmarshal resume response: %v; body=%s", err, attackRespBody)
	}
	if resumeResult["new_run_id"] == aliceRunID {
		t.Fatalf("SECURITY: server honored mallory's client-supplied new_run_id and used alice's run id %q as the resumed run's write target", aliceRunID)
	}
	// mallory's prior run's single node is already NodeCompleted by the time
	// Resume loads it (pinned-output resume), so no node dispatch happens for
	// the resumed run — wait on its own run.json reaching a terminal state
	// instead of the (never-firing) dispatch channel.
	waitForRunStatus(t, workDir, resumeResult["new_run_id"], "completed")

	// alice's run.json must be byte-for-byte untouched by the attack.
	after, err := os.ReadFile(aliceRunJSONPath)
	if err != nil {
		t.Fatalf("read alice's run.json after attack: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("SECURITY: alice's run.json changed as a result of mallory's resume attempt.\nbefore: %s\nafter:  %s", before, after)
	}

	// alice must still be able to read her own, untouched run.
	aliceReadResp := doAs(alice, http.MethodGet, "/flows/api/run?id="+aliceRunID, "")
	aliceReadBody := bodyStr(t, aliceReadResp)
	if aliceReadResp.StatusCode != http.StatusOK {
		t.Errorf("alice read her own run after the attack: status=%d; want 200; body=%s", aliceReadResp.StatusCode, aliceReadBody)
	}

	// mallory must still be refused reading alice's run directly.
	malloryReadResp := doAs(mallory, http.MethodGet, "/flows/api/run?id="+aliceRunID, "")
	malloryReadBody := bodyStr(t, malloryReadResp)
	if malloryReadResp.StatusCode != http.StatusForbidden {
		t.Errorf("mallory read alice's run: status=%d; want 403; body=%s", malloryReadResp.StatusCode, malloryReadBody)
	}
}

// TestFlows_GetRun_OwnerScoping proves R10 (round-1 security review): a
// run's owner is enforced on GET /flows/api/run. The creator (same resolved
// operator ID the run was recorded under) can read it; a different resolved
// operator cannot (403); a caller with no resolvable identity at all cannot
// either (403 — "unresolved identity fails closed"). Reverting
// checkRunOwnership's call in handleGetRun to a no-op makes the "other
// operator" and "unresolved" subtests fail (they'd observe 200 instead of
// 403), confirming this test exercises the fix and not a tautology.
func TestFlows_GetRun_OwnerScoping(t *testing.T) {
	_, workDir, doAs := newOwnerScopeTestServer(t)

	runID := "run-20260101-000000-aaaaaa"
	writeOwnedRun(t, workDir, runID, "alice")

	creator := netid.Identity{OperatorID: "alice", Role: netid.RoleAdmin, Authenticated: true, Resolved: true}
	other := netid.Identity{OperatorID: "mallory", Role: netid.RoleAdmin, Authenticated: true, Resolved: true}
	unresolved := netid.Identity{} // Resolved=false: resolver never ran / no identity at all.

	cases := []struct {
		name string
		id   netid.Identity
		want int
	}{
		{"creator reads own run", creator, http.StatusOK},
		{"different operator denied", other, http.StatusForbidden},
		{"unresolved identity denied", unresolved, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := doAs(tc.id, http.MethodGet, "/flows/api/run?id="+runID, "")
			body := bodyStr(t, resp)
			if resp.StatusCode != tc.want {
				t.Errorf("status=%d; want %d; body=%s", resp.StatusCode, tc.want, body)
			}
		})
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

	// Manually create a node stdout file, plus the run.json that production
	// always writes before any node output exists (engine.go persists
	// run.json, including its owner, before executeGraph starts). R10
	// (round-1 security review): handleGetNodeOutput now reads run.json to
	// scope the response to the run's owner, so a run.json-less node stdout
	// file no longer reflects a reachable production state.
	runID := "run-20240101-000000-aabbcc"
	runDir := filepath.Join(workDir, "workflows", "runs", runID)
	nodesDir := filepath.Join(runDir, "nodes")
	if err := os.MkdirAll(nodesDir, 0755); err != nil {
		t.Fatal(err)
	}
	runJSON := `{"run_id":"` + runID + `","workflow_name":"my-flow","workflow_hash":"x","status":"running","nodes":{}}`
	if err := os.WriteFile(filepath.Join(runDir, "run.json"), []byte(runJSON), 0644); err != nil {
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

// TestFlows_GetNodeOutput_OwnerScoping proves R10 (round-1 security review)
// for the node-output artifact read: same three-way split as
// TestFlows_GetRun_OwnerScoping (creator OK, different operator 403,
// unresolved identity 403). This is the finding's original headline repro
// ("operator B issues GET /flows/api/run/node?... and receives operator A's
// raw agent stdout").
func TestFlows_GetNodeOutput_OwnerScoping(t *testing.T) {
	_, workDir, doAs := newOwnerScopeTestServer(t)

	runID := "run-20260101-000000-bbbbbb"
	writeOwnedRun(t, workDir, runID, "alice")
	nodesDir := filepath.Join(workDir, "workflows", "runs", runID, "nodes")
	if err := os.MkdirAll(nodesDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nodesDir, "step1.stdout"), []byte("agent secret output\n"), 0644); err != nil {
		t.Fatal(err)
	}

	creator := netid.Identity{OperatorID: "alice", Role: netid.RoleAdmin, Authenticated: true, Resolved: true}
	other := netid.Identity{OperatorID: "mallory", Role: netid.RoleAdmin, Authenticated: true, Resolved: true}
	unresolved := netid.Identity{}

	cases := []struct {
		name string
		id   netid.Identity
		want int
	}{
		{"creator reads own node output", creator, http.StatusOK},
		{"different operator denied", other, http.StatusForbidden},
		{"unresolved identity denied", unresolved, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := doAs(tc.id, http.MethodGet, "/flows/api/run/node?id="+runID+"&node=step1", "")
			body := bodyStr(t, resp)
			if resp.StatusCode != tc.want {
				t.Errorf("status=%d; want %d; body=%s", resp.StatusCode, tc.want, body)
			}
			if tc.want == http.StatusOK && body != "agent secret output\n" {
				t.Errorf("body=%q; want the node's stdout", body)
			}
			if tc.want != http.StatusOK && strings.Contains(body, "agent secret output") {
				t.Errorf("leaked node output to a non-owner: body=%q", body)
			}
		})
	}
}

// TestFlows_Resume_OwnerScoping proves R10 (round-1 security review): the
// prior run resumed by POST /flows/api/resume is scoped to its recorded
// owner, so resuming (and thereby inheriting the pinned outputs of) another
// operator's run is denied the same as a direct read.
func TestFlows_Resume_OwnerScoping(t *testing.T) {
	_, workDir, doAs := newOwnerScopeTestServer(t)

	runID := "run-20260101-000000-cccccc"
	writeOwnedRun(t, workDir, runID, "alice")
	// Write the workflow definition too, so a request that gets past the
	// ownership gate proceeds all the way to the (nil, in this harness)
	// engine check — isolating the assertion to the ownership gate itself
	// rather than an incidental "workflow file missing" 404.
	writeWorkflow(t, workDir, "my-flow", minimalYAML)

	other := netid.Identity{OperatorID: "mallory", Role: netid.RoleAdmin, Authenticated: true, Resolved: true}

	resp := doAs(other, http.MethodPost, "/flows/api/resume", `{"run_id":"`+runID+`"}`)
	body := bodyStr(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status=%d; want 403 (resume of another operator's run); body=%s", resp.StatusCode, body)
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

// TestFlows_Resume_NewRunIDFieldIgnored proves K1's fix
// (k82-security-review-2026-09-23.md): the deprecated new_run_id field is
// decoded (so older frontend builds that still send it don't get a decode
// error) but never consulted — not even to validate it. A path-traversal-
// shaped new_run_id produces the exact same outcome as any other value or
// no value at all (404, prior run not found), because the field is never
// reached by workflow.ValidateID or any path construction; the resumed
// run's ID is always minted server-side. Before the fix this same payload
// would have been rejected with 400 from the traversal guard on
// new_run_id — a different, and misleading, signal that the field was
// still load-bearing.
func TestFlows_Resume_NewRunIDFieldIgnored(t *testing.T) {
	ts, tok, _ := newFlowsTestServer(t)
	body := `{"run_id":"run-20240101-000000-aabbcc","new_run_id":"../../../etc/passwd"}`
	resp := authedPost(t, ts.URL+"/flows/api/resume", tok, body)
	respBody := bodyStr(t, resp)

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status=%d; want 404 (prior run not found; new_run_id must never be validated or consulted); body=%s", resp.StatusCode, respBody)
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

	srv := consoleui.MustNew(t, consoleui.Config{
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

// ---- R10 (round-1 security review) owner-scoping regression tests ------------
//
// The suites above cover role gating and traversal; these cover the R10
// finding itself: attribution must come from the resolved server identity,
// never a client body field, and every read/cancel surface must scope to
// the recorded owner. Each subtest below is proven to fail against the
// pre-fix code (checkRunOwnership / resolveRunOperatorID reverted to
// no-ops): "different operator" and "unresolved identity" cases observe 200
// or 202 instead of 403, and the self-asserted-body-field case observes the
// impersonating identity succeeding at cancel.

// newOwnerScopeCancelServer builds a nodeRunFn-backed flows handler (same
// seam as newCancelTestServer) and returns a helper that issues a request
// with a given identity injected into its context — mirroring
// newOwnerScopeTestServer's doAs, but for the bare test-only mux (no token
// or content-type middleware, matching the existing cancel tests).
func newOwnerScopeCancelServer(t *testing.T) (workDir string, started <-chan struct{}, doAs func(id netid.Identity, method, path, body string) *http.Response) {
	t.Helper()
	wDir := t.TempDir()

	startedCh := make(chan struct{})
	unblockCh := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-unblockCh:
		default:
			close(unblockCh)
		}
	})
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

	doAs = func(id netid.Identity, method, path, reqBody string) *http.Response {
		t.Helper()
		ts := httptest.NewServer(injectIdentityMiddleware(id, handler))
		defer ts.Close()
		var bodyReader io.Reader
		if reqBody != "" {
			bodyReader = strings.NewReader(reqBody)
		}
		req, err := http.NewRequest(method, ts.URL+path, bodyReader)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		if reqBody != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		return resp
	}
	return wDir, startedCh, doAs
}

// TestFlows_Cancel_OwnerScoping proves R10 (round-1 security review) for
// cancellation, and specifically the landmine the round-2 implementation
// report identified: the run is started as "alice" (the resolved identity)
// while the request body ALSO carries a self-asserted operator_id of
// "mallory". A caller presenting "mallory" — matching that body token, not
// the real creator — must still be denied, proving the body field was never
// used to attribute the run. The real creator ("alice") can cancel; an
// unresolved identity cannot ("fails closed").
func TestFlows_Cancel_OwnerScoping(t *testing.T) {
	workDir, started, doAs := newOwnerScopeCancelServer(t)

	dir := filepath.Join(workDir, "workflows")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "my-flow.yaml"), []byte(minimalYAML), 0644); err != nil {
		t.Fatal(err)
	}

	creator := netid.Identity{OperatorID: "alice", Role: netid.RoleAdmin, Authenticated: true, Resolved: true}
	impersonator := netid.Identity{OperatorID: "mallory", Role: netid.RoleAdmin, Authenticated: true, Resolved: true}
	unresolved := netid.Identity{}

	runResp := doAs(creator, http.MethodPost, "/flows/api/run?name=my-flow", `{"operator_id":"mallory"}`)
	runBody := bodyStr(t, runResp)
	if runResp.StatusCode != http.StatusAccepted {
		t.Fatalf("run status=%d; want 202; body=%s", runResp.StatusCode, runBody)
	}
	var runResult map[string]string
	if err := json.Unmarshal([]byte(runBody), &runResult); err != nil {
		t.Fatalf("unmarshal run response: %v", err)
	}
	runID := runResult["run_id"]
	if runID == "" {
		t.Fatal("empty run_id in run response")
	}

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for run to start")
	}

	// "mallory" matches the self-asserted body token from run-start, but is
	// NOT the real resolved creator: must be denied.
	resp := doAs(impersonator, http.MethodPost, "/flows/api/cancel?id="+runID, "")
	body := bodyStr(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("self-asserted-body impersonation cancel: status=%d; want 403; body=%s", resp.StatusCode, body)
	}

	// No resolvable identity at all: denied too.
	resp2 := doAs(unresolved, http.MethodPost, "/flows/api/cancel?id="+runID, "")
	body2 := bodyStr(t, resp2)
	if resp2.StatusCode != http.StatusForbidden {
		t.Errorf("unresolved identity cancel: status=%d; want 403; body=%s", resp2.StatusCode, body2)
	}

	// The real creator can cancel.
	resp3 := doAs(creator, http.MethodPost, "/flows/api/cancel?id="+runID, "")
	body3 := bodyStr(t, resp3)
	if resp3.StatusCode != http.StatusAccepted {
		t.Errorf("creator cancel: status=%d; want 202; body=%s", resp3.StatusCode, body3)
	}
}

// TestResolveRunOperatorID unit-tests the resolution rule directly (R10,
// round-1 security review): authenticated identity and the stable loopback
// ID both win outright; an unresolved identity (the resolver never ran, or
// ran and stamped nothing) fails closed with an error rather than falling
// through to any caller-supplied value.
func TestResolveRunOperatorID(t *testing.T) {
	newReq := func(id netid.Identity) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/flows/api/run", nil)
		return r.WithContext(netid.WithIdentityForTest(r.Context(), id))
	}

	t.Run("authenticated identity wins", func(t *testing.T) {
		id := netid.Identity{OperatorID: "alice", Authenticated: true, Resolved: true}
		got, err := consoleui.ResolveRunOperatorIDForTest(newReq(id))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "alice" {
			t.Errorf("got %q; want alice", got)
		}
	})

	t.Run("loopback stable ID used when unauthenticated", func(t *testing.T) {
		id := netid.Identity{OperatorID: "lbop-tw", Authenticated: false, Resolved: true}
		got, err := consoleui.ResolveRunOperatorIDForTest(newReq(id))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "lbop-tw" {
			t.Errorf("got %q; want lbop-tw", got)
		}
	})

	t.Run("unresolved identity fails closed", func(t *testing.T) {
		id := netid.Identity{}
		got, err := consoleui.ResolveRunOperatorIDForTest(newReq(id))
		if err == nil {
			t.Errorf("want error for unresolved identity; got nil (resolved %q)", got)
		}
	})
}
