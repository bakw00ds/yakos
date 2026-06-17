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
	"golang.org/x/net/websocket"
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

	srv := consoleui.MustNew(t, consoleui.Config{
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

// newAuthTestServer builds a server wrapped with RequireToken at the edge
// (using the production RequireTokenForNonStatic that exempts static assets),
// as the production server does, but via httptest (random port).
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

	srv := consoleui.MustNew(t, consoleui.Config{
		Token:             tok,
		KanbanBoardPath:   t.TempDir() + "/kanban.md",
		KanbanProject:     "test",
		MetricsProjectDir: t.TempDir(),
		PerfWorkDir:       t.TempDir(),
		Bus:               bus,
	})

	// Wrap inner handler with the production edge middleware stack
	// (Content-Type gate + token, with static-asset exemptions).
	// RequireLocalHost is port-sensitive so we omit it for httptest
	// (it is exercised separately below).
	wrapped := consoleui.RequireTokenForNonStatic(tok,
		consoleui.RequireJSONForMutations(srv.Handler()))
	ts := httptest.NewServer(wrapped)
	t.Cleanup(ts.Close)
	return ts, tok
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

// post issues a POST to url with the given bearer token and content type.
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

// postNoContentType issues a POST without a Content-Type header.
func postNoContentType(t *testing.T, url, tok, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
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

// TestConsoleBoot_ServesSW verifies /sw.js is served.
func TestConsoleBoot_ServesSW(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	resp := get(t, ts.URL+"/sw.js", "") // no token — sw.js is public
	defer drainClose(resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /sw.js (no token): status=%d; want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/javascript") {
		t.Errorf("GET /sw.js: Content-Type=%q; want application/javascript", ct)
	}
	swa := resp.Header.Get("Service-Worker-Allowed")
	if swa != "/" {
		t.Errorf("GET /sw.js: Service-Worker-Allowed=%q; want /", swa)
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

// ---- 2. Static-asset no-token path tests ------------------------------------
// Verifies that /, /app.js, /styles.css, /sw.js return 200 WITHOUT a token.

func TestStaticAssets_NoTokenRequired_Root(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	resp := get(t, ts.URL+"/", "")
	defer drainClose(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET / (no token): status=%d; want 200 (root is token-exempt)", resp.StatusCode)
	}
}

func TestStaticAssets_NoTokenRequired_AppJS(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	resp := get(t, ts.URL+"/app.js", "")
	defer drainClose(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /app.js (no token): status=%d; want 200 (app.js is token-exempt)", resp.StatusCode)
	}
}

func TestStaticAssets_NoTokenRequired_StylesCSS(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	resp := get(t, ts.URL+"/styles.css", "")
	defer drainClose(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /styles.css (no token): status=%d; want 200 (styles.css is token-exempt)", resp.StatusCode)
	}
}

func TestStaticAssets_NoTokenRequired_SWJS(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	resp := get(t, ts.URL+"/sw.js", "")
	defer drainClose(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /sw.js (no token): status=%d; want 200 (sw.js is token-exempt)", resp.StatusCode)
	}
}

// TestVendorRoute_MermaidServedNoToken verifies /vendor/mermaid.min.js is
// served as application/javascript without a token (same-origin static asset).
func TestVendorRoute_MermaidServedNoToken(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	resp := get(t, ts.URL+"/vendor/mermaid.min.js", "")
	defer drainClose(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /vendor/mermaid.min.js (no token): status=%d; want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/javascript") && !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("GET /vendor/mermaid.min.js: Content-Type=%q; want application/javascript", ct)
	}
}

// TestVendorRoute_404ForUnknownFile verifies /vendor/nonexistent.js returns
// 404 and not a server error.
func TestVendorRoute_404ForUnknownFile(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	resp := get(t, ts.URL+"/vendor/nonexistent.js", "")
	defer drainClose(resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /vendor/nonexistent.js: status=%d; want 404", resp.StatusCode)
	}
}

// TestVendorRoute_CORP verifies /vendor/mermaid.min.js carries the
// Cross-Origin-Resource-Policy: same-origin header, consistent with all
// other static asset handlers.
func TestVendorRoute_CORP(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	resp := get(t, ts.URL+"/vendor/mermaid.min.js", "")
	defer drainClose(resp)
	corp := resp.Header.Get("Cross-Origin-Resource-Policy")
	if corp != "same-origin" {
		t.Errorf("GET /vendor/mermaid.min.js: Cross-Origin-Resource-Policy=%q; want same-origin", corp)
	}
}

// ---- 3. Content-Type 415 gate -----------------------------------------------
// Non-GET requests without Content-Type: application/json must receive 415.

func TestContentType_415_OnMissingContentType(t *testing.T) {
	ts, tok := newAuthTestServer(t)
	resp := postNoContentType(t, ts.URL+"/kanban/api/delete", tok, `{"id":"K-1"}`)
	defer drainClose(resp)
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("POST /kanban/api/delete (no Content-Type): status=%d; want 415", resp.StatusCode)
	}
}

func TestContentType_415_OnWrongContentType(t *testing.T) {
	ts, tok := newAuthTestServer(t)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/kanban/api/add", strings.NewReader(`{"title":"t"}`))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer drainClose(resp)
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("POST (text/plain Content-Type): status=%d; want 415", resp.StatusCode)
	}
}

func TestContentType_OK_OnApplicationJSON(t *testing.T) {
	ts, tok := newAuthTestServer(t)
	// POST with correct Content-Type must NOT get 415 (may get auth error or
	// board error, but not 415).
	resp := post(t, ts.URL+"/kanban/api/delete", tok, `{"id":"K-1"}`)
	defer drainClose(resp)
	if resp.StatusCode == http.StatusUnsupportedMediaType {
		t.Errorf("POST (application/json): status=415; should not be rejected on Content-Type")
	}
}

// TestContentType_GetNotGated verifies GET requests are NOT subject to the
// Content-Type gate (they never send a body).
func TestContentType_GetNotGated(t *testing.T) {
	ts, tok := newAuthTestServer(t)
	resp := get(t, ts.URL+"/kanban/api/board", tok)
	defer drainClose(resp)
	if resp.StatusCode == http.StatusUnsupportedMediaType {
		t.Errorf("GET /kanban/api/board: got 415; GET should not be subject to Content-Type gate")
	}
}

// ---- 4. Auth matrix tests --------------------------------------------------
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

	srv := consoleui.MustNew(t, consoleui.Config{
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

	srv := consoleui.MustNew(t, consoleui.Config{
		Token:           tok,
		KanbanBoardPath: t.TempDir() + "/kanban.md",
		KanbanProject:   "test",
		Bus:             bus,
	})

	protected := dashauth.RequireLocalHost("127.0.0.1:9999", consoleui.RequireTokenForNonStatic(tok, srv.Handler()))

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

// ---- 5. Token management tests ---------------------------------------------

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

// ---- 6. DefaultAddr --------------------------------------------------------

func TestDefaultAddr(t *testing.T) {
	if consoleui.DefaultAddr() != "127.0.0.1:7890" {
		t.Errorf("DefaultAddr=%q; want 127.0.0.1:7890", consoleui.DefaultAddr())
	}
}

// ---- 7. IDE editor spike: CSP isolation tests ------------------------------
//
// These tests are the gating proof for the Monaco IDE spike (Phase 1):
//   (a) GET /ide/editor returns the scoped CSP containing wasm-unsafe-eval
//       and worker-src blob:.  This CSP is set only by ideEditorCSP().
//   (b) GET / (index) CSP is UNCHANGED — still script-src 'self' with NO
//       wasm-unsafe-eval and NO blob: in worker-src.
//
// Together they prove the relaxation is SCOPED to /ide/editor and that the
// main console CSP has not been widened.

// TestIDEEditor_ScopedCSP_ContainsWasmUnsafeEvalAndBlobWorker verifies that
// GET /ide/editor responds with a CSP header containing both 'wasm-unsafe-eval'
// (required by the Monaco AMD loader) and 'worker-src blob:' (required by the
// Monaco web worker blob-wrapper pattern).
// No token is required — /ide/editor is a token-exempt shell (see isStaticAsset).
func TestIDEEditor_ScopedCSP_ContainsWasmUnsafeEvalAndBlobWorker(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	resp := get(t, ts.URL+"/ide/editor", "") // no token — shell is exempt
	defer drainClose(resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /ide/editor: status=%d; want 200", resp.StatusCode)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("GET /ide/editor: missing Content-Security-Policy header")
	}
	if !strings.Contains(csp, "'wasm-unsafe-eval'") {
		t.Errorf("GET /ide/editor CSP: missing 'wasm-unsafe-eval'\n  CSP: %s", csp)
	}
	if !strings.Contains(csp, "worker-src") || !strings.Contains(csp, "blob:") {
		t.Errorf("GET /ide/editor CSP: missing 'worker-src blob:'\n  CSP: %s", csp)
	}
}

// TestIDEEditor_ScopedCSP_HasFrameAncestorsSelf verifies that the /ide/editor
// CSP restricts framing to same-origin only (frame-ancestors 'self'), preventing
// the editor host document from being embedded by a cross-origin page.
func TestIDEEditor_ScopedCSP_HasFrameAncestorsSelf(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	resp := get(t, ts.URL+"/ide/editor", "") // no token — shell is exempt
	defer drainClose(resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /ide/editor: status=%d; want 200", resp.StatusCode)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "frame-ancestors 'self'") {
		t.Errorf("GET /ide/editor CSP: missing frame-ancestors 'self'\n  CSP: %s", csp)
	}
}

// TestIDEEditor_MainIndexCSP_Unchanged verifies that GET / (the main console
// index) still returns the original CSP — specifically that:
//   - script-src 'self' is present (no relaxation)
//   - 'wasm-unsafe-eval' is NOT present (not widened)
//   - 'blob:' is NOT present in worker-src (not widened)
//
// This is the critical invariant: the scoped CSP for /ide/editor must NOT
// have leaked into the shared cspHeader() used by the main console.
func TestIDEEditor_MainIndexCSP_Unchanged(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	resp := get(t, ts.URL+"/", "") // / is token-exempt
	defer drainClose(resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /: status=%d; want 200", resp.StatusCode)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("GET /: missing Content-Security-Policy header")
	}
	// Main CSP must still have script-src 'self'.
	if !strings.Contains(csp, "script-src 'self'") {
		t.Errorf("GET / CSP: missing script-src 'self'\n  CSP: %s", csp)
	}
	// Main CSP must NOT have wasm-unsafe-eval (would widen the attack surface).
	if strings.Contains(csp, "wasm-unsafe-eval") {
		t.Errorf("GET / CSP: unexpectedly contains 'wasm-unsafe-eval' — main CSP must not be widened\n  CSP: %s", csp)
	}
	// Main CSP must NOT have blob: in worker-src.
	if strings.Contains(csp, "blob:") {
		t.Errorf("GET / CSP: unexpectedly contains 'blob:' — main CSP must not be widened\n  CSP: %s", csp)
	}
}

// TestIDEEditor_ServedNoToken verifies that GET /ide/editor returns 200 WITHOUT
// a bearer token — the shell is token-exempt, exactly like index.html.
// The shell serves no workspace content; file content is gated separately by
// the RoleRead-gated /api/files/* endpoints and postMessage from the parent.
func TestIDEEditor_ServedNoToken(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	resp := get(t, ts.URL+"/ide/editor", "") // no token
	defer drainClose(resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /ide/editor (no token): status=%d; want 200 (shell is token-exempt)", resp.StatusCode)
	}
}

// TestIDEEditor_ServesHTMLContent verifies that /ide/editor returns an HTML
// document with the expected Content-Type. No token required.
func TestIDEEditor_ServesHTMLContent(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	resp := get(t, ts.URL+"/ide/editor", "") // no token — shell is exempt
	defer drainClose(resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /ide/editor: status=%d; want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("GET /ide/editor: Content-Type=%q; want text/html", ct)
	}
}

// TestIDEEditor_CORP verifies /ide/editor carries the Cross-Origin-Resource-Policy:
// same-origin header, consistent with other static asset handlers.
func TestIDEEditor_CORP(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	resp := get(t, ts.URL+"/ide/editor", "") // no token — shell is exempt
	defer drainClose(resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /ide/editor: status=%d; want 200", resp.StatusCode)
	}
	corp := resp.Header.Get("Cross-Origin-Resource-Policy")
	if corp != "same-origin" {
		t.Errorf("GET /ide/editor: Cross-Origin-Resource-Policy=%q; want same-origin", corp)
	}
}

// TestIDEEditor_XContentTypeOptions verifies /ide/editor sets
// X-Content-Type-Options: nosniff (security-review followup item 3).
func TestIDEEditor_XContentTypeOptions(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	resp := get(t, ts.URL+"/ide/editor", "") // no token — shell is exempt
	defer drainClose(resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /ide/editor: status=%d; want 200", resp.StatusCode)
	}
	xcto := resp.Header.Get("X-Content-Type-Options")
	if xcto != "nosniff" {
		t.Errorf("GET /ide/editor: X-Content-Type-Options=%q; want nosniff", xcto)
	}
}

// TestIDEEditor_ScopedCSP_HasExplicitFontSrc verifies that the /ide/editor CSP
// contains an explicit font-src directive (security-review followup item 2).
// codicon.ttf must be covered by font-src 'self', not the default-src fallback.
func TestIDEEditor_ScopedCSP_HasExplicitFontSrc(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	resp := get(t, ts.URL+"/ide/editor", "") // no token — shell is exempt
	defer drainClose(resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /ide/editor: status=%d; want 200", resp.StatusCode)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "font-src 'self'") {
		t.Errorf("GET /ide/editor CSP: missing explicit font-src 'self' directive\n  CSP: %s", csp)
	}
}

// TestIDEEditorJS_ServedNoToken verifies that /ide-editor.js is served without
// a bearer token (it is a token-exempt static asset, like /app.js) and is
// delivered with Content-Type: application/javascript and CORP: same-origin.
// This is required because the /ide/editor iframe must load the bootstrap script
// before the Service Worker has injected an Authorization header.
func TestIDEEditorJS_ServedNoToken(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	resp := get(t, ts.URL+"/ide-editor.js", "") // no token
	defer drainClose(resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /ide-editor.js (no token): status=%d; want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/javascript") {
		t.Errorf("GET /ide-editor.js: Content-Type=%q; want application/javascript", ct)
	}
	corp := resp.Header.Get("Cross-Origin-Resource-Policy")
	if corp != "same-origin" {
		t.Errorf("GET /ide-editor.js: Cross-Origin-Resource-Policy=%q; want same-origin", corp)
	}
}

// TestIDEEditor_NoInlineScript verifies that ide-editor.html contains no inline
// JavaScript — only an external <script src=...> reference is allowed.
// This ensures the document can be served under script-src 'self' (no
// 'unsafe-inline') without triggering a CSP violation.
func TestIDEEditor_NoInlineScript(t *testing.T) {
	ts, tok := newAuthTestServer(t)
	resp := get(t, ts.URL+"/ide/editor", tok)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /ide/editor: status=%d; want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading /ide/editor body: %v", err)
	}
	html := string(body)

	// The only permitted <script> element is the external loader:
	// <script src="/ide-editor.js"></script>
	// Any <script> element with content (even whitespace beyond the tag)
	// would violate script-src 'self' (no 'unsafe-inline').
	if !strings.Contains(html, `<script src="/ide-editor.js">`) {
		t.Errorf("ide-editor.html: missing <script src=\"/ide-editor.js\"> reference")
	}

	// Scan for any <script> element that contains inline code.
	// Strip HTML comments first so that mentions of <script> inside comments
	// (e.g. documentation prose in the file header) are not flagged.
	stripped := html
	for {
		start := strings.Index(stripped, "<!--")
		if start < 0 {
			break
		}
		end := strings.Index(stripped[start:], "-->")
		if end < 0 {
			break
		}
		stripped = stripped[:start] + stripped[start+end+3:]
	}

	lower := strings.ToLower(stripped)
	searchFrom := 0
	for {
		idx := strings.Index(lower[searchFrom:], "<script")
		if idx < 0 {
			break
		}
		absIdx := searchFrom + idx
		// Find the end of the opening tag.
		closeIdx := strings.Index(lower[absIdx:], ">")
		if closeIdx < 0 {
			break
		}
		openTag := lower[absIdx : absIdx+closeIdx+1]
		// If the opening tag has no src= attribute, it is an inline script.
		if !strings.Contains(openTag, "src=") {
			t.Errorf("ide-editor.html: found inline <script> element (no src= attribute): %q — violates script-src 'self'", openTag)
		}
		searchFrom = absIdx + closeIdx + 1
	}
}

// TestVendorRoute_MonacoLoaderServedNoToken verifies that the Monaco AMD
// loader (/vendor/monaco/min/vs/loader.js) is served without a token
// (vendor paths are token-exempt static assets) and carries CORP: same-origin.
func TestVendorRoute_MonacoLoaderServedNoToken(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	resp := get(t, ts.URL+"/vendor/monaco/min/vs/loader.js", "")
	defer drainClose(resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /vendor/monaco/min/vs/loader.js (no token): status=%d; want 200", resp.StatusCode)
	}
	corp := resp.Header.Get("Cross-Origin-Resource-Policy")
	if corp != "same-origin" {
		t.Errorf("GET /vendor/monaco/min/vs/loader.js: Cross-Origin-Resource-Policy=%q; want same-origin", corp)
	}
}

// TestVendorRoute_MonacoWorkerMainServedNoToken verifies that the Monaco worker
// script (/vendor/monaco/min/vs/base/worker/workerMain.js) is accessible
// without a token so that the blob-wrapper pattern can XHR-fetch it.
func TestVendorRoute_MonacoWorkerMainServedNoToken(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	resp := get(t, ts.URL+"/vendor/monaco/min/vs/base/worker/workerMain.js", "")
	defer drainClose(resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /vendor/monaco/.../workerMain.js (no token): status=%d; want 200", resp.StatusCode)
	}
}

// ---- 5. WebSocket edge bypass tests -----------------------------------------
// The edge middleware (requireTokenForNonStatic) must not apply the
// Authorization-header token check to WebSocket upgrade requests — browsers
// cannot set Authorization on WS upgrades.  The WS handler enforces its own
// subprotocol token auth.

// TestWSEdge_UpgradePassesThroughEdge verifies that a WS upgrade request
// is NOT rejected 401 by the edge token check.  The edge must forward it
// to the downstream handler so the WS subprotocol auth can run.
func TestWSEdge_UpgradePassesThroughEdge(t *testing.T) {
	tok := strings.Repeat("a", 64)
	reached := false
	downstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusSwitchingProtocols) // 101
	})
	wrapped := consoleui.RequireTokenForNonStatic(tok, downstream)

	// Simulate a WS upgrade: no Authorization header, Upgrade: websocket.
	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if !reached {
		t.Error("downstream handler was not reached for WS upgrade — edge blocked it (401 regression)")
	}
	if rr.Code == http.StatusUnauthorized {
		t.Error("WS upgrade got 401 at edge; Authorization check must be skipped for WS upgrades")
	}
}

// TestWSEdge_WrongTokenNonWSStillGets401 verifies that non-WS requests without
// a valid token are still rejected 401 at the edge (regression guard).
func TestWSEdge_WrongTokenNonWSStillGets401(t *testing.T) {
	tok := strings.Repeat("a", 64)
	downstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	wrapped := consoleui.RequireTokenForNonStatic(tok, downstream)

	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	// No Upgrade header, no Authorization header.
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("non-WS request without token: status=%d; want 401", rr.Code)
	}
}

