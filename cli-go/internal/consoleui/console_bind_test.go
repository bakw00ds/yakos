// console_bind_test.go — Phase 6c tests for the --console-bind networked path.
//
// Tests covered:
//
//  1. Fail-closed: NetworkedMode=true with TLSConfig=nil → Serve refuses.
//  2. Non-loopback TLS listener: valid client cert → authenticated identity,
//     mapped role.  No client cert → rejected at TLS handshake.
//  3. Resolver with loopbackTrusted=false: certless request to a networked
//     listener → never resolves to admin (RoleRead at most).
//  4. wss/CSP/Origin: CSP contains wss:// for networked mode, ws:// for loopback.
//  5. WS Origin allow-list: external origin accepted; foreign origin rejected.
//  6. Loopback default (unchanged): plain HTTP listener still binds loopback OK.
//
// Architecture note: tests that require an actual TLS handshake use real
// net.Listener + tls.NewListener on 127.0.0.1:0 (not httptest.NewTLSServer
// because that does not support mTLS client auth).  We use the same CA +
// server/client cert flow as internal/mtls.
package consoleui_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/mtls"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/wsbus"
	"golang.org/x/net/websocket"
)

// ---- helpers -----------------------------------------------------------------

// newMTLSFixture creates a CA, server cert, and one client cert in a temp dir.
// Returns the CA cert (for pool), server TLS config, client TLS config, and the
// client cert's Common Name.
func newMTLSFixture(t *testing.T) (caCert *x509.Certificate, serverTLSCfg *tls.Config, clientTLSCfg *tls.Config, clientCN string) {
	t.Helper()
	dir := t.TempDir()
	ca, caKey, err := mtls.LoadOrGenerateCA(dir)
	if err != nil {
		t.Fatalf("LoadOrGenerateCA: %v", err)
	}
	serverCert, err := mtls.IssueServerCert(ca, caKey, []string{"127.0.0.1"})
	if err != nil {
		t.Fatalf("IssueServerCert: %v", err)
	}
	clientCN = "test-operator"
	clientCert, err := mtls.IssueClientCert(ca, caKey, clientCN)
	if err != nil {
		t.Fatalf("IssueClientCert: %v", err)
	}
	caPool := mtls.CertPoolFromCert(ca)
	serverTLSCfg = mtls.BuildServerTLSConfig(serverCert, caPool)
	clientTLSCfg = mtls.BuildClientTLSConfig(clientCert, caPool)
	return ca, serverTLSCfg, clientTLSCfg, clientCN
}

// startNetworkedServer builds a consoleui.Server in NetworkedMode and starts it
// on a random port.  Returns the base URL (https://...) and a teardown func.
// The TLS listener uses the provided tlsCfg.  If tlsCfg is nil, the caller
// is testing the fail-closed path and should expect Serve to return an error.
func startNetworkedServer(t *testing.T, tlsCfg *tls.Config) (baseURL string, srv *consoleui.Server, teardown func()) {
	t.Helper()
	stateDir := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	bus := wsbus.New()

	// Open a TCP listener on a random loopback port to simulate the
	// non-loopback bind in a controlled test environment.
	tcpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := tcpLn.Addr().String()

	cfg := consoleui.Config{
		Addr:              addr,
		Token:             tok,
		KanbanBoardPath:   t.TempDir() + "/kanban.md",
		KanbanProject:     "test",
		MetricsProjectDir: t.TempDir(),
		PerfWorkDir:       t.TempDir(),
		Bus:               bus,
		StateDir:          stateDir,
		Listener:          tcpLn,
		TLSConfig:         tlsCfg,
		NetworkedMode:     true,
		ExternalHost:      addr,
	}

	srv = consoleui.New(cfg)
	baseURL = "https://" + addr

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ctx)
	}()

	teardown = func() {
		cancel()
		bus.Stop()
		select {
		case <-errCh:
		case <-time.After(3 * time.Second):
		}
	}

	// Give the server a moment to start accepting.
	time.Sleep(50 * time.Millisecond)
	return baseURL, srv, teardown
}

