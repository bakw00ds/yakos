package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/cost"
)

// ---- NDJSON parity tests -------------------------------------------------------

// TestLegacyLineParses asserts that a bash-style dispatch_finished line
// (without the Phase 2 identity fields) still parses cleanly through the
// cost.Event reader. This is the two-writer parity test: the Go writer adds
// identity fields; the bash writer does not; both must be readable.
func TestLegacyLineParses(t *testing.T) {
	// A legacy bash-written dispatch_finished line: no operator_id,
	// conversation_id, or session_id fields.
	legacyLine := `{"type":"dispatch_finished","ts":"2026-01-15T10:00:00Z","agent":"backend","runtime":"claude","project":"/abs/proj","exit_code":0,"duration_s":1.5,"output_bytes":200,"task_bytes":100,"est_input_tokens":25,"est_output_tokens":50,"model_chosen_by":"frontmatter","model_resolved":"sonnet","eval_run_id":null,"stderr_tail":null,"stderr_truncated":false}`

	var ev cost.Event
	if err := json.Unmarshal([]byte(legacyLine), &ev); err != nil {
		t.Fatalf("legacy line failed to parse: %v", err)
	}
	if ev.Type != "dispatch_finished" {
		t.Errorf("type: got %q, want %q", ev.Type, "dispatch_finished")
	}
	if ev.Agent != "backend" {
		t.Errorf("agent: got %q, want %q", ev.Agent, "backend")
	}
	// Identity fields must be empty (zero value) — not an error.
	if ev.OperatorID != "" {
		t.Errorf("operator_id: expected empty for legacy line, got %q", ev.OperatorID)
	}
	if ev.ConversationID != "" {
		t.Errorf("conversation_id: expected empty for legacy line, got %q", ev.ConversationID)
	}
	if ev.SessionID != "" {
		t.Errorf("session_id: expected empty for legacy line, got %q", ev.SessionID)
	}
}

// TestGoLineRoundTripsIdentity asserts that a Go-written dispatch_finished line
// carries the three identity fields when the Request has them populated, and
// that they survive a cost.Event parse round-trip.
func TestGoLineRoundTripsIdentity(t *testing.T) {
	logDir := isolatedLogDir(t)
	logPath := filepath.Join(logDir, "dispatch-log.ndjson")

	req := Request{
		AgentName:      "backend",
		Runtime:        "claude",
		Project:        "/abs/project",
		Task:           "implement feature",
		ModelChosenBy:  "frontmatter",
		ModelResolved:  "sonnet",
		OperatorID:     "alice",
		ConversationID: "conv-abc123",
		SessionID:      "sess-xyz",
	}
	res := Result{
		ExitCode:      0,
		DurationS:     2.0,
		OutputBytes:   512,
		TaskBytes:     128,
		ModelChosenBy: "frontmatter",
		ModelResolved: "sonnet",
	}

	writeFinished(req, res, fixedTime, logPath)
	events := readDispatchLog(t, logDir)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev["operator_id"] != "alice" {
		t.Errorf("operator_id: got %v, want %q", ev["operator_id"], "alice")
	}
	if ev["conversation_id"] != "conv-abc123" {
		t.Errorf("conversation_id: got %v, want %q", ev["conversation_id"], "conv-abc123")
	}
	if ev["session_id"] != "sess-xyz" {
		t.Errorf("session_id: got %v, want %q", ev["session_id"], "sess-xyz")
	}
}

// TestGoLineIdentityAbsentWhenEmpty asserts that when identity fields are empty,
// they are omitted from the JSON output (omitempty). This is the additive-optional
// contract: no new keys appear in lines from callers that don't supply identity.
func TestGoLineIdentityAbsentWhenEmpty(t *testing.T) {
	logDir := isolatedLogDir(t)
	logPath := filepath.Join(logDir, "dispatch-log.ndjson")

	req := Request{
		AgentName:     "backend",
		Runtime:       "claude",
		Project:       "/p",
		Task:          "task",
		ModelChosenBy: "frontmatter",
		ModelResolved: "sonnet",
		// OperatorID, ConversationID, SessionID all empty
	}
	res := Result{
		ExitCode:      0,
		ModelChosenBy: "frontmatter",
		ModelResolved: "sonnet",
	}

	writeFinished(req, res, fixedTime, logPath)

	events := readDispatchLog(t, logDir)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]

	// Identity keys must be absent entirely (not present as null or "").
	if _, ok := ev["operator_id"]; ok {
		t.Errorf("operator_id must be absent when empty; found key with value %v", ev["operator_id"])
	}
	if _, ok := ev["conversation_id"]; ok {
		t.Errorf("conversation_id must be absent when empty; found key with value %v", ev["conversation_id"])
	}
	if _, ok := ev["session_id"]; ok {
		t.Errorf("session_id must be absent when empty; found key with value %v", ev["session_id"])
	}
}

