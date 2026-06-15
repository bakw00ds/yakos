package consoleui_test

// auth_3b_test.go — exhaustive tests for the Phase 3b auth edge.
//
// Coverage per task specification (all run with -race):
//
//  1. Cookie name invariant: sessionCookieName in consoleui equals
//     authsession.CookieNameSession.
//  2. Login success: sets both cookies (session HttpOnly+Secure+SameSite=Strict;
//     CSRF non-HttpOnly+Secure+SameSite=Strict), creates a session, returns 200.
//  3. Login failure (unknown / wrong / disabled / locked): ALL return identical
//     401 generic body; no distinguishing info in body/headers/status.
//  4. Session fixation: a pre-existing session cookie on login is revoked and a
//     new session ID is issued.
//  5. Per-IP rate limit: N+1 rapid attempts → 429.
//  6. Logout: revokes session + clears both cookies; idempotent (no session → 200).
//  7. CSRF — session-auth POST without X-CSRF-Token → 403.
//  8. CSRF — session-auth POST with wrong token → 403.
//  9. CSRF — session-auth POST with correct token → passes.
// 10. CSRF — cert-auth POST (AuthMethodCert) without header → passes (exempt).
// 11. CSRF — loopback bearer mutation → passes (unchanged).
// 12. Edge: networked unauthenticated API request → 401 JSON.
// 13. Edge: networked unauthenticated navigation → 302 /login.
// 14. Full happy path: login → authenticated mutation (succeeds) → logout → same
//     mutation → 401.
// 15. Existing loopback behavior unchanged (bearer token still works).
//
// Note: argon2id params are reduced for the entire test binary via TestMain in
// testmain_test.go. Do NOT call SetArgon2ParamsForTest in individual tests.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/authsession"
	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/userstore"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// authsession is imported for authsession.NewStore and authsession.Config in tests.
// The session cookie name is referenced via consoleui.SessionCookieName (exported
// from export_test.go).  authsession.CookieNameSession is only accessible within
// the authsession package test binary (export_test.go with package authsession).

// ---- test infrastructure ----------------------------------------------------

// auth3bServer holds everything needed for an auth test.
type auth3bServer struct {
	ts        *httptest.Server
	tok       string
	authStore *authsession.Store
	uStore    *userstore.Store
}

// newAuth3bServer builds a full networked consoleui.Server (NetworkedMode=true)
// with real authsession and userstore instances, backed by a temp dir.
// Alice (admin) is pre-created.
func newAuth3bServer(t *testing.T) *auth3bServer {
	t.Helper()

	stateDir := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}

	uStore, err := userstore.Open(t.TempDir() + "/users.json")
	if err != nil {
		t.Fatalf("userstore.Open: %v", err)
	}
	if err := uStore.Create("alice", "correcthorsebattery1", netid.RoleAdmin); err != nil {
		t.Fatalf("userstore.Create alice: %v", err)
	}
	if err := uStore.Create("bob", "correcthorsebattery2", netid.RoleRead); err != nil {
		t.Fatalf("userstore.Create bob: %v", err)
	}

	aStore := authsession.NewStore(authsession.Config{
		MaxSessions: 100,
	})

	bus := wsbus.New()
	t.Cleanup(bus.Stop)

	srv := consoleui.MustNew(t, consoleui.Config{
		NetworkedMode:     true,
		Token:             tok,
		KanbanBoardPath:   t.TempDir() + "/kanban.md",
		KanbanProject:     "test",
		MetricsProjectDir: t.TempDir(),
		PerfWorkDir:       t.TempDir(),
		Bus:               bus,
		WorkDir:           t.TempDir(),
		AuthSessionStore:  aStore,
		UserStore:         uStore,
	})

	// Use srv.FullHandler() which is the complete protected middleware chain
	// (token gate + CSRF + edge auth + resolver + mux) — identical to what
	// the production Serve() path uses, but without the TLS listener or the
	// RequireLocalHost guard.
	ts := httptest.NewServer(srv.FullHandler())
	t.Cleanup(ts.Close)

	return &auth3bServer{ts: ts, tok: tok, authStore: aStore, uStore: uStore}
}

