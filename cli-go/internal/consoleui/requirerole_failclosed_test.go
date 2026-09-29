package consoleui_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// S-2 R17 (s2-daemon-security-review-2026-09-21.md): requireRole and the
// per-method role checks enforced only `if id.Resolved && !Allows(...)`, i.e.
// an UNRESOLVED identity (the resolver never ran) bypassed every role gate.
// Unreachable in production today (the resolver is always installed and
// stamps Resolved=true), but one future mount of Server.Handler() away from
// arbitrary command execution behind /api/console/bash. The checks now fail
// closed, and Server.Handler() supplies the loopback identity for tests that
// mount it bare.

func newRoleGateServer(t *testing.T) (*consoleui.Server, string) {
	t.Helper()
	tk, err := consoleui.LoadOrCreateToken(t.TempDir())
	if err != nil {
		t.Fatal(err)
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
		WorkDir:           t.TempDir(),
	})
	return srv, tk
}

func doJSON(t *testing.T, h http.Handler, tk, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tk)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// Every role-gated mutation route must 403 an explicitly unresolved identity.
func TestRoleGates_UnresolvedIdentity_FailsClosed(t *testing.T) {
	srv, tk := newRoleGateServer(t)
	unresolved := netid.Identity{} // Resolved=false, injected explicitly
	h := injectIdentityMiddleware(unresolved, srv.Handler())

	routes := []struct{ method, path, body string }{
		{http.MethodPost, "/flows/api/workflow", `{"name":"x","yaml":"","version":""}`},
		{http.MethodDelete, "/flows/api/workflow?name=x", ``},
		{http.MethodPost, "/flows/api/run?name=x", `{}`},
		{http.MethodPost, "/flows/api/cancel", `{"run_id":"run-19700101-000000-aa"}`},
		{http.MethodPost, "/api/files/write", `{"path":"a.txt","content":"x"}`},
		{http.MethodPost, "/kanban/api/add", `{"title":"t"}`},
	}
	for _, rt := range routes {
		rr := doJSON(t, h, tk, rt.method, rt.path, rt.body)
		if rr.Code != http.StatusForbidden {
			t.Errorf("SECURITY: %s %s with an unresolved identity: status=%d body=%s; want 403 (fail closed)", rt.method, rt.path, rr.Code, rr.Body.String())
		}
	}
}

// A bare srv.Handler() (no identity in context at all) is treated as the
// loopback operator, so it does not 403 the same routes.
func TestServerHandler_BareMount_IsLoopbackOperator(t *testing.T) {
	srv, tk := newRoleGateServer(t)
	rr := doJSON(t, srv.Handler(), tk, http.MethodPost, "/flows/api/workflow", `{"name":"x","yaml":"not: [valid","version":""}`)
	if rr.Code == http.StatusForbidden {
		t.Errorf("bare Server.Handler() request was forbidden (%s); it must act as the loopback operator", rr.Body.String())
	}
}

// An explicitly injected low-privilege identity is enforced, not overwritten.
func TestServerHandler_InjectedIdentity_NotOverwritten(t *testing.T) {
	srv, tk := newRoleGateServer(t)
	readOnly := netid.Identity{OperatorID: "ro", Role: netid.RoleRead, Resolved: true, Authenticated: true}
	h := injectIdentityMiddleware(readOnly, srv.Handler())
	rr := doJSON(t, h, tk, http.MethodPost, "/flows/api/workflow", `{"name":"x","yaml":"","version":""}`)
	if rr.Code != http.StatusForbidden {
		t.Errorf("read-only identity: status=%d; want 403 (Handler() must not upgrade an injected identity)", rr.Code)
	}
}