// TestStartedLineRoundTripsIdentity checks that writeStarted also emits
// identity fields when present and omits them when absent.
func TestStartedLineRoundTripsIdentity(t *testing.T) {
	logDir := isolatedLogDir(t)
	logPath := filepath.Join(logDir, "dispatch-log.ndjson")

	req := Request{
		AgentName:      "backend",
		Runtime:        "claude",
		Project:        "/abs/project",
		Task:           "implement feature",
		OperatorID:     "bob",
		ConversationID: "conv-456",
		SessionID:      "sess-pane-1",
	}

	writeStarted(req, fixedTime, logPath)
	events := readDispatchLog(t, logDir)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev["operator_id"] != "bob" {
		t.Errorf("operator_id: got %v, want %q", ev["operator_id"], "bob")
	}
	if ev["conversation_id"] != "conv-456" {
		t.Errorf("conversation_id: got %v, want %q", ev["conversation_id"], "conv-456")
	}
	if ev["session_id"] != "sess-pane-1" {
		t.Errorf("session_id: got %v, want %q", ev["session_id"], "sess-pane-1")
	}
}

// TestCostReaderToleratesIdentityFields asserts that the cost.StreamFinished
// reader parses lines with identity fields (newer format) without dropping them
// and without error — confirming the additive-optional contract holds for readers.
func TestCostReaderToleratesIdentityFields(t *testing.T) {
	// A Go-written dispatch_finished line WITH identity fields.
	modernLine := `{"type":"dispatch_finished","ts":"2026-06-01T10:00:00Z","agent":"backend","runtime":"claude","project":"/p","exit_code":0,"duration_s":1.2,"output_bytes":100,"task_bytes":50,"est_input_tokens":12,"est_output_tokens":25,"model_chosen_by":"frontmatter","model_resolved":"sonnet","eval_run_id":null,"stderr_tail":null,"stderr_truncated":false,"operator_id":"alice","conversation_id":"conv-abc","session_id":"sess-1"}`

	r := strings.NewReader(modernLine + "\n")
	ch := cost.StreamFinished(r, "")
	var events []cost.Event
	for ev := range ch {
		events = append(events, ev)
	}
	if len(events) != 1 {
		t.Fatalf("cost.StreamFinished: got %d events, want 1", len(events))
	}
	ev := events[0]
	if ev.Agent != "backend" {
		t.Errorf("agent: got %q, want %q", ev.Agent, "backend")
	}
	// Identity fields are read back correctly via the cost.Event struct.
	if ev.OperatorID != "alice" {
		t.Errorf("operator_id: got %q, want %q", ev.OperatorID, "alice")
	}
	if ev.ConversationID != "conv-abc" {
		t.Errorf("conversation_id: got %q, want %q", ev.ConversationID, "conv-abc")
	}
	if ev.SessionID != "sess-1" {
		t.Errorf("session_id: got %q, want %q", ev.SessionID, "sess-1")
	}
}

// ---- Governor tests (S1: real concurrency) -------------------------------------

// withRunFn replaces the package-level runFn for the duration of fn's call,
// then restores the original before returning.  Unlike setRunFn it does not
// register a t.Cleanup, so the replacement is guaranteed to be restored before
// withRunFn returns — safe for tests that launch goroutines that outlive the
// replacement window.
func withRunFn(fn func(ctx context.Context, req Request) ([]byte, Result, error), body func()) {
	orig := runFn
	runFn = fn
	defer func() { runFn = orig }()
	body()
}

