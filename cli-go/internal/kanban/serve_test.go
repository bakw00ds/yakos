// serve_test.go — unit tests for `yakos kanban serve` HTTP server (rank 41).
//
// All tests use net/http/httptest.NewServer with an injected net.Listener so
// no OS port binding is required for the happy-path handlers; mutation handlers
// use a real temp-dir board file to exercise the Save/Load cycle.
//
// Scenarios:
//
//	(a)  GET /                  → 200, Content-Type text/html
//	(b)  GET /api/board         → 200, JSON with TODO/IN PROGRESS/DONE keys
//	(c)  GET /api/meta          → 200, JSON with project/file/columns/categories
//	(d)  POST /api/add          → 200, task appears in returned board JSON
//	(e)  POST /api/move         → 200, task moved to target column
//	(f)  POST /api/notes        → 200, notes updated in returned board JSON
//	(g)  POST /api/delete       → 200, task absent from returned board JSON
//	(h)  Host header block      → 403 for forbidden host
//	(i)  POST body > maxBodyBytes → 413
//	(j)  Invalid JSON body      → 400
//	(k)  Bad task ID            → 400
//	(l)  Bad column name        → 400
//	(m)  Title too long         → 400
//	(n)  Backslash in title     → 400
//	(o)  Notes too long         → 400
//	(p)  Unknown route          → 404
//	(q)  GET on POST-only route → 405
//	(r)  boardToAPIMap shape    → typed field extraction verified
//	(s)  parseTaskIDTitle       → id/title splitting
//	(t)  boardCategories        → default + extras dedup
//	(u)  isLoopback             → address classification
package kanban

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- test harness helpers ----------------------------------------------------

// newTestServer starts a real httptest.Server backed by a Serve listener.
// It uses an injected net.Listener so no background goroutine management is
// needed — httptest.Server handles lifecycle, including closing the listener
// on ts.Close().
//
// boardPath: path to kanban.md (or "" for a temp-dir board).
// Returns the server (call ts.Close() in defer) and the base URL.
func newTestServer(t *testing.T, boardPath, project string) (*httptest.Server, string) {
	t.Helper()
	if boardPath == "" {
		dir := t.TempDir()
		boardPath = filepath.Join(dir, "kanban.md")
	}
	if project == "" {
		project = "test-project"
	}

	// Use httptest to get a listener on an OS-chosen port.
	ts := httptest.NewUnstartedServer(nil)

	cfg := ServeConfig{
		BoardPath: boardPath,
		Project:   project,
		Host:      "127.0.0.1",
		Listener:  ts.Listener,
		ErrWriter: io.Discard,
	}

	// Build the handler without actually calling httpSrv.Serve.
	// We wire the mux directly and let httptest manage the serve loop.
	host := "127.0.0.1"
	allowed := map[string]struct{}{
		"127.0.0.1": {},
		"localhost":  {},
		"[::1]":      {},
		"::1":        {},
	}
	allowed[host] = struct{}{}

	// Ensure board dir and seed.
	_ = os.MkdirAll(pathDir(cfg.BoardPath), 0755)
	if _, err := os.Stat(cfg.BoardPath); os.IsNotExist(err) {
		b := Seed()
		_ = b.Save(cfg.BoardPath)
	}

	srv := &server{
		boardPath:    cfg.BoardPath,
		project:      project,
		host:         host,
		allowedHosts: allowed,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.handleRoot)
	mux.HandleFunc("/api/board", srv.handleBoard)
	mux.HandleFunc("/api/meta", srv.handleMeta)
	mux.HandleFunc("/api/add", srv.handleAdd)
	mux.HandleFunc("/api/move", srv.handleMove)
	mux.HandleFunc("/api/notes", srv.handleNotes)
	mux.HandleFunc("/api/delete", srv.handleDelete)
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	ts.Config.Handler = mux
	ts.Start()
	t.Cleanup(ts.Close)

	return ts, ts.URL
}

