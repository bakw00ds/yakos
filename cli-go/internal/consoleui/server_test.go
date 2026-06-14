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

	srv := consoleui.New(consoleui.Config{
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
func TestIDEEditor_ScopedCSP_ContainsWasmUnsafeEvalAndBlobWorker(t *testing.T) {
	ts, tok := newAuthTestServer(t)
	resp := get(t, ts.URL+"/ide/editor", tok)
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
	ts, tok := newAuthTestServer(t)
	resp := get(t, ts.URL+"/ide/editor", tok)
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

// TestIDEEditor_RequiresToken verifies that /ide/editor returns 401 without
// a bearer token (it is NOT a token-exempt static asset like /).
func TestIDEEditor_RequiresToken(t *testing.T) {
	ts, _ := newAuthTestServer(t)
	resp := get(t, ts.URL+"/ide/editor", "") // no token
	defer drainClose(resp)

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /ide/editor (no token): status=%d; want 401", resp.StatusCode)
	}
}

// TestIDEEditor_ServesHTMLContent verifies that /ide/editor returns an HTML
// document with the expected Content-Type.
func TestIDEEditor_ServesHTMLContent(t *testing.T) {
	ts, tok := newAuthTestServer(t)
	resp := get(t, ts.URL+"/ide/editor", tok)
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
	ts, tok := newAuthTestServer(t)
	resp := get(t, ts.URL+"/ide/editor", tok)
	defer drainClose(resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /ide/editor: status=%d; want 200", resp.StatusCode)
	}
	corp := resp.Header.Get("Cross-Origin-Resource-Policy")
	if corp != "same-origin" {
		t.Errorf("GET /ide/editor: Cross-Origin-Resource-Policy=%q; want same-origin", corp)
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