// TestWSEdge_WSHandlerRejectsBadSubprotocol verifies that a WS upgrade that
// passes the edge but carries a wrong subprotocol token is rejected by the
// WS handler's own auth (not the edge).  End-to-end via newAuthTestServer.
func TestWSEdge_WSHandlerRejectsBadSubprotocol(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/events"

	cfg, err := websocket.NewConfig(wsURL, "http://127.0.0.1/")
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	// Wrong token in subprotocol — should be rejected by the WS handler.
	cfg.Protocol = []string{"yakos-bearer", strings.Repeat("z", 64)}
	_, err = websocket.DialConfig(cfg)
	if err == nil {
		t.Fatal("expected dial error for bad subprotocol token; got nil (WS handler failed to reject)")
	}
}

// TestWSEdge_SpoofedUpgradeOnNonWSRouteStill401 is a security regression test.
// A request to a gated non-WS route (e.g. /api/files/content) carrying a
// spoofed "Upgrade: websocket" header must NOT bypass the token check.
// The bypass in requireTokenForNonStatic is scoped to /v1/events only.
func TestWSEdge_SpoofedUpgradeOnNonWSRouteStill401(t *testing.T) {
	tok := strings.Repeat("a", 64)
	downstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	wrapped := consoleui.RequireTokenForNonStatic(tok, downstream)

	gatedPaths := []string{
		"/api/files/content",
		"/api/files/tree",
		"/api/chat/dispatch",
		"/flows/api/workflows",
		"/api/presence",
	}
	for _, path := range gatedPaths {
		path := path
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			// Spoofed Upgrade header — no token.
			req.Header.Set("Upgrade", "websocket")
			req.Header.Set("Connection", "Upgrade")
			rr := httptest.NewRecorder()
			wrapped.ServeHTTP(rr, req)

			if rr.Code != http.StatusUnauthorized {
				t.Errorf("GET %s with spoofed Upgrade: websocket (no token): status=%d; want 401 — bypass must be scoped to /v1/events", path, rr.Code)
			}
		})
	}
}