// postJSON sends a POST request with a JSON body and returns the *http.Response.
func postJSON(t *testing.T, baseURL, path string, body interface{}) *http.Response {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(baseURL+path, "application/json", bytes.NewReader(data)) //nolint:noctx
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

// decodeJSON reads and decodes the response body into dst; closes the body.
func decodeJSON(t *testing.T, resp *http.Response, dst interface{}) {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		t.Fatalf("unmarshal %q: %v", string(raw), err)
	}
}

// bodyString reads the response body as a string; closes the body.
func bodyString(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(raw)
}

// seedBoardWithTask writes a minimal board with one TODO task to path.
func seedBoardWithTask(t *testing.T, path, taskID, title string) {
	t.Helper()
	content := fmt.Sprintf(
		"# Kanban %s test\n\n## TODO\n- [ ] %s %s %s\n  - category: bug\n  - assigned: unassigned\n  - blockers: none\n  - notes: \n\n## IN PROGRESS\n\n## DONE\n",
		emDash, taskID, emDash, title,
	)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write board: %v", err)
	}
}

// ---- scenario (a): GET / returns HTML ----------------------------------------

func TestServe_GetRoot_ReturnsHTML(t *testing.T) {
	_, baseURL := newTestServer(t, "", "")

	resp, err := http.Get(baseURL + "/") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("want Content-Type text/html, got %q", ct)
	}
	body := bodyString(t, resp)
	if !strings.Contains(body, "<!DOCTYPE html>") && !strings.Contains(body, "<html") {
		t.Error("expected HTML body from GET /")
	}
}

// ---- scenario (b): GET /api/board returns JSON with column keys ---------------

func TestServe_GetBoard_ReturnsColumns(t *testing.T) {
	_, baseURL := newTestServer(t, "", "")

	resp, err := http.Get(baseURL + "/api/board") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /api/board: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}

	var board map[string]interface{}
	decodeJSON(t, resp, &board)

	for _, col := range []string{ColTODO, ColInProgress, ColDone} {
		if _, ok := board[col]; !ok {
			t.Errorf("board JSON missing column %q", col)
		}
	}
}

// ---- scenario (c): GET /api/meta returns metadata ----------------------------

func TestServe_GetMeta_ReturnsMetadata(t *testing.T) {
	dir := t.TempDir()
	boardPath := filepath.Join(dir, "kanban.md")
	_, baseURL := newTestServer(t, boardPath, "my-project")

	resp, err := http.Get(baseURL + "/api/meta") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /api/meta: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}

	var meta map[string]interface{}
	decodeJSON(t, resp, &meta)

	if meta["project"] != "my-project" {
		t.Errorf("want project=my-project, got %v", meta["project"])
	}
	if meta["file"] != boardPath {
		t.Errorf("want file=%s, got %v", boardPath, meta["file"])
	}
	cols, _ := meta["columns"].([]interface{})
	if len(cols) != 3 {
		t.Errorf("want 3 columns, got %d", len(cols))
	}
	cats, _ := meta["categories"].([]interface{})
	if len(cats) == 0 {
		t.Error("want categories list, got empty")
	}
}

// ---- scenario (d): POST /api/add creates task --------------------------------

func TestServe_PostAdd_CreatesTask(t *testing.T) {
	_, baseURL := newTestServer(t, "", "")

	resp := postJSON(t, baseURL, "/api/add", map[string]interface{}{
		"title":    "fix the thing",
		"category": "bug",
	})
	if resp.StatusCode != http.StatusOK {
		body := bodyString(t, resp)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}

	var board map[string][]interface{}
	decodeJSON(t, resp, &board)

	todos := board[ColTODO]
	if len(todos) == 0 {
		t.Fatal("expected task in TODO after /api/add")
	}
	task, _ := todos[0].(map[string]interface{})
	if task["title"] != "fix the thing" {
		t.Errorf("want title='fix the thing', got %v", task["title"])
	}
	if task["category"] != "bug" {
		t.Errorf("want category='bug', got %v", task["category"])
	}
}

// ---- scenario (e): POST /api/move moves task to column -----------------------

