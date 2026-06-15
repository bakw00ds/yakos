package netid_test

// phase3g_test.go — Phase 3g defense-in-depth tests for RoleNone.
//
// These tests verify the specific security properties introduced in Phase 3g:
//
//  1. RoleNone.Allows(x) is false for every real role including RoleRead.
//  2. An unauthenticated networked identity (RoleNone) is rejected by
//     requireRole(RoleRead) even when requireAuthOrRedirect is bypassed —
//     i.e. the role layer is ALSO fail-closed for unauthenticated identities.
//  3. Authenticated session/cert users with real roles still pass their routes
//     (regression: Phase 3g must not break legitimate flows).
//  4. Loopback unauthenticated cooperative identity is unchanged (still
//     produces RoleAdmin, not RoleNone).
//  5. RoleNone is not parseable from any config string (ParseRole("none") must
//     return RoleRead, not RoleNone).
//  6. IsAssignableRole rejects RoleNone; accepts all four real roles.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bakw00ds/yakos/internal/netid"
)

// ---- 1. RoleNone.Allows is false for every real role -----------------------

func TestRoleNone_AllowsNothing(t *testing.T) {
	t.Parallel()
	realRoles := []struct {
		name string
		role netid.Role
	}{
		{"read", netid.RoleRead},
		{"dispatch", netid.RoleDispatch},
		{"flows-run", netid.RoleFlowsRun},
		{"admin", netid.RoleAdmin},
	}
	for _, tc := range realRoles {
		if netid.RoleNone.Allows(tc.role) {
			t.Errorf("RoleNone.Allows(%s) = true; want false (Phase 3g: unauthenticated identity must fail all role checks)", tc.name)
		}
	}
}

// ---- 2. requireRole rejects RoleNone even if requireAuthOrRedirect bypassed -

// simulatedRequireRole is an in-package simulation of the consoleui.requireRole
// logic so we can test the role-layer fail-closed property without importing
// consoleui (which would create a circular test dependency).
//
// The actual production requireRole gates on id.Resolved before calling Allows.
// This simulation matches that logic exactly.
func simulatedRequireRole(required netid.Role, id netid.Identity) bool {
	// If the resolver did not stamp the identity, let it through (Resolved=false
	// means "test / loopback via srv.Handler() — no enforcement").
	if !id.Resolved {
		return true // not blocked
	}
	return id.Role.Allows(required)
}

// TestRoleNone_RejectsRoleRead_EvenIfAuthOrRedirectBypassed is the critical
// Phase 3g regression test.  It directly exercises the role-layer logic with a
// RoleNone, Resolved=true identity — simulating what happens if a future route
// is registered without requireAuthOrRedirect in front of it.
//
// The identity must be REJECTED by requireRole(RoleRead) even though the
// primary requireAuthOrRedirect gate is absent.
func TestRoleNone_RejectsRoleRead_EvenIfAuthOrRedirectBypassed(t *testing.T) {
	t.Parallel()

	unauthNetworked := netid.Identity{
		OperatorID:    "",
		Role:          netid.RoleNone,
		Authenticated: false,
		Resolved:      true, // resolver did stamp it — enforcement fires
		AuthMethod:    netid.AuthMethodNone,
	}

	roles := []struct {
		name string
		role netid.Role
	}{
		{"read", netid.RoleRead},
		{"dispatch", netid.RoleDispatch},
		{"flows-run", netid.RoleFlowsRun},
		{"admin", netid.RoleAdmin},
	}
	for _, tc := range roles {
		if simulatedRequireRole(tc.role, unauthNetworked) {
			t.Errorf("requireRole(%s) passed an unauthenticated networked identity (RoleNone, Resolved=true); want rejected", tc.name)
		}
	}
}

// ---- 3. Authenticated users with real roles still pass ----------------------

