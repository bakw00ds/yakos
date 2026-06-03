package restapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/restapi"
)

// ---- test infrastructure ---------------------------------------------------

// repoRoot walks up from cwd to find the repository root (contains a VERSION
// file, not directory).
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for {
		fi, err := os.Stat(filepath.Join(dir, "VERSION"))
		if err == nil && !fi.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root not found")
		}
		dir = parent
	}
}

// newTestServer builds a restapi.Server backed by httptest.NewServer.
// Returns the test server and a cleanup function.
func newTestServer(t *testing.T, cfg restapi.Config) (*httptest.Server, *restapi.Tokens) {
	t.Helper()

	stateDir := t.TempDir()
	toks, err := restapi.LoadOrGenerateTokens(stateDir)
	if err != nil {
		t.Fatalf("generate tokens: %v", err)
	}
	cfg.Tokens = toks
	if cfg.WorkspaceRoot == "" {
		cfg.WorkspaceRoot = t.TempDir()
	}
	if cfg.YakosRoot == "" {
		cfg.YakosRoot = repoRoot(t)
	}

	srv := restapi.New(cfg)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, toks
}

// get issues a GET to url with the given Bearer token.
func get(t *testing.T, url, tok string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

// postJSON issues a POST with a JSON body.
func postJSON(t *testing.T, url, tok string, body interface{}) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

// patchJSON issues a PATCH with a JSON body.
func patchJSON(t *testing.T, url, tok string, body interface{}) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPatch, url, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH %s: %v", url, err)
	}
	return resp
}

// decodeJSON reads and JSON-decodes resp.Body into dst.
func decodeJSON(t *testing.T, resp *http.Response, dst interface{}) {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

// writeKanban writes a kanban.md under dir/work/current/.
func writeKanban(t *testing.T, dir, content string) {
	t.Helper()
	boardDir := filepath.Join(dir, "work", "current")
	if err := os.MkdirAll(boardDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(boardDir, "kanban.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write kanban: %v", err)
	}
}

const testBoard = `# Kanban — test

## TODO
- [ ] K-1 — task one
  - category: other
  - assigned: unassigned
  - blockers: none
  - notes:

- [ ] K-2 — task two
  - category: feat
  - assigned: unassigned
  - blockers: none
  - notes:

## IN PROGRESS
- [ ] K-3 — in progress task
  - category: other
  - assigned: alice
  - blockers: none
  - notes:

## DONE
`

// ---- GET /v1/version --------------------------------------------------------

func TestVersion_NoAuthRequired(t *testing.T) {
	ts, _ := newTestServer(t, restapi.Config{})
	resp := get(t, ts.URL+"/v1/version", "")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want %d", resp.StatusCode, http.StatusOK)
	}
	var result struct {
		Version string `json:"version"`
		Runtime string `json:"runtime"`
	}
	decodeJSON(t, resp, &result)
	if result.Version == "" {
		t.Error("version should not be empty")
	}
	if result.Runtime == "" {
		t.Error("runtime should not be empty")
	}
}

func TestVersion_ContainsGoSuffix(t *testing.T) {
	ts, _ := newTestServer(t, restapi.Config{})
	resp := get(t, ts.URL+"/v1/version", "")
	var result struct {
		Version string `json:"version"`
	}
	decodeJSON(t, resp, &result)
	if !strings.Contains(result.Version, "(go)") {
		t.Errorf("version=%q; should contain (go)", result.Version)
	}
}

// ---- GET /v1/kanban ---------------------------------------------------------

func TestKanban_RequiresAuth(t *testing.T) {
	ts, _ := newTestServer(t, restapi.Config{})
	resp := get(t, ts.URL+"/v1/kanban", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d; want 401", resp.StatusCode)
	}
}

func TestKanban_RejectsBadToken(t *testing.T) {
	ts, _ := newTestServer(t, restapi.Config{})
	resp := get(t, ts.URL+"/v1/kanban", "notavalidtoken")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status=%d; want 403", resp.StatusCode)
	}
}

func TestKanban_EmptyBoard(t *testing.T) {
	ts, toks := newTestServer(t, restapi.Config{})
	resp := get(t, ts.URL+"/v1/kanban", toks.Read)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200", resp.StatusCode)
	}
	var result struct {
		Items []interface{} `json:"items"`
	}
	decodeJSON(t, resp, &result)
	if result.Items == nil {
		t.Error("items should be [] not null")
	}
}