func TestServe_PostMove_MovesTask(t *testing.T) {
	dir := t.TempDir()
	boardPath := filepath.Join(dir, "kanban.md")
	seedBoardWithTask(t, boardPath, "K-1", "move me")
	_, baseURL := newTestServer(t, boardPath, "")

	resp := postJSON(t, baseURL, "/api/move", map[string]interface{}{
		"id":  "K-1",
		"col": "IN PROGRESS",
	})
	if resp.StatusCode != http.StatusOK {
		body := bodyString(t, resp)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}

	var board map[string][]interface{}
	decodeJSON(t, resp, &board)

	if len(board[ColTODO]) != 0 {
		t.Error("task should no longer be in TODO after move")
	}
	if len(board[ColInProgress]) != 1 {
		t.Errorf("expected 1 task in IN PROGRESS, got %d", len(board[ColInProgress]))
	}
}

// ---- scenario (f): POST /api/notes updates notes ----------------------------

func TestServe_PostNotes_UpdatesNotes(t *testing.T) {
	dir := t.TempDir()
	boardPath := filepath.Join(dir, "kanban.md")
	seedBoardWithTask(t, boardPath, "K-1", "notes task")
	_, baseURL := newTestServer(t, boardPath, "")

	resp := postJSON(t, baseURL, "/api/notes", map[string]interface{}{
		"id":    "K-1",
		"notes": "updated note text",
	})
	if resp.StatusCode != http.StatusOK {
		body := bodyString(t, resp)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}

	var board map[string][]interface{}
	decodeJSON(t, resp, &board)

	todos := board[ColTODO]
	if len(todos) == 0 {
		t.Fatal("task should still be in TODO")
	}
	task, _ := todos[0].(map[string]interface{})
	if task["notes"] != "updated note text" {
		t.Errorf("want notes='updated note text', got %v", task["notes"])
	}
}

// ---- scenario (g): POST /api/delete removes task ----------------------------

func TestServe_PostDelete_RemovesTask(t *testing.T) {
	dir := t.TempDir()
	boardPath := filepath.Join(dir, "kanban.md")
	seedBoardWithTask(t, boardPath, "K-1", "delete me")
	_, baseURL := newTestServer(t, boardPath, "")

	resp := postJSON(t, baseURL, "/api/delete", map[string]interface{}{
		"id": "K-1",
	})
	if resp.StatusCode != http.StatusOK {
		body := bodyString(t, resp)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}

	var board map[string][]interface{}
	decodeJSON(t, resp, &board)

	if len(board[ColTODO]) != 0 {
		t.Errorf("expected empty TODO after delete, got %d items", len(board[ColTODO]))
	}
}

// ---- scenario (h): Host header block → 403 ----------------------------------

func TestServe_ForbiddenHost_Returns403(t *testing.T) {
	_, baseURL := newTestServer(t, "", "")

	// Send request with a forged Host header.
	req, err := http.NewRequest(http.MethodGet, baseURL+"/api/board", nil) //nolint:noctx
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Host = "evil.example.com"

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("want 403, got %d", resp.StatusCode)
	}
}

// ---- scenario (i): POST body > maxBodyBytes → 413 ---------------------------