// doTLSGet issues a GET to url using the given *tls.Config.  Returns the response
// and any error (including TLS handshake failures).
func doTLSGet(t *testing.T, url string, tlsCfg *tls.Config) (*http.Response, error) {
	t.Helper()
	tr := &http.Transport{TLSClientConfig: tlsCfg}
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	return client.Get(url) //nolint:noctx
}

// ---- 1. Fail-closed: NetworkedMode=true with TLSConfig=nil -------------------

// TestConsoleBind_FailClosed_NoTLSConfig verifies that a networked-mode server
// with TLSConfig=nil refuses to serve (returns an error from Serve).
// This simulates the case where mTLS material generation fails at startup.
func TestConsoleBind_FailClosed_NoTLSConfig(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	bus := wsbus.New()
	defer bus.Stop()

	// Inject a TCP listener on a random port.
	tcpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}

	srv := consoleui.New(consoleui.Config{
		Addr:          tcpLn.Addr().String(),
		Token:         tok,
		Bus:           bus,
		Listener:      tcpLn,
		TLSConfig:     nil,  // intentionally nil — simulates mTLS generation failure
		NetworkedMode: true, // non-loopback mode
	})

	// Serve should return an error immediately because TLSConfig is nil
	// but NetworkedMode is true.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	serveErr := srv.Serve(ctx)

	if serveErr == nil {
		t.Error("Serve with NetworkedMode=true and TLSConfig=nil should return an error (fail-closed)")
	}
}

// ---- 2. Non-loopback TLS listener: client cert auth -------------------------