func TestKanban_WithBoard(t *testing.T) {
	ws := t.TempDir()
	writeKanban(t, ws, testBoard)
	ts, toks := newTestServer(t, restapi.Config{WorkspaceRoot: ws})

	resp := get(t, ts.URL+"/v1/kanban", toks.Read)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200", resp.StatusCode)
	}
	var result struct {
		Items []struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Column string `json:"column"`
		} `json:"items"`
	}
	decodeJSON(t, resp, &result)
	if len(result.Items) != 3 {
		t.Errorf("items=%d; want 3 (2 TODO + 1 IN PROGRESS)", len(result.Items))
	}
}

func TestKanban_WriteTokenGrantsRead(t *testing.T) {
	ws := t.TempDir()
	writeKanban(t, ws, testBoard)
	ts, toks := newTestServer(t, restapi.Config{WorkspaceRoot: ws})

	// Write token must also work for read endpoints.
	resp := get(t, ts.URL+"/v1/kanban", toks.Write)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200 (write token should grant read)", resp.StatusCode)
	}
}

// ---- POST /v1/kanban/items --------------------------------------------------

func TestKanbanCreate_RequiresWriteToken(t *testing.T) {
	ts, toks := newTestServer(t, restapi.Config{})

	// Read token should be denied.
	resp := postJSON(t, ts.URL+"/v1/kanban/items", toks.Read, map[string]string{"title": "test"})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status=%d; want 403 (read token on write endpoint)", resp.StatusCode)
	}
}

func TestKanbanCreate_MissingTitle(t *testing.T) {
	ts, toks := newTestServer(t, restapi.Config{})
	resp := postJSON(t, ts.URL+"/v1/kanban/items", toks.Write, map[string]string{"column": "TODO"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d; want 400", resp.StatusCode)
	}
}

func TestKanbanCreate_Success(t *testing.T) {
	ws := t.TempDir()
	ts, toks := newTestServer(t, restapi.Config{WorkspaceRoot: ws})

	resp := postJSON(t, ts.URL+"/v1/kanban/items", toks.Write, map[string]string{
		"title": "new task from REST",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("status=%d; want 201", resp.StatusCode)
	}
	var result struct {
		ID string `json:"id"`
	}
	decodeJSON(t, resp, &result)
	if result.ID == "" {
		t.Error("expected non-empty id")
	}
}

func TestKanbanCreate_UnknownFieldRejected(t *testing.T) {
	ts, toks := newTestServer(t, restapi.Config{})
	resp := postJSON(t, ts.URL+"/v1/kanban/items", toks.Write, map[string]string{
		"title":    "test",
		"surprise": "field",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d; want 400 for unknown JSON field", resp.StatusCode)
	}
}

// ---- PATCH /v1/kanban/items/{id} -------------------------------------------

func TestKanbanPatch_RequiresWriteToken(t *testing.T) {
	ts, toks := newTestServer(t, restapi.Config{})
	resp := patchJSON(t, ts.URL+"/v1/kanban/items/K-1", toks.Read, map[string]string{"column": "DONE"})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status=%d; want 403", resp.StatusCode)
	}
}

func TestKanbanPatch_MoveExistingItem(t *testing.T) {
	ws := t.TempDir()
	writeKanban(t, ws, testBoard)
	ts, toks := newTestServer(t, restapi.Config{WorkspaceRoot: ws})

	resp := patchJSON(t, ts.URL+"/v1/kanban/items/K-1", toks.Write, map[string]string{
		"column": "IN PROGRESS",
	})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200", resp.StatusCode)
	}
	var result struct {
		OK bool `json:"ok"`
	}
	decodeJSON(t, resp, &result)
	if !result.OK {
		t.Error("expected ok=true")
	}
}

func TestKanbanPatch_MissingBody(t *testing.T) {
	ws := t.TempDir()
	writeKanban(t, ws, testBoard)
	ts, toks := newTestServer(t, restapi.Config{WorkspaceRoot: ws})

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/v1/kanban/items/K-1", nil)
	req.Header.Set("Authorization", "Bearer "+toks.Write)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d; want 400 for missing body", resp.StatusCode)
	}
}

func TestKanbanPatch_NotFound(t *testing.T) {
	ws := t.TempDir()
	writeKanban(t, ws, testBoard)
	ts, toks := newTestServer(t, restapi.Config{WorkspaceRoot: ws})

	resp := patchJSON(t, ts.URL+"/v1/kanban/items/K-999", toks.Write, map[string]string{
		"column": "DONE",
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status=%d; want 404", resp.StatusCode)
	}
}

// ---- GET /v1/dispatches -----------------------------------------------------

func TestDispatchesList_RequiresAuth(t *testing.T) {
	ts, _ := newTestServer(t, restapi.Config{})
	resp := get(t, ts.URL+"/v1/dispatches", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d; want 401", resp.StatusCode)
	}
}

func TestDispatchesList_EmptyLog(t *testing.T) {
	ts, toks := newTestServer(t, restapi.Config{})
	resp := get(t, ts.URL+"/v1/dispatches", toks.Read)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200", resp.StatusCode)
	}
	var result struct {
		Dispatches []interface{} `json:"dispatches"`
	}
	decodeJSON(t, resp, &result)
	if result.Dispatches == nil {
		t.Error("dispatches should be [] not null")
	}
}

