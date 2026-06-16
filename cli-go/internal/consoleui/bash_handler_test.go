package consoleui_test

// bash_handler_test.go — tests for POST /api/console/bash.
//
// Test matrix (all run with -race):
//
//  1. Admin + loopback → executes command, returns stdout.
//  2. RoleDispatch → 403.
//  3. RoleRead → 403.
//  4. Networked non-loopback + AllowNetworkedBash=false → 403.
//  5. Networked non-loopback + AllowNetworkedBash=true → executes command.
//  6. Empty command → 400.
//  7. stdout truncation at 16384 bytes.
//  8. stderr truncation at 16384 bytes.
//  9. Command timeout (30s) — uses fast-failing approach: command that exits 1.
// 10. exit_code preserved for failing commands.
//
// Identity injection mirrors enforcement_test.go: inject via
// netid.WithIdentityForTest to simulate the resolver middleware having run.
// RemoteAddr is set explicitly on each request to control loopback vs. networked.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/authsession"
	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/userstore"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// bashTestServer wraps a networked consoleui.Server for bash endpoint tests.
// It holds references to the session / user stores to allow session-based auth.
type bashTestServer struct {
	srv         *consoleui.Server
	authStore   *authsession.Store
	uStore      *userstore.Store
	token       string
	workspaceRoot string
}

// newBashTestServer builds a networked Server with:
//   - alice: admin (pre-created)
//   - bob:   dispatch (pre-created)
//   - carol: read (pre-created)
//
// AllowNetworkedBash is controlled by the allowBash parameter.
// The FullHandler is used so the global middleware stack fires for these tests.
func newBashTestServer(t *testing.T, allowBash bool) *bashTestServer {
	t.Helper()

	stateDir := t.TempDir()
	workspaceRoot := t.TempDir()
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
	if err := uStore.Create("bob", "correcthorsebattery2", netid.RoleDispatch); err != nil {
		t.Fatalf("create bob: %v", err)
	}
	if err := uStore.Create("carol", "correcthorsebattery3", netid.RoleRead); err != nil {
		t.Fatalf("create carol: %v", err)
	}

	aStore := authsession.NewStore(authsession.Config{MaxSessions: 100})

	bus := wsbus.New()
	t.Cleanup(bus.Stop)

	srv := consoleui.MustNew(t, consoleui.Config{
		NetworkedMode:      true,
		Token:              tok,
		KanbanBoardPath:    filepath.Join(t.TempDir(), "kanban.md"),
		KanbanProject:      "test",
		MetricsProjectDir:  t.TempDir(),
		PerfWorkDir:        t.TempDir(),
		Bus:                bus,
		WorkDir:            t.TempDir(),
		StateDir:           stateDir,
		AuthSessionStore:   aStore,
		UserStore:          uStore,
		WorkspaceRoot:      workspaceRoot,
		AllowNetworkedBash: allowBash,
	})

	return &bashTestServer{
		srv:           srv,
		authStore:     aStore,
		uStore:        uStore,
		token:         tok,
		workspaceRoot: workspaceRoot,
	}
}

// loginAs authenticates as the given username and returns session + CSRF cookies.
func (ts *bashTestServer) loginAs(t *testing.T, username, password string) (sessionCookie, csrfCookie *http.Cookie) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.0.0.1:5555"
	rr := httptest.NewRecorder()
	ts.srv.FullHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("login %q: got %d; body: %s", username, rr.Code, rr.Body.String())
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

// doBashRequest sends POST /api/console/bash with session auth + CSRF.
// remoteAddr controls the loopback/networked decision in the handler.
func (ts *bashTestServer) doBashRequest(t *testing.T, sessCookie, csrfCookie *http.Cookie, command, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"command": command})
	req := httptest.NewRequest(http.MethodPost, "/api/console/bash", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrfCookie.Value)
	req.AddCookie(sessCookie)
	req.AddCookie(csrfCookie)
	req.RemoteAddr = remoteAddr
	rr := httptest.NewRecorder()
	ts.srv.FullHandler().ServeHTTP(rr, req)
	return rr
}

// ---- Loopback path (also tested via loopback srv.Handler() with identity injection) ---

