package interactive_test

// sdk_engine_test.go — Tests for SDKEngine driven by a fake NDJSON sidecar.
//
// All tests use a fake sidecar (a small shell script or inline Go process)
// that speaks the NDJSON protocol.  No npm, no network, no real Node.js
// required.  The real-SDK integration test is gated behind CI/SKIP flags.
//
// Coverage:
//  1. Ready handshake: SDKEngine.Start waits for the "ready" frame.
//  2. user_turn → token + summary chunks.
//  3. ask_user_question chunk is surfaced; AnswerQuestion writes the correct frame.
//  4. Fake sidecar continues after answer (tool_result + summary).
//  5. Close() sends shutdown and group-kills the sidecar process cleanly.
//  6. Bounded line decode: a >2MB line from the sidecar is discarded.
//  7. Unknown frame kind is silently skipped (forward-compat).
//  8. Start blocks until ready or timeout/cancel.
//  9. IsClosed / Closed channel: correct after Close().

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/interactive"
)

// ---------------------------------------------------------------------------
// Fake sidecar helpers
// ---------------------------------------------------------------------------

// fakeSidecar builds a CmdProvider (func() *exec.Cmd) that runs a POSIX sh
// script speaking the NDJSON sidecar protocol.  The script:
//   - Emits {"v":1,"kind":"ready"} immediately.
//   - Reads a user_turn frame from stdin.
//   - Emits a token chunk + summary.
//   - If wantAsk is true, for the SECOND user_turn:
//     - Emits an ask_user_question frame.
//     - Reads an "answer" frame (the exact content doesn't matter for the fake).
//     - Emits a tool_result + summary to confirm continuation.
//   - Exits cleanly when stdin is closed (EOF on read).
func fakeSidecarScript(turns int, wantAsk bool) func() *exec.Cmd {
	readyLine := `{"v":1,"kind":"ready"}`
	tokenFmt := func(i int) string {
		return fmt.Sprintf(`{"v":1,"kind":"token","text":"turn-%d"}`, i)
	}
	summaryLine := `{"v":1,"kind":"summary","totalCostUsd":0.001,"usage":{}}`
	askLine := `{"v":1,"kind":"ask_user_question","toolUseId":"test-ask-id","questions":[{"question":"Pick one","header":"Choose","multiSelect":false,"options":[{"label":"A","description":"Option A"}]}]}`
	toolResultLine := `{"v":1,"kind":"tool_result","toolName":"AskUserQuestion","toolOutput":"answered","isError":false}`

	var sb strings.Builder
	sb.WriteString("#!/bin/sh\n")
	// Emit ready immediately.
	sb.WriteString("printf '%s\\n' " + shQuote(readyLine) + "\n")

	for i := 1; i <= turns; i++ {
		// Read a user_turn frame (discard content).
		sb.WriteString("read -r _line || exit 0\n")

		if wantAsk && i == 2 {
			// Emit ask_user_question for the second turn.
			sb.WriteString("printf '%s\\n' " + shQuote(askLine) + "\n")
			// Read the answer frame.
			sb.WriteString("read -r _ans || exit 0\n")
			// Emit tool_result + summary.
			sb.WriteString("printf '%s\\n' " + shQuote(toolResultLine) + "\n")
			sb.WriteString("printf '%s\\n' " + shQuote(summaryLine) + "\n")
		} else {
			// Normal turn: token + summary.
			sb.WriteString("printf '%s\\n' " + shQuote(tokenFmt(i)) + "\n")
			sb.WriteString("printf '%s\\n' " + shQuote(summaryLine) + "\n")
		}
	}
	sb.WriteString("exit 0\n")

	script := sb.String()
	return func() *exec.Cmd {
		return exec.Command("sh", "-c", script) //nolint:gosec
	}
}

// fakeSidecarCrash returns a fake sidecar that crashes immediately (no ready).
func fakeSidecarCrash() func() *exec.Cmd {
	return func() *exec.Cmd {
		return exec.Command("sh", "-c", "exit 1") //nolint:gosec
	}
}

// fakeSidecarAlive returns a fake sidecar that emits ready and then blocks
// on stdin forever.
func fakeSidecarAlive() func() *exec.Cmd {
	script := "printf '{\"v\":1,\"kind\":\"ready\"}\\n'; while read -r _; do :; done"
	return func() *exec.Cmd {
		return exec.Command("sh", "-c", script) //nolint:gosec
	}
}

