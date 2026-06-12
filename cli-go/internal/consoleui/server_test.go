package consoleui_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/dashauth"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// ---- test infrastructure ---------------------------------------------------

// newTestServer builds a consoleui.Server backed by httptest.NewServer.
// It uses the Server.Handler() so the dashauth edge middleware is NOT applied
// by the server itself — the test applies it manually for specific auth-matrix
// tests, and for boot tests uses bare access.
func newTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	stateDir := t.TempDir()
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
	})
	// Use the inner Handler (no Host middleware) for httptest.
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, tok
}

// newAuthTestServer builds a server wrapped with RequireLocalHost + RequireToken
// at the edge, as the production server does, but via httptest (random port).
// We can't use RequireLocalHost because httptest uses a random port and the Host
// header won't match.  Instead we wrap only the token middleware, which covers
// the 401 part of the auth matrix.
func newAuthTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	stateDir := t.TempDir()
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
	})

	// Wrap inner handler with token middleware (RequireLocalHost is
	// port-sensitive so we apply it separately in the Host-check tests).
	wrapped := requireTokenExceptRoot(tok, srv.Handler())
	ts := httptest.NewServer(wrapped)
	t.Cleanup(ts.Close)
	return ts, tok
}

// requireTokenExceptRoot mirrors the production logic from server.go.
// Exposed here to let test helpers replicate the edge middleware without
// importing internal details.
func requireTokenExceptRoot(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			next.ServeHTTP(w, r)
			return
		}
		dashauth.RequireToken(token, next.ServeHTTP)(w, r)
	})
}

// get issues a GET to url with the given bearer token (empty = no header).
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

// post issues a POST to url with the given bearer token.
func post(t *testing.T, url, tok, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
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

func drainClose(resp *http.Response) {
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

// ---- 1. Boot and SPA shell tests -------------------------------------------

// TestConsoleBoot_ServesIndexAtRoot verifies the console serves its SPA at /.
func TestConsoleBoot_ServesIndexAtRoot(t *testing.T) {
	ts, _ := newTestServer(t)
	resp := get(t, ts.URL+"/", "")
	defer drainClose(resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /: status=%d; want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("GET /: Content-Type=%q; want text/html", ct)
	}
}

// TestConsoleBoot_ServesAppJS verifies /app.js is served.
func TestConsoleBoot_ServesAppJS(t *testing.T) {
	ts, tok := newAuthTestServer(t)
	resp := get(t, ts.URL+"/app.js", tok)
	defer drainClose(resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /app.js: status=%d; want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/javascript") {
		t.Errorf("GET /app.js: Content-Type=%q; want application/javascript", ct)
	}
}

// TestConsoleBoot_ServesCSS verifies /styles.css is served.
func TestConsoleBoot_ServesCSS(t *testing.T) {
	ts, tok := newAuthTestServer(t)
	resp := get(t, ts.URL+"/styles.css", tok)
	defer drainClose(resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /styles.css: status=%d; want 200", resp.StatusCode)
	}
}

// TestConsoleBoot_MountsKanbanPrefix verifies /kanban/ is reachable.
func TestConsoleBoot_MountsKanbanPrefix(t *testing.T) {
	ts, tok := newAuthTestServer(t)
	resp := get(t, ts.URL+"/kanban/", tok)
	defer drainClose(resp)
	// The kanban handler serves HTML at /, so status 200 is expected.
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /kanban/: status=%d; want 200", resp.StatusCode)
	}
}

// TestConsoleBoot_MountsCostPrefix verifies /cost/ is reachable.
func TestConsoleBoot_MountsCostPrefix(t *testing.T) {
	ts, tok := newAuthTestServer(t)
	resp := get(t, ts.URL+"/cost/", tok)
	defer drainClose(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /cost/: status=%d; want 200", resp.StatusCode)
	}
}

// TestConsoleBoot_MountsPerfPrefix verifies /perf/ is reachable.
func TestConsoleBoot_MountsPerfPrefix(t *testing.T) {
	ts, tok := newAuthTestServer(t)
	resp := get(t, ts.URL+"/perf/", tok)
	defer drainClose(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /perf/: status=%d; want 200", resp.StatusCode)
	}
}

// ---- 2. Auth matrix tests --------------------------------------------------
//
// The spec requires:
//   - POST /kanban/api/delete → 401 without token
//   - POST /kanban/api/delete → 403 with wrong token
//   - POST /kanban/api/delete → 200 (or board error, not auth error) with correct token
//
// The 403 on bad Host is tested via RequireLocalHost directly (httptest uses
// a random port so we can't use the real edge server for Host checks; instead
// we exercise RequireLocalHost in a dedicated sub-test).

// TestAuthMatrix_KanbanDelete_401WithoutToken checks 401 on missing token.
func TestAuthMatrix_KanbanDelete_401WithoutToken(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	resp := post(t, ts.URL+"/kanban/api/delete", "", `{"id":"K-1"}`)
	defer drainClose(resp)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("POST /kanban/api/delete (no token): status=%d; want 401", resp.StatusCode)
	}
}

