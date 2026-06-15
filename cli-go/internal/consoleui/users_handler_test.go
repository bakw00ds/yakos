package consoleui_test

// users_handler_test.go — tests for the ADR-0005 Phase 5 Users management API.
//
// Test matrix (all run with -race):
//
//  1. GET /api/users — returns all users, no password hashes.
//  2. POST /api/users — creates user; 409 duplicate; 400 invalid username;
//     400 password too short; 400 invalid role.
//  3. POST /api/users/role — sets role; bumps epoch (stale sessions invalidated);
//     cannot demote last admin (409).
//  4. POST /api/users/reset-password — sets temp password + passwordResetReq.
//  5. POST /api/users/disable — disables user + purges sessions; cannot disable
//     last admin (409).
//  6. POST /api/users/enable — re-enables user.
//  7. POST /api/users/delete — deletes user + purges sessions; cannot delete
//     last admin (409).
//  8. POST /api/account/password — self-service; verifies old password; changes
//     own password; cannot change another's via this endpoint.
//  9. GET /api/account — returns operatorId, role, authMethod.
// 10. RBAC: RoleRead / RoleDispatch → 403 on all /api/users/* routes.
// 11. CSRF: session-auth POST without X-CSRF-Token → 403.
// 12. Loopback (no session, bearer token) path reaches /api/users/role.
// 13. Networked path (session cookie + CSRF) path.
//
// Note: argon2id params are reduced to t=1,m=64KiB,p=1 for the test binary via
// TestMain in testmain_test.go. Do NOT call SetArgon2ParamsForTest here.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/authsession"
	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/userstore"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// ---- test infrastructure ----------------------------------------------------

// usersTestServer holds a full networked consoleui.Server with pre-populated
// users for testing the /api/users/* and /api/account/* endpoints.
type usersTestServer struct {
	srv       *consoleui.Server
	authStore *authsession.Store
	uStore    *userstore.Store
	token     string
}

// newUsersTestServer builds a networked Server with:
//   - alice: admin (pre-created)
//   - bob:   read (pre-created)
//
// The server's FullHandler is used so the full middleware stack (CSRF, auth
// redirect, JSON-mutation gate) is exercised.
func newUsersTestServer(t *testing.T) *usersTestServer {
	t.Helper()

	stateDir := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}

	uStore, err := userstore.Open(filepath.Join(t.TempDir(), "users.json"))
	if err != nil {
		t.Fatalf("userstore.Open: %v", err)
	}
	if err := uStore.Create("alice", "correcthorsebattery1", netid.RoleAdmin); err != nil {
		t.Fatalf("create alice: %v", err)
	}
	if err := uStore.Create("bob", "correcthorsebattery2", netid.RoleRead); err != nil {
		t.Fatalf("create bob: %v", err)
	}

	aStore := authsession.NewStore(authsession.Config{MaxSessions: 100})

	bus := wsbus.New()
	t.Cleanup(bus.Stop)

	srv := consoleui.MustNew(t, consoleui.Config{
		NetworkedMode:     true,
		Token:             tok,
		KanbanBoardPath:   filepath.Join(t.TempDir(), "kanban.md"),
		KanbanProject:     "test",
		MetricsProjectDir: t.TempDir(),
		PerfWorkDir:       t.TempDir(),
		Bus:               bus,
		WorkDir:           t.TempDir(),
		StateDir:          stateDir,
		AuthSessionStore:  aStore,
		UserStore:         uStore,
	})
	return &usersTestServer{
		srv:       srv,
		authStore: aStore,
		uStore:    uStore,
		token:     tok,
	}
}

// login authenticates as username/password and returns the session and CSRF
// cookies extracted from the response.
func (ts *usersTestServer) login(t *testing.T, username, password string) (sessionCookie, csrfCookie *http.Cookie) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.0.0.1:5555"
	rr := httptest.NewRecorder()
	ts.srv.FullHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("login %q: got %d, want 200; body: %s", username, rr.Code, rr.Body.String())
	}
	for _, c := range rr.Result().Cookies() {
		switch c.Name {
		case consoleui.SessionCookieName:
			sessionCookie = c
		case "yakos_csrf":
			csrfCookie = c
		}
	}
	if sessionCookie == nil || csrfCookie == nil {
		t.Fatalf("login %q: missing session or CSRF cookie", username)
	}
	return
}