func TestRoleNone_AuthenticatedUsers_Unaffected(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		identity netid.Identity
		required netid.Role
		want     bool
	}{
		{
			name: "cert-admin-passes-admin",
			identity: netid.Identity{
				Role: netid.RoleAdmin, Authenticated: true,
				Resolved: true, AuthMethod: netid.AuthMethodCert,
			},
			required: netid.RoleAdmin,
			want:     true,
		},
		{
			name: "session-dispatch-passes-read",
			identity: netid.Identity{
				Role: netid.RoleDispatch, Authenticated: true,
				Resolved: true, AuthMethod: netid.AuthMethodSession,
			},
			required: netid.RoleRead,
			want:     true,
		},
		{
			name: "cert-read-blocked-on-dispatch",
			identity: netid.Identity{
				Role: netid.RoleRead, Authenticated: true,
				Resolved: true, AuthMethod: netid.AuthMethodCert,
			},
			required: netid.RoleDispatch,
			want:     false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := simulatedRequireRole(tc.required, tc.identity)
			if got != tc.want {
				t.Errorf("simulatedRequireRole(%v) with %+v: got %v; want %v", tc.required, tc.identity, got, tc.want)
			}
		})
	}
}

// ---- 4. Loopback identity unchanged: RoleAdmin, not RoleNone ---------------

func TestRoleNone_LoopbackUnchanged(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	m := netid.NewRoleMapper(stateDir)
	res := netid.NewResolver(m, func(r *http.Request) string {
		return "local-op"
	}, true /* loopbackTrusted */)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	id := res.Resolve(r)

	if id.Role == netid.RoleNone {
		t.Error("loopback identity: Role=RoleNone; want RoleAdmin (loopback behavior must be unchanged by Phase 3g)")
	}
	if id.Role != netid.RoleAdmin {
		t.Errorf("loopback identity: Role=%v; want RoleAdmin", id.Role)
	}
	if id.Authenticated {
		t.Error("loopback identity: Authenticated=true; want false (cooperative, not cryptographic)")
	}
}

// ---- 5. ParseRole("none") must NOT return RoleNone -------------------------

func TestRoleNone_NotParseable(t *testing.T) {
	t.Parallel()
	// "none" is the String() of RoleNone, but ParseRole must not map it to
	// RoleNone — it must fall through to the least-assignable default (RoleRead).
	// This prevents any config file or URL parameter from assigning RoleNone to
	// a real user.
	got := netid.ParseRole("none")
	if got == netid.RoleNone {
		t.Error(`ParseRole("none") returned RoleNone; want RoleRead (RoleNone is not assignable via config strings)`)
	}
	if got != netid.RoleRead {
		t.Errorf(`ParseRole("none") = %v; want RoleRead (unknown strings → least assignable privilege)`, got)
	}
}

// ---- 6. IsAssignableRole: rejects RoleNone, accepts the four real roles ----

func TestIsAssignableRole(t *testing.T) {
	t.Parallel()

	rejectCases := []struct {
		name string
		role netid.Role
	}{
		{"none", netid.RoleNone},
		{"unknown-negative", netid.Role(-1)},
		{"out-of-range-high", netid.Role(999)},
	}
	for _, tc := range rejectCases {
		if netid.IsAssignableRole(tc.role) {
			t.Errorf("IsAssignableRole(%v) = true; want false (not a valid user role)", tc.name)
		}
	}

	acceptCases := []struct {
		name string
		role netid.Role
	}{
		{"read", netid.RoleRead},
		{"dispatch", netid.RoleDispatch},
		{"flows-run", netid.RoleFlowsRun},
		{"admin", netid.RoleAdmin},
	}
	for _, tc := range acceptCases {
		if !netid.IsAssignableRole(tc.role) {
			t.Errorf("IsAssignableRole(%v) = false; want true (valid user role)", tc.name)
		}
	}
}

// ---- 7. Resolver produces RoleNone on the two fail-closed networked paths --