// TestAuthMatrix_KanbanDelete_403WithBadToken checks 403 on wrong token.
func TestAuthMatrix_KanbanDelete_403WithBadToken(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	badTok := strings.Repeat("b", 64) // valid-length but wrong token
	resp := post(t, ts.URL+"/kanban/api/delete", badTok, `{"id":"K-1"}`)
	defer drainClose(resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST /kanban/api/delete (bad token): status=%d; want 403", resp.StatusCode)
	}
}

// TestAuthMatrix_KanbanDelete_SucceedsWithToken checks that the correct token
// allows the request through to the kanban handler (which will return a board
// error because K-1 doesn't exist, but NOT an auth error).
func TestAuthMatrix_KanbanDelete_SucceedsWithToken(t *testing.T) {
	ts, tok := newAuthTestServer(t)
	resp := post(t, ts.URL+"/kanban/api/delete", tok, `{"id":"K-1"}`)
	defer drainClose(resp)
	// Auth passes; kanban returns an error about missing task — that's 500
	// from kanban, not 401/403.
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		t.Errorf("POST /kanban/api/delete (correct token): status=%d; want non-401/403", resp.StatusCode)
	}
}

// TestAuthMatrix_CostAPI_401WithoutToken checks /cost/* without token.
func TestAuthMatrix_CostAPI_401WithoutToken(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	resp := get(t, ts.URL+"/cost/api/metrics/snapshot", "")
	defer drainClose(resp)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /cost/api/metrics/snapshot (no token): status=%d; want 401", resp.StatusCode)
	}
}

// TestAuthMatrix_PerfAPI_401WithoutToken checks /perf/* without token.
func TestAuthMatrix_PerfAPI_401WithoutToken(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	resp := get(t, ts.URL+"/perf/api/perf/summary", "")
	defer drainClose(resp)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /perf/api/perf/summary (no token): status=%d; want 401", resp.StatusCode)
	}
}

// TestAuthMatrix_CostAPI_403WithBadToken checks /cost/* with wrong token.
func TestAuthMatrix_CostAPI_403WithBadToken(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	resp := get(t, ts.URL+"/cost/api/metrics/snapshot", strings.Repeat("c", 64))
	defer drainClose(resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET /cost/api/metrics/snapshot (bad token): status=%d; want 403", resp.StatusCode)
	}
}

// TestAuthMatrix_PerfAPI_403WithBadToken checks /perf/* with wrong token.
func TestAuthMatrix_PerfAPI_403WithBadToken(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	resp := get(t, ts.URL+"/perf/api/perf/summary", strings.Repeat("d", 64))
	defer drainClose(resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET /perf/api/perf/summary (bad token): status=%d; want 403", resp.StatusCode)
	}
}

// TestAuthMatrix_SPARoot_NoTokenRequired verifies GET / does NOT require a token.
func TestAuthMatrix_SPARoot_NoTokenRequired(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	resp := get(t, ts.URL+"/", "")
	defer drainClose(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET / (no token): status=%d; want 200 (SPA root is public)", resp.StatusCode)
	}
}