// doAdminRequest sends an HTTP request as alice (admin) via session auth with CSRF.
func (ts *usersTestServer) doAdminRequest(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	sessCookie, csrfCookie := ts.login(t, "alice", "correcthorsebattery1")
	return ts.doSessionRequest(t, method, path, body, sessCookie, csrfCookie)
}

// doSessionRequest sends a request with the provided session and CSRF cookies.
func (ts *usersTestServer) doSessionRequest(t *testing.T, method, path, body string, sessCookie, csrfCookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var bodyReader *strings.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	} else {
		bodyReader = strings.NewReader("{}")
	}
	req := httptest.NewRequest(method, path, bodyReader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrfCookie.Value)
	req.AddCookie(sessCookie)
	req.AddCookie(csrfCookie)
	req.RemoteAddr = "10.0.0.1:5555"
	rr := httptest.NewRecorder()
	ts.srv.FullHandler().ServeHTTP(rr, req)
	return rr
}

// doLoopbackRequest sends a request via the loopback bearer-token path.
// The loopback path skips CSRF (AuthMethodNone); role is always admin on loopback.
// This uses srv.Handler() (no outer middleware) so the bearer token requirement
// is bypassed — matching the pattern in auth_3b_test.go for loopback tests.
func (ts *usersTestServer) doLoopbackRequest(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var bodyReader *strings.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	} else {
		bodyReader = strings.NewReader("{}")
	}
	req := httptest.NewRequest(method, path, bodyReader)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "127.0.0.1:5555"
	rr := httptest.NewRecorder()
	ts.srv.Handler().ServeHTTP(rr, req)
	return rr
}

// ---- 1. GET /api/users -------------------------------------------------------

func TestListUsers_AdminGetsList(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)

	rr := ts.doAdminRequest(t, http.MethodGet, "/api/users", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/users: got %d, want 200; body: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	users, ok := resp["users"].([]interface{})
	if !ok {
		t.Fatalf("response missing 'users' array: %v", resp)
	}
	if len(users) < 2 {
		t.Errorf("expected at least 2 users, got %d", len(users))
	}

	// Confirm no password hash in any user object.
	for _, u := range users {
		uMap, _ := u.(map[string]interface{})
		if _, hasHash := uMap["passwordHash"]; hasHash {
			t.Errorf("response contains passwordHash for user %v", uMap["username"])
		}
	}
}

func TestListUsers_RoleReadForbidden(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)
	sessCookie, csrfCookie := ts.login(t, "bob", "correcthorsebattery2")
	rr := ts.doSessionRequest(t, http.MethodGet, "/api/users", "", sessCookie, csrfCookie)
	if rr.Code != http.StatusForbidden {
		t.Errorf("GET /api/users as RoleRead: got %d, want 403", rr.Code)
	}
}

// ---- 2. POST /api/users (create) --------------------------------------------

func TestCreateUser_AdminCreatesUser(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)

	body := `{"username":"charlie","password":"securelongpassword1","role":"read"}`
	rr := ts.doAdminRequest(t, http.MethodPost, "/api/users", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("POST /api/users: got %d, want 201; body: %s", rr.Code, rr.Body.String())
	}

	// Verify the user exists in the store.
	pu, found := ts.uStore.Get("charlie")
	if !found {
		t.Fatal("charlie not found in store after create")
	}
	if pu.RoleString != "read" {
		t.Errorf("charlie role: got %q, want %q", pu.RoleString, "read")
	}
}

func TestCreateUser_DuplicateUsername_409(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)

	body := `{"username":"alice","password":"securelongpassword1","role":"read"}`
	rr := ts.doAdminRequest(t, http.MethodPost, "/api/users", body)
	if rr.Code != http.StatusConflict {
		t.Errorf("POST /api/users duplicate: got %d, want 409", rr.Code)
	}
}

