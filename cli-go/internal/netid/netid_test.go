package netid_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/bakw00ds/yakos/internal/mtls"
	"github.com/bakw00ds/yakos/internal/netid"
)

// ---- Role ordering / Allows -------------------------------------------------

func TestRole_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		role netid.Role
		want string
	}{
		{netid.RoleRead, "read"},
		{netid.RoleDispatch, "dispatch"},
		{netid.RoleFlowsRun, "flows-run"},
		{netid.RoleAdmin, "admin"},
	}
	for _, tc := range cases {
		if got := tc.role.String(); got != tc.want {
			t.Errorf("Role.String()=%q; want %q", got, tc.want)
		}
	}
}

func TestParseRole_KnownValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		s    string
		want netid.Role
	}{
		{"read", netid.RoleRead},
		{"dispatch", netid.RoleDispatch},
		{"flows-run", netid.RoleFlowsRun},
		{"admin", netid.RoleAdmin},
	}
	for _, tc := range cases {
		if got := netid.ParseRole(tc.s); got != tc.want {
			t.Errorf("ParseRole(%q)=%v; want %v", tc.s, got, tc.want)
		}
	}
}

func TestParseRole_Unknown_DefaultsToRead(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"", "superuser", "ADMIN", "ROOT"} {
		if got := netid.ParseRole(s); got != netid.RoleRead {
			t.Errorf("ParseRole(%q)=%v; want RoleRead (unknown → least privilege)", s, got)
		}
	}
}

func TestRole_Allows_SameRole(t *testing.T) {
	t.Parallel()
	roles := []netid.Role{netid.RoleRead, netid.RoleDispatch, netid.RoleFlowsRun, netid.RoleAdmin}
	for _, r := range roles {
		if !r.Allows(r) {
			t.Errorf("%v.Allows(%v) = false; want true (same role should allow itself)", r, r)
		}
	}
}

func TestRole_Allows_Ordering(t *testing.T) {
	t.Parallel()
	// admin allows everything
	if !netid.RoleAdmin.Allows(netid.RoleFlowsRun) {
		t.Error("admin should allow flows-run")
	}
	if !netid.RoleAdmin.Allows(netid.RoleDispatch) {
		t.Error("admin should allow dispatch")
	}
	if !netid.RoleAdmin.Allows(netid.RoleRead) {
		t.Error("admin should allow read")
	}
	// flows-run allows dispatch and read but not admin
	if !netid.RoleFlowsRun.Allows(netid.RoleDispatch) {
		t.Error("flows-run should allow dispatch")
	}
	if !netid.RoleFlowsRun.Allows(netid.RoleRead) {
		t.Error("flows-run should allow read")
	}
	if netid.RoleFlowsRun.Allows(netid.RoleAdmin) {
		t.Error("flows-run should NOT allow admin")
	}
	// dispatch allows read but not flows-run or admin
	if !netid.RoleDispatch.Allows(netid.RoleRead) {
		t.Error("dispatch should allow read")
	}
	if netid.RoleDispatch.Allows(netid.RoleFlowsRun) {
		t.Error("dispatch should NOT allow flows-run")
	}
	if netid.RoleDispatch.Allows(netid.RoleAdmin) {
		t.Error("dispatch should NOT allow admin")
	}
	// read allows nothing higher
	if netid.RoleRead.Allows(netid.RoleDispatch) {
		t.Error("read should NOT allow dispatch")
	}
}

// ---- RoleMapper -------------------------------------------------------------

func TestRoleMapper_MissingFile_DefaultsToRead(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	m := netid.NewRoleMapper(stateDir)
	if got := m.Lookup("alice"); got != netid.RoleRead {
		t.Errorf("Lookup with missing file: got %v; want RoleRead", got)
	}
}