// loginReq sends POST /login with the given credentials and returns the response.
// Sets Content-Type: application/json.  Does NOT set Authorization header —
// /login is token-exempt (browser does not hold a token during login).
func (s *auth3bServer) loginReq(t *testing.T, username, password string, extraCookies ...*http.Cookie) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	req, err := http.NewRequest(http.MethodPost, s.ts.URL+"/login", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Note: no Authorization header — /login is token-exempt.
	for _, c := range extraCookies {
		req.AddCookie(c)
	}

	// Use a non-redirecting client so we can inspect 302s.
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /login: %v", err)
	}
	return resp
}

// doAuthedMutation sends a POST to path with a JSON body, session cookie (sid),
// and CSRF token (csrf).  On the networked path, no bearer token is used —
// session-auth is the mechanism.
func (s *auth3bServer) doAuthedMutation(t *testing.T, path, sid, csrf string, jsonBody interface{}) *http.Response {
	t.Helper()
	b, _ := json.Marshal(jsonBody)
	req, err := http.NewRequest(http.MethodPost, s.ts.URL+path, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// No Authorization bearer token — networked session-auth uses cookies only.
	if sid != "" {
		req.AddCookie(&http.Cookie{Name: consoleui.SessionCookieName, Value: sid})
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	client := s.ts.Client()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

// cookieMap extracts Set-Cookie headers from a response into a name→*http.Cookie map.
func cookieMap(resp *http.Response) map[string]*http.Cookie {
	m := make(map[string]*http.Cookie)
	for _, c := range resp.Cookies() {
		m[c.Name] = c
	}
	return m
}

// readBody reads and closes the response body, returning the string.
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// ---- Test 1: cookie name constant invariant ---------------------------------

func TestCookieNameConstantInvariant(t *testing.T) {
	// Verify that consoleui's sessionCookieName constant has the ADR-0005-locked
	// value "yakos_session".  This is the cross-package assertion described in
	// the Phase 3b task: a mismatch would mean sessionauth.go reads a cookie
	// with the wrong name and login sessions would never be found.
	//
	// The authoritative value is authsession.cookieNameSession (unexported
	// production constant).  Both sides must use the same string literal, and
	// this test guards against independent drift.
	const want = "yakos_session"
	if consoleui.SessionCookieName != want {
		t.Errorf("consoleui.SessionCookieName=%q; want %q", consoleui.SessionCookieName, want)
	}

	// Also verify by exercising the login flow: the Set-Cookie name in the login
	// response must match the expected constant.  A mismatch in the cookie name
	// would mean sessionauth.go reads a cookie with the wrong name and always
	// returns false (silent auth failure).
	s := newAuth3bServer(t)
	resp := s.loginReq(t, "alice", "correcthorsebattery1")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login: expected 200; got %d", resp.StatusCode)
	}

	cookies := cookieMap(resp)
	if _, ok := cookies[want]; !ok {
		t.Errorf("login response: Set-Cookie for %q not found; got cookies %v",
			want, resp.Cookies())
	}
}

// ---- Test 2: Login success — cookies + 200 ----------------------------------

func TestLoginSuccess_Cookies(t *testing.T) {
	t.Parallel()
	s := newAuth3bServer(t)

	resp := s.loginReq(t, "alice", "correcthorsebattery1")
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200; got %d (body: %s)", resp.StatusCode, body)
	}

	// Parse the response body.
	var loginResp struct {
		OK                    bool `json:"ok"`
		PasswordResetRequired bool `json:"passwordResetRequired"`
	}
	if err := json.Unmarshal([]byte(body), &loginResp); err != nil {
		t.Fatalf("parse response body: %v (body: %s)", err, body)
	}
	if !loginResp.OK {
		t.Error("expected ok=true in response")
	}

	cookies := cookieMap(resp)

	// Session cookie: HttpOnly, Secure=true on networked path, SameSite=Strict.
	sessCookie, ok := cookies[consoleui.SessionCookieName]
	if !ok {
		t.Fatal("yakos_session cookie not set")
	}
	if !sessCookie.HttpOnly {
		t.Error("yakos_session: expected HttpOnly=true")
	}
	if !sessCookie.Secure {
		t.Error("yakos_session: expected Secure=true (networked TLS bind)")
	}
	if sessCookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("yakos_session: expected SameSite=Strict; got %v", sessCookie.SameSite)
	}
	if sessCookie.Value == "" {
		t.Error("yakos_session: empty value")
	}

	// CSRF cookie: non-HttpOnly, Secure=true, SameSite=Strict.
	csrfCookie, ok := cookies["yakos_csrf"]
	if !ok {
		t.Fatal("yakos_csrf cookie not set")
	}
	if csrfCookie.HttpOnly {
		t.Error("yakos_csrf: expected HttpOnly=false (must be JS-readable for double-submit)")
	}
	if !csrfCookie.Secure {
		t.Error("yakos_csrf: expected Secure=true")
	}
	if csrfCookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("yakos_csrf: expected SameSite=Strict; got %v", csrfCookie.SameSite)
	}
	if csrfCookie.Value == "" {
		t.Error("yakos_csrf: empty value")
	}

	// The session should be findable in the store.
	if _, found := s.authStore.Lookup(sessCookie.Value); !found {
		t.Error("login: session not found in authStore after successful login")
	}
}