func TestServe_LargeBody_Returns413(t *testing.T) {
	_, baseURL := newTestServer(t, "", "")

	// Build a body larger than maxBodyBytes (256 KiB).
	huge := bytes.Repeat([]byte("x"), maxBodyBytes+1)
	resp, err := http.Post(baseURL+"/api/add", "application/json", bytes.NewReader(huge)) //nolint:noctx
	if err != nil {
		t.Fatalf("POST /api/add: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Content-Length is set (larger than cap), so we expect 413.
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("want 413, got %d", resp.StatusCode)
	}
}

// ---- scenario (j): Invalid JSON body → 400 ----------------------------------

func TestServe_InvalidJSONBody_Returns400(t *testing.T) {
	_, baseURL := newTestServer(t, "", "")

	resp, err := http.Post(baseURL+"/api/add", "application/json", strings.NewReader("{not json}")) //nolint:noctx
	if err != nil {
		t.Fatalf("POST /api/add: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
}

// ---- scenario (k): Bad task ID → 400 ----------------------------------------

func TestServe_BadTaskID_Returns400(t *testing.T) {
	_, baseURL := newTestServer(t, "", "")

	for _, path := range []string{"/api/move", "/api/notes", "/api/delete"} {
		resp := postJSON(t, baseURL, path, map[string]interface{}{
			"id":  "NOT-VALID",
			"col": "DONE",
		})
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("POST %s with bad id: want 400, got %d", path, resp.StatusCode)
		}
	}
}

// ---- scenario (l): Bad column name → 400 ------------------------------------

func TestServe_BadColumnName_Returns400(t *testing.T) {
	dir := t.TempDir()
	boardPath := filepath.Join(dir, "kanban.md")
	seedBoardWithTask(t, boardPath, "K-1", "some task")
	_, baseURL := newTestServer(t, boardPath, "")

	resp := postJSON(t, baseURL, "/api/move", map[string]interface{}{
		"id":  "K-1",
		"col": "INVALID_COLUMN",
	})
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
}

// ---- scenario (m): Title too long → 400 -------------------------------------

func TestServe_TitleTooLong_Returns400(t *testing.T) {
	_, baseURL := newTestServer(t, "", "")

	longTitle := strings.Repeat("a", maxTitleLen+1)
	resp := postJSON(t, baseURL, "/api/add", map[string]interface{}{
		"title": longTitle,
	})
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
}

// ---- scenario (n): Backslash in title → 400 ---------------------------------

func TestServe_BackslashInTitle_Returns400(t *testing.T) {
	_, baseURL := newTestServer(t, "", "")

	resp := postJSON(t, baseURL, "/api/add", map[string]interface{}{
		"title": `path\to\thing`,
	})
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
}

// ---- scenario (o): Notes too long → 400 -------------------------------------

func TestServe_NotesTooLong_Returns400(t *testing.T) {
	dir := t.TempDir()
	boardPath := filepath.Join(dir, "kanban.md")
	seedBoardWithTask(t, boardPath, "K-1", "task")
	_, baseURL := newTestServer(t, boardPath, "")

	longNotes := strings.Repeat("n", maxNotesLen+1)
	resp := postJSON(t, baseURL, "/api/notes", map[string]interface{}{
		"id":    "K-1",
		"notes": longNotes,
	})
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
}

// ---- scenario (p): Unknown route → 404 --------------------------------------

func TestServe_UnknownRoute_Returns404(t *testing.T) {
	_, baseURL := newTestServer(t, "", "")

	resp, err := http.Get(baseURL + "/api/does-not-exist") //nolint:noctx
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
}

// ---- scenario (q): GET on POST-only route → 405 -----------------------------

func TestServe_GetOnPostRoute_Returns405(t *testing.T) {
	_, baseURL := newTestServer(t, "", "")

	for _, path := range []string{"/api/add", "/api/move", "/api/notes", "/api/delete"} {
		resp, err := http.Get(baseURL + path) //nolint:noctx
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("GET %s: want 405, got %d", path, resp.StatusCode)
		}
	}
}

// ---- scenario (r): boardToAPIMap shape --------------------------------------

func TestBoardToAPIMap_Shape(t *testing.T) {
	b := SeedWithTitle("test")
	b.Add("my task", "feature", "some notes")

	m := boardToAPIMap(b)

	// boardToAPIMap stores internal struct values; round-trip through JSON to
	// get the wire-format map[string]interface{} shape (same as HTTP clients see).
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire map[string][]map[string]interface{}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal wire: %v", err)
	}

	todos := wire[ColTODO]
	if len(todos) != 1 {
		t.Fatalf("expected 1 TODO task, got %d", len(todos))
	}
	task := todos[0]
	for _, field := range []string{"id", "title", "done", "category", "assigned", "blockers", "notes"} {
		if _, ok := task[field]; !ok {
			t.Errorf("task missing field %q", field)
		}
	}
	if task["title"] != "my task" {
		t.Errorf("want title='my task', got %v", task["title"])
	}
	if task["category"] != "feature" {
		t.Errorf("want category='feature', got %v", task["category"])
	}
	if done, _ := task["done"].(bool); done {
		t.Errorf("want done=false, got %v", task["done"])
	}
	id, _ := task["id"].(string)
	if !taskIDPattern.MatchString(id) {
		t.Errorf("id %q does not match K-N pattern", id)
	}
}