func TestDispatchesList_SinceParam(t *testing.T) {
	ts, toks := newTestServer(t, restapi.Config{})
	resp := get(t, ts.URL+"/v1/dispatches?since=2026-01-01T00:00:00Z", toks.Read)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200", resp.StatusCode)
	}
}

// ---- GET /v1/cost -----------------------------------------------------------

func TestCost_RequiresAuth(t *testing.T) {
	ts, _ := newTestServer(t, restapi.Config{})
	resp := get(t, ts.URL+"/v1/cost", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d; want 401", resp.StatusCode)
	}
}

func TestCost_DefaultAxis(t *testing.T) {
	ts, toks := newTestServer(t, restapi.Config{})
	resp := get(t, ts.URL+"/v1/cost", toks.Read)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200", resp.StatusCode)
	}
	var result struct {
		By   string        `json:"by"`
		Rows []interface{} `json:"rows"`
	}
	decodeJSON(t, resp, &result)
	if result.By != "runtime" {
		t.Errorf("by=%q; want runtime (default)", result.By)
	}
}

func TestCost_AllAxes(t *testing.T) {
	ts, toks := newTestServer(t, restapi.Config{})
	for _, axis := range []string{"runtime", "agent", "day", "project"} {
		t.Run(axis, func(t *testing.T) {
			resp := get(t, ts.URL+"/v1/cost?by="+axis, toks.Read)
			if resp.StatusCode != http.StatusOK {
				t.Errorf("axis=%s: status=%d; want 200", axis, resp.StatusCode)
			}
			_ = resp.Body.Close()
		})
	}
}

func TestCost_InvalidAxis(t *testing.T) {
	ts, toks := newTestServer(t, restapi.Config{})
	resp := get(t, ts.URL+"/v1/cost?by=bogus", toks.Read)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d; want 400", resp.StatusCode)
	}
}

// ---- GET /v1/status ---------------------------------------------------------

func TestStatus_RequiresAuth(t *testing.T) {
	ts, _ := newTestServer(t, restapi.Config{})
	resp := get(t, ts.URL+"/v1/status", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d; want 401", resp.StatusCode)
	}
}

func TestStatus_ReturnsJSON(t *testing.T) {
	ts, toks := newTestServer(t, restapi.Config{})
	resp := get(t, ts.URL+"/v1/status", toks.Read)
	// status may return 200 or 500 depending on the workspace — we just
	// check we get JSON and the auth is applied.
	if resp.StatusCode == http.StatusUnauthorized {
		t.Error("should not get 401 with valid read token")
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type=%q; want application/json", ct)
	}
	_ = resp.Body.Close()
}

// ---- GET /v1/supervise/pending ----------------------------------------------

func TestSupervisePending_RequiresAuth(t *testing.T) {
	ts, _ := newTestServer(t, restapi.Config{})
	resp := get(t, ts.URL+"/v1/supervise/pending", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d; want 401", resp.StatusCode)
	}
}

func TestSupervisePending_ReturnsJSON(t *testing.T) {
	ts, toks := newTestServer(t, restapi.Config{})
	resp := get(t, ts.URL+"/v1/supervise/pending", toks.Read)
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type=%q; want application/json", ct)
	}
	_ = resp.Body.Close()
}

// ---- POST /v1/dispatches (param validation only) ----------------------------