// TestVendorRoute_MonacoLanguageGrammarsServed verifies that representative
// Monaco language grammar files (basic-languages) and language service workers
// (language/) serve 200 with application/javascript and no token required.
// These files are lazy-loaded by Monaco at runtime and were previously 404ing
// because only the core editor files had been vendored.
func TestVendorRoute_MonacoLanguageGrammarsServed(t *testing.T) {
	ts, _ := newAuthTestServer(t)

	files := []string{
		// Monarch syntax grammars (basic-languages)
		"/vendor/monaco/min/vs/basic-languages/go/go.js",
		"/vendor/monaco/min/vs/basic-languages/python/python.js",
		"/vendor/monaco/min/vs/basic-languages/typescript/typescript.js",
		"/vendor/monaco/min/vs/basic-languages/yaml/yaml.js",
		"/vendor/monaco/min/vs/basic-languages/rust/rust.js",
		"/vendor/monaco/min/vs/basic-languages/shell/shell.js",
		// Language service workers (language/)
		"/vendor/monaco/min/vs/language/typescript/tsMode.js",
		"/vendor/monaco/min/vs/language/json/jsonMode.js",
		"/vendor/monaco/min/vs/language/css/cssMode.js",
		"/vendor/monaco/min/vs/language/html/htmlMode.js",
	}

	for _, path := range files {
		path := path
		t.Run(path, func(t *testing.T) {
			resp := get(t, ts.URL+path, "") // no token — vendor paths are exempt
			defer drainClose(resp)

			if resp.StatusCode != http.StatusOK {
				t.Errorf("GET %s (no token): status=%d; want 200", path, resp.StatusCode)
				return
			}
			ct := resp.Header.Get("Content-Type")
			if !strings.Contains(ct, "javascript") {
				t.Errorf("GET %s: Content-Type=%q; want application/javascript", path, ct)
			}
		})
	}
}