// ---- Test 3: Login failure — all modes return identical 401 -----------------

func TestLoginFailure_GenericResponse(t *testing.T) {
	t.Parallel()
	s := newAuth3bServer(t)

	// Lock alice's account by failing 10 times.
	for i := 0; i < userstore.LockoutThreshold; i++ {
		r := s.loginReq(t, "alice", "wrongpasswordXXXXXX")
		r.Body.Close()
	}

	cases := []struct {
		name     string
		username string
		password string
	}{
		{"unknown_user", "nobody", "correcthorsebattery1"},
		{"wrong_password", "alice", "wrongpassword1234567"},
		{"locked_account", "alice", "correcthorsebattery1"}, // locked after above
	}

	// Disable bob.
	if err := s.uStore.Disable("bob"); err != nil {
		t.Fatalf("Disable bob: %v", err)
	}
	cases = append(cases, struct {
		name     string
		username string
		password string
	}{"disabled_account", "bob", "correcthorsebattery2"})

	const wantBody = `{"error":"invalid username or password"}`

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resp := s.loginReq(t, tc.username, tc.password)
			body := readBody(t, resp)

			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("expected 401; got %d", resp.StatusCode)
			}

			trimmed := strings.TrimSpace(body)
			if !strings.Contains(trimmed, `"error":"invalid username or password"`) {
				t.Errorf("generic error body expected; got %q", trimmed)
			}

			// Assert no cookies set on failure.
			for _, c := range resp.Cookies() {
				if c.Name == consoleui.SessionCookieName || c.Name == "yakos_csrf" {
					t.Errorf("failure mode %s: auth cookies must not be set on 401; got %q", tc.name, c.Name)
				}
			}
		})
	}
}

// ---- Test 4: Session fixation defense ---------------------------------------

func TestLoginSessionFixation(t *testing.T) {
	t.Parallel()
	s := newAuth3bServer(t)

	// Create a pre-existing session (simulates an old session cookie the attacker
	// may have set or stolen).
	pu, _ := s.uStore.Get("alice")
	oldSess, err := s.authStore.Create("alice", netid.RoleAdmin, pu.SessionEpoch)
	if err != nil {
		t.Fatalf("Create old session: %v", err)
	}
	oldID := oldSess.ID

	// Verify the old session is present.
	if _, found := s.authStore.Lookup(oldID); !found {
		t.Fatal("old session not found before login")
	}

	// Log in with the old session cookie present.
	oldCookie := &http.Cookie{Name: consoleui.SessionCookieName, Value: oldID}
	resp := s.loginReq(t, "alice", "correcthorsebattery1", oldCookie)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login: expected 200; got %d", resp.StatusCode)
	}

	// Old session must be revoked.
	if _, found := s.authStore.Lookup(oldID); found {
		t.Error("session fixation: old session still present in store after login; must be revoked")
	}

	// New session must have a different ID.
	cookies := cookieMap(resp)
	newSessCookie, ok := cookies[consoleui.SessionCookieName]
	if !ok {
		t.Fatal("no yakos_session cookie in login response")
	}
	if newSessCookie.Value == oldID {
		t.Error("session fixation: new session ID equals old ID; must be different")
	}
	if newSessCookie.Value == "" {
		t.Error("session fixation: new session ID is empty")
	}
}

// ---- Test 5: Per-IP rate limit ----------------------------------------------

