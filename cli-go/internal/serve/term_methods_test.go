package serve

// term_methods_test.go — tests for handleTermCreate (round-2 review R3).
//
// Before this fix, RegisterExternalSession took no owner and the manager
// granted ownership to whoever called ClaimOwner first — a race any admin
// could win by polling GET /api/term. The fix records the owner AT
// REGISTRATION, using the daemon's own stable loopback operator ID (the
// same one consoleui stamps on loopback HTTP/WS requests), never a value
// from RPC params. This test proves handleTermCreate actually does that:
// the resulting session's owner must equal loopbackowner.LoadOrCreate's
// output for the configured state dir, and it must reject the caller's own
// operatorId even if termCreateParams were extended to carry one (it isn't
// — the params type has no such field, which this test also pins down).

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bakw00ds/yakos/internal/loopbackowner"
	"github.com/bakw00ds/yakos/internal/terminalmanager"
)

// TestHandleTermCreate_RegistersDaemonDerivedOwner is the core R3 regression
// for the JSON-RPC yakos.term.create handler: the session it registers must
// be owned by the daemon's own stable loopback operator ID, not left
// unowned (which round-1's ClaimOwner would have granted to the first
// browser/WS attacher — the exact land-grab R3 closes).
func TestHandleTermCreate_RegistersDaemonDerivedOwner(t *testing.T) {
	stateDir := t.TempDir()
	mgr := terminalmanager.New(context.Background(), terminalmanager.Config{Cap: 4})
	defer mgr.Stop()

	cfg := Config{
		RESTStateDir:    stateDir,
		TerminalManager: mgr,
	}

	handler := handleTermCreate(cfg)
	params, err := json.Marshal(termCreateParams{
		SessionID:     "sess-r3-owner-test",
		Argv:          []string{"claude"},
		WorkspaceRoot: "/tmp/project",
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	if _, err := handler(context.Background(), params); err != nil {
		t.Fatalf("handleTermCreate: %v", err)
	}

	wantOwner := loopbackowner.LoadOrCreate(stateDir)
	if wantOwner == "" {
		t.Fatal("loopbackowner.LoadOrCreate returned empty (test setup broken)")
	}

	// The registered session must be claimable by the stable loopback owner
	// (proving RegisterExternalSession recorded it as owner)...
	if err := mgr.ClaimOwner("sess-r3-owner-test", wantOwner); err != nil {
		t.Errorf("ClaimOwner(%q) — the stable loopback owner — on the session handleTermCreate registered: %v (owner was not recorded at registration)", wantOwner, err)
	}
}

// TestHandleTermCreate_RejectsArbitraryCallerAsOwner proves termCreateParams
// carries no owner/operatorId field a caller could forge: an arbitrary
// operator identity must NOT be able to claim the session, only the daemon's
// own stable loopback ID can.
func TestHandleTermCreate_RejectsArbitraryCallerAsOwner(t *testing.T) {
	stateDir := t.TempDir()
	mgr := terminalmanager.New(context.Background(), terminalmanager.Config{Cap: 4})
	defer mgr.Stop()

	cfg := Config{
		RESTStateDir:    stateDir,
		TerminalManager: mgr,
	}

	handler := handleTermCreate(cfg)
	// Attempt to smuggle an owner/operatorId field via raw JSON — since
	// termCreateParams has no such field, json.Unmarshal silently ignores it
	// (Go's default decoder behavior), which is exactly the point: there is
	// no code path for a caller-supplied owner to reach RegisterExternalSession.
	rawParams := json.RawMessage(`{"sessionId":"sess-r3-forged-owner","argv":["claude"],"workspaceRoot":"/tmp/project","owner":"mallory","operatorId":"mallory"}`)

	if _, err := handler(context.Background(), rawParams); err != nil {
		t.Fatalf("handleTermCreate: %v", err)
	}

	if err := mgr.ClaimOwner("sess-r3-forged-owner", "mallory"); err == nil {
		t.Fatal("ClaimOwner(mallory) succeeded — a caller-supplied owner field was honored (R3 regression)")
	}
}

// TestHandleTermList_ScopedToOwner is the "Should" follow-up the lead
// dispatched alongside R10 (K-82): scope yakos.term.list the same way
// GET /api/term is scoped (round-2 review R18), for the same reason —
// consistency across every session-listing surface, not because the
// mode-0600 owner-UID JSON-RPC socket is itself exploitable (round-2 review
// N6/accepted-residual-risk table judged that unscoped listing acceptable
// given the socket's own permissions). Every yakos.term.create call stamps
// the SAME daemon-derived stable owner (R3), so in production this is a
// no-op for the only caller that exists; the test proves it by planting a
// second, foreign-owned session directly via RegisterExternalSession (a
// shape the socket's trust boundary would need to be loosened to reach) and
// asserting it is excluded.
func TestHandleTermList_ScopedToOwner(t *testing.T) {
	stateDir := t.TempDir()
	mgr := terminalmanager.New(context.Background(), terminalmanager.Config{Cap: 4})
	defer mgr.Stop()

	cfg := Config{
		RESTStateDir:    stateDir,
		TerminalManager: mgr,
	}

	// Owned by the daemon's own stable loopback ID, via the real
	// registration path (mirrors production: every yakos.term.create call
	// stamps this ID).
	createHandler := handleTermCreate(cfg)
	createParams, err := json.Marshal(termCreateParams{
		SessionID:     "sess-term-list-owned",
		Argv:          []string{"claude"},
		WorkspaceRoot: "/tmp/project-a",
	})
	if err != nil {
		t.Fatalf("marshal create params: %v", err)
	}
	if _, err := createHandler(context.Background(), createParams); err != nil {
		t.Fatalf("handleTermCreate: %v", err)
	}

	// A second, foreign-owned session — not reachable via yakos.term.create
	// (which never accepts a caller-supplied owner; see
	// TestHandleTermCreate_RejectsArbitraryCallerAsOwner above), planted
	// directly to prove the list handler itself filters rather than merely
	// inheriting a coincidence of every session sharing one owner.
	if err := mgr.RegisterExternalSession("sess-term-list-foreign", "/tmp/project-b", []string{"claude"}, "mallory"); err != nil {
		t.Fatalf("RegisterExternalSession (foreign owner): %v", err)
	}

	listHandler := handleTermList(cfg)
	result, err := listHandler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handleTermList: %v", err)
	}
	got, ok := result.(termListResult)
	if !ok {
		t.Fatalf("handleTermList result type = %T; want termListResult", result)
	}

	var ids []string
	for _, s := range got.Sessions {
		ids = append(ids, s.SessionID)
	}
	foundOwned, foundForeign := false, false
	for _, id := range ids {
		if id == "sess-term-list-owned" {
			foundOwned = true
		}
		if id == "sess-term-list-foreign" {
			foundForeign = true
		}
	}
	if !foundOwned {
		t.Errorf("yakos.term.list omitted the daemon's own session; sessions=%v", ids)
	}
	if foundForeign {
		t.Errorf("yakos.term.list leaked a foreign-owned session (%q); sessions=%v", "sess-term-list-foreign", ids)
	}
}