func TestRoleMapper_KnownCN_MapsRole(t *testing.T) {
	t.Parallel()
	stateDir := writeRolesFile(t, map[string]string{
		"alice": "admin",
		"bob":   "dispatch",
		"ci":    "flows-run",
	})
	m := netid.NewRoleMapper(stateDir)
	if got := m.Lookup("alice"); got != netid.RoleAdmin {
		t.Errorf("alice: got %v; want admin", got)
	}
	if got := m.Lookup("bob"); got != netid.RoleDispatch {
		t.Errorf("bob: got %v; want dispatch", got)
	}
	if got := m.Lookup("ci"); got != netid.RoleFlowsRun {
		t.Errorf("ci: got %v; want flows-run", got)
	}
}

func TestRoleMapper_UnknownCN_DefaultsToRead(t *testing.T) {
	t.Parallel()
	stateDir := writeRolesFile(t, map[string]string{
		"alice": "admin",
	})
	m := netid.NewRoleMapper(stateDir)
	if got := m.Lookup("unknown-operator"); got != netid.RoleRead {
		t.Errorf("unknown CN: got %v; want RoleRead", got)
	}
}

func TestRoleMapper_CorruptFile_DefaultsToRead(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	rolesDir := filepath.Join(stateDir, "mtls")
	if err := os.MkdirAll(rolesDir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rolesDir, "roles.json"), []byte("not valid json{{"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	m := netid.NewRoleMapper(stateDir)
	if got := m.Lookup("alice"); got != netid.RoleRead {
		t.Errorf("corrupt file: got %v; want RoleRead (fail-closed)", got)
	}
}

func TestRoleMapper_UnknownRoleString_DefaultsToRead(t *testing.T) {
	t.Parallel()
	stateDir := writeRolesFile(t, map[string]string{
		"dan": "superuser", // not a valid role
	})
	m := netid.NewRoleMapper(stateDir)
	if got := m.Lookup("dan"); got != netid.RoleRead {
		t.Errorf("unknown role string: got %v; want RoleRead", got)
	}
}

func TestRoleMapper_ConcurrentReads_Race(t *testing.T) {
	// Run without t.Parallel() at the outer level so -race can observe this
	// goroutine pattern; inner goroutines do concurrent Lookups.
	stateDir := writeRolesFile(t, map[string]string{
		"alice": "admin",
		"bob":   "dispatch",
	})
	m := netid.NewRoleMapper(stateDir)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.Lookup("alice")
			_ = m.Lookup("bob")
			_ = m.Lookup("unknown")
		}()
	}
	wg.Wait()
}

// ---- CNFromTLS / CNFromRequest ----------------------------------------------

func TestCNFromTLS_Nil_ReturnsFalse(t *testing.T) {
	t.Parallel()
	cn, ok := netid.CNFromTLS(nil)
	if ok || cn != "" {
		t.Errorf("CNFromTLS(nil) = (%q, %v); want (\"\", false)", cn, ok)
	}
}

func TestCNFromTLS_NoVerifiedChains_ReturnsFalse(t *testing.T) {
	t.Parallel()
	cs := &tls.ConnectionState{} // no VerifiedChains
	cn, ok := netid.CNFromTLS(cs)
	if ok || cn != "" {
		t.Errorf("CNFromTLS(empty chains) = (%q, %v); want (\"\", false)", cn, ok)
	}
}

func TestCNFromTLS_VerifiedChain_ReturnsCN(t *testing.T) {
	t.Parallel()
	// Perform a real mTLS handshake via loopback and capture the server-side
	// ConnectionState, which carries VerifiedChains from the client cert.
	cs := performMTLSHandshake(t, "test-operator")
	cn, ok := netid.CNFromTLS(cs)
	if !ok {
		t.Fatal("CNFromTLS: ok=false; want true")
	}
	if cn != "test-operator" {
		t.Errorf("CNFromTLS: CN=%q; want test-operator", cn)
	}
}

func TestCNFromRequest_NilTLS_ReturnsFalse(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	// r.TLS is nil for plain HTTP httptest requests
	cn, ok := netid.CNFromRequest(r)
	if ok || cn != "" {
		t.Errorf("CNFromRequest(plain HTTP) = (%q, %v); want (\"\", false)", cn, ok)
	}
}