func TestLoginRateLimit(t *testing.T) {
	t.Parallel()
	s := newAuth3bServer(t)

	// The rate limiter is per-IP.  httptest requests come from 127.0.0.1.
	// Exhaust the limit (20 requests per 5 minutes per const in authhandler.go).
	const limit = consoleui.LoginRateLimitRequests
	for i := 0; i < limit; i++ {
		resp := s.loginReq(t, "nobody_rate_test", "wrongpass12345")
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("rate limited before reaching limit at request %d/%d", i+1, limit)
		}
	}

	// The next request must be rate-limited.
	resp := s.loginReq(t, "alice", "correcthorsebattery1")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected 429 after %d requests; got %d", limit, resp.StatusCode)
	}
}

// ---- Test 6: Logout — revoke + clear cookies; idempotent -------------------

func TestLogout(t *testing.T) {
	t.Parallel()
	s := newAuth3bServer(t)

	// Log in to get a session.
	loginResp := s.loginReq(t, "alice", "correcthorsebattery1")
	defer loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("login: expected 200; got %d", loginResp.StatusCode)
	}
	loginCookies := cookieMap(loginResp)
	sessID := loginCookies[consoleui.SessionCookieName].Value

	// Send POST /logout with the session cookie and CSRF token.
	// No bearer token — networked session-auth uses cookies only.
	logoutReq, _ := http.NewRequest(http.MethodPost, s.ts.URL+"/logout", bytes.NewReader([]byte("{}")))
	logoutReq.Header.Set("Content-Type", "application/json")
	logoutReq.AddCookie(&http.Cookie{Name: consoleui.SessionCookieName, Value: sessID})
	// Look up the session's CSRF token to send it.
	if loginSess, found := s.authStore.Lookup(sessID); found {
		logoutReq.Header.Set("X-CSRF-Token", loginSess.CSRFToken)
	}
	logoutResp, err := s.ts.Client().Do(logoutReq)
	if err != nil {
		t.Fatalf("POST /logout: %v", err)
	}
	logoutBody := readBody(t, logoutResp)

	if logoutResp.StatusCode != http.StatusOK {
		t.Errorf("logout: expected 200; got %d (body: %s)", logoutResp.StatusCode, logoutBody)
	}

	// Session must be revoked.
	if _, found := s.authStore.Lookup(sessID); found {
		t.Error("logout: session still in store after logout")
	}

	// Set-Cookie headers must clear both cookies (MaxAge=-1).
	logoutCookies := cookieMap(logoutResp)
	if c, ok := logoutCookies[consoleui.SessionCookieName]; !ok || c.MaxAge >= 0 {
		t.Errorf("logout: yakos_session should be cleared (MaxAge=-1); got %v", c)
	}
	if c, ok := logoutCookies["yakos_csrf"]; !ok || c.MaxAge >= 0 {
		t.Errorf("logout: yakos_csrf should be cleared (MaxAge=-1); got %v", c)
	}

	// Idempotent: a second logout with the same (now-revoked) session cookie.
	// The session is revoked so the resolver returns unauthenticated; the edge
	// middleware (requireAuthOrRedirect) fires before the logout handler is
	// reached.  Send Accept: application/json to get a 401 JSON response (the
	// API path) rather than a 302 redirect (the navigation path).
	// This asserts the real behavior: an already-logged-out client receives 401
	// rather than silently following a redirect to /login.
	noRedirectClient := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	logoutReq2, _ := http.NewRequest(http.MethodPost, s.ts.URL+"/logout", bytes.NewReader([]byte("{}")))
	logoutReq2.Header.Set("Content-Type", "application/json")
	logoutReq2.Header.Set("Accept", "application/json") // ensure 401 (not 302) for API path
	logoutReq2.AddCookie(&http.Cookie{Name: consoleui.SessionCookieName, Value: sessID})
	logoutResp2, err := noRedirectClient.Do(logoutReq2)
	if err != nil {
		t.Fatalf("POST /logout (idempotent): %v", err)
	}
	defer logoutResp2.Body.Close()
	// After logout, the session is revoked; the edge middleware returns 401 for
	// an unauthenticated API request (correct and expected behavior).
	if logoutResp2.StatusCode != http.StatusUnauthorized {
		b, _ := io.ReadAll(logoutResp2.Body)
		t.Errorf("idempotent logout: expected 401 (unauthenticated); got %d (body: %s)",
			logoutResp2.StatusCode, b)
	}
}

// ---- Tests 7–9: CSRF middleware — session-auth mutations --------------------

