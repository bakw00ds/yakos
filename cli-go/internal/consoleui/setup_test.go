package consoleui_test

// setup_test.go — tests for ADR-0005 Phase 3c: /setup route, edge redirect,
// and zero-users navigation behaviour.
//
// Test matrix:
//   - Zero users + valid token → 200, admin created, token consumed.
//   - Replay consumed token → 403.
//   - Expired token → 403.
//   - Wrong token → 403.
//   - Users already exist → POST /setup 409; GET /setup 302 /login.
//   - Password < MinPasswordLen → 400.
//   - Invalid username → 400.
//   - Edge: networked zero-users navigation → 302 /setup.
//   - Edge: after first admin created → 302 /login.
//   - Loopback path unchanged (no /setup route, no redirect change).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/authsession"
	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/setuptoken"
	"github.com/bakw00ds/yakos/internal/userstore"
)

// ---- helpers ----------------------------------------------------------------

// buildNetworkedSetupSrv builds a networked console Server with an in-memory
// user store and a setup-token state for testing /setup.
// The uStore is returned so callers can pre-populate users or inspect state.
func buildNetworkedSetupSrv(t *testing.T, st *setuptoken.State) (*consoleui.Server, *userstore.Store, *authsession.Store) {
	t.Helper()
	dir := t.TempDir()
	uStore, err := userstore.Open(filepath.Join(dir, "users.json"))
	if err != nil {
		t.Fatalf("userstore.Open: %v", err)
	}
	authStore := authsession.NewStore(authsession.Config{})

	srv := consoleui.MustNew(t, consoleui.Config{
		Addr:             "10.0.0.1:7890",
		NetworkedMode:    true,
		Token:            "test-token",
		AuthSessionStore: authStore,
		UserStore:        uStore,
		SetupToken:       st,
		// No TLSConfig: Serve() would fail, but Handler/FullHandler tests work.
	})
	return srv, uStore, authStore
}

// postSetup sends POST /setup with the given JSON body to srv's FullHandler.
func postSetup(t *testing.T, srv *consoleui.Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/setup",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.0.0.1:5555"
	rr := httptest.NewRecorder()
	srv.FullHandler().ServeHTTP(rr, req)
	return rr
}

// getSetup sends GET /setup to srv's FullHandler.
func getSetup(t *testing.T, srv *consoleui.Server) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/setup", nil)
	req.RemoteAddr = "10.0.0.1:5555"
	rr := httptest.NewRecorder()
	srv.FullHandler().ServeHTTP(rr, req)
	return rr
}

// newSetupState builds a setuptoken.State with an optional fixed clock.
// filePath is under the test's TempDir.
func newSetupState(t *testing.T, nowFn func() time.Time) *setuptoken.State {
	t.Helper()
	dir := t.TempDir()
	return setuptoken.New(filepath.Join(dir, "setup-token"), nowFn)
}

// ---- POST /setup: happy path ------------------------------------------------