// ---- Identity context -------------------------------------------------------

func TestIdentityFrom_NoMiddleware_ReturnsZero(t *testing.T) {
	t.Parallel()
	id := netid.IdentityFrom(context.Background())
	if id.Authenticated {
		t.Error("zero Identity should be unauthenticated")
	}
	if id.OperatorID != "" {
		t.Errorf("zero Identity OperatorID=%q; want empty", id.OperatorID)
	}
	if id.Role != netid.RoleRead {
		t.Errorf("zero Identity Role=%v; want RoleRead", id.Role)
	}
}

// ---- Resolver ---------------------------------------------------------------

func TestResolver_LoopbackBearer_AdminUnauthenticated(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	m := netid.NewRoleMapper(stateDir)
	res := netid.NewResolver(m, func(r *http.Request) string {
		return "local-operator"
	})

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	// No TLS → loopback/bearer path
	id := res.Resolve(r)

	if id.Authenticated {
		t.Error("loopback bearer: Authenticated=true; want false")
	}
	if id.Role != netid.RoleAdmin {
		t.Errorf("loopback bearer: Role=%v; want RoleAdmin", id.Role)
	}
	if id.OperatorID != "local-operator" {
		t.Errorf("loopback bearer: OperatorID=%q; want local-operator", id.OperatorID)
	}
}

func TestResolver_VerifiedClientCert_AuthenticatedWithMappedRole(t *testing.T) {
	t.Parallel()
	// Set up role mapping for the CN we will use.
	stateDir := writeRolesFile(t, map[string]string{
		"alice-workstation": "dispatch",
	})
	m := netid.NewRoleMapper(stateDir)
	res := netid.NewResolver(m, func(r *http.Request) string {
		return "fallback-label"
	})

	cs := performMTLSHandshake(t, "alice-workstation")
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.TLS = cs

	id := res.Resolve(r)

	if !id.Authenticated {
		t.Error("verified cert: Authenticated=false; want true")
	}
	if id.OperatorID != "alice-workstation" {
		t.Errorf("verified cert: OperatorID=%q; want alice-workstation", id.OperatorID)
	}
	if id.Role != netid.RoleDispatch {
		t.Errorf("verified cert: Role=%v; want RoleDispatch", id.Role)
	}
}

func TestResolver_VerifiedClientCert_UnmappedCN_DefaultsToRead(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir() // no roles.json → all default to RoleRead
	m := netid.NewRoleMapper(stateDir)
	res := netid.NewResolver(m, nil)

	cs := performMTLSHandshake(t, "new-operator")
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.TLS = cs

	id := res.Resolve(r)

	if !id.Authenticated {
		t.Error("verified cert: Authenticated=false; want true")
	}
	if id.Role != netid.RoleRead {
		t.Errorf("verified cert, no mapping: Role=%v; want RoleRead (default)", id.Role)
	}
}

func TestResolver_Middleware_StoresIdentityInContext(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	m := netid.NewRoleMapper(stateDir)
	res := netid.NewResolver(m, func(r *http.Request) string {
		return "middleware-operator"
	})

	var capturedID netid.Identity
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = netid.IdentityFrom(r.Context())
	})

	handler := res.Middleware(next)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if capturedID.OperatorID != "middleware-operator" {
		t.Errorf("middleware: OperatorID=%q; want middleware-operator", capturedID.OperatorID)
	}
	if capturedID.Role != netid.RoleAdmin {
		t.Errorf("middleware (loopback): Role=%v; want RoleAdmin", capturedID.Role)
	}
	if capturedID.Authenticated {
		t.Error("middleware (loopback): Authenticated=true; want false")
	}
}

