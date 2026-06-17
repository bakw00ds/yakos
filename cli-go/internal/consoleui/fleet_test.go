package consoleui_test

// fleet_test.go — tests for GET /api/fleet.
//
// Coverage:
//   1. 401 without token.
//   2. 401 with wrong token.
//   3. 200 + empty sessions list when no sessions are active.
//   4. Cache-Control: no-store header is set.
//   5. Cross-operator isolation: operator B does NOT see operator A's unshared session.
//      SECURITY REQUIREMENT: this is the key invariant test.
//   6. Shared sessions visible to all callers.
//   7. attachable=true for owned session; attachable=true for shared; no foreign entries.
//   8. Loopback path (Resolved=false) sees all sessions.
//   9. TaskPreview present in REST snapshot, capped at 120 runes.
//  10. Method not allowed for POST.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// fleetResponseResult is used for decoding fleet JSON responses in tests.
type fleetResponseResult struct {
	Sessions []fleetSessionResult `json:"sessions"`
}

type fleetSessionResult struct {
	SessionID      string    `json:"session_id"`
	ConversationID string    `json:"conversation_id"`
	Agent          string    `json:"agent"`
	Runtime        string    `json:"runtime"`
	Status         string    `json:"status"`
	StartedAt      time.Time `json:"started_at"`
	TaskPreview    string    `json:"task_preview"`
	Attachable     bool      `json:"attachable"`
}

// decodeFleetResponse decodes a JSON fleet response body.
func decodeFleetResponse(t *testing.T, resp *http.Response) fleetResponseResult {
	t.Helper()
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	var fr fleetResponseResult
	if err := json.NewDecoder(resp.Body).Decode(&fr); err != nil {
		t.Fatalf("decode fleet response: %v", err)
	}
	return fr
}

// newFleetTestServer builds a consoleui.Server and returns the test HTTP server,
// the bearer token, the SessionRegistry (via RegistryForTest), and the Server.
// The Handler() is used (no middleware injected); caller wraps as needed.
func newFleetTestServer(t *testing.T) (*httptest.Server, string, *dispatch.SessionRegistry, *consoleui.Server) {
	t.Helper()
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
	})

	wrapped := consoleui.RequireTokenForNonStatic(tok,
		consoleui.RequireJSONForMutations(srv.Handler()))
	ts := httptest.NewServer(wrapped)
	t.Cleanup(ts.Close)
	return ts, tok, consoleui.RegistryForTest(srv), srv
}

// newFleetTestServerWithIdentity builds a fleet test server where every request
// is pre-stamped with id via injectIdentityMiddleware (mirrors enforcement_test.go).
// This simulates the resolver.Middleware + mTLS path so requireRole and the
// fleet handler's scoping logic fire as in production.
func newFleetTestServerWithIdentity(t *testing.T, id netid.Identity) (*httptest.Server, string, *dispatch.SessionRegistry, *consoleui.Server) {
	t.Helper()
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
	})

	// Inject identity before the mux so requireRole and fleet scoping fire.
	handler := consoleui.RequireTokenForNonStatic(tok,
		consoleui.RequireJSONForMutations(
			injectIdentityMiddleware(id, srv.Handler())))
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts, tok, consoleui.RegistryForTest(srv), srv
}

// ---- 1 & 2. Auth matrix -----------------------------------------------------

func TestFleet_401WithoutToken(t *testing.T) {
	ts, _, _, _ := newFleetTestServer(t)
	resp := get(t, ts.URL+"/api/fleet", "")
	defer drainClose(resp)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /api/fleet (no token): status=%d; want 401", resp.StatusCode)
	}
}

func TestFleet_403WithWrongToken(t *testing.T) {
	ts, _, _, _ := newFleetTestServer(t)
	resp := get(t, ts.URL+"/api/fleet", "wrong-token")
	defer drainClose(resp)
	// RequireToken returns 401 for missing token, 403 for invalid (wrong) token.
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET /api/fleet (wrong token): status=%d; want 403", resp.StatusCode)
	}
}

// ---- 3. Empty list ----------------------------------------------------------