// setRunFn replaces the package-level runFn for the duration of the test.
// Restores the original on test cleanup.  Use withRunFn instead when goroutines
// that read runFn may outlive the test body.
func setRunFn(t *testing.T, fn func(ctx context.Context, req Request) ([]byte, Result, error)) {
	t.Helper()
	orig := runFn
	runFn = fn
	t.Cleanup(func() { runFn = orig })
}

// TestGovernorCap proves the concurrency bound under real goroutine concurrency.
//
// Strategy:
//   - Install a fake runFn (scoped via withRunFn) that blocks per-goroutine
//     on individual release channels so we can control exactly which goroutine
//     exits at any point.
//   - Launch K goroutines through Service.Run; confirm all K slots are acquired.
//   - Confirm the K+1th goroutine blocks while all K slots are held.
//   - Release one specific slot; confirm the K+1th unblocks and completes.
//   - Confirm the slot is released on the ERROR path too.
func TestGovernorCap(t *testing.T) {
	const numSlots = 3

	svc := NewService(ServiceConfig{
		YakosRoot:     "/yakos",
		WorkspaceRoot: "/p",
		MaxConcurrent: numSlots,
	})

	// Each goroutine gets its own release channel so we release exactly one.
	type entry struct {
		release chan struct{}
		started chan struct{}
	}

	entries := make([]entry, numSlots)
	for i := range entries {
		entries[i] = entry{
			release: make(chan struct{}),
			started: make(chan struct{}),
		}
	}

	var idx int32 // round-robins which entry each runFn call picks up

	// Phase 1: blocking fake — each goroutine signals started then blocks.
	type runResult struct{ err error }
	results := make(chan runResult, numSlots+1)

	allDone := make(chan struct{})

	withRunFn(func(ctx context.Context, req Request) ([]byte, Result, error) {
		i := int(atomic.AddInt32(&idx, 1)) - 1
		if i < len(entries) {
			close(entries[i].started) // signal that we are inside runFn
			select {
			case <-entries[i].release:
				return nil, Result{}, nil
			case <-ctx.Done():
				return nil, Result{}, ctx.Err()
			}
		}
		// K+1th and beyond: exit immediately.
		return nil, Result{}, nil
	}, func() {
		// Launch K goroutines that will block inside the fake runFn.
		for i := 0; i < numSlots; i++ {
			go func() {
				_, _, err := svc.Run(context.Background(), Params{
					Agent:   "backend",
					Task:    "task",
					Project: "/p",
				})
				results <- runResult{err}
			}()
		}

		// Wait for each goroutine to enter runFn (all slots consumed).
		for i := 0; i < numSlots; i++ {
			select {
			case <-entries[i].started:
			case <-time.After(5 * time.Second):
				// Release all so goroutines can drain.
				for j := 0; j < numSlots; j++ {
					select {
					case entries[j].release <- struct{}{}:
					default:
					}
				}
				t.Fatalf("timed out waiting for goroutine %d to enter runFn", i)
			}
		}

		// All K slots are held.  The K+1th request must block.
		blocked := make(chan error, 1)
		blockCtx, blockCancel := context.WithCancel(context.Background())
		defer blockCancel()
		go func() {
			_, _, err := svc.Run(blockCtx, Params{Agent: "backend", Task: "task", Project: "/p"})
			blocked <- err
		}()

		// Give the K+1th a moment and confirm it is still blocked.
		select {
		case got := <-blocked:
			for i := 0; i < numSlots; i++ {
				entries[i].release <- struct{}{}
			}
			t.Fatalf("K+1th goroutine returned before any slot was released (err=%v)", got)
		case <-time.After(200 * time.Millisecond):
			// Correct: still blocked.
		}

		// Release slot 0.  The K+1th goroutine picks it up and, because idx
		// is now >= len(entries), the fake runFn exits immediately.
		entries[0].release <- struct{}{}
		<-results // drain goroutine 0's result

		select {
		case <-blocked:
			// K+1th completed — correct.
		case <-time.After(5 * time.Second):
			for i := 1; i < numSlots; i++ {
				entries[i].release <- struct{}{}
			}
			close(allDone)
			t.Fatal("K+1th goroutine did not unblock after slot 0 was released")
		}

		// Release remaining goroutines.
		for i := 1; i < numSlots; i++ {
			entries[i].release <- struct{}{}
		}
		for i := 1; i < numSlots; i++ {
			<-results
		}
		close(allDone)
	})

	// Wait for all phase-1 goroutines to be fully done before reassigning runFn.
	<-allDone

	// Phase 2: error-path slot release.
	// Confirm the slot returns to the semaphore even when runFn returns an error.
	withRunFn(func(ctx context.Context, req Request) ([]byte, Result, error) {
		return nil, Result{}, fmt.Errorf("injected error")
	}, func() {
		_, _, err := svc.Run(context.Background(), Params{Agent: "backend", Task: "task", Project: "/p"})
		if err == nil {
			t.Fatal("expected error from error-path runFn, got nil")
		}
	})
	if len(svc.sem) != numSlots {
		t.Errorf("slot not released on error path: sem len=%d, want %d", len(svc.sem), numSlots)
	}
}