func TestCreateUser_InvalidUsername_400(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)

	body := `{"username":"../evil","password":"securelongpassword1","role":"read"}`
	rr := ts.doAdminRequest(t, http.MethodPost, "/api/users", body)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("POST /api/users invalid username: got %d, want 400", rr.Code)
	}
}

func TestCreateUser_PasswordTooShort_400(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)

	body := `{"username":"newuser","password":"short","role":"read"}`
	rr := ts.doAdminRequest(t, http.MethodPost, "/api/users", body)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("POST /api/users short password: got %d, want 400", rr.Code)
	}
}

func TestCreateUser_InvalidRole_400(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)

	body := `{"username":"newuser","password":"securelongpassword1","role":"superadmin"}`
	rr := ts.doAdminRequest(t, http.MethodPost, "/api/users", body)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("POST /api/users invalid role: got %d, want 400", rr.Code)
	}
}

func TestCreateUser_RoleReadForbidden(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)
	sessCookie, csrfCookie := ts.login(t, "bob", "correcthorsebattery2")
	body := `{"username":"newuser","password":"securelongpassword1","role":"read"}`
	rr := ts.doSessionRequest(t, http.MethodPost, "/api/users", body, sessCookie, csrfCookie)
	if rr.Code != http.StatusForbidden {
		t.Errorf("POST /api/users as RoleRead: got %d, want 403", rr.Code)
	}
}

func TestCreateUser_NoCSRF_Forbidden(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)
	sessCookie, _ := ts.login(t, "alice", "correcthorsebattery1")

	// Send POST without CSRF header.
	body := `{"username":"newuser","password":"securelongpassword1","role":"read"}`
	req := httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// No X-CSRF-Token header.
	req.AddCookie(sessCookie)
	req.RemoteAddr = "10.0.0.1:5555"
	rr := httptest.NewRecorder()
	ts.srv.FullHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("POST /api/users without CSRF: got %d, want 403", rr.Code)
	}
}

// ---- 3. POST /api/users/role ------------------------------------------------

func TestSetRole_AdminSetsRole(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)

	// Get bob's initial epoch.
	bobBefore, _ := ts.uStore.Get("bob")
	epochBefore := bobBefore.SessionEpoch

	body := `{"username":"bob","role":"dispatch"}`
	rr := ts.doAdminRequest(t, http.MethodPost, "/api/users/role", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /api/users/role: got %d, want 200; body: %s", rr.Code, rr.Body.String())
	}

	// Epoch must be bumped.
	bobAfter, _ := ts.uStore.Get("bob")
	if bobAfter.SessionEpoch <= epochBefore {
		t.Errorf("set-role: sessionEpoch not bumped (before=%d, after=%d)", epochBefore, bobAfter.SessionEpoch)
	}
	if bobAfter.RoleString != "dispatch" {
		t.Errorf("set-role: role not updated (got %q, want %q)", bobAfter.RoleString, "dispatch")
	}
}

func TestSetRole_StaleSessions_Invalidated(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)

	// Give bob a session.
	bobPU, _ := ts.uStore.Get("bob")
	bobSess, err := ts.authStore.Create("bob", bobPU.Role, bobPU.SessionEpoch)
	if err != nil {
		t.Fatalf("create bob session: %v", err)
	}

	// Admin sets bob's role — this bumps epoch and should invalidate his session.
	body := `{"username":"bob","role":"dispatch"}`
	rr := ts.doAdminRequest(t, http.MethodPost, "/api/users/role", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /api/users/role: got %d; body: %s", rr.Code, rr.Body.String())
	}

	// Bob's old session should be gone from the session store.
	if _, found := ts.authStore.Lookup(bobSess.ID); found {
		t.Error("set-role: bob's old session should be invalidated after role change")
	}
}

func TestSetRole_CannotDemoteLastAdmin_409(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)

	// alice is the only admin; demoting her should be refused.
	body := `{"username":"alice","role":"read"}`
	rr := ts.doAdminRequest(t, http.MethodPost, "/api/users/role", body)
	if rr.Code != http.StatusConflict {
		t.Errorf("set-role demote last admin: got %d, want 409", rr.Code)
	}
}