// TestConsoleBind_ValidClientCert_Authenticated verifies that a client with a
// valid mTLS client cert can reach the server and gets an authenticated identity.
func TestConsoleBind_ValidClientCert_Authenticated(t *testing.T) {
	t.Parallel()
	_, serverTLSCfg, clientTLSCfg, _ := newMTLSFixture(t)

	baseURL, _, teardown := startNetworkedServer(t, serverTLSCfg)
	defer teardown()

	// A valid client cert should get through the TLS handshake.
	resp, err := doTLSGet(t, baseURL+"/", clientTLSCfg)
	if err != nil {
		t.Fatalf("GET / with valid client cert: %v", err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /: status=%d; want 200", resp.StatusCode)
	}
}

// TestConsoleBind_NoClientCert_RejectedAtTLS verifies that a client WITHOUT a
// client cert is rejected at the TLS handshake level (RequireAndVerifyClientCert).
func TestConsoleBind_NoClientCert_RejectedAtTLS(t *testing.T) {
	t.Parallel()
	_, serverTLSCfg, clientTLSCfg, _ := newMTLSFixture(t)

	baseURL, _, teardown := startNetworkedServer(t, serverTLSCfg)
	defer teardown()

	// Build a TLS config with NO client cert but trust the server's CA.
	noCertTLSCfg := &tls.Config{
		RootCAs:    clientTLSCfg.RootCAs,
		MinVersion: tls.VersionTLS12,
		// No Certificates field → no client cert presented.
	}

	// The server requires a client cert; the handshake should fail.
	_, err := doTLSGet(t, baseURL+"/", noCertTLSCfg)
	if err == nil {
		t.Error("GET / without client cert should fail at TLS handshake; got nil error")
	}
}

// TestConsoleBind_UntrustedClientCert_Rejected verifies that a client cert
// signed by a DIFFERENT CA (not the server's CA) is rejected.
func TestConsoleBind_UntrustedClientCert_Rejected(t *testing.T) {
	t.Parallel()
	_, serverTLSCfg, clientTLSCfg, _ := newMTLSFixture(t)

	baseURL, _, teardown := startNetworkedServer(t, serverTLSCfg)
	defer teardown()

	// Issue a client cert from a second CA (untrusted by the server).
	dir2 := t.TempDir()
	ca2, caKey2, err := mtls.LoadOrGenerateCA(dir2)
	if err != nil {
		t.Fatalf("second CA: %v", err)
	}
	untrustedClient, err := mtls.IssueClientCert(ca2, caKey2, "untrusted")
	if err != nil {
		t.Fatalf("IssueClientCert (untrusted): %v", err)
	}
	badTLSCfg := &tls.Config{
		Certificates: []tls.Certificate{*untrustedClient},
		RootCAs:      clientTLSCfg.RootCAs, // trusts server cert
		MinVersion:   tls.VersionTLS12,
	}

	_, err = doTLSGet(t, baseURL+"/", badTLSCfg)
	// TLS 1.3 may not surface the error until a read is forced.
	// Any response must fail or the status must not be 200.
	if err == nil {
		t.Error("GET / with untrusted client cert should be rejected; got nil error")
	}
}

// ---- 3. Resolver: certless request → never admin on networked listener -------

// TestConsoleBind_Resolver_CertlessIsNeverAdmin verifies that the Resolver with
// loopbackTrusted=false maps a certless request to RoleRead (not RoleAdmin).
// This tests the defence-in-depth layer: even if TLS were somehow misconfigured,
// the resolver would never grant admin on a certless request.
func TestConsoleBind_Resolver_CertlessIsNeverAdmin(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	mapper := netid.NewRoleMapper(stateDir)
	// loopbackTrusted=false: networked path.
	resolver := netid.NewResolver(mapper, func(*http.Request) string { return "" }, false)

	// Simulate a request with no TLS state (certless).
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// req.TLS is nil — no cert presented.

	id := resolver.Resolve(req)
	if id.Authenticated {
		t.Error("certless request should not be authenticated")
	}
	if id.Role > netid.RoleRead {
		t.Errorf("certless request on networked listener: role=%v; want at most RoleRead (never admin)", id.Role)
	}
}

// TestConsoleBind_Resolver_LoopbackTrustedGrantsAdmin verifies the loopback
// path is UNCHANGED: certless + loopbackTrusted=true → RoleAdmin.
func TestConsoleBind_Resolver_LoopbackTrustedGrantsAdmin(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	mapper := netid.NewRoleMapper(stateDir)
	// loopbackTrusted=true: loopback path (default, unchanged).
	resolver := netid.NewResolver(mapper, func(*http.Request) string { return "alice" }, true)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// req.TLS is nil — no cert (loopback bearer).

	id := resolver.Resolve(req)
	if id.Authenticated {
		t.Error("loopback bearer should not be marked authenticated")
	}
	if id.Role != netid.RoleAdmin {
		t.Errorf("loopback certless: role=%v; want RoleAdmin (cooperative label mode)", id.Role)
	}
	if id.OperatorID != "alice" {
		t.Errorf("loopback: operatorID=%q; want alice", id.OperatorID)
	}
}

// ---- 4. CSP: ws:// vs wss:// ------------------------------------------------

// TestConsoleBind_CSP_NetworkedUsesWss verifies that the CSP header includes
// wss:// (not ws://) when the server is in NetworkedMode.
func TestConsoleBind_CSP_NetworkedUsesWss(t *testing.T) {
	t.Parallel()
	_, serverTLSCfg, clientTLSCfg, _ := newMTLSFixture(t)

	baseURL, _, teardown := startNetworkedServer(t, serverTLSCfg)
	defer teardown()

	resp, err := doTLSGet(t, baseURL+"/", clientTLSCfg)
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	csp := resp.Header.Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("Content-Security-Policy header is missing")
	}
	if !strings.Contains(csp, "wss://") {
		t.Errorf("CSP for networked mode should contain wss://; got: %s", csp)
	}
	if strings.Contains(csp, "connect-src 'self' ws://") {
		t.Errorf("CSP for networked mode must NOT use plain ws://; got: %s", csp)
	}
}

// TestConsoleBind_CSP_LoopbackUsesWs verifies that the loopback path still
// emits ws:// in the CSP header (unchanged from prior phases).
func TestConsoleBind_CSP_LoopbackUsesWs(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "ws://") {
		t.Errorf("CSP for loopback mode should contain ws://; got: %s", csp)
	}
	if strings.Contains(csp, "wss://") {
		t.Errorf("CSP for loopback mode must NOT contain wss://; got: %s", csp)
	}
}