// ---- 6. Vendored font serve tests -------------------------------------------
// Verifies all 8 woff2 font files are served at /vendor/fonts/... without a
// token (same-origin static; token-exempt like all /vendor/* paths).

func TestVendorRoute_FontsServedNoToken(t *testing.T) {
	ts, _ := newAuthTestServer(t)

	fonts := []string{
		"/vendor/fonts/inter-v4-latin-400.woff2",
		"/vendor/fonts/inter-v4-latin-500.woff2",
		"/vendor/fonts/inter-v4-latin-600.woff2",
		"/vendor/fonts/inter-v4-latin-700.woff2",
		"/vendor/fonts/jetbrains-mono-v13-latin-400.woff2",
		"/vendor/fonts/jetbrains-mono-v13-latin-500.woff2",
		"/vendor/fonts/jetbrains-mono-v13-latin-600.woff2",
		"/vendor/fonts/jetbrains-mono-v13-latin-700.woff2",
	}

	for _, path := range fonts {
		path := path
		t.Run(path, func(t *testing.T) {
			resp := get(t, ts.URL+path, "") // no token — /vendor/* is exempt
			defer drainClose(resp)

			if resp.StatusCode != http.StatusOK {
				t.Errorf("GET %s (no token): status=%d; want 200 (font 404 regression)", path, resp.StatusCode)
			}
		})
	}
}