func TestSetRole_RoleReadForbidden(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)
	sessCookie, csrfCookie := ts.login(t, "bob", "correcthorsebattery2")
	body := `{"username":"alice","role":"read"}`
	rr := ts.doSessionRequest(t, http.MethodPost, "/api/users/role", body, sessCookie, csrfCookie)
	if rr.Code != http.StatusForbidden {
		t.Errorf("POST /api/users/role as RoleRead: got %d, want 403", rr.Code)
	}
}

// ---- 4. POST /api/users/reset-password --------------------------------------

func TestResetPassword_AdminResetsPassword(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)

	body := `{"username":"bob","newPassword":"newsecurepassword1"}`
	rr := ts.doAdminRequest(t, http.MethodPost, "/api/users/reset-password", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /api/users/reset-password: got %d; body: %s", rr.Code, rr.Body.String())
	}

	// Verify passwordResetReq is set.
	bob, found := ts.uStore.Get("bob")
	if !found {
		t.Fatal("bob not found after reset-password")
	}
	if !bob.PasswordResetReq {
		t.Error("reset-password: passwordResetReq should be true after admin reset")
	}

	// Verify new password works.
	if _, err := ts.uStore.Verify("bob", "newsecurepassword1"); err != nil {
		t.Errorf("reset-password: new password should verify: %v", err)
	}
}

func TestResetPassword_RoleReadForbidden(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)
	sessCookie, csrfCookie := ts.login(t, "bob", "correcthorsebattery2")
	body := `{"username":"alice","newPassword":"newsecurepassword1"}`
	rr := ts.doSessionRequest(t, http.MethodPost, "/api/users/reset-password", body, sessCookie, csrfCookie)
	if rr.Code != http.StatusForbidden {
		t.Errorf("POST /api/users/reset-password as RoleRead: got %d, want 403", rr.Code)
	}
}

// ---- 5. POST /api/users/disable ---------------------------------------------

func TestDisableUser_AdminDisablesUser(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)

	// Give bob a live session.
	bobPU, _ := ts.uStore.Get("bob")
	bobSess, err := ts.authStore.Create("bob", bobPU.Role, bobPU.SessionEpoch)
	if err != nil {
		t.Fatalf("create bob session: %v", err)
	}

	body := `{"username":"bob"}`
	rr := ts.doAdminRequest(t, http.MethodPost, "/api/users/disable", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /api/users/disable: got %d; body: %s", rr.Code, rr.Body.String())
	}

	// Bob should be disabled in the store.
	bob, _ := ts.uStore.Get("bob")
	if !bob.Disabled {
		t.Error("disable: bob should be disabled in the store")
	}

	// Bob's live session should be purged.
	if _, found := ts.authStore.Lookup(bobSess.ID); found {
		t.Error("disable: bob's live session should be purged")
	}
}

func TestDisableUser_CannotDisableLastAdmin_409(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)

	body := `{"username":"alice"}`
	rr := ts.doAdminRequest(t, http.MethodPost, "/api/users/disable", body)
	if rr.Code != http.StatusConflict {
		t.Errorf("disable last admin: got %d, want 409", rr.Code)
	}
}

func TestDisableUser_RoleReadForbidden(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)
	sessCookie, csrfCookie := ts.login(t, "bob", "correcthorsebattery2")
	body := `{"username":"alice"}`
	rr := ts.doSessionRequest(t, http.MethodPost, "/api/users/disable", body, sessCookie, csrfCookie)
	if rr.Code != http.StatusForbidden {
		t.Errorf("POST /api/users/disable as RoleRead: got %d, want 403", rr.Code)
	}
}

// ---- 6. POST /api/users/enable ----------------------------------------------

func TestEnableUser_AdminEnablesUser(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)

	// Disable bob first.
	if err := ts.uStore.Disable("bob"); err != nil {
		t.Fatalf("disable bob: %v", err)
	}

	body := `{"username":"bob"}`
	rr := ts.doAdminRequest(t, http.MethodPost, "/api/users/enable", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /api/users/enable: got %d; body: %s", rr.Code, rr.Body.String())
	}

	bob, _ := ts.uStore.Get("bob")
	if bob.Disabled {
		t.Error("enable: bob should not be disabled after enable")
	}
}