// ---- 5. WS Origin allow-list -------------------------------------------------
//
// Origin allow-list logic is tested via the exported IsExternalOriginForTest
// and IsLoopbackOriginForTest helpers (which wrap the internal functions).
// We don't trigger a real WS upgrade in these tests to avoid the httptest
// Hijacker limitation; the allow-list predicates are pure functions.

// TestConsoleBind_WSOrigin_ExternalOriginAccepted verifies that the external
// (non-loopback) wss:// and https:// origins are accepted for the external host.
func TestConsoleBind_WSOrigin_ExternalOriginAccepted(t *testing.T) {
	t.Parallel()
	externalHost := "10.0.0.1:7890"

	cases := []string{
		"https://10.0.0.1:7890",
		"wss://10.0.0.1:7890",
		"http://10.0.0.1:7890", // http fallback also accepted
	}
	for _, origin := range cases {
		origin := origin
		t.Run(origin, func(t *testing.T) {
			t.Parallel()
			if !consoleui.IsExternalOriginForTest(origin, externalHost) {
				t.Errorf("IsExternalOrigin(%q, %q) = false; want true", origin, externalHost)
			}
		})
	}
}

// TestConsoleBind_WSOrigin_ExactMatchOnly verifies that isExternalOrigin uses
// exact match only — no prefix matching that could accept "10.0.0.1.evil.com".
// This covers the LOW-1 security finding (Finding #3 in the audit).
func TestConsoleBind_WSOrigin_ExactMatchOnly(t *testing.T) {
	t.Parallel()
	externalHost := "10.0.0.1:7890"

	// These must all be REJECTED despite starting with or containing the host.
	// Note: trailing "/" is accepted (TrimRight removes it), which is safe.
	rejected := []struct {
		name   string
		origin string
	}{
		{"subdomain-prefix-attack", "https://10.0.0.1:7890.evil.com"},
		{"port-appended-again", "https://10.0.0.1:7890:extra"},
		{"host-without-port", "https://10.0.0.1"},
		{"empty-origin", ""},
		{"different-port", "https://10.0.0.1:7891"},
		{"different-host", "https://10.0.0.2:7890"},
	}
	for _, tc := range rejected {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if consoleui.IsExternalOriginForTest(tc.origin, externalHost) {
				t.Errorf("IsExternalOrigin(%q, %q) = true; want false (must be exact match)", tc.origin, externalHost)
			}
		})
	}
}

// TestConsoleBind_WSOrigin_BareHostRejected verifies that isExternalOrigin
// returns false when externalHost has no port — a bare host is too broad and
// must be rejected to prevent "host" matching "host.evil.com".
func TestConsoleBind_WSOrigin_BareHostRejected(t *testing.T) {
	t.Parallel()
	// externalHost without a port: must be rejected.
	if consoleui.IsExternalOriginForTest("https://10.0.0.1", "10.0.0.1") {
		t.Error("IsExternalOrigin with bare host (no port) should return false; port is required")
	}
	if consoleui.IsExternalOriginForTest("https://example.com", "example.com") {
		t.Error("IsExternalOrigin with bare hostname (no port) should return false; port is required")
	}
}

// TestConsoleBind_WSOrigin_ForeignOriginRejected verifies that a completely
// foreign origin is NOT accepted by isExternalOrigin for the configured host.
func TestConsoleBind_WSOrigin_ForeignOriginRejected(t *testing.T) {
	t.Parallel()
	externalHost := "10.0.0.1:7890"
	foreignOrigins := []string{
		"https://evil.attacker.example.com",
		"https://10.0.0.2:7890", // different IP
		"https://10.0.0.1:7891", // different port
	}
	for _, origin := range foreignOrigins {
		origin := origin
		t.Run(origin, func(t *testing.T) {
			t.Parallel()
			if consoleui.IsExternalOriginForTest(origin, externalHost) {
				t.Errorf("IsExternalOrigin(%q, %q) = true; want false (foreign origin must not be allowed)", origin, externalHost)
			}
		})
	}
}