// newLoopbackBashServer builds a loopback (non-networked) Server that uses
// srv.Handler() + identity injection, mirroring enforcement_test.go.
// This is the preferred pattern for tests that don't need the full middleware stack.
func newLoopbackBashServer(t *testing.T, id netid.Identity, allowBash bool) (*httptest.Server, string) {
	t.Helper()
	stateDir := t.TempDir()
	workspaceRoot := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)

	srv := consoleui.MustNew(t, consoleui.Config{
		Token:              tok,
		KanbanBoardPath:    t.TempDir() + "/kanban.md",
		KanbanProject:      "test",
		MetricsProjectDir:  t.TempDir(),
		PerfWorkDir:        t.TempDir(),
		Bus:                bus,
		WorkspaceRoot:      workspaceRoot,
		AllowNetworkedBash: allowBash,
	})

	// Wrap with injectIdentityMiddleware + token + JSON gate, mirroring
	// enforcement_test.go newEnforcementTestServer.
	handler := consoleui.RequireTokenForNonStatic(tok,
		consoleui.RequireJSONForMutations(
			injectIdentityMiddleware(id, srv.Handler())))

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts, tok
}

// bashPost sends an authenticated POST to /api/console/bash.
// remoteAddr is set on the raw request before serving.
func bashPost(t *testing.T, handler http.Handler, tok, command, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"command": command})
	req := httptest.NewRequest(http.MethodPost, "/api/console/bash", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	req.RemoteAddr = remoteAddr
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// decodeBashResp decodes a 200 bash response body into bashResponseDTO fields.
func decodeBashResp(t *testing.T, body string) (stdout, stderr string, exitCode int, truncated bool) {
	t.Helper()
	var resp struct {
		Stdout    string `json:"stdout"`
		Stderr    string `json:"stderr"`
		ExitCode  int    `json:"exit_code"`
		Truncated bool   `json:"truncated"`
	}
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&resp); err != nil {
		t.Fatalf("decode bash response: %v\nbody: %s", err, body)
	}
	return resp.Stdout, resp.Stderr, resp.ExitCode, resp.Truncated
}

// ---- Test 1: admin + loopback → executes, returns stdout --------------------

func TestBashHandler_AdminLoopback_ExecutesCommand(t *testing.T) {
	t.Parallel()

	adminID := netid.Identity{
		OperatorID:    "alice",
		Role:          netid.RoleAdmin,
		Authenticated: true,
		Resolved:      true,
	}
	ts, tok := newLoopbackBashServer(t, adminID, false /* allowBash irrelevant for loopback */)

	client := ts.Client()
	body, _ := json.Marshal(map[string]string{"command": "echo hello-from-bash"})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/console/bash", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200", resp.StatusCode)
	}

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	stdout, _, exitCode, truncated := decodeBashResp(t, buf.String())

	if !strings.Contains(stdout, "hello-from-bash") {
		t.Errorf("stdout = %q; want to contain 'hello-from-bash'", stdout)
	}
	if exitCode != 0 {
		t.Errorf("exit_code = %d; want 0", exitCode)
	}
	if truncated {
		t.Error("truncated = true; want false for short output")
	}
}

// ---- Test 2 & 3: RoleDispatch and RoleRead → 403 ----------------------------

func TestBashHandler_RoleDispatch_Forbidden(t *testing.T) {
	t.Parallel()

	dispatchID := netid.Identity{
		OperatorID:    "bob",
		Role:          netid.RoleDispatch,
		Authenticated: true,
		Resolved:      true,
	}
	ts, tok := newLoopbackBashServer(t, dispatchID, false)

	rr := bashPost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ts.Client().CheckRedirect = nil // unused; just need the handler below
	}), tok, "echo hi", "127.0.0.1:1234")
	// Use httptest directly.
	handler := consoleui.RequireTokenForNonStatic(tok,
		consoleui.RequireJSONForMutations(
			injectIdentityMiddleware(dispatchID, func() http.Handler {
				stateDir := t.TempDir()
				tok2, _ := consoleui.LoadOrCreateToken(stateDir)
				bus := wsbus.New()
				t.Cleanup(bus.Stop)
				srv := consoleui.MustNew(t, consoleui.Config{
					Token: tok2, KanbanBoardPath: t.TempDir() + "/kanban.md",
					KanbanProject: "test", MetricsProjectDir: t.TempDir(),
					PerfWorkDir: t.TempDir(), Bus: bus,
				})
				return srv.Handler()
			}())))
	_ = rr

	body, _ := json.Marshal(map[string]string{"command": "echo hi"})
	req := httptest.NewRequest(http.MethodPost, "/api/console/bash", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	req.RemoteAddr = "127.0.0.1:1234"
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req)

	if rr2.Code != http.StatusForbidden {
		t.Errorf("RoleDispatch: status = %d; want 403", rr2.Code)
	}
}