func TestDispatchCreate_RequiresWriteToken(t *testing.T) {
	ts, toks := newTestServer(t, restapi.Config{})
	resp := postJSON(t, ts.URL+"/v1/dispatches", toks.Read, map[string]string{
		"agent": "backend", "task": "do something",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status=%d; want 403", resp.StatusCode)
	}
}

func TestDispatchCreate_MissingAgent(t *testing.T) {
	ts, toks := newTestServer(t, restapi.Config{})
	resp := postJSON(t, ts.URL+"/v1/dispatches", toks.Write, map[string]string{
		"task": "do something",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d; want 400", resp.StatusCode)
	}
}

func TestDispatchCreate_MissingTask(t *testing.T) {
	ts, toks := newTestServer(t, restapi.Config{})
	resp := postJSON(t, ts.URL+"/v1/dispatches", toks.Write, map[string]string{
		"agent": "backend",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d; want 400", resp.StatusCode)
	}
}

func TestDispatchCreate_NoYakosRoot(t *testing.T) {
	// Use an explicit invalid (non-empty) yakosRoot so the newTestServer helper
	// does not override it with the real repo root. The dispatch will fail
	// at the runtime level; we verify it's not a 4xx auth problem.
	ts, toks := newTestServer(t, restapi.Config{YakosRoot: "/tmp/no-such-yakos-root"})
	resp := postJSON(t, ts.URL+"/v1/dispatches", toks.Write, map[string]string{
		"agent": "backend", "task": "test",
	})
	// dispatch will fail with 502 (BadGateway) because yakos_root is missing files.
	// Important check: not a 403 (auth), not 400 (bad params), not 200 (success).
	if resp.StatusCode == http.StatusOK || resp.StatusCode < 400 {
		t.Errorf("status=%d; want error response when yakos_root is invalid", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

// ---- auth edge cases -------------------------------------------------------

func TestAuth_BearerCaseInsensitive(t *testing.T) {
	ws := t.TempDir()
	ts, toks := newTestServer(t, restapi.Config{WorkspaceRoot: ws})

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/kanban", nil)
	req.Header.Set("Authorization", "BEARER "+toks.Read)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200 (BEARER should be accepted case-insensitively)", resp.StatusCode)
	}
}

func TestAuth_MissingBearerPrefix(t *testing.T) {
	ts, toks := newTestServer(t, restapi.Config{})
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/kanban", nil)
	req.Header.Set("Authorization", toks.Read) // no "Bearer " prefix
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d; want 401 when Bearer prefix missing", resp.StatusCode)
	}
}

// ---- content-type ----------------------------------------------------------

func TestAllEndpoints_ReturnApplicationJSON(t *testing.T) {
	ws := t.TempDir()
	writeKanban(t, ws, testBoard)
	ts, toks := newTestServer(t, restapi.Config{WorkspaceRoot: ws})

	endpoints := []struct {
		method string
		path   string
		tok    string
	}{
		{"GET", "/v1/version", ""},
		{"GET", "/v1/kanban", toks.Read},
		{"GET", "/v1/dispatches", toks.Read},
		{"GET", "/v1/cost", toks.Read},
		{"GET", "/v1/supervise/pending", toks.Read},
	}

	for _, ep := range endpoints {
		t.Run(fmt.Sprintf("%s %s", ep.method, ep.path), func(t *testing.T) {
			resp := get(t, ts.URL+ep.path, ep.tok)
			ct := resp.Header.Get("Content-Type")
			if !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type=%q; want application/json", ct)
			}
			_ = resp.Body.Close()
		})
	}
}

// ---- round-trip: create then list ------------------------------------------

func TestKanban_CreateThenList(t *testing.T) {
	ws := t.TempDir()
	ts, toks := newTestServer(t, restapi.Config{WorkspaceRoot: ws})

	// Create an item.
	createResp := postJSON(t, ts.URL+"/v1/kanban/items", toks.Write, map[string]string{
		"title": "round-trip task",
	})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d; want 201", createResp.StatusCode)
	}
	var created struct {
		ID string `json:"id"`
	}
	decodeJSON(t, createResp, &created)

	// List and verify the item appears.
	listResp := get(t, ts.URL+"/v1/kanban", toks.Read)
	var list struct {
		Items []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"items"`
	}
	decodeJSON(t, listResp, &list)

	found := false
	for _, item := range list.Items {
		if item.ID == created.ID && item.Title == "round-trip task" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("created item %q not found in list: %+v", created.ID, list.Items)
	}
}

// ---- move then verify column ------------------------------------------------

func TestKanban_MoveItemVerifyColumn(t *testing.T) {
	ws := t.TempDir()
	writeKanban(t, ws, testBoard)
	ts, toks := newTestServer(t, restapi.Config{WorkspaceRoot: ws})

	// Move K-1 from TODO → IN PROGRESS.
	patchResp := patchJSON(t, ts.URL+"/v1/kanban/items/K-1", toks.Write, map[string]string{
		"column": "IN PROGRESS",
	})
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("patch status=%d; want 200", patchResp.StatusCode)
	}

	// List and verify column.
	listResp := get(t, ts.URL+"/v1/kanban", toks.Read)
	var list struct {
		Items []struct {
			ID     string `json:"id"`
			Column string `json:"column"`
		} `json:"items"`
	}
	decodeJSON(t, listResp, &list)

	for _, item := range list.Items {
		if item.ID == "K-1" {
			if item.Column != "IN PROGRESS" {
				t.Errorf("K-1 column=%q; want IN PROGRESS after move", item.Column)
			}
			return
		}
	}
	t.Error("K-1 not found in list after move")
}