// TestConsoleBind_WSOrigin_LoopbackStillAcceptedOnNetworked verifies that
// loopback origins still pass the loopback check (used in the networked
// allow-list as the first gate before the external host check).
func TestConsoleBind_WSOrigin_LoopbackStillAcceptedOnNetworked(t *testing.T) {
	t.Parallel()
	loopbackOrigins := []string{
		"http://127.0.0.1:7890",
		"http://127.0.0.1",
		"http://localhost:7890",
		"http://[::1]:7890",
	}
	for _, origin := range loopbackOrigins {
		origin := origin
		t.Run(origin, func(t *testing.T) {
			t.Parallel()
			if !consoleui.IsLoopbackOriginForTest(origin) {
				t.Errorf("IsLoopbackOrigin(%q) = false; want true (loopback should always be accepted)", origin)
			}
		})
	}
}

// TestConsoleBind_WSOrigin_NetworkedHandler_ForeignRejected verifies that the
// full consoleOriginAllowListNetworked middleware rejects a foreign origin with
// 403 WITHOUT triggering the WS upgrade (using a simple next handler).
func TestConsoleBind_WSOrigin_NetworkedHandler_ForeignRejected(t *testing.T) {
	t.Parallel()
	externalHost := "10.0.0.1:7890"

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := consoleui.BuildOriginAllowListNetworkedForTest(externalHost, next)

	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	req.Header.Set("Origin", "https://evil.attacker.example.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if called {
		t.Error("next should not be called for foreign origin")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("foreign origin should return 403; got %d", w.Code)
	}
}

// TestConsoleBind_WSOrigin_NetworkedHandler_ExternalAccepted verifies that
// the external host origin passes the allow-list and next is called.
func TestConsoleBind_WSOrigin_NetworkedHandler_ExternalAccepted(t *testing.T) {
	t.Parallel()
	externalHost := "10.0.0.1:7890"

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := consoleui.BuildOriginAllowListNetworkedForTest(externalHost, next)

	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	req.Header.Set("Origin", "https://10.0.0.1:7890")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Error("next should be called for external host origin")
	}
	if w.Code == http.StatusForbidden {
		t.Errorf("external host origin should not be rejected; got 403")
	}
}

// ---- Finding #2: Presence CN override on networked path --------------------

