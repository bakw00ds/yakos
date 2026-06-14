package netid_test

// resolver_session_test.go — Phase 2 (ADR-0005) tests for the session-auth
// path and AuthMethod discriminator in netid.Resolver / netid.Identity.
//
// Coverage:
//  1. AuthMethod.String() values (none / cert / session).
//  2. NewResolver (nil sessionLookupFn): cert path → AuthMethodCert.
//  3. NewResolver (nil sessionLookupFn): loopback + no cert → AuthMethodNone.
//  4. NewResolver (nil sessionLookupFn): networked + no cert → AuthMethodNone.
//  5. Cert present + session fn also returns ok → cert wins (AuthMethodCert),
//     session fn is NOT consulted (i.e. consulted-flag stays false).
//  6. No cert + valid session, networked → AuthMethodSession, correct
//     OperatorID/Role.
//  7. No cert + session fn ok=false, networked → fall-closed, AuthMethodNone.
//  8. Loopback-trusted + session fn set → session fn NOT consulted; identity
//     is loopback-admin with AuthMethodNone (loopback isolation invariant).
//  9. NewResolverWithSession with nil sessionLookupFn == NewResolver behavior.

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/bakw00ds/yakos/internal/netid"
)

// ---- AuthMethod.String() ----------------------------------------------------

func TestAuthMethod_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		m    netid.AuthMethod
		want string
	}{
		{netid.AuthMethodNone, "none"},
		{netid.AuthMethodCert, "cert"},
		{netid.AuthMethodSession, "session"},
	}
	for _, tc := range cases {
		if got := tc.m.String(); got != tc.want {
			t.Errorf("AuthMethod(%d).String()=%q; want %q", tc.m, got, tc.want)
		}
	}
}

// TestAuthMethod_ZeroValue confirms the zero value of AuthMethod is AuthMethodNone
// and its string is "none" — important because existing Identity struct literals
// that don't set AuthMethod should serialise as "none".
func TestAuthMethod_ZeroValue(t *testing.T) {
	t.Parallel()
	var m netid.AuthMethod
	if m != netid.AuthMethodNone {
		t.Errorf("zero AuthMethod = %v; want AuthMethodNone", m)
	}
	if got := m.String(); got != "none" {
		t.Errorf("zero AuthMethod.String()=%q; want \"none\"", got)
	}
}

// ---- NewResolver (existing constructor) — AuthMethod stamping ---------------

// TestResolver_NewResolver_Cert_StampsAuthMethodCert verifies that NewResolver
// (no session fn) stamps AuthMethodCert when a verified cert is present.
func TestResolver_NewResolver_Cert_StampsAuthMethodCert(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	writeRolesFile(t, stateDir, map[string]string{"alice": "dispatch"})
	m := netid.NewRoleMapper(stateDir)
	res := netid.NewResolver(m, nil, false)

	cs := performMTLSHandshake(t, "alice")
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.TLS = cs

	id := res.Resolve(r)

	if id.AuthMethod != netid.AuthMethodCert {
		t.Errorf("cert present: AuthMethod=%v; want AuthMethodCert", id.AuthMethod)
	}
	if !id.Authenticated {
		t.Error("cert present: Authenticated=false; want true")
	}
	if id.OperatorID != "alice" {
		t.Errorf("cert present: OperatorID=%q; want alice", id.OperatorID)
	}
}

// TestResolver_NewResolver_Loopback_StampsAuthMethodNone verifies that the
// loopback path via NewResolver produces AuthMethodNone (zero value).
func TestResolver_NewResolver_Loopback_StampsAuthMethodNone(t *testing.T) {
	t.Parallel()
	m := netid.NewRoleMapper("")
	res := netid.NewResolver(m, func(r *http.Request) string { return "local-op" }, true)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	id := res.Resolve(r)

	if id.AuthMethod != netid.AuthMethodNone {
		t.Errorf("loopback: AuthMethod=%v; want AuthMethodNone", id.AuthMethod)
	}
	if id.Authenticated {
		t.Error("loopback: Authenticated=true; want false")
	}
	if id.Role != netid.RoleAdmin {
		t.Errorf("loopback: Role=%v; want RoleAdmin", id.Role)
	}
}

// TestResolver_NewResolver_NetworkedNoCert_StampsAuthMethodNone verifies that
// the fail-closed path (networked, no cert, no session fn) produces AuthMethodNone.
func TestResolver_NewResolver_NetworkedNoCert_StampsAuthMethodNone(t *testing.T) {
	t.Parallel()
	m := netid.NewRoleMapper("")
	res := netid.NewResolver(m, nil, false /* loopbackTrusted=false */)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	id := res.Resolve(r)

	if id.AuthMethod != netid.AuthMethodNone {
		t.Errorf("networked no-cert: AuthMethod=%v; want AuthMethodNone", id.AuthMethod)
	}
	if id.Authenticated {
		t.Error("networked no-cert: Authenticated=true; want false")
	}
	if id.Role != netid.RoleRead {
		t.Errorf("networked no-cert: Role=%v; want RoleRead (fail-closed)", id.Role)
	}
}