func TestResolver_Middleware_DoesNotGateRequests(t *testing.T) {
	t.Parallel()
	// Verify the middleware is purely additive: a request with no auth still
	// reaches the downstream handler.  Nothing is rejected.
	stateDir := t.TempDir()
	m := netid.NewRoleMapper(stateDir)
	res := netid.NewResolver(m, nil)

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := res.Middleware(next)
	r := httptest.NewRequest(http.MethodGet, "/sensitive-path", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if !called {
		t.Error("middleware gated the request; want pass-through (no enforcement yet)")
	}
	if w.Code != http.StatusOK {
		t.Errorf("middleware: status=%d; want 200 (no enforcement)", w.Code)
	}
}

// ---- helpers ----------------------------------------------------------------

// writeRolesFile creates <stateDir>/mtls/roles.json with the given mapping and
// returns stateDir.
func writeRolesFile(t *testing.T, mapping map[string]string) string {
	t.Helper()
	stateDir := t.TempDir()
	dir := filepath.Join(stateDir, "mtls")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, err := json.Marshal(mapping)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "roles.json"), data, 0600); err != nil {
		t.Fatalf("write roles.json: %v", err)
	}
	return stateDir
}

// performMTLSHandshake performs a real mTLS handshake via loopback using fresh
// CA/cert material generated from the mtls package.  clientCN is the Common
// Name set on the client certificate.
//
// Returns the server-side *tls.ConnectionState, which contains VerifiedChains
// populated from the verified client certificate.  This gives tests a real
// ConnectionState to pass to CNFromTLS / Resolver.Resolve without mocking
// the TLS stack.
func performMTLSHandshake(t *testing.T, clientCN string) *tls.ConnectionState {
	t.Helper()

	dir := t.TempDir()
	caCert, caKey, err := mtls.LoadOrGenerateCA(dir)
	if err != nil {
		t.Fatalf("LoadOrGenerateCA: %v", err)
	}
	serverCert, err := mtls.IssueServerCert(caCert, caKey, []string{"127.0.0.1"})
	if err != nil {
		t.Fatalf("IssueServerCert: %v", err)
	}
	clientCert, err := mtls.IssueClientCert(caCert, caKey, clientCN)
	if err != nil {
		t.Fatalf("IssueClientCert(%q): %v", clientCN, err)
	}

	caPool := mtls.CertPoolFromCert(caCert)
	serverTLSCfg := mtls.BuildServerTLSConfig(serverCert, caPool)
	clientTLSCfg := mtls.BuildClientTLSConfig(clientCert, caPool)

	ln, err := tls.Listen("tcp", "127.0.0.1:0", serverTLSCfg)
	if err != nil {
		t.Fatalf("tls.Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	stateCh := make(chan *tls.ConnectionState, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			stateCh <- nil
			return
		}
		defer func() { _ = conn.Close() }()
		tlsConn := conn.(*tls.Conn)
		if err := tlsConn.Handshake(); err != nil {
			stateCh <- nil
			return
		}
		cs := tlsConn.ConnectionState()
		stateCh <- &cs
	}()

	clientConn, err := tls.Dial("tcp", ln.Addr().String(), clientTLSCfg)
	if err != nil {
		t.Fatalf("tls.Dial: %v", err)
	}
	t.Cleanup(func() { _ = clientConn.Close() })

	cs := <-stateCh
	if cs == nil {
		t.Fatal("server did not complete TLS handshake")
	}

	// Validate the VerifiedChains are populated as expected.
	if len(cs.VerifiedChains) == 0 {
		t.Fatal("server ConnectionState: no VerifiedChains; mTLS handshake did not complete client auth")
	}
	leaf := cs.VerifiedChains[0][0]
	if leaf.Subject.CommonName != clientCN {
		t.Fatalf("server ConnectionState: CN=%q; want %q", leaf.Subject.CommonName, clientCN)
	}

	// Also confirm that CertPoolFromCert works as expected (belt-and-suspenders).
	opts := x509.VerifyOptions{
		Roots:     caPool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	if _, err := leaf.Verify(opts); err != nil {
		t.Fatalf("client cert verification sanity check failed: %v", err)
	}

	return cs
}