func TestPostSetup_ZeroUsersValidToken_CreatesAdmin(t *testing.T) {
	t.Parallel()
	st := newSetupState(t, nil)
	tok, _ := st.Generate()
	srv, uStore, _ := buildNetworkedSetupSrv(t, st)

	body := fmt.Sprintf(`{"token":%q,"username":"firstadmin","password":"securepassword1"}`, tok)
	rr := postSetup(t, srv, body)

	if rr.Code != http.StatusOK {
		t.Fatalf("POST /setup want 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.OK {
		t.Error("response.ok should be true")
	}

	// User should exist in the store with admin role.
	pu, ok := uStore.Get("firstadmin")
	if !ok {
		t.Fatal("user 'firstadmin' should exist after POST /setup")
	}
	if pu.Role != netid.RoleAdmin {
		t.Errorf("first user role = %q, want admin", pu.Role)
	}
}

func TestPostSetup_ZeroUsersValidToken_ConsumesToken(t *testing.T) {
	t.Parallel()
	st := newSetupState(t, nil)
	tok, _ := st.Generate()
	srv, _, _ := buildNetworkedSetupSrv(t, st)

	body := fmt.Sprintf(`{"token":%q,"username":"firstadmin","password":"securepassword1"}`, tok)
	rr := postSetup(t, srv, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /setup want 200, got %d", rr.Code)
	}

	// Token must be consumed: IsActive() returns false.
	if st.IsActive() {
		t.Error("setup token should be consumed after successful POST /setup")
	}
}

// ---- POST /setup: replay / expired / wrong token ----------------------------

func TestPostSetup_ReplayConsumedToken_Returns403(t *testing.T) {
	t.Parallel()
	st := newSetupState(t, nil)
	tok, _ := st.Generate()
	srv, _, _ := buildNetworkedSetupSrv(t, st)

	body := fmt.Sprintf(`{"token":%q,"username":"firstadmin","password":"securepassword1"}`, tok)
	// First request: success.
	rr1 := postSetup(t, srv, body)
	if rr1.Code != http.StatusOK {
		t.Fatalf("first POST /setup want 200, got %d", rr1.Code)
	}

	// Second request: replay — must fail with 403 (users exist now → 409) OR 403 if
	// checked before Count. The implementation checks Count first → 409.
	rr2 := postSetup(t, srv, body)
	if rr2.Code != http.StatusConflict && rr2.Code != http.StatusForbidden {
		t.Errorf("replay POST /setup want 409 or 403, got %d", rr2.Code)
	}
}

func TestPostSetup_WrongToken_Returns403(t *testing.T) {
	t.Parallel()
	st := newSetupState(t, nil)
	st.Generate() //nolint:errcheck
	srv, _, _ := buildNetworkedSetupSrv(t, st)

	body := `{"token":"definitely-the-wrong-token","username":"firstadmin","password":"securepassword1"}`
	rr := postSetup(t, srv, body)
	if rr.Code != http.StatusForbidden {
		t.Errorf("wrong token: want 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestPostSetup_ExpiredToken_Returns403(t *testing.T) {
	t.Parallel()
	// Use a controllable clock: starts in the past (Generate phase),
	// then advances to the present (Validate phase) so the token appears expired.
	now := time.Now()
	past := now.Add(-(setuptoken.TokenTTL + time.Minute))

	// phase 0 = Generate time (past), phase 1 = real time (expired)
	phase := 0
	clockFn := func() time.Time {
		if phase == 0 {
			return past
		}
		return now
	}

	dir := t.TempDir()
	st := setuptoken.New(filepath.Join(dir, "setup-token"), clockFn)
	tok, err := st.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Advance clock: Validate will now see the token as expired.
	phase = 1

	srv, _, _ := buildNetworkedSetupSrv(t, st)

	body := fmt.Sprintf(`{"token":%q,"username":"firstadmin","password":"securepassword1"}`, tok)
	rr := postSetup(t, srv, body)
	if rr.Code != http.StatusForbidden {
		t.Errorf("expired token: want 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ---- POST /setup: users already exist → 409 ---------------------------------

func TestPostSetup_UsersExist_Returns409(t *testing.T) {
	t.Parallel()
	st := newSetupState(t, nil)
	tok, _ := st.Generate()
	srv, uStore, _ := buildNetworkedSetupSrv(t, st)

	// Pre-populate a user.
	if err := uStore.Create("existing", "existingpassword123", netid.RoleAdmin); err != nil {
		t.Fatalf("pre-create user: %v", err)
	}

	body := fmt.Sprintf(`{"token":%q,"username":"newadmin","password":"securepassword1"}`, tok)
	rr := postSetup(t, srv, body)
	if rr.Code != http.StatusConflict {
		t.Errorf("users exist: want 409, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestGetSetup_UsersExist_RedirectsToLogin(t *testing.T) {
	t.Parallel()
	st := newSetupState(t, nil)
	srv, uStore, _ := buildNetworkedSetupSrv(t, st)

	if err := uStore.Create("existing", "existingpassword123", netid.RoleAdmin); err != nil {
		t.Fatalf("pre-create user: %v", err)
	}

	rr := getSetup(t, srv)
	if rr.Code != http.StatusFound {
		t.Errorf("GET /setup with users: want 302, got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/login" {
		t.Errorf("GET /setup with users: redirect location = %q, want /login", loc)
	}
}

// ---- POST /setup: validation errors -----------------------------------------

func TestPostSetup_PasswordTooShort_Returns400(t *testing.T) {
	t.Parallel()
	st := newSetupState(t, nil)
	tok, _ := st.Generate()
	srv, _, _ := buildNetworkedSetupSrv(t, st)

	// Password is shorter than MinPasswordLen (12).
	body := fmt.Sprintf(`{"token":%q,"username":"firstadmin","password":"short"}`, tok)
	rr := postSetup(t, srv, body)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("short password: want 400, got %d: %s", rr.Code, rr.Body.String())
	}
	// Token must NOT be consumed (setup failed).
	if !st.IsActive() {
		t.Error("setup token should still be active after 400")
	}
}

func TestPostSetup_InvalidUsername_Returns400(t *testing.T) {
	t.Parallel()
	st := newSetupState(t, nil)
	tok, _ := st.Generate()
	srv, _, _ := buildNetworkedSetupSrv(t, st)

	// Username with invalid characters.
	body := fmt.Sprintf(`{"token":%q,"username":"invalid/user!","password":"securepassword1"}`, tok)
	rr := postSetup(t, srv, body)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("invalid username: want 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestPostSetup_EmptyToken_Returns403(t *testing.T) {
	t.Parallel()
	st := newSetupState(t, nil)
	st.Generate() //nolint:errcheck
	srv, _, _ := buildNetworkedSetupSrv(t, st)

	body := `{"token":"","username":"firstadmin","password":"securepassword1"}`
	rr := postSetup(t, srv, body)
	if rr.Code != http.StatusForbidden {
		t.Errorf("empty token: want 403, got %d", rr.Code)
	}
}

// ---- Edge: zero-users navigation → /setup redirect -------------------------
//
// These tests use a real httptest.Server (not httptest.ResponseRecorder) so that
// the resolver middleware runs fully and produces a real unauthenticated identity
// (no cert, no session cookie → Resolved=true, Authenticated=false).

// newNetworkedZeroUsersSrv builds a networked httptest.Server with zero users
// and an active setup token.  Returns the server and the setup-token State.
func newNetworkedZeroUsersSrv(t *testing.T) (*httptest.Server, *setuptoken.State, *userstore.Store) {
	t.Helper()
	dir := t.TempDir()
	uStore, err := userstore.Open(filepath.Join(dir, "users.json"))
	if err != nil {
		t.Fatalf("userstore.Open: %v", err)
	}
	authStore := authsession.NewStore(authsession.Config{})
	st := setuptoken.New(filepath.Join(dir, "setup-token"), nil)
	st.Generate() //nolint:errcheck

	srv := consoleui.MustNew(t, consoleui.Config{
		Addr:             "10.0.0.1:7890",
		NetworkedMode:    true,
		Token:            "test-token",
		AuthSessionStore: authStore,
		UserStore:        uStore,
		SetupToken:       st,
	})
	ts := httptest.NewServer(srv.FullHandler())
	t.Cleanup(ts.Close)
	return ts, st, uStore
}

func TestEdge_ZeroUsersNavigation_RedirectsToSetup(t *testing.T) {
	t.Parallel()
	ts, _, _ := newNetworkedZeroUsersSrv(t)

	// Browser navigation to /kanban/ with no session: resolver → unauthenticated.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/kanban/", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	// No Authorization header, no session cookie.

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /kanban/: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Errorf("zero-users navigation: want 302, got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc != "/setup" {
		t.Errorf("zero-users navigation redirect = %q, want /setup", loc)
	}
}

func TestEdge_AfterFirstAdmin_RedirectsToLogin(t *testing.T) {
	t.Parallel()
	ts, _, uStore := newNetworkedZeroUsersSrv(t)

	// Create the first admin — now Count() > 0.
	if err := uStore.Create("admin", "adminpassword123", netid.RoleAdmin); err != nil {
		t.Fatalf("create admin: %v", err)
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/kanban/", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /kanban/ after admin: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Errorf("after admin: want 302, got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc != "/login" {
		t.Errorf("after admin redirect = %q, want /login", loc)
	}
}

func TestEdge_ZeroUsersAPIRequest_Returns401(t *testing.T) {
	t.Parallel()
	ts, _, _ := newNetworkedZeroUsersSrv(t)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/presence", nil)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /api/presence: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("zero-users API: want 401, got %d", resp.StatusCode)
	}
}

// ---- GET /setup: accessible without auth ------------------------------------

func TestGetSetup_ZeroUsers_ServesSrv(t *testing.T) {
	t.Parallel()
	st := newSetupState(t, nil)
	st.Generate() //nolint:errcheck
	srv, _, _ := buildNetworkedSetupSrv(t, st)

	rr := getSetup(t, srv)
	// Should be 200 (serves the setup HTML).
	if rr.Code != http.StatusOK {
		t.Errorf("GET /setup want 200, got %d", rr.Code)
	}
	ct := rr.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("GET /setup Content-Type = %q, want text/html", ct)
	}
}

// ---- GET /setup.js: token-exempt --------------------------------------------

func TestGetSetupJS_TokenExempt(t *testing.T) {
	t.Parallel()
	st := newSetupState(t, nil)
	st.Generate() //nolint:errcheck
	srv, _, _ := buildNetworkedSetupSrv(t, st)

	req := httptest.NewRequest(http.MethodGet, "/setup.js", nil)
	req.RemoteAddr = "10.0.0.1:5555"
	rr := httptest.NewRecorder()
	srv.FullHandler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("GET /setup.js want 200, got %d", rr.Code)
	}
	ct := rr.Header().Get("Content-Type")
	if !strings.Contains(ct, "javascript") {
		t.Errorf("GET /setup.js Content-Type = %q, want javascript", ct)
	}
}

// ---- Loopback path: unchanged -----------------------------------------------

func TestLoopback_SetupRouteNotWired(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	uStore, _ := userstore.Open(filepath.Join(dir, "users.json"))
	authStore := authsession.NewStore(authsession.Config{})

	// Loopback (NetworkedMode=false): setup route is not wired.
	srv := consoleui.MustNew(t, consoleui.Config{
		Addr:             "127.0.0.1:7890",
		NetworkedMode:    false,
		Token:            "test-token",
		AuthSessionStore: authStore,
		UserStore:        uStore,
		// SetupToken is nil: loopback never generates one.
	})

	// GET /setup on loopback should 404 (route not registered).
	req := httptest.NewRequest(http.MethodGet, "/setup", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("loopback GET /setup want 404, got %d", rr.Code)
	}
}

func TestLoopback_NavigationNotRedirectedToSetup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	uStore, _ := userstore.Open(filepath.Join(dir, "users.json"))
	authStore := authsession.NewStore(authsession.Config{})

	srv := consoleui.MustNew(t, consoleui.Config{
		Addr:             "127.0.0.1:7890",
		NetworkedMode:    false,
		Token:            "test-token",
		AuthSessionStore: authStore,
		UserStore:        uStore,
	})

	// Loopback navigation (no auth) should NOT redirect to /setup.
	// The loopback path uses the requireTokenForNonStatic middleware, which
	// either serves static assets or requires the bearer token.
	req := httptest.NewRequest(http.MethodGet, "/api/presence", nil)
	// No bearer token: 401 or 403 (depends on loopback middleware), but NOT a /setup redirect.
	rr := httptest.NewRecorder()
	srv.FullHandler().ServeHTTP(rr, req)

	if loc := rr.Header().Get("Location"); loc == "/setup" {
		t.Error("loopback: should NOT redirect to /setup")
	}
}

// ---- Consume-then-fail lockout regression tests -----------------------------
//
// HIGH finding: a malformed username that passes the handler's weak check
// (username != "") but fails userstore.ValidateUsername (e.g. "bad/name",
// "..", ".", 65-char, "bad name") would previously reach ValidateAndConsume,
// burn the one-time token, then fail CreateFirstAdmin with 400 → zero admins,
// token gone, setup locked out.
//
// The fix: handler calls userstore.ValidateUsername BEFORE ValidateAndConsume.
// These tests assert: (a) each bad username → 400, (b) token still active,
// (c) Count==0, (d) a subsequent valid POST with the SAME token succeeds.

func TestPostSetup_MalformedUsername_TokenNotConsumed(t *testing.T) {
	t.Parallel()

	badUsernames := []struct {
		name  string
		input string
	}{
		{"slash", "bad/name"},
		{"dot-dot", ".."},
		{"dot", "."},
		{"too-long", strings.Repeat("a", 65)},
		{"space", "bad name"},
		{"bang", "user!"},
	}

	for _, tc := range badUsernames {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := newSetupState(t, nil)
			tok, err := st.Generate()
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			srv, uStore, _ := buildNetworkedSetupSrv(t, st)

			// POST with the malformed username + valid token + valid password.
			body := fmt.Sprintf(`{"token":%q,"username":%q,"password":"securepassword1"}`, tok, tc.input)
			rr := postSetup(t, srv, body)

			// Must be 400 (validation error).
			if rr.Code != http.StatusBadRequest {
				t.Errorf("username %q: want 400, got %d: %s", tc.input, rr.Code, rr.Body.String())
			}
			// Token must NOT be consumed.
			if !st.IsActive() {
				t.Errorf("username %q: setup token was consumed on 400 — DoS lockout bug", tc.input)
			}
			// No user must have been created.
			if uStore.Count() != 0 {
				t.Errorf("username %q: Count()=%d after 400, want 0", tc.input, uStore.Count())
			}

			// Prove the same token still works: a valid POST after the bad one must succeed.
			goodBody := fmt.Sprintf(`{"token":%q,"username":"validadmin","password":"securepassword1"}`, tok)
			rr2 := postSetup(t, srv, goodBody)
			if rr2.Code != http.StatusOK {
				t.Errorf("username %q: recovery POST with same token: want 200, got %d: %s",
					tc.input, rr2.Code, rr2.Body.String())
			}
			if uStore.Count() != 1 {
				t.Errorf("username %q: Count()=%d after recovery POST, want 1", tc.input, uStore.Count())
			}
		})
	}
}

// ---- Concurrent TOCTOU regression test -------------------------------------
//
// Red-team finding: N goroutines all POST /setup with the SAME valid token
// and DISTINCT usernames — must result in EXACTLY ONE 200 and Count()==1.
// The test also runs under -race to detect data-race amplifications.
//
// Each goroutine uses a distinct RemoteAddr (unique fake IP per goroutine) so
// the per-IP rate limiter does not mask the race: without distinct IPs, the
// rate limiter fires after loginRateLimitRequests=20 attempts from the same IP,
// hiding whether the token-consumption logic is correct for goroutines 21-N.
//
// This test uses a real httptest.Server (not httptest.NewRecorder) so the full
// middleware stack fires and each goroutine gets a distinct HTTP connection.

func TestConcurrentSetup_ExactlyOneAdminCreated(t *testing.T) {
	// Not Parallel: this test creates a lot of goroutines and is sensitive to
	// scheduler timing; running it serially avoids interference with other tests.
	const goroutines = 32

	dir := t.TempDir()
	uStore, err := userstore.Open(filepath.Join(dir, "users.json"))
	if err != nil {
		t.Fatalf("userstore.Open: %v", err)
	}
	authStore := authsession.NewStore(authsession.Config{})
	st := setuptoken.New(filepath.Join(dir, "setup-token"), nil)
	tok, err := st.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	srv := consoleui.MustNew(t, consoleui.Config{
		Addr:             "10.0.0.1:7890",
		NetworkedMode:    true,
		Token:            "test-token",
		AuthSessionStore: authStore,
		UserStore:        uStore,
		SetupToken:       st,
	})

	// Wrap FullHandler() so we can inject a per-request RemoteAddr.
	// httptest.NewServer uses net/http which sets RemoteAddr from the TCP
	// connection; we intercept at the handler level instead.
	var goroutineIP [goroutines]string
	for i := 0; i < goroutines; i++ {
		goroutineIP[i] = fmt.Sprintf("10.1.%d.%d:12345", i/256, i%256)
	}

	// remoteAddrKey is a private context key for injecting RemoteAddr in tests.
	type remoteAddrKey struct{}
	_ = remoteAddrKey{} // ensure it is used

	// We use httptest.NewRecorder per goroutine rather than a shared server so
	// we can set req.RemoteAddr directly (httptest.Server overwrites RemoteAddr
	// from the TCP dial, which always gives 127.0.0.1).
	var (
		wg         sync.WaitGroup
		startGate  = make(chan struct{})
		successCnt int32 // atomic — 200 responses
	)

	handler := srv.FullHandler()

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-startGate

			username := fmt.Sprintf("attacker%d", idx)
			body := fmt.Sprintf(`{"token":%q,"username":%q,"password":"securepassword1"}`, tok, username)
			req := httptest.NewRequest(http.MethodPost, "/setup",
				bytes.NewBufferString(body))
			req.Header.Set("Content-Type", "application/json")
			// Distinct RemoteAddr per goroutine: bypasses per-IP rate limiter so
			// all goroutines reach the token-validation path.
			req.RemoteAddr = goroutineIP[idx]

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code == http.StatusOK {
				atomic.AddInt32(&successCnt, 1)
			}
		}(i)
	}

	close(startGate)
	wg.Wait()

	got200 := atomic.LoadInt32(&successCnt)
	count := uStore.Count()

	// Security invariants: exactly one admin created, exactly one 200.
	if got200 != 1 {
		t.Errorf("concurrent POST /setup: want exactly 1 success (200), got %d", got200)
	}
	if count != 1 {
		t.Errorf("concurrent POST /setup: userStore.Count() = %d, want 1", count)
	}
	// Token must be consumed: IsActive() false.
	if st.IsActive() {
		t.Error("setup token should be consumed after successful POST /setup")
	}
}