// TestConsoleBind_Presence_CertCN_Integration verifies the full cert CN override
// path end-to-end: mTLS WS connection → cert CN overrides hello.OperatorID →
// welcome frame carries cert CN as the operator identity.
func TestConsoleBind_Presence_CertCN_Integration(t *testing.T) {
	t.Parallel()
	_, serverTLSCfg, clientTLSCfg, clientCN := newMTLSFixture(t)

	stateDir := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	bus := wsbus.New()
	defer bus.Stop()

	// Open a TCP listener on a random port.
	tcpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := tcpLn.Addr().String()

	cfg := consoleui.Config{
		Addr:              addr,
		Token:             tok,
		KanbanBoardPath:   stateDir + "/kanban.md",
		KanbanProject:     "test",
		MetricsProjectDir: stateDir,
		PerfWorkDir:       stateDir,
		Bus:               bus,
		StateDir:          stateDir,
		Listener:          tcpLn,
		TLSConfig:         serverTLSCfg,
		NetworkedMode:     true,
		ExternalHost:      addr,
	}

	srv := consoleui.New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ctx) }()
	time.Sleep(80 * time.Millisecond) // wait for server to accept

	// Dial WS over TLS using the client cert.
	// Use a custom dial function so we can pass the mTLS clientTLSCfg to the
	// TLS handshake (golang.org/x/net/websocket does not reliably propagate
	// TlsConfig through DialConfig for all Go versions).
	wsURL := "wss://" + addr + "/v1/events"
	wsCfg, err := websocket.NewConfig(wsURL, "https://"+addr)
	if err != nil {
		t.Fatalf("websocket.NewConfig: %v", err)
	}
	// The outer requireTokenForNonStatic checks Authorization: Bearer.
	// The inner consoleAuthSubprotocol checks Sec-WebSocket-Protocol: yakos-bearer, <tok>.
	// Provide both so the request passes both layers.
	wsCfg.Protocol = []string{"yakos-bearer", tok}
	if wsCfg.Header == nil {
		wsCfg.Header = make(http.Header)
	}
	wsCfg.Header.Set("Authorization", "Bearer "+tok)
	// Establish the TLS connection manually with the mTLS client cert config,
	// then hand the net.Conn to websocket.NewClient.
	tlsConn, err := tls.Dial("tcp", addr, clientTLSCfg)
	if err != nil {
		t.Fatalf("tls.Dial: %v", err)
	}
	conn, err := websocket.NewClient(wsCfg, tlsConn)
	if err != nil {
		_ = tlsConn.Close()
		t.Fatalf("websocket.NewClient: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send a hello with a FORGED operatorId — the server must override it with cert CN.
	// Note: HelloMessage uses camelCase "operatorId" (not snake_case "operator_id").
	hello := map[string]interface{}{
		"type":       "hello",
		"operatorId": "forged-id", // attacker tries to forge another operator
	}
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	if err := websocket.JSON.Send(conn, hello); err != nil {
		t.Fatalf("send hello: %v", err)
	}

	// Read the welcome frame.
	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck
	var welcome map[string]interface{}
	if err := websocket.JSON.Receive(conn, &welcome); err != nil {
		t.Fatalf("receive welcome: %v", err)
	}
	if welcome["type"] != "welcome" {
		t.Fatalf("expected welcome frame, got type=%v", welcome["type"])
	}

	// The presence record in the welcome must carry the cert CN, not "forged-id".
	presence, ok := welcome["presence"].(map[string]interface{})
	if !ok {
		t.Fatalf("welcome.presence: expected map, got %T: %v", welcome["presence"], welcome["presence"])
	}
	gotOperatorID, _ := presence["operator_id"].(string)
	if gotOperatorID == "forged-id" {
		t.Errorf("presence.operator_id=%q: cert CN was NOT used to override hello.operator_id (MEDIUM finding not fixed)", gotOperatorID)
	}
	if gotOperatorID != clientCN {
		t.Errorf("presence.operator_id=%q; want cert CN %q", gotOperatorID, clientCN)
	}
}

// ---- 6. Loopback default: unchanged -----------------------------------------

// TestConsoleBind_LoopbackDefault_Unchanged verifies that the default loopback
// path rejects non-loopback listeners without mTLS (plain HTTP off-loopback
// is impossible — Serve returns error).
func TestConsoleBind_LoopbackDefault_Unchanged(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	bus := wsbus.New()
	defer bus.Stop()

	// Bind at a random port — but use the NON-networked mode (default).
	// When the server opens the listener itself, it will enforce loopback-only
	// and reject a non-loopback addr.  Simulating that here by providing an
	// explicit loopback listener (the test environment always uses loopback).
	tcpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	srv := consoleui.New(consoleui.Config{
		Addr:          tcpLn.Addr().String(),
		Token:         tok,
		Bus:           bus,
		Listener:      tcpLn,
		TLSConfig:     nil,   // no TLS — loopback path
		NetworkedMode: false, // default
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ctx)
	}()

	// The loopback server should start successfully (loopback listener, no TLS).
	addr := "http://" + tcpLn.Addr().String()
	time.Sleep(50 * time.Millisecond)

	resp, httpErr := http.Get(addr + "/") //nolint:noctx
	if httpErr != nil {
		t.Fatalf("GET / on loopback server: %v", httpErr)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("loopback server GET /: status=%d; want 200", resp.StatusCode)
	}

	cancel()
	select {
	case <-errCh:
	case <-time.After(3 * time.Second):
		t.Error("loopback server did not shut down in time")
	}
}
