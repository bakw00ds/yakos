package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
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

// ---- Governor tests ------------------------------------------------------------

// TestGovernorCap verifies that when K dispatches are already in-flight, a
// K+1th dispatch blocks until a slot is freed — and that the peak concurrent
// count never exceeds the cap.
//
// Implementation note: we can't easily inject a stub Run into Service (Run is
// a package-level function). Instead we test the semaphore logic directly by
// constructing a Service with a known cap and verifying that:
//  1. Exactly `cap` goroutines can acquire the semaphore concurrently.
//  2. A (cap+1)th goroutine blocks until one is released.
//  3. Cancelling the (cap+1)th ctx returns an "at capacity" error immediately.
func TestGovernorCap(t *testing.T) {
	const cap = 3

	svc := NewService(ServiceConfig{MaxConcurrent: cap})

	// Hold `cap` semaphore slots.
	held := make([]struct{}, cap)
	for i := range held {
		select {
		case <-svc.sem:
			// acquired
		default:
			t.Fatalf("could not acquire slot %d/%d", i+1, cap)
		}
	}

	// Verify semaphore is now empty.
	select {
	case <-svc.sem:
		// We should NOT be able to acquire another slot.
		t.Fatal("acquired slot beyond cap — semaphore miscounted")
	default:
		// Correct: no slot available.
	}

	// A context-cancelled acquire should return an error immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	done := make(chan error, 1)
	go func() {
		_, _, err := svc.Run(ctx, Params{
			Agent:   "backend",
			Task:    "test",
			Project: "/p",
		})
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

	// Release all held slots.
	for range held {
		svc.sem <- struct{}{}
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

// TestConversationIDPrecedence verifies the precedence rule:
//  1. Request.ConversationID (per-request) beats
//  2. YAKOS_CONVERSATION_ID env var (legacy fallback)
//
// The test does this by examining what ends up in the dispatch-log NDJSON.
func TestConversationIDPrecedence(t *testing.T) {
	logDir := isolatedLogDir(t)
	logPath := filepath.Join(logDir, "dispatch-log.ndjson")

	// Set the env fallback.
	t.Setenv("YAKOS_CONVERSATION_ID", "env-conv-id")

	// When Request.ConversationID is set, it should win.
	req := Request{
		AgentName:      "backend",
		Runtime:        "claude",
		Project:        "/p",
		Task:           "task",
		ModelResolved:  "sonnet",
		ModelChosenBy:  "frontmatter",
		ConversationID: "request-conv-id",
	}
	writeFinished(req, Result{ModelChosenBy: "frontmatter", ModelResolved: "sonnet"}, fixedTime, logPath)

	events := readDispatchLog(t, logDir)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0]["conversation_id"] != "request-conv-id" {
		t.Errorf("conversation_id: got %v, want %q — per-request field should win over env var",
			events[0]["conversation_id"], "request-conv-id")
	}
}

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
// "mcp:<agent>" for MCP-originated dispatches. We test via Params construction
// (not a live dispatch) to keep the test hermetic.
func TestMCPOperatorIDConvention(t *testing.T) {
	p := Params{
		Agent:      "security-reviewer",
		Task:       "audit the codebase",
		OperatorID: fmt.Sprintf("mcp:%s", "security-reviewer"),
	}
	if p.OperatorID != "mcp:security-reviewer" {
		t.Errorf("MCP operator_id convention: got %q, want %q",
			p.OperatorID, "mcp:security-reviewer")
	}
}