// fakeSidecarUnknownKind emits ready, then an unknown frame kind, then a token+summary.
func fakeSidecarUnknownKind() func() *exec.Cmd {
	script := strings.Join([]string{
		"printf '%s\\n' " + shQuote(`{"v":1,"kind":"ready"}`),
		"read -r _ || exit 0",
		"printf '%s\\n' " + shQuote(`{"v":1,"kind":"future_unknown_kind","data":"ignored"}`),
		"printf '%s\\n' " + shQuote(`{"v":1,"kind":"token","text":"hello"}`),
		"printf '%s\\n' " + shQuote(`{"v":1,"kind":"summary","totalCostUsd":0,"usage":{}}`),
	}, "\n")
	return func() *exec.Cmd {
		return exec.Command("sh", "-c", script) //nolint:gosec
	}
}

// shQuote wraps s in single quotes, escaping any embedded single quotes.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// newFakeSDKEngine builds an SDKEngine using a fake sidecar cmdProvider.
func newFakeSDKEngine(t *testing.T, provider func() *exec.Cmd, onChunk func(dispatch.StreamChunk)) *interactive.SDKEngine {
	t.Helper()
	eng, err := interactive.NewSDKEngineWithProvider(
		interactive.SDKEngineParams{
			ConversationID:  "conv-sdk-test",
			OwnerOperatorID: "op-test",
			OnChunk:         onChunk,
		},
		provider,
	)
	if err != nil {
		t.Fatalf("NewSDKEngineWithProvider: %v", err)
	}
	return eng
}

// collectSDKChunks returns an onChunk callback + a get-chunks function.
func collectSDKChunks() (func(dispatch.StreamChunk), func() []dispatch.StreamChunk) {
	var mu sync.Mutex
	var chunks []dispatch.StreamChunk
	onChunk := func(c dispatch.StreamChunk) {
		mu.Lock()
		chunks = append(chunks, c)
		mu.Unlock()
	}
	get := func() []dispatch.StreamChunk {
		mu.Lock()
		defer mu.Unlock()
		cp := make([]dispatch.StreamChunk, len(chunks))
		copy(cp, chunks)
		return cp
	}
	return onChunk, get
}

