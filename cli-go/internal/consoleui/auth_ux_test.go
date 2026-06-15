package consoleui_test

// auth_ux_test.go — tests for the two auth-UX bugs fixed in fix/auth-ux.
//
// Bug 1 — setup.js / login.js error surfacing:
//   These bugs are in JavaScript (dist/setup.js, dist/login.js).  The Go test
//   here covers the server contract that the JS depends on: each error path must
//   return the correct HTTP status AND a JSON body with an "error" field.  The
//   JS client parses that body to produce the inline error message.
//
// Bug 2 — web root "/" gated on the networked bind:
//   On the networked bind, unauthenticated GET / must return 302 to /setup
//   (zero users) or /login (users exist).  An authenticated session or cert
//   request must still receive 200.  Sub-resources (/app.js, /styles.css,
//   /sw.js, /vendor/*) remain fully exempt.  The loopback path is unchanged.
//
// Tests:
//
//  1. TestSetupErrorBodies_ServerReturnsJSONErrorField — POST /setup with
//     various invalid inputs verifies the server returns {"error":"..."} with
//     the right status, so the JS can display it.
//
//  2. TestWebRoot_Networked_UnauthenticatedZeroUsers_RedirectsSetup —
//     GET / on networked bind, zero users, no session → 302 /setup.
//
//  3. TestWebRoot_Networked_UnauthenticatedUsersExist_RedirectsLogin —
//     GET / on networked bind, user exists, no session → 302 /login.
//
//  4. TestWebRoot_Networked_AuthenticatedSession_Returns200 —
//     GET / on networked bind, valid session cookie → 200 (shell served).
//
//  5. TestWebRoot_Networked_SubResourcesExempt —
//     GET /app.js, /styles.css, /sw.js, /vendor/... on networked bind,
//     no session → 200 (sub-resources are still exempt).
//
//  6. TestWebRoot_Loopback_Unchanged —
//     GET / on loopback bind, no token → 200 (loopback unchanged; bearer
//     token model is cooperative-labeling; "/" is token-exempt on loopback).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/authsession"
	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/setuptoken"
	"github.com/bakw00ds/yakos/internal/userstore"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// ---- Bug 1: server contract for JS error surfacing --------------------------

// TestSetupErrorBodies_ServerReturnsJSONErrorField verifies that every
// non-2xx response from POST /setup includes a JSON body containing an
// "error" field.  The JS client (dist/setup.js) parses this field to display
// the inline error message; if the server omits it the UI silently no-ops.
func TestSetupErrorBodies_ServerReturnsJSONErrorField(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	st := setuptoken.New(filepath.Join(dir, "setup-token"), nil)
	validTok, err := st.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	uStore, err := userstore.Open(filepath.Join(dir, "users.json"))
	if err != nil {
		t.Fatalf("userstore.Open: %v", err)
	}
	aStore := authsession.NewStore(authsession.Config{})

	srv := consoleui.MustNew(t, consoleui.Config{
		Addr:             "10.0.0.1:7890",
		NetworkedMode:    true,
		Token:            "test-token",
		AuthSessionStore: aStore,
		UserStore:        uStore,
		SetupToken:       st,
	})
	handler := srv.FullHandler()

	cases := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{
			name:       "password_too_short_400",
			body:       `{"token":"` + validTok + `","username":"admin","password":"short"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid_username_400",
			body:       `{"token":"` + validTok + `","username":"bad/name!","password":"securepassword123"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "wrong_token_403",
			body:       `{"token":"wrong-token-value","username":"admin","password":"securepassword123"}`,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "empty_token_403",
			body:       `{"token":"","username":"admin","password":"securepassword123"}`,
			wantStatus: http.StatusForbidden,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, "/setup", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.RemoteAddr = "10.0.0.1:5555"
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != tc.wantStatus {
				t.Fatalf("POST /setup %s: status=%d; want %d (body: %s)",
					tc.name, rr.Code, tc.wantStatus, rr.Body.String())
			}

			// The response body must be valid JSON containing an "error" field.
			// The JS client parses this to display the inline error message.
			var resp map[string]interface{}
			if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
				t.Fatalf("POST /setup %s: response body is not valid JSON: %v (body: %s)",
					tc.name, err, rr.Body.String())
			}
			errMsg, ok := resp["error"].(string)
			if !ok || errMsg == "" {
				t.Errorf("POST /setup %s: JSON body missing non-empty 'error' field; got: %v",
					tc.name, resp)
			}

			// Content-Type must be application/json so the JS can parse it.
			ct := rr.Header().Get("Content-Type")
			if !strings.Contains(ct, "application/json") {
				t.Errorf("POST /setup %s: Content-Type=%q; want application/json", tc.name, ct)
			}
		})
	}

	// Additional case: 409 Conflict (users already exist).
	// Must also carry a JSON error body so the JS can display it.
	t.Run("users_exist_409", func(t *testing.T) {
		t.Parallel()

		dir2 := t.TempDir()
		st2 := setuptoken.New(filepath.Join(dir2, "setup-token"), nil)
		tok2, _ := st2.Generate()
		uStore2, _ := userstore.Open(filepath.Join(dir2, "users.json"))
		aStore2 := authsession.NewStore(authsession.Config{})
		if err := uStore2.Create("existing", "existingpassword123", netid.RoleAdmin); err != nil {
			t.Fatalf("pre-create user: %v", err)
		}

		srv2 := consoleui.MustNew(t, consoleui.Config{
			Addr:             "10.0.0.1:7890",
			NetworkedMode:    true,
			Token:            "test-token",
			AuthSessionStore: aStore2,
			UserStore:        uStore2,
			SetupToken:       st2,
		})

		body := `{"token":"` + tok2 + `","username":"admin","password":"securepassword123"}`
		req := httptest.NewRequest(http.MethodPost, "/setup", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "10.0.0.1:5555"
		rr := httptest.NewRecorder()
		srv2.FullHandler().ServeHTTP(rr, req)

		if rr.Code != http.StatusConflict {
			t.Fatalf("409 case: status=%d; want 409 (body: %s)", rr.Code, rr.Body.String())
		}
		var resp map[string]interface{}
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("409 case: response body is not valid JSON: %v", err)
		}
		if _, ok := resp["error"].(string); !ok {
			t.Errorf("409 case: JSON body missing 'error' field; got: %v", resp)
		}
	})
}