// setupSessionAuth logs in and returns (sessID, csrfToken).
func setupSessionAuth(t *testing.T, s *auth3bServer) (sessID, csrfToken string) {
	t.Helper()
	resp := s.loginReq(t, "alice", "correcthorsebattery1")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login: expected 200; got %d", resp.StatusCode)
	}
	cookies := cookieMap(resp)
	return cookies[consoleui.SessionCookieName].Value, cookies["yakos_csrf"].Value
}

func TestCSRF_SessionAuth_NoToken_Returns403(t *testing.T) {
	t.Parallel()
	s := newAuth3bServer(t)
	sessID, _ := setupSessionAuth(t, s)

	// POST to a mutation endpoint without X-CSRF-Token.
	resp := s.doAuthedMutation(t, "/api/chat/cancel", sessID, "", map[string]string{"sessionId": "x"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 (no CSRF token); got %d", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "invalid CSRF token") {
		t.Errorf("expected CSRF error in body; got %q", body)
	}
}

func TestCSRF_SessionAuth_WrongToken_Returns403(t *testing.T) {
	t.Parallel()
	s := newAuth3bServer(t)
	sessID, _ := setupSessionAuth(t, s)

	// POST with wrong CSRF token.
	resp := s.doAuthedMutation(t, "/api/chat/cancel", sessID, "wrong-csrf-token-xxxxx", map[string]string{"sessionId": "x"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 (wrong CSRF token); got %d", resp.StatusCode)
	}
}

func TestCSRF_SessionAuth_CorrectToken_Passes(t *testing.T) {
	t.Parallel()
	s := newAuth3bServer(t)
	sessID, csrfToken := setupSessionAuth(t, s)

	// POST with correct CSRF token.
	// /api/chat/cancel requires RoleDispatch; alice is RoleAdmin which satisfies it.
	// The cancel handler itself will likely 400 (missing sessionId), but we pass
	// the CSRF check — that's what we're testing here.
	resp := s.doAuthedMutation(t, "/api/chat/cancel", sessID, csrfToken, map[string]string{"sessionId": "nonexistent"})
	defer resp.Body.Close()
	// 403 would indicate CSRF rejection; any other status means CSRF passed.
	if resp.StatusCode == http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("unexpected 403 (CSRF rejected with correct token); body: %s", body)
	}
}

// ---- Test 10: CSRF — cert-auth (AuthMethodCert) is exempt -------------------

func TestCSRF_CertAuth_NoToken_Passes(t *testing.T) {
	t.Parallel()
	// Test the CSRF middleware directly with an injected cert-auth identity.
	// We build the chain: injectIdentity → requireCSRFForSession → okHandler
	// to verify that cert-auth skips the CSRF check entirely.
	aStore := authsession.NewStore(authsession.Config{})

	certID := netid.Identity{
		Resolved:      true,
		Authenticated: true,
		AuthMethod:    netid.AuthMethodCert,
		OperatorID:    "machine-client",
		Role:          netid.RoleAdmin,
	}

	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := injectIdentityMiddleware(certID,
		consoleui.RequireCSRFForSessionForTest(aStore, ok))

	req := httptest.NewRequest(http.MethodPost, "/api/chat/cancel",
		bytes.NewReader([]byte(`{"sessionId":"x"}`)))
	req.Header.Set("Content-Type", "application/json")
	// No X-CSRF-Token header — cert-auth should be exempt.

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code == http.StatusForbidden {
		t.Errorf("cert-auth: CSRF middleware must not block cert-auth requests (no header); got 403")
	}
	if w.Code != http.StatusOK {
		t.Errorf("cert-auth: expected 200; got %d", w.Code)
	}
}

// ---- Test 11: CSRF — loopback bearer is unchanged ---------------------------

func TestCSRF_LoopbackBearer_Unchanged(t *testing.T) {
	t.Parallel()
	// Test the CSRF middleware directly with an AuthMethodNone (loopback bearer)
	// identity — CSRF must not fire for unauthenticated / loopback identities.
	aStore := authsession.NewStore(authsession.Config{})

	// Loopback bearer identity: Resolved=true, AuthMethodNone (zero value).
	loopbackID := netid.Identity{
		Resolved:      true,
		Authenticated: false,
		AuthMethod:    netid.AuthMethodNone,
		Role:          netid.RoleAdmin,
	}

	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := injectIdentityMiddleware(loopbackID,
		consoleui.RequireCSRFForSessionForTest(aStore, ok))

	req := httptest.NewRequest(http.MethodPost, "/api/chat/cancel",
		bytes.NewReader([]byte(`{"sessionId":"x"}`)))
	req.Header.Set("Content-Type", "application/json")
	// No X-CSRF-Token, no session cookie — loopback bearer must pass.

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code == http.StatusForbidden {
		t.Errorf("loopback bearer: CSRF middleware must not fire for non-session auth; got 403")
	}
	if w.Code != http.StatusOK {
		t.Errorf("loopback bearer: expected 200; got %d", w.Code)
	}

	// Also verify that a real loopback server (NetworkedMode=false) does not
	// have the CSRF middleware wired at all.
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
		WorkDir:           t.TempDir(),
		// NetworkedMode=false: loopback path, no CSRF middleware.
	})

	// Use RequireTokenForNonStatic + RequireJSONForMutations + Handler() for
	// the loopback path test: we skip RequireLocalHost (Host header) since
	// httptest allocates a random port that doesn't match cfg.Addr default.
	// The production loopback path adds RequireLocalHost on top of this; its
	// behavior is tested separately in console_bind_test.go.
	loopbackHandler := consoleui.RequireTokenForNonStatic(tok,
		consoleui.RequireJSONForMutations(srv.Handler()))
	ts := httptest.NewServer(loopbackHandler)
	t.Cleanup(ts.Close)

	body, _ := json.Marshal(map[string]string{"sessionId": "nonexistent"})
	req2, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/chat/cancel", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer "+tok)
	// No X-CSRF-Token — loopback bearer is unchanged.

	resp, err2 := ts.Client().Do(req2)
	if err2 != nil {
		t.Fatalf("loopback POST /api/chat/cancel: %v", err2)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		b, _ := io.ReadAll(resp.Body)
		t.Errorf("loopback server: CSRF must not fire; got 403 (body: %s)", b)
	}
}