func TestEnableUser_RoleReadForbidden(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)
	sessCookie, csrfCookie := ts.login(t, "bob", "correcthorsebattery2")
	body := `{"username":"alice"}`
	rr := ts.doSessionRequest(t, http.MethodPost, "/api/users/enable", body, sessCookie, csrfCookie)
	if rr.Code != http.StatusForbidden {
		t.Errorf("POST /api/users/enable as RoleRead: got %d, want 403", rr.Code)
	}
}

// ---- 7. POST /api/users/delete ----------------------------------------------

func TestDeleteUser_AdminDeletesUser(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)

	// Give bob a live session; it should be purged on delete.
	bobPU, _ := ts.uStore.Get("bob")
	bobSess, err := ts.authStore.Create("bob", bobPU.Role, bobPU.SessionEpoch)
	if err != nil {
		t.Fatalf("create bob session: %v", err)
	}

	body := `{"username":"bob"}`
	rr := ts.doAdminRequest(t, http.MethodPost, "/api/users/delete", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /api/users/delete: got %d; body: %s", rr.Code, rr.Body.String())
	}

	// Bob should not exist in the store.
	if _, found := ts.uStore.Get("bob"); found {
		t.Error("delete: bob should be removed from the store")
	}

	// Bob's live session should be purged.
	if _, found := ts.authStore.Lookup(bobSess.ID); found {
		t.Error("delete: bob's live session should be purged")
	}
}

func TestDeleteUser_CannotDeleteLastAdmin_409(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)

	body := `{"username":"alice"}`
	rr := ts.doAdminRequest(t, http.MethodPost, "/api/users/delete", body)
	if rr.Code != http.StatusConflict {
		t.Errorf("delete last admin: got %d, want 409", rr.Code)
	}
}

func TestDeleteUser_RoleReadForbidden(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)
	sessCookie, csrfCookie := ts.login(t, "bob", "correcthorsebattery2")
	body := `{"username":"alice"}`
	rr := ts.doSessionRequest(t, http.MethodPost, "/api/users/delete", body, sessCookie, csrfCookie)
	if rr.Code != http.StatusForbidden {
		t.Errorf("POST /api/users/delete as RoleRead: got %d, want 403", rr.Code)
	}
}

// ---- 8. POST /api/account/password (self-service) ---------------------------

func TestChangePassword_SelfService_OldVerified(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)

	sessCookie, csrfCookie := ts.login(t, "bob", "correcthorsebattery2")
	body := `{"oldPassword":"correcthorsebattery2","newPassword":"mynewpassword456"}`
	rr := ts.doSessionRequest(t, http.MethodPost, "/api/account/password", body, sessCookie, csrfCookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /api/account/password: got %d; body: %s", rr.Code, rr.Body.String())
	}

	// New password should work.
	if _, err := ts.uStore.Verify("bob", "mynewpassword456"); err != nil {
		t.Errorf("change password: new password should verify: %v", err)
	}
	// Old password should no longer work.
	if _, err := ts.uStore.Verify("bob", "correcthorsebattery2"); err == nil {
		t.Error("change password: old password should no longer work")
	}
}

func TestChangePassword_WrongOldPassword_401(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)

	sessCookie, csrfCookie := ts.login(t, "bob", "correcthorsebattery2")
	body := `{"oldPassword":"wrongpassword123","newPassword":"mynewpassword456"}`
	rr := ts.doSessionRequest(t, http.MethodPost, "/api/account/password", body, sessCookie, csrfCookie)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("change password wrong old: got %d, want 401", rr.Code)
	}
}

func TestChangePassword_NewTooShort_400(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)

	sessCookie, csrfCookie := ts.login(t, "bob", "correcthorsebattery2")
	body := `{"oldPassword":"correcthorsebattery2","newPassword":"short"}`
	rr := ts.doSessionRequest(t, http.MethodPost, "/api/account/password", body, sessCookie, csrfCookie)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("change password short new: got %d, want 400", rr.Code)
	}
}