func TestFleet_EmptyListWhenNoSessions(t *testing.T) {
	ts, tok, _, _ := newFleetTestServer(t)
	resp := get(t, ts.URL+"/api/fleet", tok)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/fleet: status=%d; want 200", resp.StatusCode)
	}
	fr := decodeFleetResponse(t, resp)
	if len(fr.Sessions) != 0 {
		t.Errorf("sessions len=%d; want 0 when registry is empty", len(fr.Sessions))
	}
}

// ---- 4. Cache-Control: no-store ---------------------------------------------

func TestFleet_CacheControlNoStore(t *testing.T) {
	ts, tok, _, _ := newFleetTestServer(t)
	resp := get(t, ts.URL+"/api/fleet", tok)
	defer drainClose(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d; want 200", resp.StatusCode)
	}
	cc := resp.Header.Get("Cache-Control")
	if cc != "no-store" {
		t.Errorf("Cache-Control=%q; want 'no-store'", cc)
	}
}

// ---- 5. Cross-operator isolation (SECURITY requirement) ---------------------
//
// Operator B (bob) MUST NOT see operator A (alice)'s unshared session
// in /api/fleet.  This mirrors the ChatHub per-operator isolation in chathub.go.

func TestFleet_CrossOperatorIsolation(t *testing.T) {
	// Inject bob's authenticated identity so the fleet handler applies scoping.
	bobID := netid.Identity{
		Resolved:      true,
		Authenticated: true,
		AuthMethod:    netid.AuthMethodCert,
		OperatorID:    "bob",
		Role:          netid.RoleRead,
	}
	ts, tok, reg, srv := newFleetTestServerWithIdentity(t, bobID)

	hub := srv.ChatHub()

	// Register alice's unshared session in hub + registry.
	if err := hub.OpenSession("sess-alice-priv", "alice", false /*shared=false*/); err != nil {
		t.Fatalf("OpenSession alice: %v", err)
	}
	t.Cleanup(func() { hub.CloseSession("sess-alice-priv") })

	reg.Add(dispatch.SessionEntry{
		SessionID:   "sess-alice-priv",
		Agent:       "backend",
		Runtime:     "claude",
		OperatorID:  "alice",
		TaskPreview: "implement authentication",
		StartedAt:   time.Now(),
		Status:      dispatch.StatusRunning,
	})

	resp := get(t, ts.URL+"/api/fleet", tok)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/fleet: status=%d; want 200", resp.StatusCode)
	}
	fr := decodeFleetResponse(t, resp)

	// bob must see ZERO sessions — alice's unshared session is invisible.
	for _, s := range fr.Sessions {
		if s.SessionID == "sess-alice-priv" {
			t.Errorf("SECURITY: cross-operator isolation violated — bob sees alice's unshared session %q", s.SessionID)
		}
	}
}

// ---- 6. Shared sessions visible to all callers ------------------------------

func TestFleet_SharedSessionVisibleToOtherOperator(t *testing.T) {
	bobID := netid.Identity{
		Resolved:      true,
		Authenticated: true,
		AuthMethod:    netid.AuthMethodCert,
		OperatorID:    "bob",
		Role:          netid.RoleRead,
	}
	ts, tok, reg, srv := newFleetTestServerWithIdentity(t, bobID)

	hub := srv.ChatHub()

	// Alice's session is shared.
	if err := hub.OpenSession("sess-alice-shared", "alice", true /*shared=true*/); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	t.Cleanup(func() { hub.CloseSession("sess-alice-shared") })

	reg.Add(dispatch.SessionEntry{
		SessionID:   "sess-alice-shared",
		Agent:       "docs",
		Runtime:     "claude",
		OperatorID:  "alice",
		TaskPreview: "write docs",
		StartedAt:   time.Now(),
		Status:      dispatch.StatusRunning,
	})

	resp := get(t, ts.URL+"/api/fleet", tok)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d; want 200", resp.StatusCode)
	}
	fr := decodeFleetResponse(t, resp)

	var found bool
	for _, s := range fr.Sessions {
		if s.SessionID == "sess-alice-shared" {
			found = true
			if !s.Attachable {
				t.Errorf("shared session: attachable=false; want true")
			}
		}
	}
	if !found {
		t.Errorf("bob does not see alice's shared session 'sess-alice-shared'; want visible")
	}
}