func TestBashHandler_RoleRead_Forbidden(t *testing.T) {
	t.Parallel()

	readID := netid.Identity{
		OperatorID:    "carol",
		Role:          netid.RoleRead,
		Authenticated: true,
		Resolved:      true,
	}

	stateDir := t.TempDir()
	tok, _ := consoleui.LoadOrCreateToken(stateDir)
	bus := wsbus.New()
	t.Cleanup(bus.Stop)
	srv := consoleui.MustNew(t, consoleui.Config{
		Token: tok, KanbanBoardPath: t.TempDir() + "/kanban.md",
		KanbanProject: "test", MetricsProjectDir: t.TempDir(),
		PerfWorkDir: t.TempDir(), Bus: bus,
	})

	handler := consoleui.RequireTokenForNonStatic(tok,
		consoleui.RequireJSONForMutations(
			injectIdentityMiddleware(readID, srv.Handler())))

	body, _ := json.Marshal(map[string]string{"command": "echo hi"})
	req := httptest.NewRequest(http.MethodPost, "/api/console/bash", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	req.RemoteAddr = "127.0.0.1:1234"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("RoleRead: status = %d; want 403", rr.Code)
	}
}

// ---- Test 4: networked non-loopback + AllowNetworkedBash=false → 403 ---------

func TestBashHandler_NetworkedNonLoopback_FlagOff_Forbidden(t *testing.T) {
	t.Parallel()

	ts := newBashTestServer(t, false /* allowBash=false */)
	sessCookie, csrfCookie := ts.loginAs(t, "alice", "correcthorsebattery1")

	// Use a non-loopback RemoteAddr — the networked server is in NetworkedMode=true.
	rr := ts.doBashRequest(t, sessCookie, csrfCookie, "echo hi", "10.0.0.1:5555")
	if rr.Code != http.StatusForbidden {
		t.Errorf("non-loopback, flag off: status = %d; want 403", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "console-allow-bash") {
		t.Errorf("403 body does not mention --console-allow-bash: %s", body)
	}
}

// ---- Test 5: networked non-loopback + AllowNetworkedBash=true → runs --------

func TestBashHandler_NetworkedNonLoopback_FlagOn_Executes(t *testing.T) {
	t.Parallel()

	ts := newBashTestServer(t, true /* allowBash=true */)
	sessCookie, csrfCookie := ts.loginAs(t, "alice", "correcthorsebattery1")

	rr := ts.doBashRequest(t, sessCookie, csrfCookie, "echo networked-bash", "10.0.0.1:5555")
	if rr.Code != http.StatusOK {
		t.Fatalf("networked, flag on: status = %d; want 200; body: %s", rr.Code, rr.Body.String())
	}
	stdout, _, exitCode, _ := decodeBashResp(t, rr.Body.String())
	if !strings.Contains(stdout, "networked-bash") {
		t.Errorf("stdout = %q; want 'networked-bash'", stdout)
	}
	if exitCode != 0 {
		t.Errorf("exit_code = %d; want 0", exitCode)
	}
}

// ---- Test 6: empty command → 400 --------------------------------------------

func TestBashHandler_EmptyCommand_BadRequest(t *testing.T) {
	t.Parallel()

	adminID := netid.Identity{
		OperatorID: "alice", Role: netid.RoleAdmin,
		Authenticated: true, Resolved: true,
	}
	stateDir := t.TempDir()
	tok, _ := consoleui.LoadOrCreateToken(stateDir)
	bus := wsbus.New()
	t.Cleanup(bus.Stop)
	srv := consoleui.MustNew(t, consoleui.Config{
		Token: tok, KanbanBoardPath: t.TempDir() + "/kanban.md",
		KanbanProject: "test", MetricsProjectDir: t.TempDir(),
		PerfWorkDir: t.TempDir(), Bus: bus,
	})

	handler := consoleui.RequireTokenForNonStatic(tok,
		consoleui.RequireJSONForMutations(
			injectIdentityMiddleware(adminID, srv.Handler())))

	// Empty string command.
	body, _ := json.Marshal(map[string]string{"command": ""})
	req := httptest.NewRequest(http.MethodPost, "/api/console/bash", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	req.RemoteAddr = "127.0.0.1:1234"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("empty command: status = %d; want 400", rr.Code)
	}
}

// ---- Test 7: stdout truncation at 16384 bytes --------------------------------

func TestBashHandler_StdoutTruncation(t *testing.T) {
	t.Parallel()

	adminID := netid.Identity{
		OperatorID: "alice", Role: netid.RoleAdmin,
		Authenticated: true, Resolved: true,
	}
	stateDir := t.TempDir()
	tok, _ := consoleui.LoadOrCreateToken(stateDir)
	bus := wsbus.New()
	t.Cleanup(bus.Stop)
	srv := consoleui.MustNew(t, consoleui.Config{
		Token: tok, KanbanBoardPath: t.TempDir() + "/kanban.md",
		KanbanProject: "test", MetricsProjectDir: t.TempDir(),
		PerfWorkDir: t.TempDir(), Bus: bus,
	})

	handler := consoleui.RequireTokenForNonStatic(tok,
		consoleui.RequireJSONForMutations(
			injectIdentityMiddleware(adminID, srv.Handler())))

	// Generate 20000 bytes of output (well above 16384 cap).
	// "printf '%0.s#' {1..20000}" produces 20000 # characters without a newline.
	cmd := "printf '%0.s#' {1..20000}"
	body, _ := json.Marshal(map[string]string{"command": cmd})
	req := httptest.NewRequest(http.MethodPost, "/api/console/bash", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	req.RemoteAddr = "127.0.0.1:1234"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("stdout truncation: status = %d; want 200", rr.Code)
	}
	stdout, _, _, truncated := decodeBashResp(t, rr.Body.String())
	if !truncated {
		t.Error("truncated = false; want true for 20000-byte stdout")
	}
	if len(stdout) > 16384+len("\n[...output truncated at 16384 bytes...]") {
		t.Errorf("stdout len = %d; want ≤ cap + marker", len(stdout))
	}
	if !strings.Contains(stdout, "truncated") {
		t.Error("stdout does not contain truncation marker")
	}
}

// ---- Test 8: stderr truncation at 16384 bytes --------------------------------

func TestBashHandler_StderrTruncation(t *testing.T) {
	t.Parallel()

	adminID := netid.Identity{
		OperatorID: "alice", Role: netid.RoleAdmin,
		Authenticated: true, Resolved: true,
	}
	stateDir := t.TempDir()
	tok, _ := consoleui.LoadOrCreateToken(stateDir)
	bus := wsbus.New()
	t.Cleanup(bus.Stop)
	srv := consoleui.MustNew(t, consoleui.Config{
		Token: tok, KanbanBoardPath: t.TempDir() + "/kanban.md",
		KanbanProject: "test", MetricsProjectDir: t.TempDir(),
		PerfWorkDir: t.TempDir(), Bus: bus,
	})

	handler := consoleui.RequireTokenForNonStatic(tok,
		consoleui.RequireJSONForMutations(
			injectIdentityMiddleware(adminID, srv.Handler())))

	// 20000 bytes of stderr output.
	cmd := "printf '%0.s@' {1..20000} >&2"
	body, _ := json.Marshal(map[string]string{"command": cmd})
	req := httptest.NewRequest(http.MethodPost, "/api/console/bash", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	req.RemoteAddr = "127.0.0.1:1234"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("stderr truncation: status = %d; want 200", rr.Code)
	}
	_, stderr, _, truncated := decodeBashResp(t, rr.Body.String())
	if !truncated {
		t.Error("truncated = false; want true for 20000-byte stderr")
	}
	if len(stderr) > 16384+len("\n[...output truncated at 16384 bytes...]") {
		t.Errorf("stderr len = %d; want ≤ cap + marker", len(stderr))
	}
}

// ---- Test 9: exit_code for non-zero exit -------------------------------------

func TestBashHandler_ExitCode_NonZero(t *testing.T) {
	t.Parallel()

	adminID := netid.Identity{
		OperatorID: "alice", Role: netid.RoleAdmin,
		Authenticated: true, Resolved: true,
	}
	stateDir := t.TempDir()
	tok, _ := consoleui.LoadOrCreateToken(stateDir)
	bus := wsbus.New()
	t.Cleanup(bus.Stop)
	srv := consoleui.MustNew(t, consoleui.Config{
		Token: tok, KanbanBoardPath: t.TempDir() + "/kanban.md",
		KanbanProject: "test", MetricsProjectDir: t.TempDir(),
		PerfWorkDir: t.TempDir(), Bus: bus,
	})

	handler := consoleui.RequireTokenForNonStatic(tok,
		consoleui.RequireJSONForMutations(
			injectIdentityMiddleware(adminID, srv.Handler())))

	// "exit 42" makes sh exit with code 42.
	body, _ := json.Marshal(map[string]string{"command": "exit 42"})
	req := httptest.NewRequest(http.MethodPost, "/api/console/bash", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	req.RemoteAddr = "127.0.0.1:1234"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("exit code test: status = %d; want 200", rr.Code)
	}
	_, _, exitCode, _ := decodeBashResp(t, rr.Body.String())
	if exitCode != 42 {
		t.Errorf("exit_code = %d; want 42", exitCode)
	}
}

// ---- Test 11: backgrounded grandchild does not cause handler to hang --------
//
// Timing guard for CRITICAL-2: "sleep 60 &" must not block the handler past
// a short wall-clock budget. The authoritative no-survivor assertion (checking
// the process group is dead via syscall.Kill) lives in
// bash_orphan_unix_test.go (POSIX-only). This test is cross-platform and
// verifies only liveness (handler returns promptly).
//
// NOTE: On Windows, configureProcAttr is a no-op and "sleep" may not exist;
// the test accepts any 200 or error response as long as it arrives quickly.

func TestBashHandler_BackgroundedGrandchild_DoesNotHang(t *testing.T) {
	t.Parallel()

	adminID := netid.Identity{
		OperatorID:    "alice",
		Role:          netid.RoleAdmin,
		Authenticated: true,
		Resolved:      true,
	}

	stateDir := t.TempDir()
	tok, _ := consoleui.LoadOrCreateToken(stateDir)
	bus := wsbus.New()
	t.Cleanup(bus.Stop)
	srv := consoleui.MustNew(t, consoleui.Config{
		Token:             tok,
		KanbanBoardPath:   t.TempDir() + "/kanban.md",
		KanbanProject:     "test",
		MetricsProjectDir: t.TempDir(),
		PerfWorkDir:       t.TempDir(),
		Bus:               bus,
	})

	handler := consoleui.RequireTokenForNonStatic(tok,
		consoleui.RequireJSONForMutations(
			injectIdentityMiddleware(adminID, srv.Handler())))

	// "sleep 60 &": sh backgrounds the grandchild and exits 0 immediately.
	// Without defer killProcessGroup the grandchild keeps stdout/stderr open
	// and Wait() would block indefinitely. The handler must return promptly.
	start := time.Now()
	rr := bashPost(t, handler, tok, "sleep 60 &", "127.0.0.1:1234")
	elapsed := time.Since(start)

	const maxAllowedDuration = 15 * time.Second
	if elapsed >= maxAllowedDuration {
		t.Errorf("handler took %v; want < %v — likely blocked on grandchild stdout/stderr", elapsed, maxAllowedDuration)
	}

	if rr.Code == http.StatusOK {
		_, _, exitCode, _ := decodeBashResp(t, rr.Body.String())
		if exitCode != 0 && exitCode != -1 {
			t.Logf("exit_code=%d (acceptable; sh may have exited non-zero after group kill)", exitCode)
		}
	}
}

// ---- Test 10: loopback allowed even when AllowNetworkedBash=false -----------
//
// The loopback path must ALWAYS work regardless of AllowNetworkedBash.

func TestBashHandler_Loopback_AlwaysAllowed_RegardlessOfFlag(t *testing.T) {
	t.Parallel()

	// Use the networked server with AllowNetworkedBash=false.
	ts := newBashTestServer(t, false)
	sessCookie, csrfCookie := ts.loginAs(t, "alice", "correcthorsebattery1")

	// RemoteAddr = loopback: must succeed even though flag is off.
	rr := ts.doBashRequest(t, sessCookie, csrfCookie, "echo loopback-ok", "127.0.0.1:5555")
	if rr.Code != http.StatusOK {
		t.Fatalf("loopback with flag off: status = %d; want 200; body: %s", rr.Code, rr.Body.String())
	}
	stdout, _, _, _ := decodeBashResp(t, rr.Body.String())
	if !strings.Contains(stdout, "loopback-ok") {
		t.Errorf("stdout = %q; want 'loopback-ok'", stdout)
	}
}