// ---- scenario (s): parseTaskIDTitle splits correctly -------------------------

func TestParseTaskIDTitle(t *testing.T) {
	tests := []struct {
		input   string
		wantID  string
		wantTtl string
	}{
		{"K-1 " + emDash + " fix login bug", "K-1", "fix login bug"},
		{"K-42 " + emDash + " some task", "K-42", "some task"},
		{"K-1 - short", "K-1", "short"},
		{"no id here", "", "no id here"},
		{"K-1", "K-1", ""},
	}
	for _, tc := range tests {
		id, title := parseTaskIDTitle(tc.input)
		if id != tc.wantID {
			t.Errorf("parseTaskIDTitle(%q) id=%q, want %q", tc.input, id, tc.wantID)
		}
		if title != tc.wantTtl {
			t.Errorf("parseTaskIDTitle(%q) title=%q, want %q", tc.input, title, tc.wantTtl)
		}
	}
}

// ---- scenario (t): boardCategories dedup ------------------------------------

func TestBoardCategories_DefaultAndExtras(t *testing.T) {
	b := SeedWithTitle("test")
	b.Add("task 1", "bug", "")
	b.Add("task 2", "custom-cat", "")

	// boardCategories operates on the wire-format map (map[string]interface{} where
	// each task is a map[string]interface{}). Round-trip through JSON to get that shape.
	raw, err := json.Marshal(boardToAPIMap(b))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire map[string]interface{}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal wire: %v", err)
	}

	cats := boardCategories(wire)

	seen := map[string]int{}
	for _, c := range cats {
		seen[c]++
	}
	// No duplicates.
	for c, n := range seen {
		if n > 1 {
			t.Errorf("category %q appears %d times (want 1)", c, n)
		}
	}
	// Default categories present.
	for _, dc := range defaultCategories {
		if seen[dc] == 0 {
			t.Errorf("default category %q missing from boardCategories output", dc)
		}
	}
	// Custom category present.
	if seen["custom-cat"] == 0 {
		t.Error("extra category 'custom-cat' not included in boardCategories output")
	}
}

// ---- scenario (u): isLoopback classifies correctly --------------------------

func TestIsLoopback(t *testing.T) {
	loopbacks := []string{"127.0.0.1", "localhost", "::1", "[::1]", "127.0.0.2", "127.255.255.255"}
	for _, h := range loopbacks {
		if !isLoopback(h) {
			t.Errorf("isLoopback(%q) = false, want true", h)
		}
	}
	nonLoopbacks := []string{"0.0.0.0", "192.168.1.1", "10.0.0.1", "example.com", ""}
	for _, h := range nonLoopbacks {
		if isLoopback(h) {
			t.Errorf("isLoopback(%q) = true, want false", h)
		}
	}
}

// ---- Serve with injected listener (smoke-test) ------------------------------

func TestServe_InjectedListener(t *testing.T) {
	dir := t.TempDir()
	boardPath := filepath.Join(dir, "kanban.md")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	var errW bytes.Buffer
	cfg := ServeConfig{
		BoardPath:   boardPath,
		Project:     "smoke",
		Host:        "127.0.0.1",
		Listener:    ln,
		OpenBrowser: false,
		ErrWriter:   &errW,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		// httpSrv.Serve blocks until the listener is closed; we close it from
		// outside via ln.Close(), which causes Serve to return ErrServerClosed.
		_, _ = Serve(cfg)
	}()

	// Give the server a moment to bind.
	url := fmt.Sprintf("http://127.0.0.1:%d/api/board", port)
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		_ = ln.Close()
		<-done
		t.Fatalf("GET board after Serve start: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}

	// Shut down by closing the listener.
	_ = ln.Close()
	<-done

	if s := errW.String(); !strings.Contains(s, "kanban web UI") {
		t.Errorf("expected startup message in ErrWriter; got %q", s)
	}
}