// ---- 7. Owned session is attachable ----------------------------------------

func TestFleet_OwnedSessionIsAttachable(t *testing.T) {
	aliceID := netid.Identity{
		Resolved:      true,
		Authenticated: true,
		AuthMethod:    netid.AuthMethodCert,
		OperatorID:    "alice",
		Role:          netid.RoleRead,
	}
	ts, tok, reg, srv := newFleetTestServerWithIdentity(t, aliceID)

	hub := srv.ChatHub()
	if err := hub.OpenSession("sess-alice-own", "alice", false); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	t.Cleanup(func() { hub.CloseSession("sess-alice-own") })

	reg.Add(dispatch.SessionEntry{
		SessionID:   "sess-alice-own",
		Agent:       "frontend",
		Runtime:     "claude",
		OperatorID:  "alice",
		TaskPreview: "fix button",
		StartedAt:   time.Now(),
		Status:      dispatch.StatusRunning,
	})

	resp := get(t, ts.URL+"/api/fleet", tok)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d; want 200", resp.StatusCode)
	}
	fr := decodeFleetResponse(t, resp)

	var found bool
	for _, s := range fr.Sessions {
		if s.SessionID == "sess-alice-own" {
			found = true
			if !s.Attachable {
				t.Errorf("owned session: attachable=false; want true")
			}
		}
	}
	if !found {
		t.Errorf("alice does not see her own session 'sess-alice-own' in fleet")
	}
}

// ---- 8. Loopback path sees all sessions (no isolation on loopback) ----------
//
// On the loopback path, id.Resolved=false (no resolver middleware applied via
// srv.Handler() without identity injection).  The fleet handler treats
// Resolved=false as the loopback trusted path and returns all sessions.

func TestFleet_LoopbackSeesAllSessions(t *testing.T) {
	// No identity injection: simulates the loopback path where the resolver
	// middleware has not run (srv.Handler() is used without identity middleware).
	ts, tok, reg, srv := newFleetTestServer(t)

	hub := srv.ChatHub()

	// Two unshared sessions from different operators.
	if err := hub.OpenSession("sess-lp-a", "alice", false); err != nil {
		t.Fatalf("OpenSession a: %v", err)
	}
	if err := hub.OpenSession("sess-lp-b", "bob", false); err != nil {
		t.Fatalf("OpenSession b: %v", err)
	}
	t.Cleanup(func() {
		hub.CloseSession("sess-lp-a")
		hub.CloseSession("sess-lp-b")
	})

	reg.Add(dispatch.SessionEntry{
		SessionID: "sess-lp-a", Agent: "a", Runtime: "claude",
		OperatorID: "alice", TaskPreview: "task a", StartedAt: time.Now(),
		Status: dispatch.StatusRunning,
	})
	reg.Add(dispatch.SessionEntry{
		SessionID: "sess-lp-b", Agent: "b", Runtime: "claude",
		OperatorID: "bob", TaskPreview: "task b", StartedAt: time.Now(),
		Status: dispatch.StatusRunning,
	})

	resp := get(t, ts.URL+"/api/fleet", tok)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d; want 200", resp.StatusCode)
	}
	fr := decodeFleetResponse(t, resp)

	sessionIDs := make(map[string]bool)
	for _, s := range fr.Sessions {
		sessionIDs[s.SessionID] = true
	}
	if !sessionIDs["sess-lp-a"] {
		t.Errorf("loopback fleet: 'sess-lp-a' not returned; want all sessions visible on loopback path")
	}
	if !sessionIDs["sess-lp-b"] {
		t.Errorf("loopback fleet: 'sess-lp-b' not returned; want all sessions visible on loopback path")
	}
}

// ---- 9. TaskPreview is capped at 120 runes in REST snapshot -----------------