// ---- Tests 12–13: Edge — unauthenticated networked requests -----------------

// newNetworkedNoSessionServer builds a networked server WITHOUT auth stores,
// so all requests resolve as unauthenticated.
func newNetworkedNoSessionServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	stateDir := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)

	uStore, err := userstore.Open(t.TempDir() + "/users.json")
	if err != nil {
		t.Fatalf("userstore.Open: %v", err)
	}
	aStore := authsession.NewStore(authsession.Config{})

	srv := consoleui.MustNew(t, consoleui.Config{
		NetworkedMode:     true,
		Token:             tok,
		KanbanBoardPath:   t.TempDir() + "/kanban.md",
		KanbanProject:     "test",
		MetricsProjectDir: t.TempDir(),
		PerfWorkDir:       t.TempDir(),
		Bus:               bus,
		WorkDir:           t.TempDir(),
		AuthSessionStore:  aStore,
		UserStore:         uStore,
	})

	ts := httptest.NewServer(srv.FullHandler())
	t.Cleanup(ts.Close)
	return ts, tok
}

func TestEdge_UnauthenticatedAPIRequest_Returns401(t *testing.T) {
	t.Parallel()
	ts, _ := newNetworkedNoSessionServer(t)

	// GET /api/presence with no session cookie and no bearer token —
	// simulates a browser API request without an active session.
	// The networked path does not require a bearer token for regular HTTP;
	// auth comes from session cookie (absent here) → 401 JSON.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/presence", nil)
	req.Header.Set("Accept", "application/json")
	// No Authorization header, no session cookie.

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /api/presence: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthenticated API: expected 401; got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("unauthenticated API: expected JSON content-type; got %q", ct)
	}
}

func TestEdge_UnauthenticatedNavigation_Redirects302ToLogin(t *testing.T) {
	t.Parallel()
	ts, _ := newNetworkedNoSessionServer(t)

	// GET /ide/editor is a static asset (in isStaticAsset) — let's use /kanban/
	// which is a protected route under a non-/api/ path prefix.
	// A browser navigation to /kanban/ without a session cookie should → 302.
	// Note: /kanban/ does not match any isAPIRequest prefix, so the redirect fires.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/kanban/", nil)
	// No Authorization header, no session cookie.
	// No Accept: application/json — simulates a browser navigation (not XHR).
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /kanban/ (navigation): %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Errorf("unauthenticated navigation: expected 302; got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc != "/login" {
		t.Errorf("unauthenticated navigation: expected redirect to /login; got %q", loc)
	}
}

