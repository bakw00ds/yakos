package consoleui_test

// enforcement_test.go — Phase 6b enforcement tests for the consoleui package.
//
// Coverage:
//  1. C1: authenticated operator B cannot flip shared flag on operator A's session
//     by supplying A's operatorId in the body — cert CN wins.
//  2. Role enforcement: RoleRead identity gets 403 on dispatch route.
//  3. Role enforcement: RoleAdmin identity passes dispatch route.
//  4. Role enforcement: RoleRead identity can reach presence (read-only) route.
//  5. Loopback invariant: zero-value Identity (Resolved=false) is never blocked.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// ---- test helpers -----------------------------------------------------------

// injectIdentityMiddleware wraps handler to inject id into every request's context.
// This simulates Resolver.Middleware having run with a specific identity, so
// role enforcement in requireRole() fires as it would in production.
func injectIdentityMiddleware(id netid.Identity, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(netid.WithIdentityForTest(r.Context(), id)))
	})
}

// newEnforcementTestServer builds a consoleui Server and returns its Handler()
// wrapped with:
//   - RequireTokenForNonStatic + RequireJSONForMutations (matching production edge)
//   - An identity-injection middleware so role enforcement can be tested.
//
// Returns the server, token, and hub for set-up/teardown.
func newEnforcementTestServer(t *testing.T, id netid.Identity) (*httptest.Server, string, *consoleui.ChatHub) {
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
	})

	// Wrap with injectIdentityMiddleware (innermost) then token/JSON gates.
	handler := consoleui.RequireTokenForNonStatic(tok,
		consoleui.RequireJSONForMutations(
			injectIdentityMiddleware(id, srv.Handler())))

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts, tok, srv.ChatHub()
}

// authPost is a convenience wrapper for sending a POST with JSON body + auth.
func authPost(t *testing.T, ts *httptest.Server, tok, path string, body interface{}) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+path, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

// authGet sends a GET with Authorization header.
func authGet(t *testing.T, ts *httptest.Server, tok, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

// ---- C1: Share-pane confidentiality -----------------------------------------

// TestC1_AuthenticatedOperatorB_CannotFlipOperatorASession verifies that an
// authenticated operator B cannot flip the shared flag on operator A's session
// by supplying A's operatorId in the request body.  The cert CN (B) is used for
// the ownership check; the body operatorId is ignored.
//
// This is the core C1 regression test for Phase 6b.
func TestC1_AuthenticatedOperatorB_CannotFlipOperatorASession(t *testing.T) {
	t.Parallel()

	// Inject operator B's authenticated identity (cert CN = "operator-b").
	idB := netid.Identity{
		OperatorID:    "operator-b",
		Role:          netid.RoleAdmin,
		Authenticated: true,
		Resolved:      true,
	}
	ts, tok, hub := newEnforcementTestServer(t, idB)

	// Set up a session owned by operator A.
	if err := hub.OpenSession("sess-alice", "operator-a", false); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}

	// Operator B attempts to flip the shared flag on A's session,
	// supplying A's operatorId in the body (the attack vector).
	resp := authPost(t, ts, tok, "/api/chat/share", map[string]interface{}{
		"sessionId":  "sess-alice",
		"operatorId": "operator-a", // forged — should be ignored; cert CN (B) is used
		"shared":     true,
	})
	defer resp.Body.Close()

	// Expect 403: the hub compares cert CN "operator-b" against owner "operator-a".
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("C1: operator B supplying A's operatorId: got %d; want 403 Forbidden", resp.StatusCode)
	}
}

// TestC1_AuthenticatedOwner_CanFlipOwnSession verifies that an authenticated
// operator can flip the shared flag on their own session (happy path).
func TestC1_AuthenticatedOwner_CanFlipOwnSession(t *testing.T) {
	t.Parallel()

	idA := netid.Identity{
		OperatorID:    "operator-a",
		Role:          netid.RoleAdmin,
		Authenticated: true,
		Resolved:      true,
	}
	ts, tok, hub := newEnforcementTestServer(t, idA)

	if err := hub.OpenSession("sess-own", "operator-a", false); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}

	resp := authPost(t, ts, tok, "/api/chat/share", map[string]interface{}{
		"sessionId":  "sess-own",
		"operatorId": "operator-b", // body operatorId is ignored; cert CN "operator-a" is used
		"shared":     true,
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("C1: authenticated owner flipping own session: got %d; want 200 OK", resp.StatusCode)
	}
}