// TestAuthMatrix_HostCheck_403OnBadHost verifies that RequireLocalHost rejects
// a mismatched Host header.  We build the middleware directly and use httptest
// to verify the 403 response.
func TestAuthMatrix_HostCheck_403OnBadHost(t *testing.T) {
	stateDir := t.TempDir()
	tok, _ := consoleui.LoadOrCreateToken(stateDir)
	bus := wsbus.New()
	defer bus.Stop()

	srv := consoleui.New(consoleui.Config{
		Token:           tok,
		KanbanBoardPath: t.TempDir() + "/kanban.md",
		KanbanProject:   "test",
		Bus:             bus,
	})

	// Wrap with RequireLocalHost for a known fake address.
	// Any request with a Host that doesn't match 127.0.0.1:9999 / localhost:9999 /
	// [::1]:9999 should get 403.
	protected := dashauth.RequireLocalHost("127.0.0.1:9999", srv.Handler())

	req := httptest.NewRequest(http.MethodGet, "/kanban/api/board", nil)
	req.Host = "evil.example.com:9999"
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	protected.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("bad Host: status=%d; want 403", w.Code)
	}
}

// TestAuthMatrix_HostCheck_AllowsLoopbackHost verifies that a correct loopback
// Host is accepted.
func TestAuthMatrix_HostCheck_AllowsLoopbackHost(t *testing.T) {
	stateDir := t.TempDir()
	tok, _ := consoleui.LoadOrCreateToken(stateDir)
	bus := wsbus.New()
	defer bus.Stop()

	srv := consoleui.New(consoleui.Config{
		Token:           tok,
		KanbanBoardPath: t.TempDir() + "/kanban.md",
		KanbanProject:   "test",
		Bus:             bus,
	})

	protected := dashauth.RequireLocalHost("127.0.0.1:9999", requireTokenExceptRoot(tok, srv.Handler()))

	req := httptest.NewRequest(http.MethodGet, "/kanban/api/board", nil)
	req.Host = "127.0.0.1:9999"
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	protected.ServeHTTP(w, req)

	// The kanban handler returns 200 for GET /api/board.
	if w.Code == http.StatusForbidden || w.Code == http.StatusUnauthorized {
		t.Errorf("good Host+token: status=%d; want non-403/401", w.Code)
	}
}

// ---- 3. Token management tests ---------------------------------------------

// TestToken_CreatesOnMissing verifies LoadOrCreateToken creates a 64-hex token.
func TestToken_CreatesOnMissing(t *testing.T) {
	stateDir := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	if len(tok) != 64 {
		t.Errorf("token length=%d; want 64", len(tok))
	}
}

// TestToken_Idempotent verifies repeated calls return the same token.
func TestToken_Idempotent(t *testing.T) {
	stateDir := t.TempDir()
	tok1, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	tok2, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if tok1 != tok2 {
		t.Error("LoadOrCreateToken should return the same token on repeated calls")
	}
}

// TestToken_RotateChangesToken verifies RotateToken produces a different token.
func TestToken_RotateChangesToken(t *testing.T) {
	stateDir := t.TempDir()
	tok1, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	tok2, err := consoleui.RotateToken(stateDir)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if tok1 == tok2 {
		t.Error("RotateToken should produce a different token")
	}
}

// TestToken_FilePath verifies TokenFilePath returns a meaningful path.
func TestToken_FilePath(t *testing.T) {
	stateDir := t.TempDir()
	path := consoleui.TokenFilePath(stateDir)
	if !strings.HasSuffix(path, "console-token") {
		t.Errorf("path=%q; should end in console-token", path)
	}
}

// ---- 4. DefaultAddr --------------------------------------------------------

func TestDefaultAddr(t *testing.T) {
	if consoleui.DefaultAddr() != "127.0.0.1:7890" {
		t.Errorf("DefaultAddr=%q; want 127.0.0.1:7890", consoleui.DefaultAddr())
	}
}