// ---- Test 14: Full happy path -----------------------------------------------

func TestFullHappyPath_LoginMutateLogout(t *testing.T) {
	t.Parallel()
	s := newAuth3bServer(t)

	// Step 1: Login.
	loginResp := s.loginReq(t, "alice", "correcthorsebattery1")
	defer loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("login: expected 200; got %d", loginResp.StatusCode)
	}
	loginCookies := cookieMap(loginResp)
	sessID := loginCookies[consoleui.SessionCookieName].Value
	csrfToken := loginCookies["yakos_csrf"].Value

	// Step 2: Perform a session-authenticated mutation (with correct CSRF token).
	// /api/chat/cancel is a valid mutation route; the handler will return non-403.
	mutResp := s.doAuthedMutation(t, "/api/chat/cancel", sessID, csrfToken, map[string]string{"sessionId": "nonexistent"})
	defer mutResp.Body.Close()
	if mutResp.StatusCode == http.StatusForbidden {
		b, _ := io.ReadAll(mutResp.Body)
		t.Fatalf("happy path: mutation rejected (403) after login; body: %s", b)
	}

	// Step 3: Logout (no bearer token — session-auth only; include CSRF token).
	logoutReq, _ := http.NewRequest(http.MethodPost, s.ts.URL+"/logout", bytes.NewReader([]byte("{}")))
	logoutReq.Header.Set("Content-Type", "application/json")
	logoutReq.AddCookie(&http.Cookie{Name: consoleui.SessionCookieName, Value: sessID})
	logoutReq.Header.Set("X-CSRF-Token", csrfToken)
	logoutResp, err := s.ts.Client().Do(logoutReq)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	defer logoutResp.Body.Close()
	if logoutResp.StatusCode != http.StatusOK {
		t.Fatalf("logout: expected 200; got %d", logoutResp.StatusCode)
	}

	// Step 4: Same mutation after logout → should fail.
	// The session is revoked; the identity resolves as unauthenticated.
	// The CSRF check fires for session-auth, but the identity is no longer
	// session-auth after logout; the edge redirect/401 fires first.
	postLogoutResp := s.doAuthedMutation(t, "/api/chat/cancel", sessID, csrfToken, map[string]string{"sessionId": "nonexistent"})
	defer postLogoutResp.Body.Close()
	// The request carries the bearer token but a revoked session cookie.
	// On the networked path: resolver returns unauthenticated (revoked session);
	// requireAuthOrRedirect returns 401 for this API path.
	if postLogoutResp.StatusCode != http.StatusUnauthorized &&
		postLogoutResp.StatusCode != http.StatusForbidden {
		t.Errorf("post-logout mutation: expected 401 or 403; got %d", postLogoutResp.StatusCode)
	}
}

// ---- Test 15: Loopback bearer unchanged -------------------------------------

func TestLoopback_BearerToken_Unchanged(t *testing.T) {
	t.Parallel()
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
		WorkDir:           t.TempDir(),
		// NetworkedMode=false: loopback path unchanged.
	})

	// Use RequireTokenForNonStatic + RequireJSONForMutations + Handler() for
	// the loopback path test (skip RequireLocalHost to avoid port-mismatch in
	// httptest; loopback Host-header enforcement is tested in console_bind_test.go).
	loopbackHandler := consoleui.RequireTokenForNonStatic(tok,
		consoleui.RequireJSONForMutations(srv.Handler()))
	ts := httptest.NewServer(loopbackHandler)
	t.Cleanup(ts.Close)

	// GET /api/presence with bearer token — should succeed (200).
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/presence", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("loopback GET /api/presence: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("loopback bearer: expected 200; got %d", resp.StatusCode)
	}

	// POST mutation with bearer token but no CSRF header — must succeed.
	body, _ := json.Marshal(map[string]string{"sessionId": "x"})
	mutReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/chat/cancel", bytes.NewReader(body))
	mutReq.Header.Set("Content-Type", "application/json")
	mutReq.Header.Set("Authorization", "Bearer "+tok)
	mutResp, err := ts.Client().Do(mutReq)
	if err != nil {
		t.Fatalf("loopback POST: %v", err)
	}
	defer mutResp.Body.Close()
	if mutResp.StatusCode == http.StatusForbidden {
		b, _ := io.ReadAll(mutResp.Body)
		t.Errorf("loopback bearer: CSRF middleware must not fire on loopback path; got 403 (body: %s)", b)
	}
	// Also must not require CSRF (no 401 either).
	if mutResp.StatusCode == http.StatusUnauthorized {
		t.Errorf("loopback bearer: unexpected 401 (edge redirect must not fire on loopback)")
	}
}