// ---- Session-path security invariants (security-critical) -------------------

// TestResolver_CertBeatsSession verifies the primary security invariant:
// when a verified client certificate is present, the session lookup function
// is NOT consulted and the identity comes from the cert (AuthMethodCert).
//
// This prevents a machine presenting a cert from being silently downgraded
// to a stray browser session.
func TestResolver_CertBeatsSession(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	writeRolesFile(t, stateDir, map[string]string{"machine": "flows-run"})
	m := netid.NewRoleMapper(stateDir)

	var sessionCalled atomic.Bool
	sessionFn := netid.SessionLookupFn(func(r *http.Request) (string, netid.Role, bool) {
		sessionCalled.Store(true)
		return "browser-user", netid.RoleRead, true // would-match if consulted
	})

	res := netid.NewResolverWithSession(m, nil, false, sessionFn)

	cs := performMTLSHandshake(t, "machine")
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.TLS = cs

	id := res.Resolve(r)

	// Session fn must NOT have been called.
	if sessionCalled.Load() {
		t.Error("session fn was called when a cert was present; cert MUST beat session (session fn must not be consulted)")
	}

	if id.AuthMethod != netid.AuthMethodCert {
		t.Errorf("cert present: AuthMethod=%v; want AuthMethodCert", id.AuthMethod)
	}
	if id.OperatorID != "machine" {
		t.Errorf("cert present: OperatorID=%q; want machine", id.OperatorID)
	}
	if id.Role != netid.RoleFlowsRun {
		t.Errorf("cert present: Role=%v; want RoleFlowsRun", id.Role)
	}
	if !id.Authenticated {
		t.Error("cert present: Authenticated=false; want true")
	}
}

// TestResolver_SessionPath_ValidSession_AuthMethodSession verifies the happy
// path: no cert, valid session, non-loopback → AuthMethodSession with the
// operatorID and role returned by the session fn.
func TestResolver_SessionPath_ValidSession_AuthMethodSession(t *testing.T) {
	t.Parallel()
	m := netid.NewRoleMapper("")
	sessionFn := netid.SessionLookupFn(func(r *http.Request) (string, netid.Role, bool) {
		return "carol", netid.RoleDispatch, true
	})
	res := netid.NewResolverWithSession(m, nil, false /* loopbackTrusted=false */, sessionFn)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	// No r.TLS → no cert.
	id := res.Resolve(r)

	if id.AuthMethod != netid.AuthMethodSession {
		t.Errorf("valid session: AuthMethod=%v; want AuthMethodSession", id.AuthMethod)
	}
	if !id.Authenticated {
		t.Error("valid session: Authenticated=false; want true")
	}
	if id.OperatorID != "carol" {
		t.Errorf("valid session: OperatorID=%q; want carol", id.OperatorID)
	}
	if id.Role != netid.RoleDispatch {
		t.Errorf("valid session: Role=%v; want RoleDispatch", id.Role)
	}
	if !id.Resolved {
		t.Error("valid session: Resolved=false; want true")
	}
}

// TestResolver_SessionPath_InvalidSession_FailClosed verifies that when the
// session fn returns ok=false, the resolver falls through to the fail-closed
// identity (RoleRead, Authenticated=false, AuthMethodNone).
func TestResolver_SessionPath_InvalidSession_FailClosed(t *testing.T) {
	t.Parallel()
	m := netid.NewRoleMapper("")
	sessionFn := netid.SessionLookupFn(func(r *http.Request) (string, netid.Role, bool) {
		return "", netid.RoleRead, false // expired / absent session
	})
	res := netid.NewResolverWithSession(m, nil, false, sessionFn)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	id := res.Resolve(r)

	if id.Authenticated {
		t.Error("invalid session: Authenticated=true; want false (fail-closed)")
	}
	if id.Role != netid.RoleRead {
		t.Errorf("invalid session: Role=%v; want RoleRead (fail-closed)", id.Role)
	}
	if id.AuthMethod != netid.AuthMethodNone {
		t.Errorf("invalid session: AuthMethod=%v; want AuthMethodNone", id.AuthMethod)
	}
	if id.OperatorID != "" {
		t.Errorf("invalid session: OperatorID=%q; want empty", id.OperatorID)
	}
	if !id.Resolved {
		t.Error("invalid session: Resolved=false; want true")
	}
}