func TestChangePassword_CannotChangeOtherUserPassword(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)

	// bob is authenticated; he cannot change alice's password via /api/account/password.
	// The endpoint uses the authenticated identity's username, not a supplied one.
	// There is no "target username" field in changePasswordDTO by design.
	// bob's "old password" would be checked against bob's stored password;
	// the endpoint changes bob's password, not alice's.
	// This test confirms that even a correct old password for bob doesn't touch alice.
	sessCookie, csrfCookie := ts.login(t, "bob", "correcthorsebattery2")
	body := `{"oldPassword":"correcthorsebattery2","newPassword":"mynewpassword456"}`
	rr := ts.doSessionRequest(t, http.MethodPost, "/api/account/password", body, sessCookie, csrfCookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("bob changing own password: got %d; body: %s", rr.Code, rr.Body.String())
	}

	// Alice's password must be unchanged.
	if _, err := ts.uStore.Verify("alice", "correcthorsebattery1"); err != nil {
		t.Errorf("alice's password should be unchanged: %v", err)
	}
}

// ---- 9. GET /api/account (whoami) -------------------------------------------

func TestAccount_ReturnsIdentity(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)

	sessCookie, csrfCookie := ts.login(t, "alice", "correcthorsebattery1")
	req := httptest.NewRequest(http.MethodGet, "/api/account", nil)
	req.AddCookie(sessCookie)
	req.AddCookie(csrfCookie)
	req.RemoteAddr = "10.0.0.1:5555"
	rr := httptest.NewRecorder()
	ts.srv.FullHandler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/account: got %d; body: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode /api/account: %v", err)
	}
	if resp["operatorId"] != "alice" {
		t.Errorf("account operatorId: got %v, want alice", resp["operatorId"])
	}
	if resp["role"] != "admin" {
		t.Errorf("account role: got %v, want admin", resp["role"])
	}
}

// ---- 10. Loopback path reaches admin endpoints ------------------------------

func TestSetRole_LoopbackTokenPath(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)

	// On the loopback path, the mux is called directly (no resolver middleware),
	// so requireRole checks are skipped (Resolved=false → pass-through).
	// This mirrors how the existing loopback tests work in auth_3b_test.go.
	body := `{"username":"bob","role":"dispatch"}`
	rr := ts.doLoopbackRequest(t, http.MethodPost, "/api/users/role", body)
	// On the loopback path (Handler(), not FullHandler()), the middleware stack
	// does not apply CSRF or auth. The request reaches the handler. Expect 200.
	if rr.Code != http.StatusOK {
		t.Errorf("set-role loopback: got %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
}

// ---- 11. Self-protection: two admins required to allow demote ---------------

func TestSetRole_CanDemoteNonLastAdmin(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)

	// Promote bob to admin first.
	if err := ts.uStore.SetRole("bob", netid.RoleAdmin); err != nil {
		t.Fatalf("promote bob: %v", err)
	}

	// Now demoting alice is allowed (bob is still admin).
	body := `{"username":"alice","role":"read"}`
	rr := ts.doAdminRequest(t, http.MethodPost, "/api/users/role", body)
	if rr.Code != http.StatusOK {
		t.Errorf("demote alice (two admins): got %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
}

func TestDelete_CanDeleteNonLastAdmin(t *testing.T) {
	t.Parallel()
	ts := newUsersTestServer(t)

	// Promote bob to admin so alice is no longer the only admin.
	if err := ts.uStore.SetRole("bob", netid.RoleAdmin); err != nil {
		t.Fatalf("promote bob: %v", err)
	}

	body := `{"username":"alice"}`
	rr := ts.doAdminRequest(t, http.MethodPost, "/api/users/delete", body)
	// alice logs in to do the request; after deleting herself the delete still
	// completes (alice's session is present but alice is removed from the store).
	if rr.Code != http.StatusOK {
		t.Errorf("delete alice (two admins): got %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
}