func TestFleet_TaskPreviewCappedAt120Runes(t *testing.T) {
	ts, tok, reg, srv := newFleetTestServer(t)

	hub := srv.ChatHub()
	if err := hub.OpenSession("sess-tp", "alice", false); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	t.Cleanup(func() { hub.CloseSession("sess-tp") })

	// 200 'x' characters — well above the 120-rune cap.
	longTask := ""
	for i := 0; i < 200; i++ {
		longTask += "x"
	}

	reg.Add(dispatch.SessionEntry{
		SessionID:   "sess-tp",
		Agent:       "backend",
		Runtime:     "claude",
		OperatorID:  "alice",
		TaskPreview: longTask,
		StartedAt:   time.Now(),
		Status:      dispatch.StatusRunning,
	})

	resp := get(t, ts.URL+"/api/fleet", tok)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d; want 200", resp.StatusCode)
	}
	fr := decodeFleetResponse(t, resp)

	for _, s := range fr.Sessions {
		if s.SessionID == "sess-tp" {
			if len([]rune(s.TaskPreview)) > 120 {
				t.Errorf("task_preview rune count=%d; want <=120", len([]rune(s.TaskPreview)))
			}
			return
		}
	}
	t.Error("session 'sess-tp' not found in fleet snapshot")
}

// ---- 10. Method not allowed -------------------------------------------------

func TestFleet_MethodNotAllowed(t *testing.T) {
	ts, tok, _, _ := newFleetTestServer(t)
	resp := post(t, ts.URL+"/api/fleet", tok, `{}`)
	defer drainClose(resp)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/fleet: status=%d; want 405", resp.StatusCode)
	}
}

// ---- 11. Fleet rows include conversation_id ---------------------------------
//
// Regression test for the IDE↔Chat mirroring fix: fleet rows must expose
// conversation_id so the IDE picker can bind panes by conversation (the stable
// key) rather than by the ephemeral session_id.

func TestFleet_ConversationIDPresentInRow(t *testing.T) {
	ts, tok, reg, srv := newFleetTestServer(t)

	hub := srv.ChatHub()
	if err := hub.OpenSession("sess-convid-1", "alice", false); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	t.Cleanup(func() { hub.CloseSession("sess-convid-1") })

	reg.Add(dispatch.SessionEntry{
		SessionID:      "sess-convid-1",
		ConversationID: "conv-fixed-id-abc",
		Agent:          "backend",
		Runtime:        "claude",
		OperatorID:     "alice",
		TaskPreview:    "implement feature x",
		StartedAt:      time.Now(),
		Status:         dispatch.StatusRunning,
	})

	resp := get(t, ts.URL+"/api/fleet", tok)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/fleet: status=%d; want 200", resp.StatusCode)
	}
	fr := decodeFleetResponse(t, resp)

	for _, s := range fr.Sessions {
		if s.SessionID == "sess-convid-1" {
			if s.ConversationID != "conv-fixed-id-abc" {
				t.Errorf("fleet row conversation_id=%q; want 'conv-fixed-id-abc'", s.ConversationID)
			}
			return
		}
	}
	t.Error("session 'sess-convid-1' not found in fleet snapshot")
}

// TestFleet_ConversationIDEmptyWhenNotSet verifies that rows with no
// ConversationID do not error and return an empty/omitted conversation_id field.
func TestFleet_ConversationIDEmptyWhenNotSet(t *testing.T) {
	ts, tok, reg, srv := newFleetTestServer(t)

	hub := srv.ChatHub()
	if err := hub.OpenSession("sess-noconvid", "alice", false); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	t.Cleanup(func() { hub.CloseSession("sess-noconvid") })

	// SessionEntry without ConversationID set (zero value).
	reg.Add(dispatch.SessionEntry{
		SessionID:   "sess-noconvid",
		Agent:       "backend",
		Runtime:     "claude",
		OperatorID:  "alice",
		TaskPreview: "some task",
		StartedAt:   time.Now(),
		Status:      dispatch.StatusRunning,
	})

	resp := get(t, ts.URL+"/api/fleet", tok)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/fleet: status=%d; want 200", resp.StatusCode)
	}
	fr := decodeFleetResponse(t, resp)

	for _, s := range fr.Sessions {
		if s.SessionID == "sess-noconvid" {
			// ConversationID may be empty string (omitted from JSON → zero value in struct).
			// This is valid — not every entry must have a ConversationID.
			if s.ConversationID != "" {
				t.Errorf("fleet row conversation_id=%q; want empty string when not set", s.ConversationID)
			}
			return
		}
	}
	t.Error("session 'sess-noconvid' not found in fleet snapshot")
}