// ---- Role enforcement at the console edge -----------------------------------

// TestRoleEnforcement_RoleRead_BlockedOnDispatch verifies that a request with
// a resolved RoleRead identity gets 403 on /api/chat/dispatch (requires RoleDispatch).
func TestRoleEnforcement_RoleRead_BlockedOnDispatch(t *testing.T) {
	t.Parallel()

	readOnly := netid.Identity{
		OperatorID:    "reader",
		Role:          netid.RoleRead,
		Authenticated: true,
		Resolved:      true,
	}
	ts, tok, _ := newEnforcementTestServer(t, readOnly)

	resp := authPost(t, ts, tok, "/api/chat/dispatch", map[string]interface{}{
		"agent":     "backend",
		"task":      "do something",
		"sessionId": "s1",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("RoleRead on /api/chat/dispatch: got %d; want 403", resp.StatusCode)
	}
}

// TestRoleEnforcement_RoleAdmin_PassesDispatch verifies that a request with
// a resolved RoleAdmin identity passes the requireRole check on /api/chat/dispatch.
// (The handler may still return an error for business reasons — we just verify
// it's not a 403.)
func TestRoleEnforcement_RoleAdmin_PassesDispatch(t *testing.T) {
	t.Parallel()

	admin := netid.Identity{
		OperatorID:    "admin-op",
		Role:          netid.RoleAdmin,
		Authenticated: true,
		Resolved:      true,
	}
	ts, tok, _ := newEnforcementTestServer(t, admin)

	resp := authPost(t, ts, tok, "/api/chat/dispatch", map[string]interface{}{
		"agent":      "backend",
		"task":       "do something",
		"sessionId":  "s2",
		"operatorId": "admin-op",
	})
	defer resp.Body.Close()

	// Must not be 403 Forbidden (the requireRole check passed).
	// May be 503 (no dispatch service configured) or 400 (missing fields) — acceptable.
	if resp.StatusCode == http.StatusForbidden {
		t.Errorf("RoleAdmin on /api/chat/dispatch: got 403; want any non-403 status")
	}
}

// TestRoleEnforcement_RoleRead_PassesPresence verifies that a RoleRead identity
// can reach /api/presence (read-only endpoint; RoleRead required).
func TestRoleEnforcement_RoleRead_PassesPresence(t *testing.T) {
	t.Parallel()

	readOnly := netid.Identity{
		OperatorID:    "viewer",
		Role:          netid.RoleRead,
		Authenticated: true,
		Resolved:      true,
	}
	ts, tok, _ := newEnforcementTestServer(t, readOnly)

	resp := authGet(t, ts, tok, "/api/presence")
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		t.Errorf("RoleRead on /api/presence: got 403; want 200 (read-only endpoint)")
	}
}

// ---- Loopback invariant -----------------------------------------------------

// TestLoopbackInvariant_ZeroValueIdentity_NotBlocked verifies that when the
// resolver middleware has NOT run (Resolved=false, zero-value Identity from
// srv.Handler()), the requireRole check is skipped entirely — no 403.
//
// This is the loopback safety guarantee: all current tests that use srv.Handler()
// directly continue to work unmodified.
func TestLoopbackInvariant_ZeroValueIdentity_NotBlocked(t *testing.T) {
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

	// Use srv.Handler() directly — no identity injection, no resolver middleware.
	// Zero-value Identity has Resolved=false → requireRole is a no-op.
	handler := consoleui.RequireTokenForNonStatic(tok,
		consoleui.RequireJSONForMutations(srv.Handler()))
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	// POST /api/chat/dispatch — would require RoleDispatch if Resolved=true.
	// With Resolved=false this must not return 403.
	body := strings.NewReader(`{"agent":"x","task":"t","sessionId":"s3","operatorId":"op1"}`)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/chat/dispatch", body)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("POST /api/chat/dispatch: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		t.Errorf("loopback (Resolved=false) on /api/chat/dispatch: got 403; want any non-403 (loopback must not be blocked)")
	}
}