// ---- Interactive-P1 wiring regression guard ---------------------------------

// TestConsoleConfig_InteractiveManagerWired asserts that consoleui.New() always
// constructs and wires a non-nil interactive.Manager into the Server, even when
// Config.InteractiveManager is not set by the caller.
//
// This is the regression guard for the bug where serve.go never constructed the
// Manager, leaving Config.InteractiveManager nil so that POST /api/chat/send
// always returned 503 "interactive mode not configured".
//
// If New() ever stops constructing the Manager (e.g. the construction block is
// accidentally deleted or skipped behind a wrong condition), this test fails.
func TestConsoleConfig_InteractiveManagerWired(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}

	// Construct a minimal Server with NO explicit InteractiveManager in Config.
	// New() must auto-construct one.
	srv := consoleui.MustNew(t, consoleui.Config{
		Token:             tok,
		KanbanBoardPath:   t.TempDir() + "/kanban.md",
		KanbanProject:     "test",
		MetricsProjectDir: t.TempDir(),
		PerfWorkDir:       t.TempDir(),
		// InteractiveManager intentionally absent — New() must wire it.
	})

	if consoleui.InteractiveMgrForTest(srv) == nil {
		t.Error("consoleui.New(): InteractiveManager is nil; " +
			"POST /api/chat/send will 503 (interactive mode not configured) — " +
			"wire interactive.NewManager in New() and assign to chatH.interactiveMgr")
	}
}
