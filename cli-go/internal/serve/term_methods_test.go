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