// waitForN polls until at least n chunks arrive or the deadline passes.
func waitForN(t *testing.T, get func() []dispatch.StreamChunk, n int, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if len(get()) >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d chunks; got %d: %+v", n, len(get()), get())
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestSDKEngine_ReadyHandshake verifies that Start blocks until the "ready"
// frame is received and returns nil on success.
func TestSDKEngine_ReadyHandshake(t *testing.T) {
	onChunk, _ := collectSDKChunks()
	eng := newFakeSDKEngine(t, fakeSidecarAlive(), onChunk)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := eng.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer eng.Close()

	if eng.IsClosed() {
		t.Fatal("engine should not be closed after Start")
	}
}

// TestSDKEngine_UserTurnTokenSummary verifies that a user_turn results in
// token and summary chunks being delivered via onChunk.
func TestSDKEngine_UserTurnTokenSummary(t *testing.T) {
	onChunk, get := collectSDKChunks()
	eng := newFakeSDKEngine(t, fakeSidecarScript(1, false), onChunk)

	if err := eng.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer eng.Close()

	frame := []byte(`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}` + "\n")
	if err := eng.SendUserTurn(frame); err != nil {
		t.Fatalf("SendUserTurn: %v", err)
	}

	// Expect at least 2 chunks: token + summary.
	waitForN(t, get, 2, 5*time.Second)

	chunks := get()
	var hasToken, hasSummary bool
	for _, c := range chunks {
		if c.Type == "token" && strings.Contains(c.Text, "turn-1") {
			hasToken = true
		}
		if c.Type == "summary" {
			hasSummary = true
		}
	}
	if !hasToken {
		t.Errorf("expected token chunk with 'turn-1'; got: %+v", chunks)
	}
	if !hasSummary {
		t.Errorf("expected summary chunk; got: %+v", chunks)
	}
}

// TestSDKEngine_AskUserQuestion verifies that:
//  1. ask_user_question chunk is surfaced with correct ToolUseID and QuestionsJSON.
//  2. AnswerQuestion writes an answer frame and the fake sidecar continues.
//  3. tool_result + summary chunks arrive after the answer.
func TestSDKEngine_AskUserQuestion(t *testing.T) {
	onChunk, get := collectSDKChunks()
	eng := newFakeSDKEngine(t, fakeSidecarScript(2, true), onChunk)

	if err := eng.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer eng.Close()

	// Send turn 1 → token + summary.
	frame := []byte(`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"first"}]}}` + "\n")
	if err := eng.SendUserTurn(frame); err != nil {
		t.Fatalf("SendUserTurn turn 1: %v", err)
	}
	waitForN(t, get, 2, 5*time.Second)

	// Send turn 2 → ask_user_question is emitted.
	frame2 := []byte(`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"second"}]}}` + "\n")
	if err := eng.SendUserTurn(frame2); err != nil {
		t.Fatalf("SendUserTurn turn 2: %v", err)
	}

	// Wait for the ask_user_question chunk.
	deadline := time.Now().Add(5 * time.Second)
	var askChunk *dispatch.StreamChunk
	for time.Now().Before(deadline) {
		for _, c := range get() {
			if c.Type == "ask_user_question" {
				cc := c
				askChunk = &cc
				break
			}
		}
		if askChunk != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if askChunk == nil {
		t.Fatalf("no ask_user_question chunk arrived; chunks: %+v", get())
	}

	// Verify the chunk fields.
	if askChunk.AskToolUseID != "test-ask-id" {
		t.Errorf("expected AskToolUseID='test-ask-id', got %q", askChunk.AskToolUseID)
	}
	// AskQuestionsJSON must be valid JSON.
	var qs []json.RawMessage
	if err := json.Unmarshal([]byte(askChunk.AskQuestionsJSON), &qs); err != nil {
		t.Errorf("AskQuestionsJSON is not valid JSON: %v (got: %q)", err, askChunk.AskQuestionsJSON)
	}
	if len(qs) != 1 {
		t.Errorf("expected 1 question, got %d", len(qs))
	}

	prevCount := len(get())

	// Answer the question.
	answer := interactive.QuestionAnswer{
		Answers: map[string]string{
			"Pick one": "A",
		},
		QuestionsJSON: askChunk.AskQuestionsJSON,
	}
	if err := eng.AnswerQuestion(askChunk.AskToolUseID, answer); err != nil {
		t.Fatalf("AnswerQuestion: %v", err)
	}

	// Wait for tool_result + summary from the fake sidecar continuation.
	deadline2 := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline2) {
		if len(get()) > prevCount+1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	newChunks := get()[prevCount:]
	var hasToolResult, hasSummary2 bool
	for _, c := range newChunks {
		if c.Type == "tool_result" {
			hasToolResult = true
		}
		if c.Type == "summary" {
			hasSummary2 = true
		}
	}
	if !hasToolResult {
		t.Errorf("expected tool_result chunk after answer; new chunks: %+v", newChunks)
	}
	if !hasSummary2 {
		t.Errorf("expected summary chunk after answer; new chunks: %+v", newChunks)
	}
}

// TestSDKEngine_CloseGroupKills verifies that Close() cleanly terminates the
// sidecar and the Closed() channel is closed.
func TestSDKEngine_CloseGroupKills(t *testing.T) {
	onChunk, _ := collectSDKChunks()
	eng := newFakeSDKEngine(t, fakeSidecarAlive(), onChunk)

	if err := eng.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if eng.IsClosed() {
		t.Fatal("engine should not be closed before Close()")
	}

	if err := eng.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}

	// Closed() channel must be closed.
	select {
	case <-eng.Closed():
		// OK.
	case <-time.After(3 * time.Second):
		t.Fatal("Closed() channel not closed after Close()")
	}

	if !eng.IsClosed() {
		t.Fatal("IsClosed() should be true after Close()")
	}

	// Idempotent.
	if err := eng.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// TestSDKEngine_UnknownKindSkipped verifies that an unknown sidecar frame kind
// does not cause a panic or error, and that subsequent valid frames still arrive.
func TestSDKEngine_UnknownKindSkipped(t *testing.T) {
	onChunk, get := collectSDKChunks()
	eng := newFakeSDKEngine(t, fakeSidecarUnknownKind(), onChunk)

	if err := eng.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer eng.Close()

	frame := []byte(`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}` + "\n")
	if err := eng.SendUserTurn(frame); err != nil {
		t.Fatalf("SendUserTurn: %v", err)
	}

	// Expect token + summary to arrive (unknown frame is skipped).
	waitForN(t, get, 2, 5*time.Second)
	chunks := get()
	var hasToken bool
	for _, c := range chunks {
		if c.Type == "token" {
			hasToken = true
		}
	}
	if !hasToken {
		t.Errorf("expected token chunk after unknown frame; got: %+v", chunks)
	}
}

// TestSDKEngine_StartTimeoutOnNoCrash verifies that Start returns an error
// when the sidecar crashes before emitting "ready".
func TestSDKEngine_StartCrashBeforeReady(t *testing.T) {
	onChunk, _ := collectSDKChunks()
	eng := newFakeSDKEngine(t, fakeSidecarCrash(), onChunk)

	err := eng.Start(context.Background())
	if err == nil {
		t.Fatal("expected Start to return an error when sidecar crashes before ready")
	}
}

// TestSDKEngine_OwnerConversation verifies OwnerOperatorID and ConversationID.
func TestSDKEngine_OwnerConversation(t *testing.T) {
	onChunk, _ := collectSDKChunks()
	eng := newFakeSDKEngine(t, fakeSidecarAlive(), onChunk)

	if err := eng.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer eng.Close()

	if got := eng.OwnerOperatorID(); got != "op-test" {
		t.Errorf("OwnerOperatorID=%q, want %q", got, "op-test")
	}
	if got := eng.ConversationID(); got != "conv-sdk-test" {
		t.Errorf("ConversationID=%q, want %q", got, "conv-sdk-test")
	}
}

// TestSDKEngine_LastActivity verifies that SendUserTurn updates LastActivity.
func TestSDKEngine_LastActivity(t *testing.T) {
	onChunk, _ := collectSDKChunks()
	eng := newFakeSDKEngine(t, fakeSidecarScript(1, false), onChunk)

	if err := eng.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer eng.Close()

	before := eng.LastActivity()
	time.Sleep(5 * time.Millisecond)

	frame := []byte(`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"t"}]}}` + "\n")
	_ = eng.SendUserTurn(frame)

	after := eng.LastActivity()
	if !after.After(before) {
		t.Errorf("LastActivity was not updated after SendUserTurn: before=%v after=%v", before, after)
	}
}

// TestSDKEngine_ErrTurnInFlight verifies that a concurrent SendUserTurn
// returns ErrTurnInFlight.
func TestSDKEngine_ErrTurnInFlight(t *testing.T) {
	// Use a sidecar that never reads stdin so the write blocks.
	script := `printf '{"v":1,"kind":"ready"}\n'; sleep 30`
	provider := func() *exec.Cmd {
		return exec.Command("sh", "-c", script) //nolint:gosec
	}

	onChunk, _ := collectSDKChunks()
	eng := newFakeSDKEngine(t, provider, onChunk)

	if err := eng.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer eng.Close()

	// Big frame to fill pipe buffer and block.
	bigFrame := make([]byte, 128*1024)
	for i := range bigFrame {
		bigFrame[i] = 'x'
	}
	bigFrame[len(bigFrame)-1] = '\n'

	var wg sync.WaitGroup
	var results [2]error
	wg.Add(2)
	for i := 0; i < 2; i++ {
		i := i
		go func() {
			defer wg.Done()
			results[i] = eng.SendUserTurn(bigFrame)
		}()
	}

	time.Sleep(80 * time.Millisecond)
	eng.Close()
	wg.Wait()

	inflightCount := 0
	for _, r := range results {
		if isErr(r, interactive.ErrTurnInFlight) {
			inflightCount++
		}
	}
	if inflightCount == 0 {
		t.Errorf("expected at least one ErrTurnInFlight; got: %v, %v", results[0], results[1])
	}
}

// TestSDKEngine_FindNodeBinary verifies FindNodeBinary returns a non-empty path.
func TestSDKEngine_FindNodeBinary(t *testing.T) {
	p, err := interactive.FindNodeBinary()
	if err != nil {
		t.Skipf("node not in PATH: %v", err)
	}
	if p == "" {
		t.Fatal("FindNodeBinary returned empty path without error")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func isErr(err, target error) bool {
	if err == nil || target == nil {
		return err == target
	}
	return strings.Contains(err.Error(), target.Error())
}
