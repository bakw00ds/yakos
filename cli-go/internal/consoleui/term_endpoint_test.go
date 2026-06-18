package consoleui_test

// term_endpoint_test.go — ADR-0008 Phase 1 endpoint tests.
//
// Tests:
//  1. /v1/term and /api/term are absent when TerminalManager is nil (flag off).
//  2. /api/term requires RoleAdmin — non-admin (RoleRead, RoleDispatch) gets 403.
//  3. /api/term returns empty JSON array when no sessions are active.
//  4. /v1/term/<sessionId> requires RoleAdmin — non-admin gets 403.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/netid"
	termmanager "github.com/bakw00ds/yakos/internal/terminalmanager"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// buildTermTestHandler builds a consoleui.Server with a real TerminalManager
// and returns its inner http.Handler (no edge middleware, no RequireLocalHost).
func buildTermTestHandler(t *testing.T) (http.Handler, *termmanager.Manager) {
	t.Helper()
	stateDir := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	mgr := termmanager.New(ctx, termmanager.Config{})

	srv := consoleui.MustNew(t, consoleui.Config{
		Token:             tok,
		KanbanBoardPath:   t.TempDir() + "/kanban.md",
		KanbanProject:     "test",
		MetricsProjectDir: t.TempDir(),
		PerfWorkDir:       t.TempDir(),
		Bus:               bus,
		TerminalManager:   mgr,
	})
	return srv.Handler(), mgr
}

// buildNoTermTestHandler builds a server without TerminalManager (flag off).
func buildNoTermTestHandler(t *testing.T) http.Handler {
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
		// TerminalManager intentionally nil (--share-terminal off).
	})
	return srv.Handler()
}

// requestWithRole builds an HTTP request whose context carries a resolved
// netid.Identity with the given role. The handler reads the identity directly
// from the request context (requireRole checks id.Resolved).
//
// RemoteAddr is set to "127.0.0.1:12345" (loopback) so consoleLoopbackOnly
// passes for requests that reach the WS handler middleware chain.
func requestWithRole(method, path string, role netid.Role) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = "127.0.0.1:12345" // loopback so consoleLoopbackOnly passes
	req.Header.Set("Origin", "http://127.0.0.1")
	id := netid.Identity{
		OperatorID:    "test-op",
		Role:          role,
		Authenticated: role >= netid.RoleDispatch,
		Resolved:      true,
	}
	return req.WithContext(netid.WithIdentityForTest(req.Context(), id))
}

// TestTermEndpointsAbsentWhenFlagOff verifies that /api/term and /v1/term/
// are not mounted when TerminalManager is nil.
func TestTermEndpointsAbsentWhenFlagOff(t *testing.T) {
	handler := buildNoTermTestHandler(t)

	paths := []string{"/api/term", "/v1/term/fakesession"}
	for _, path := range paths {
		req := requestWithRole(http.MethodGet, path, netid.RoleAdmin)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("GET %s (no TerminalManager): expected 404, got %d", path, w.Code)
		}
	}
}

// TestAPITermRequiresRoleAdmin verifies that GET /api/term enforces RoleAdmin.
func TestAPITermRequiresRoleAdmin(t *testing.T) {
	handler, _ := buildTermTestHandler(t)

	cases := []struct {
		role netid.Role
		want int
	}{
		{netid.RoleRead, http.StatusForbidden},
		{netid.RoleDispatch, http.StatusForbidden},
		{netid.RoleFlowsRun, http.StatusForbidden},
		{netid.RoleAdmin, http.StatusOK},
	}
	for _, tc := range cases {
		req := requestWithRole(http.MethodGet, "/api/term", tc.role)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Errorf("GET /api/term (role=%v): expected %d, got %d", tc.role, tc.want, w.Code)
		}
	}
}

// TestAPITermEmptyArray verifies that GET /api/term returns [] when no sessions exist.
func TestAPITermEmptyArray(t *testing.T) {
	handler, _ := buildTermTestHandler(t)

	req := requestWithRole(http.MethodGet, "/api/term", netid.RoleAdmin)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/term: expected 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	// The response should decode to an empty array.
	var sessions []interface{}
	if err := json.NewDecoder(w.Body).Decode(&sessions); err != nil {
		t.Fatalf("decode /api/term response: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected empty sessions array, got %v", sessions)
	}
}

// TestTermWSRequiresRoleAdmin verifies that /v1/term/ enforces RoleAdmin at
// the HTTP layer.
//
// The requireRole(RoleAdmin) wrapper is the outermost handler for /v1/term/;
// non-admin requests are rejected by it before the WS subprotocol auth chain
// runs.  We verify that RoleRead, RoleDispatch, and RoleFlowsRun all receive
// 403.
//
// For RoleAdmin we verify the route is mounted (i.e. the role gate passes and
// the request reaches the inner WS middleware chain) by checking that the
// response is NOT a "404 page not found" — any other response (including 403
// from the subprotocol check) confirms the endpoint is present and the role
// gate admitted the request.
func TestTermWSRequiresRoleAdmin(t *testing.T) {
	handler, _ := buildTermTestHandler(t)

	// Non-admin roles: role gate fires first, returns 403.
	nonAdminRoles := []netid.Role{netid.RoleRead, netid.RoleDispatch, netid.RoleFlowsRun}
	for _, role := range nonAdminRoles {
		req := requestWithRole(http.MethodGet, "/v1/term/fakesession", role)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("GET /v1/term/fakesession (role=%v): expected 403 from role gate, got %d", role, w.Code)
		}
	}

	// RoleAdmin: role gate passes; the request reaches the inner WS middleware
	// chain (which returns 403 for missing subprotocol token, NOT 404).
	// The important invariant is: the endpoint is mounted and the role gate does
	// not block RoleAdmin.
	req := requestWithRole(http.MethodGet, "/v1/term/fakesession", netid.RoleAdmin)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	// 404 would mean the endpoint is not mounted; anything else means it is
	// mounted (the role gate passed and the inner chain handled it).
	if w.Code == http.StatusNotFound {
		t.Errorf("GET /v1/term/fakesession (role=admin): endpoint appears unmounted (got 404)")
	}
	if w.Body.String() == "404 page not found\n" {
		t.Errorf("GET /v1/term/fakesession (role=admin): got 404 page not found (endpoint not mounted)")
	}
}