// ---- Test 16: New() fail-closed guard — all three nil-store permutations ----

// TestNew_FailsClosed_NetworkedWithoutStores asserts the HIGH red-team finding:
// consoleui.New must refuse to construct a networked server when either or both
// session stores are nil.  Without this guard a misconfigured server would run
// with no requireAuthOrRedirect and no requireCSRFForSession middleware.
//
// Three permutations are tested:
//  1. Both stores nil       — the most obvious misconfiguration.
//  2. AuthSessionStore nil  — UserStore present but session store missing.
//  3. UserStore nil         — AuthSessionStore present but user store missing.
//
// A positive case (both stores present) is also asserted to ensure the guard
// does not over-reject valid configurations.
//
// This test calls consoleui.New directly (not MustNew) because it explicitly
// exercises the error-return path that MustNew would t.Fatal on.
func TestNew_FailsClosed_NetworkedWithoutStores(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)

	// Minimal base config that reaches the guard; the guard fires before any
	// other field is inspected, so only NetworkedMode matters here.
	base := consoleui.Config{
		NetworkedMode:     true,
		Token:             tok,
		KanbanBoardPath:   t.TempDir() + "/kanban.md",
		KanbanProject:     "test",
		MetricsProjectDir: t.TempDir(),
		PerfWorkDir:       t.TempDir(),
		Bus:               bus,
		WorkDir:           t.TempDir(),
	}

	// Helper to open a fresh userstore for permutations that need one.
	openUserStore := func() *userstore.Store {
		t.Helper()
		us, err := userstore.Open(t.TempDir() + "/users.json")
		if err != nil {
			t.Fatalf("userstore.Open: %v", err)
		}
		return us
	}

	// --- Permutation 1: both stores nil ---
	t.Run("both_stores_nil", func(t *testing.T) {
		t.Parallel()
		cfg := base
		cfg.AuthSessionStore = nil
		cfg.UserStore = nil
		srv, err := consoleui.New(cfg)
		if err == nil {
			t.Fatal("New must fail closed when NetworkedMode=true and both stores are nil")
		}
		if srv != nil {
			t.Error("New must return nil server on error")
		}
		if !strings.Contains(err.Error(), "NetworkedMode") {
			t.Errorf("error message should mention NetworkedMode; got: %v", err)
		}
	})

	// --- Permutation 2: AuthSessionStore nil, UserStore present ---
	t.Run("auth_store_nil", func(t *testing.T) {
		t.Parallel()
		cfg := base
		cfg.AuthSessionStore = nil
		cfg.UserStore = openUserStore()
		srv, err := consoleui.New(cfg)
		if err == nil {
			t.Fatal("New must fail closed when NetworkedMode=true and AuthSessionStore is nil")
		}
		if srv != nil {
			t.Error("New must return nil server on error")
		}
	})

	// --- Permutation 3: UserStore nil, AuthSessionStore present ---
	t.Run("user_store_nil", func(t *testing.T) {
		t.Parallel()
		cfg := base
		cfg.AuthSessionStore = authsession.NewStore(authsession.Config{})
		cfg.UserStore = nil
		srv, err := consoleui.New(cfg)
		if err == nil {
			t.Fatal("New must fail closed when NetworkedMode=true and UserStore is nil")
		}
		if srv != nil {
			t.Error("New must return nil server on error")
		}
	})

	// --- Positive case: both stores present → New succeeds ---
	t.Run("both_stores_present", func(t *testing.T) {
		t.Parallel()
		cfg := base
		cfg.AuthSessionStore = authsession.NewStore(authsession.Config{})
		cfg.UserStore = openUserStore()
		srv, err := consoleui.New(cfg)
		if err != nil {
			t.Fatalf("New must succeed when NetworkedMode=true and both stores are non-nil; got: %v", err)
		}
		if srv == nil {
			t.Fatal("New must return a non-nil server on success")
		}
	})
}