// ---- Bug 2 helpers -----------------------------------------------------------

// newNetworkedZeroUsersSrvForUX is like newNetworkedZeroUsersSrv (setup_test.go)
// but returns a *consoleui.Server (not an httptest.Server) so tests can call
// FullHandler() for request-level identity injection via the resolver middleware.
func newNetworkedZeroUsersSrvForUX(t *testing.T) (*consoleui.Server, *userstore.Store, *authsession.Store) {
	t.Helper()
	dir := t.TempDir()
	uStore, err := userstore.Open(filepath.Join(dir, "users.json"))
	if err != nil {
		t.Fatalf("userstore.Open: %v", err)
	}
	aStore := authsession.NewStore(authsession.Config{})
	st := setuptoken.New(filepath.Join(dir, "setup-token"), nil)
	st.Generate() //nolint:errcheck

	srv := consoleui.MustNew(t, consoleui.Config{
		Addr:             "10.0.0.1:7890",
		NetworkedMode:    true,
		Token:            "test-token",
		AuthSessionStore: aStore,
		UserStore:        uStore,
		SetupToken:       st,
	})
	return srv, uStore, aStore
}

// ---- Bug 2, Test 2: GET / networked unauthenticated, zero users → /setup ----

// TestWebRoot_Networked_UnauthenticatedZeroUsers_RedirectsSetup verifies that
// an unauthenticated GET / on the networked bind redirects to /setup when no
// users exist yet (the first-admin bootstrap state).
//
// This is the exact scenario observed in the bug report: the operator opened
// the web root and saw the full console SPA shell even though they were
// unauthenticated.
func TestWebRoot_Networked_UnauthenticatedZeroUsers_RedirectsSetup(t *testing.T) {
	t.Parallel()
	srv, _, _ := newNetworkedZeroUsersSrvForUX(t)

	// Wrap with httptest so the resolver middleware runs and sets Resolved=true.
	ts := httptest.NewServer(srv.FullHandler())
	t.Cleanup(ts.Close)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	// Simulate a browser navigation — no session cookie, no Accept: application/json.
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse // don't follow redirects; inspect them
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Errorf("GET / (networked, zero users, no session): status=%d; want 302", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc != "/setup" {
		t.Errorf("GET / (networked, zero users, no session): redirect=%q; want /setup", loc)
	}
}

// ---- Bug 2, Test 3: GET / networked unauthenticated, users exist → /login ----

// TestWebRoot_Networked_UnauthenticatedUsersExist_RedirectsLogin verifies that
// an unauthenticated GET / on the networked bind redirects to /login when at
// least one user has been created (post-setup state).
func TestWebRoot_Networked_UnauthenticatedUsersExist_RedirectsLogin(t *testing.T) {
	t.Parallel()
	srv, uStore, _ := newNetworkedZeroUsersSrvForUX(t)

	// Create the first admin so Count() > 0.
	if err := uStore.Create("admin", "adminpassword123", netid.RoleAdmin); err != nil {
		t.Fatalf("create admin: %v", err)
	}

	ts := httptest.NewServer(srv.FullHandler())
	t.Cleanup(ts.Close)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Errorf("GET / (networked, users exist, no session): status=%d; want 302", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc != "/login" {
		t.Errorf("GET / (networked, users exist, no session): redirect=%q; want /login", loc)
	}
}

// ---- Bug 2, Test 4: GET / networked, valid session → 200 --------------------

// TestWebRoot_Networked_AuthenticatedSession_Returns200 verifies that an
// authenticated request (valid session cookie) to GET / on the networked bind
// receives 200 — the SPA shell is served normally for authenticated users.
func TestWebRoot_Networked_AuthenticatedSession_Returns200(t *testing.T) {
	t.Parallel()

	// Build the full server with an alice user and log in to get a session.
	s := newAuth3bServer(t)

	// Step 1: POST /login to get a session cookie.
	loginResp := s.loginReq(t, "alice", "correcthorsebattery1")
	if loginResp.StatusCode != http.StatusOK {
		loginResp.Body.Close()
		t.Fatalf("POST /login: status=%d; want 200", loginResp.StatusCode)
	}
	cookies := cookieMap(loginResp)
	loginResp.Body.Close()

	sessCookie, ok := cookies[consoleui.SessionCookieName]
	if !ok {
		t.Fatal("POST /login: no session cookie in response")
	}

	// Step 2: GET / with the session cookie — must return 200.
	req, _ := http.NewRequest(http.MethodGet, s.ts.URL+"/", nil)
	req.AddCookie(sessCookie)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	client := s.ts.Client()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET / with session: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET / (networked, valid session): status=%d; want 200 (SPA shell must be served when authenticated)", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("GET / (networked, valid session): Content-Type=%q; want text/html", ct)
	}
}

// ---- Bug 2, Test 5: sub-resources still exempt on networked bind -------------

// TestWebRoot_Networked_SubResourcesExempt verifies that static sub-resources
// (/app.js, /styles.css, /sw.js, /vendor/mermaid.min.js) are still served
// without authentication on the networked bind.  These carry no workspace data
// and must be reachable before the user has a session (the browser needs them
// to render the login/setup pages and bootstrap the SPA).
func TestWebRoot_Networked_SubResourcesExempt(t *testing.T) {
	t.Parallel()
	srv, _, _ := newNetworkedZeroUsersSrvForUX(t)

	ts := httptest.NewServer(srv.FullHandler())
	t.Cleanup(ts.Close)

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	subResources := []string{
		"/app.js",
		"/styles.css",
		"/sw.js",
		"/vendor/mermaid.min.js",
		"/login.js",
		"/login.css",
		"/setup.js",
	}

	for _, path := range subResources {
		path := path
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
			// No session cookie — unauthenticated.

			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("GET %s (no session): %v", path, err)
			}
			defer resp.Body.Close()

			// Sub-resources must be served (200), not redirected (302).
			if resp.StatusCode == http.StatusFound {
				loc := resp.Header.Get("Location")
				t.Errorf("GET %s (networked, no session): got 302 to %q; sub-resources must be exempt from auth gate", path, loc)
			}
			if resp.StatusCode != http.StatusOK {
				t.Errorf("GET %s (networked, no session): status=%d; want 200 (sub-resource must be exempt)", path, resp.StatusCode)
			}
		})
	}
}

// ---- Bug 2, Test 6: loopback "/" unchanged -----------------------------------

// TestWebRoot_Loopback_Unchanged verifies that the loopback bind is completely
// unaffected by the "/" gating change.  On loopback, "/" is token-exempt and
// always returns 200 regardless of auth state — the bearer-token cooperative
// model is unchanged.
func TestWebRoot_Loopback_Unchanged(t *testing.T) {
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
		// NetworkedMode=false: loopback path, "/" must remain token-exempt.
	})

	// Wrap with the loopback token gate (not the networked CSRF/edge stack).
	loopbackHandler := consoleui.RequireTokenForNonStatic(tok,
		consoleui.RequireJSONForMutations(srv.Handler()))
	ts := httptest.NewServer(loopbackHandler)
	t.Cleanup(ts.Close)

	// GET / without token — must still return 200 on the loopback path.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	// No Authorization header, no session cookie.
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("loopback GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("loopback GET / (no token): status=%d; want 200 (loopback must be unchanged)", resp.StatusCode)
	}
}