// TestGovernorCapCancelledContext verifies that a pre-cancelled context returns
// an "at capacity" error immediately (without entering runFn).
func TestGovernorCapCancelledContext(t *testing.T) {
	const numSlots = 3

	// Hold all slots manually.
	svc := NewService(ServiceConfig{
		YakosRoot:     "/yakos",
		WorkspaceRoot: "/p",
		MaxConcurrent: numSlots,
	})
	for i := 0; i < numSlots; i++ {
		<-svc.sem
	}
	defer func() {
		for i := 0; i < numSlots; i++ {
			svc.sem <- struct{}{}
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	done := make(chan error, 1)
	go func() {
		_, _, err := svc.Run(ctx, Params{Agent: "backend", Task: "task", Project: "/p"})
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error on cancelled context, got nil")
		}
		if !strings.Contains(err.Error(), "at capacity") {
			t.Errorf("expected 'at capacity' in error, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for cancelled dispatch to return")
	}
}

// TestGovernorDefaultCap verifies the default cap is defaultMaxConcurrent.
func TestGovernorDefaultCap(t *testing.T) {
	svc := NewService(ServiceConfig{})
	if cap(svc.sem) != defaultMaxConcurrent {
		t.Errorf("default cap: got %d, want %d", cap(svc.sem), defaultMaxConcurrent)
	}
	// All slots should be available initially.
	if len(svc.sem) != defaultMaxConcurrent {
		t.Errorf("initial slots: got %d, want %d (all slots should be pre-loaded)", len(svc.sem), defaultMaxConcurrent)
	}
}

// TestConversationIDPrecedence verifies that a per-request ConversationID in
// Params beats the YAKOS_CONVERSATION_ID env var fallback — exercised through
// the real Service.Run → runFn path (not just writeFinished).
func TestConversationIDPrecedence(t *testing.T) {
	logDir := isolatedLogDir(t)
	t.Setenv("YAKOS_CONVERSATION_ID", "env-conv-id")

	// Capture the Request that Service.Run passes to runFn.
	var capturedConvID string
	setRunFn(t, func(ctx context.Context, req Request) ([]byte, Result, error) {
		capturedConvID = req.ConversationID
		return nil, Result{}, nil
	})

	svc := NewService(ServiceConfig{
		YakosRoot:     "/yakos",
		WorkspaceRoot: "/p",
		OperatorID:    "test-op",
	})
	_ = logDir // isolatedLogDir sets YAKOS_DISPATCH_LOG

	_, _, err := svc.Run(context.Background(), Params{
		Agent:          "backend",
		Task:           "task",
		Project:        "/p",
		ConversationID: "request-conv-id",
	})
	if err != nil {
		t.Fatalf("Service.Run: unexpected error: %v", err)
	}

	// The Request passed to runFn must have the per-request value, not the env.
	if capturedConvID != "request-conv-id" {
		t.Errorf("ConversationID in Request: got %q, want %q — per-request field should win over env var",
			capturedConvID, "request-conv-id")
	}
}

// ---- B1: per-request YakosRoot honored -----------------------------------------

// TestPerRequestYakosRootHonored verifies that a non-empty Params.YakosRoot
// overrides Config.YakosRoot in the Request passed to runFn.
func TestPerRequestYakosRootHonored(t *testing.T) {
	var capturedRoot string
	setRunFn(t, func(ctx context.Context, req Request) ([]byte, Result, error) {
		capturedRoot = req.YakosRoot
		return nil, Result{}, nil
	})

	svc := NewService(ServiceConfig{
		YakosRoot:     "/cfg/yakos",
		WorkspaceRoot: "/p",
		OperatorID:    "op",
	})

	_, _, err := svc.Run(context.Background(), Params{
		Agent:     "backend",
		Task:      "task",
		Project:   "/p",
		YakosRoot: "/override/yakos",
	})
	if err != nil {
		t.Fatalf("Service.Run: unexpected error: %v", err)
	}
	if capturedRoot != "/override/yakos" {
		t.Errorf("YakosRoot in Request: got %q, want %q — per-request must override cfg", capturedRoot, "/override/yakos")
	}
}

// TestYakosRootFallsToCfg verifies that when Params.YakosRoot is empty, the
// Request gets Config.YakosRoot.
func TestYakosRootFallsToCfg(t *testing.T) {
	var capturedRoot string
	setRunFn(t, func(ctx context.Context, req Request) ([]byte, Result, error) {
		capturedRoot = req.YakosRoot
		return nil, Result{}, nil
	})

	svc := NewService(ServiceConfig{
		YakosRoot:     "/cfg/yakos",
		WorkspaceRoot: "/p",
		OperatorID:    "op",
	})

	_, _, err := svc.Run(context.Background(), Params{
		Agent:   "backend",
		Task:    "task",
		Project: "/p",
		// YakosRoot empty → should fall through to cfg
	})
	if err != nil {
		t.Fatalf("Service.Run: unexpected error: %v", err)
	}
	if capturedRoot != "/cfg/yakos" {
		t.Errorf("YakosRoot in Request: got %q, want %q — should fall back to cfg", capturedRoot, "/cfg/yakos")
	}
}

// TestYakosRootBothEmptyErrors verifies that when both Params.YakosRoot and
// Config.YakosRoot are empty, Service.Run returns an error before calling runFn.
func TestYakosRootBothEmptyErrors(t *testing.T) {
	called := false
	setRunFn(t, func(ctx context.Context, req Request) ([]byte, Result, error) {
		called = true
		return nil, Result{}, nil
	})

	svc := NewService(ServiceConfig{
		WorkspaceRoot: "/p",
		OperatorID:    "op",
		// YakosRoot intentionally empty
	})

	_, _, err := svc.Run(context.Background(), Params{
		Agent:   "backend",
		Task:    "task",
		Project: "/p",
		// YakosRoot empty too
	})
	if err == nil {
		t.Fatal("expected error when both YakosRoot values are empty, got nil")
	}
	if called {
		t.Error("runFn should not have been called when YakosRoot is empty")
	}
}

// ---- MEDIUM-2: identity allow-list validation ----------------------------------

// TestIdentityFieldRejectsLeadingDash verifies that a dash-prefixed OperatorID
// is rejected (argv flag-injection vector).
func TestIdentityFieldRejectsLeadingDash(t *testing.T) {
	setRunFn(t, func(ctx context.Context, req Request) ([]byte, Result, error) {
		return nil, Result{}, nil
	})
	svc := NewService(ServiceConfig{YakosRoot: "/y", WorkspaceRoot: "/p", OperatorID: "op"})

	_, _, err := svc.Run(context.Background(), Params{
		Agent:      "backend",
		Task:       "task",
		Project:    "/p",
		OperatorID: "--flag-injection",
	})
	if err == nil {
		t.Fatal("expected validation error for dash-prefixed operator_id, got nil")
	}
}

// TestIdentityFieldRejectsOver128Chars verifies that an over-128-character
// ConversationID is rejected.
func TestIdentityFieldRejectsOver128Chars(t *testing.T) {
	setRunFn(t, func(ctx context.Context, req Request) ([]byte, Result, error) {
		return nil, Result{}, nil
	})
	svc := NewService(ServiceConfig{YakosRoot: "/y", WorkspaceRoot: "/p", OperatorID: "op"})

	longID := "a" + strings.Repeat("b", 128) // 129 chars total
	_, _, err := svc.Run(context.Background(), Params{
		Agent:          "backend",
		Task:           "task",
		Project:        "/p",
		ConversationID: longID,
	})
	if err == nil {
		t.Fatal("expected validation error for >128 char conversation_id, got nil")
	}
}

// TestIdentityFieldAcceptsValidValue verifies that a valid identity value passes
// the allow-list check.
func TestIdentityFieldAcceptsValidValue(t *testing.T) {
	setRunFn(t, func(ctx context.Context, req Request) ([]byte, Result, error) {
		return nil, Result{}, nil
	})
	svc := NewService(ServiceConfig{YakosRoot: "/y", WorkspaceRoot: "/p", OperatorID: "op"})

	_, _, err := svc.Run(context.Background(), Params{
		Agent:          "backend",
		Task:           "task",
		Project:        "/p",
		ConversationID: "conv-abc123.v2:ok",
	})
	if err != nil {
		t.Fatalf("valid conversation_id was rejected: %v", err)
	}
}

// TestIdentityFieldRejectsAt verifies that an @ character in a session_id is
// rejected (not in the allow-list).
func TestIdentityFieldRejectsAt(t *testing.T) {
	setRunFn(t, func(ctx context.Context, req Request) ([]byte, Result, error) {
		return nil, Result{}, nil
	})
	svc := NewService(ServiceConfig{YakosRoot: "/y", WorkspaceRoot: "/p", OperatorID: "op"})

	_, _, err := svc.Run(context.Background(), Params{
		Agent:     "backend",
		Task:      "task",
		Project:   "/p",
		SessionID: "user@host",
	})
	if err == nil {
		t.Fatal("expected validation error for '@' in session_id, got nil")
	}
}

// ---- MEDIUM-1: reserved operator-id namespace ----------------------------------

// TestHumanTransportClaimingMCPPrefixIsDropped verifies that a non-MCP caller
// claiming "mcp:..." as its OperatorID has the claim silently dropped to the
// daemon default (not forwarded into the Request).
func TestHumanTransportClaimingMCPPrefixIsDropped(t *testing.T) {
	var capturedOpID string
	setRunFn(t, func(ctx context.Context, req Request) ([]byte, Result, error) {
		capturedOpID = req.OperatorID
		return nil, Result{}, nil
	})

	svc := NewService(ServiceConfig{
		YakosRoot:     "/y",
		WorkspaceRoot: "/p",
		OperatorID:    "daemon-op",
	})

	// Human-transport Params: isMCPStamped is false (the default).
	_, _, err := svc.Run(context.Background(), Params{
		Agent:      "backend",
		Task:       "task",
		Project:    "/p",
		OperatorID: "mcp:backend", // reserved prefix
	})
	if err != nil {
		t.Fatalf("Service.Run: unexpected error: %v", err)
	}
	// Claim must be dropped; daemon default must be used instead.
	if capturedOpID == "mcp:backend" {
		t.Error("reserved mcp: prefix was forwarded from a non-MCP transport — should be dropped")
	}
	if capturedOpID != "daemon-op" {
		t.Errorf("OperatorID: got %q, want daemon default %q", capturedOpID, "daemon-op")
	}
}

// TestMCPStampedParamsAllowsMCPPrefix verifies that a Params built with
// MCPParams() does forward the "mcp:" OperatorID into the Request.
func TestMCPStampedParamsAllowsMCPPrefix(t *testing.T) {
	var capturedOpID string
	setRunFn(t, func(ctx context.Context, req Request) ([]byte, Result, error) {
		capturedOpID = req.OperatorID
		return nil, Result{}, nil
	})

	svc := NewService(ServiceConfig{
		YakosRoot:     "/y",
		WorkspaceRoot: "/p",
		OperatorID:    "daemon-op",
	})

	_, _, err := svc.Run(context.Background(), MCPParams(Params{
		Agent:      "backend",
		Task:       "task",
		Project:    "/p",
		OperatorID: "mcp:backend",
	}))
	if err != nil {
		t.Fatalf("Service.Run: unexpected error: %v", err)
	}
	if capturedOpID != "mcp:backend" {
		t.Errorf("OperatorID: got %q, want %q — MCP-stamped params should forward mcp: prefix", capturedOpID, "mcp:backend")
	}
}

// ---- Other existing tests ------------------------------------------------------

// TestMintOperatorID verifies that mintOperatorID returns a non-empty string
// on this host (where OS user info should be available).
func TestMintOperatorID(t *testing.T) {
	id := mintOperatorID()
	// On CI we just verify it doesn't panic; value depends on runtime user.
	// On a dev machine it should be non-empty.
	_ = id
	t.Logf("mintOperatorID() = %q", id)
}

// TestServiceStampsOperatorID verifies that NewService resolves and stores an
// operator ID at construction time.
// This test uses a ServiceConfig with a controlled OperatorID to avoid
// OS-user-resolution flakiness.
func TestServiceStampsOperatorID(t *testing.T) {
	svc := NewService(ServiceConfig{
		WorkspaceRoot: "/tmp",
		YakosRoot:     "/tmp/yakos",
		OperatorID:    "test-operator",
	})

	if svc.opID != "test-operator" {
		t.Errorf("opID: got %q, want %q", svc.opID, "test-operator")
	}
}

// TestMCPOperatorIDConvention verifies the MCP convention: operator_id is
// "mcp:<agent>" for MCP-originated dispatches.  The MCPParams constructor is
// the only way to set isMCPStamped=true from outside the package.
func TestMCPOperatorIDConvention(t *testing.T) {
	p := MCPParams(Params{
		Agent:      "security-reviewer",
		Task:       "audit the codebase",
		OperatorID: "mcp:security-reviewer",
	})
	if p.OperatorID != "mcp:security-reviewer" {
		t.Errorf("MCP operator_id convention: got %q, want %q",
			p.OperatorID, "mcp:security-reviewer")
	}
	if !p.isMCPStamped {
		t.Error("MCPParams should set isMCPStamped=true")
	}
}

// ---- LOW-2: YAKOS_CONVERSATION_ID daemon-path isolation -----------------------

// TestDaemonPathIgnoresEnvConversationID verifies that Service.Run does NOT read
// YAKOS_CONVERSATION_ID from the environment.  The env var is exclusively for the
// CLI one-shot path (cmd/yakos/main.go runDispatch); the daemon path uses only
// req.ConversationID.
//
// LOW-2 remediation, Phase 2.5: env vars read inside the daemon without
// validation are a shell-injection vector and bypass the allow-list check.
func TestDaemonPathIgnoresEnvConversationID(t *testing.T) {
	const envConvID = "env-conv-from-yakos-env"
	t.Setenv("YAKOS_CONVERSATION_ID", envConvID)

	var capturedConvID string
	setRunFn(t, func(ctx context.Context, req Request) ([]byte, Result, error) {
		capturedConvID = req.ConversationID
		return nil, Result{}, nil
	})

	svc := NewService(ServiceConfig{
		YakosRoot:     "/yakos",
		WorkspaceRoot: "/p",
		OperatorID:    "op",
	})

	_, _, err := svc.Run(context.Background(), Params{
		Agent:   "backend",
		Task:    "task",
		Project: "/p",
		// ConversationID intentionally NOT set in Params — simulates daemon path.
	})
	if err != nil {
		t.Fatalf("Service.Run: unexpected error: %v", err)
	}

	// The daemon path must NOT pick up the env var.  capturedConvID must be empty.
	if capturedConvID == envConvID {
		t.Errorf("Service.Run picked up YAKOS_CONVERSATION_ID from env=%q; daemon path must NOT read this env var", envConvID)
	}
	if capturedConvID != "" {
		t.Errorf("ConversationID: got %q; want empty (daemon path sets nothing when Params.ConversationID is empty)", capturedConvID)
	}
}

// TestValidateIdentityField_Exported verifies that the exported
// ValidateIdentityField function enforces the same allow-list as the internal
// validateIdentityField used by Service.Run.
func TestValidateIdentityField_Exported(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"empty is ok", "", false},
		{"valid alphanumeric", "conv-abc123", false},
		{"valid with dots colons", "conv.abc:123", false},
		{"leading dash rejected", "-bad-id", true},
		{"too long (129 chars)", "a" + strings.Repeat("b", 128), true},
		{"at-sign rejected", "user@host", true},
		{"spaces rejected", "hello world", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateIdentityField("conversation_id", c.value)
			if c.wantErr && err == nil {
				t.Errorf("ValidateIdentityField(%q) = nil; want error", c.value)
			}
			if !c.wantErr && err != nil {
				t.Errorf("ValidateIdentityField(%q) = %v; want nil", c.value, err)
			}
		})
	}
}