func TestResolver_NetworkedFailClosed_ProducesRoleNone(t *testing.T) {
	t.Parallel()

	t.Run("no-session-fn", func(t *testing.T) {
		t.Parallel()
		m := netid.NewRoleMapper("")
		res := netid.NewResolver(m, nil, false /* loopbackTrusted=false */)
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		id := res.Resolve(r)
		if id.Role != netid.RoleNone {
			t.Errorf("no-session-fn fail-closed: Role=%v; want RoleNone", id.Role)
		}
		if id.Authenticated {
			t.Error("no-session-fn fail-closed: Authenticated=true; want false")
		}
		if !id.Resolved {
			t.Error("no-session-fn fail-closed: Resolved=false; want true")
		}
	})

	t.Run("session-fn-returns-false", func(t *testing.T) {
		t.Parallel()
		m := netid.NewRoleMapper("")
		sessionFn := netid.SessionLookupFn(func(r *http.Request) (string, netid.Role, bool) {
			return "", netid.RoleRead, false // expired / invalid session
		})
		res := netid.NewResolverWithSession(m, nil, false, sessionFn)
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		id := res.Resolve(r)
		if id.Role != netid.RoleNone {
			t.Errorf("invalid-session fail-closed: Role=%v; want RoleNone", id.Role)
		}
		if id.Authenticated {
			t.Error("invalid-session fail-closed: Authenticated=true; want false")
		}
		if !id.Resolved {
			t.Error("invalid-session fail-closed: Resolved=false; want true")
		}
	})
}

// ---- 8. Zero-value Identity (Resolved=false) is not blocked by role gates --

// This test ensures the Resolved=false guard in requireRole is still respected
// after Phase 3g.  The zero-value Identity has Role=RoleNone and Resolved=false;
// requireRole must pass it through (loopback invariant).
func TestRoleNone_ZeroIdentity_NotBlockedByRoleGate(t *testing.T) {
	t.Parallel()

	zero := netid.Identity{} // Resolved=false, Role=RoleNone
	if zero.Resolved {
		t.Fatal("zero Identity.Resolved should be false; test precondition violated")
	}
	if zero.Role != netid.RoleNone {
		t.Fatalf("zero Identity.Role=%v; want RoleNone (test precondition)", zero.Role)
	}

	// Simulate requireRole: only enforces when Resolved==true.
	passed := simulatedRequireRole(netid.RoleRead, zero)
	if !passed {
		t.Error("zero Identity (Resolved=false, RoleNone) was blocked by requireRole; loopback invariant violated")
	}
}

// ---- 9. requireRole HTTP handler rejects RoleNone identity ----------------

// TestRequireRole_RoleNone_HTTP verifies that a real HTTP handler wrapped with
// the consoleui requireRole logic (simulated here via WithIdentityForTest +
// the role gate from netid.Allows) returns 403 for a RoleNone identity with
// Resolved=true.
//
// This is an integration-style check at the netid layer: it uses
// WithIdentityForTest (exported from netid for tests) to inject the identity
// and verifies Allows rejects it.
func TestRequireRole_RoleNone_HTTP(t *testing.T) {
	t.Parallel()

	// Build a handler that checks Resolved + Allows (mirrors consoleui.requireRole).
	gateHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := netid.IdentityFrom(r.Context())
		if id.Resolved && !id.Role.Allows(netid.RoleRead) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	// Unauthenticated networked identity: RoleNone, Resolved=true.
	unauthID := netid.Identity{
		Role:     netid.RoleNone,
		Resolved: true,
	}
	req := httptest.NewRequest(http.MethodGet, "/sensitive", nil)
	req = req.WithContext(netid.WithIdentityForTest(req.Context(), unauthID))
	rec := httptest.NewRecorder()
	gateHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("RoleNone identity on gated route: got %d; want 403 Forbidden (defense-in-depth)", rec.Code)
	}
}