// TestResolver_LoopbackIsolation_SessionFnNotConsulted is the critical loopback
// isolation test: even when a sessionLookupFn is injected, the loopback path
// (loopbackTrusted=true, no cert) must NOT consult the session fn and must
// return the unchanged loopback-admin identity with AuthMethodNone.
//
// This test proves that the session injection cannot affect the loopback path.
func TestResolver_LoopbackIsolation_SessionFnNotConsulted(t *testing.T) {
	t.Parallel()
	m := netid.NewRoleMapper("")

	var sessionCalled atomic.Bool
	sessionFn := netid.SessionLookupFn(func(r *http.Request) (string, netid.Role, bool) {
		sessionCalled.Store(true)
		return "intruder", netid.RoleAdmin, true
	})

	// loopbackTrusted=true with a session fn injected.
	res := netid.NewResolverWithSession(m, func(r *http.Request) string { return "loop-op" }, true, sessionFn)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	// No r.TLS → no cert.
	id := res.Resolve(r)

	// Session fn must NOT have been called.
	if sessionCalled.Load() {
		t.Error("session fn was called on a loopback-trusted resolver; loopback path MUST NOT consult the session fn")
	}

	// Identity must be the unchanged loopback-admin identity.
	if id.AuthMethod != netid.AuthMethodNone {
		t.Errorf("loopback: AuthMethod=%v; want AuthMethodNone", id.AuthMethod)
	}
	if id.Authenticated {
		t.Error("loopback: Authenticated=true; want false")
	}
	if id.Role != netid.RoleAdmin {
		t.Errorf("loopback: Role=%v; want RoleAdmin (unchanged loopback behavior)", id.Role)
	}
	if id.OperatorID != "loop-op" {
		t.Errorf("loopback: OperatorID=%q; want loop-op", id.OperatorID)
	}
}

// TestResolver_NewResolverWithSession_NilFn_EqualsNewResolver verifies that
// NewResolverWithSession with a nil sessionLookupFn produces the same result
// as NewResolver for all three existing paths (cert / loopback / fail-closed).
func TestResolver_NewResolverWithSession_NilFn_EqualsNewResolver(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	writeRolesFile(t, stateDir, map[string]string{"dave": "admin"})
	m := netid.NewRoleMapper(stateDir)
	label := func(r *http.Request) string { return "loop-op" }

	// cert path
	t.Run("cert", func(t *testing.T) {
		t.Parallel()
		res := netid.NewResolverWithSession(m, label, false, nil)
		cs := performMTLSHandshake(t, "dave")
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.TLS = cs
		id := res.Resolve(r)
		if id.AuthMethod != netid.AuthMethodCert {
			t.Errorf("cert: AuthMethod=%v; want AuthMethodCert", id.AuthMethod)
		}
		if !id.Authenticated || id.OperatorID != "dave" || id.Role != netid.RoleAdmin {
			t.Errorf("cert: unexpected identity %+v", id)
		}
	})

	// loopback path
	t.Run("loopback", func(t *testing.T) {
		t.Parallel()
		res := netid.NewResolverWithSession(m, label, true, nil)
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		id := res.Resolve(r)
		if id.AuthMethod != netid.AuthMethodNone {
			t.Errorf("loopback: AuthMethod=%v; want AuthMethodNone", id.AuthMethod)
		}
		if id.Authenticated || id.Role != netid.RoleAdmin || id.OperatorID != "loop-op" {
			t.Errorf("loopback: unexpected identity %+v", id)
		}
	})

	// fail-closed path
	t.Run("fail-closed", func(t *testing.T) {
		t.Parallel()
		res := netid.NewResolverWithSession(m, nil, false, nil)
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		id := res.Resolve(r)
		if id.AuthMethod != netid.AuthMethodNone {
			t.Errorf("fail-closed: AuthMethod=%v; want AuthMethodNone", id.AuthMethod)
		}
		if id.Authenticated || id.Role != netid.RoleRead || id.OperatorID != "" {
			t.Errorf("fail-closed: unexpected identity %+v", id)
		}
	})
}

// TestResolver_SessionPath_RoleVariants verifies that all four roles are
// correctly threaded through from the session fn to the resulting Identity.
func TestResolver_SessionPath_RoleVariants(t *testing.T) {
	t.Parallel()
	cases := []struct {
		role netid.Role
	}{
		{netid.RoleRead},
		{netid.RoleDispatch},
		{netid.RoleFlowsRun},
		{netid.RoleAdmin},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.role.String(), func(t *testing.T) {
			t.Parallel()
			m := netid.NewRoleMapper("")
			fn := netid.SessionLookupFn(func(r *http.Request) (string, netid.Role, bool) {
				return "user", tc.role, true
			})
			res := netid.NewResolverWithSession(m, nil, false, fn)
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			id := res.Resolve(r)
			if id.Role != tc.role {
				t.Errorf("session role=%v: got %v", tc.role, id.Role)
			}
			if id.AuthMethod != netid.AuthMethodSession {
				t.Errorf("session role=%v: AuthMethod=%v; want AuthMethodSession", tc.role, id.AuthMethod)
			}
		})
	}
}
