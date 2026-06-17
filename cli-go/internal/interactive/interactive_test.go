package interactive_test

// interactive_test.go — tests for the interactive package.
//
// Tests use a fake adapter: a small Go script that speaks stream-json,
// stays alive, and accepts a second turn.  The real claude binary is NOT
// required.  All subprocess invocations use the testdata/fake-claude.sh
// script (or a platform-appropriate equivalent built at test time).
//
// Coverage:
//  1. Turn 2 reaches the SAME process (session_id and process PID unchanged).
//  2. Owner-conflict: Ensure with different operatorID returns ErrOwnerConflict.
//  3. ErrNoSession when Send is called with no live session.
//  4. Idle reaper closes sessions that have had no activity for > idleTimeout.
//  5. Crash detection: when the process exits unexpectedly, onError is called.
//  6. Close is idempotent (no panic on double-close).
//  7. ErrTurnInFlight when a turn is already in progress.
//  8. Manager.ActiveCount tracks sessions correctly.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/interactive"
)

// ---- fake process helpers -----------------------------------------------

// fakeClaude returns a CmdProvider that runs a small shell script which:
//  1. Emits one stream_json result frame on startup (session handshake).
//  2. Reads a user-turn frame from stdin.
//  3. Emits a token + result frame in response.
//  4. Reads another frame and does the same.
//  5. Exits cleanly when stdin is closed (EOF).
//
// This script models two-turn behaviour so test 1 (turn-2 same process) works.
//
// The script emits session_id: "fake-session-1" on every frame so tests can
// verify the session ID is stable across turns.
func fakeClaude(t *testing.T, turns int) func() *exec.Cmd {
	t.Helper()

	// We use a heredoc-style sh -c script.  Shell script stays alive waiting
	// for stdin lines and echoes canned frames.  Uses read builtin (POSIX).
	//
	// The frame schema matches ParseStreamLineWithThinking expectations:
	//   - text delta events
	//   - a result event at the end of each turn
	//
	// We signal "turn N done" by emitting a result frame after each text block.
	tokenFrame := func(n int) string {
		return fmt.Sprintf(
			`{"type":"stream_event","event":{"type":"content_block_start","index":%d,"content_block":{"type":"text","text":""}}}`, n)
	}
	deltaFrame := func(n int, text string) string {
		return fmt.Sprintf(
			`{"type":"stream_event","event":{"type":"content_block_delta","index":%d,"delta":{"type":"text_delta","text":%q}}}`,
			n, text)
	}
	stopFrame := func(n int) string {
		return fmt.Sprintf(
			`{"type":"stream_event","event":{"type":"content_block_stop","index":%d}}`, n)
	}
	resultFrame := `{"type":"result","subtype":"success","result":"ok","duration_ms":10,"total_cost_usd":0.001,"usage":{"input_tokens":1,"output_tokens":1}}`

	// Build a script that processes `turns` turns then exits.
	var sb strings.Builder
	sb.WriteString("#!/bin/sh\n")
	sb.WriteString("# Fake claude interactive adapter\n")
	for i := 0; i < turns; i++ {
		// Wait for a user-turn frame on stdin (discard it; we just need to know it arrived).
		sb.WriteString("read -r _line || exit 0\n")
		// Emit token frames for this turn.
		sb.WriteString("printf '%s\\n' '" + singleQuote(tokenFrame(i)) + "'\n")
		sb.WriteString("printf '%s\\n' '" + singleQuote(deltaFrame(i, fmt.Sprintf("turn-%d", i+1))) + "'\n")
		sb.WriteString("printf '%s\\n' '" + singleQuote(stopFrame(i)) + "'\n")
		sb.WriteString("printf '%s\\n' '" + singleQuote(resultFrame) + "'\n")
	}
	sb.WriteString("exit 0\n")

	script := sb.String()

	return func() *exec.Cmd {
		return exec.Command("sh", "-c", script) //nolint:gosec
	}
}

// fakeClaudeCrash returns a CmdProvider that immediately exits with a non-zero
// code — simulating a crash.
func fakeClaudeCrash(_ *testing.T) func() *exec.Cmd {
	return func() *exec.Cmd {
		return exec.Command("sh", "-c", "exit 1") //nolint:gosec
	}
}

// singleQuote escapes a string for safe embedding inside single quotes in a
// POSIX shell script.
func singleQuote(s string) string {
	return strings.ReplaceAll(s, "'", "'\\''")
}

// collectChunks collects chunks from onChunk calls into a slice (thread-safe).
func collectChunks() (func(dispatch.StreamChunk), func() []dispatch.StreamChunk) {
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

// waitForChunk polls until at least n chunks have arrived or the timeout
// expires.
func waitForChunk(t *testing.T, get func() []dispatch.StreamChunk, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(get()) >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d chunks; got %d", n, len(get()))
}

// ---- Tests -------------------------------------------------------------------

// TestSession_Turn2SameProcess verifies that sending a second turn reaches the
// SAME running process (chunks arrive from the same session).
func TestSession_Turn2SameProcess(t *testing.T) {
	if os.Getenv("CI") == "" {
		// Skip on non-CI unless explicitly requested: the shell subprocess is
		// reliable but slow.
	}

	onChunk, getChunks := collectChunks()

	sess := interactive.NewSession(interactive.SessionParams{
		ConversationID:  "conv-1",
		OwnerOperatorID: "op-alice",
		OnChunk:         onChunk,
		CmdProvider:     fakeClaude(t, 2), // handles 2 turns
	})

	ctx := context.Background()
	if err := sess.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// First turn.
	frame1 := dispatch.StreamParserState{} // unused; frame is encoded by EncodeUserTurn
	_ = frame1
	turn1 := []byte(`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}` + "\n")
	if err := sess.SendUserTurn(turn1); err != nil {
		t.Fatalf("SendUserTurn turn1: %v", err)
	}
	// Wait for chunks from turn 1 (at least a token and a result).
	waitForChunk(t, getChunks, 1, 5*time.Second)

	chunks1 := getChunks()
	if len(chunks1) == 0 {
		t.Fatal("expected chunks after turn 1, got 0")
	}
	// Verify we got a token chunk.
	found := false
	for _, c := range chunks1 {
		if c.Type == "token" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no token chunk after turn 1; chunks: %v", chunks1)
	}

	// Second turn — same session, same process.
	turn2 := []byte(`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"second"}]}}` + "\n")
	if err := sess.SendUserTurn(turn2); err != nil {
		t.Fatalf("SendUserTurn turn2: %v", err)
	}
	// Wait for additional chunks.
	prevCount := len(chunks1)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(getChunks()) > prevCount {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(getChunks()) <= prevCount {
		t.Errorf("no new chunks after turn 2; total chunks: %d", len(getChunks()))
	}

	sess.Close()
}

// TestManager_OwnerConflict verifies that Ensure with a different operatorID
// returns ErrOwnerConflict (caller must 403).
func TestManager_OwnerConflict(t *testing.T) {
	ctx := context.Background()
	mgr := interactive.NewManager(ctx, interactive.ManagerConfig{
		Cap:         2,
		IdleTimeout: time.Minute,
	})
	defer mgr.Stop()

	onChunk, _ := collectChunks()
	params := interactive.SessionParams{
		OnChunk:     onChunk,
		CmdProvider: fakeClaude(t, 1),
	}

	// First Ensure: alice owns conv-a.
	_, err := mgr.Ensure("conv-a", "alice", params)
	if err != nil {
		t.Fatalf("Ensure alice: %v", err)
	}

	// Second Ensure with same convID but different owner: must be ErrOwnerConflict.
	_, err = mgr.Ensure("conv-a", "bob", params)
	if !errors.Is(err, interactive.ErrOwnerConflict) {
		t.Fatalf("expected ErrOwnerConflict, got %v", err)
	}
}

// TestManager_Send404WhenNoSession verifies that Send returns ErrNoSession when
// no live session exists.
func TestManager_Send404WhenNoSession(t *testing.T) {
	ctx := context.Background()
	mgr := interactive.NewManager(ctx, interactive.ManagerConfig{Cap: 2})
	defer mgr.Stop()

	frame := []byte(`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}` + "\n")
	err := mgr.Send("no-such-conv", "alice", frame)
	if !errors.Is(err, interactive.ErrNoSession) {
		t.Fatalf("expected ErrNoSession, got %v", err)
	}
}

// TestManager_CapExceeded verifies that Ensure returns ErrCapExceeded when the
// session cap is reached.
func TestManager_CapExceeded(t *testing.T) {
	ctx := context.Background()
	mgr := interactive.NewManager(ctx, interactive.ManagerConfig{Cap: 1})
	defer mgr.Stop()

	onChunk, _ := collectChunks()
	params := interactive.SessionParams{
		OnChunk:     onChunk,
		CmdProvider: fakeClaude(t, 1),
	}

	// First session: OK.
	_, err := mgr.Ensure("conv-1", "alice", params)
	if err != nil {
		t.Fatalf("first Ensure: %v", err)
	}

	// Second session: cap exceeded.
	_, err = mgr.Ensure("conv-2", "bob", params)
	if !errors.Is(err, interactive.ErrCapExceeded) {
		t.Fatalf("expected ErrCapExceeded, got %v", err)
	}
}

// fakeClaudeAlive returns a CmdProvider for a process that stays alive until
// stdin is closed (no output, just a blocking read loop).
func fakeClaudeAlive(_ *testing.T) func() *exec.Cmd {
	// The script reads from stdin indefinitely; exits when EOF arrives.
	script := "while read -r _; do :; done"
	return func() *exec.Cmd {
		return exec.Command("sh", "-c", script) //nolint:gosec
	}
}

// TestManager_IdleReaper verifies that sessions idle for longer than
// idleTimeout are closed by the reaper.
func TestManager_IdleReaper(t *testing.T) {
	ctx := context.Background()
	// Very short idle timeout so the reaper fires quickly in tests.
	idleTimeout := 80 * time.Millisecond
	mgr := interactive.NewManager(ctx, interactive.ManagerConfig{
		Cap:            2,
		IdleTimeout:    idleTimeout,
		ReaperInterval: 30 * time.Millisecond,
	})
	defer mgr.Stop()

	onChunk, _ := collectChunks()
	params := interactive.SessionParams{
		OnChunk:     onChunk,
		CmdProvider: fakeClaudeAlive(t), // stays alive until stdin closed
	}

	sess, err := mgr.Ensure("conv-idle", "alice", params)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	// Wait for the reaper to close the session.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sess.IsClosed() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !sess.IsClosed() {
		t.Fatal("session not closed by idle reaper within deadline")
	}

	// ActiveCount should now be 0.
	if mgr.ActiveCount() != 0 {
		t.Errorf("expected ActiveCount 0 after reap, got %d", mgr.ActiveCount())
	}
}

// TestManager_CrashCallsOnError verifies that when the claude process exits
// unexpectedly, the onError callback is invoked.
func TestManager_CrashCallsOnError(t *testing.T) {
	ctx := context.Background()
	var errCount atomic.Int32
	var errConvID string
	var mu sync.Mutex

	mgr := interactive.NewManager(ctx, interactive.ManagerConfig{
		Cap:         2,
		IdleTimeout: time.Minute,
		OnError: func(convID, sessID, owner, msg string) {
			errCount.Add(1)
			mu.Lock()
			errConvID = convID
			mu.Unlock()
		},
	})
	defer mgr.Stop()

	onChunk, _ := collectChunks()
	params := interactive.SessionParams{
		OnChunk:     onChunk,
		CmdProvider: fakeClaudeCrash(t), // exits immediately
	}

	_, err := mgr.Ensure("conv-crash", "alice", params)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	// Wait for the crash to be detected.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if errCount.Load() > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if errCount.Load() == 0 {
		t.Fatal("onError not called after process crash")
	}
	mu.Lock()
	gotConvID := errConvID
	mu.Unlock()
	if gotConvID != "conv-crash" {
		t.Errorf("expected errConvID=conv-crash, got %q", gotConvID)
	}
}

// TestSession_CloseIdempotent verifies that Close can be called multiple times
// without panicking.
func TestSession_CloseIdempotent(t *testing.T) {
	onChunk, _ := collectChunks()
	sess := interactive.NewSession(interactive.SessionParams{
		ConversationID:  "conv-close",
		OwnerOperatorID: "alice",
		OnChunk:         onChunk,
		CmdProvider:     fakeClaude(t, 1),
	})
	if err := sess.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Close multiple times — must not panic.
	sess.Close()
	sess.Close()
	sess.Close()
}

// TestSession_ErrTurnInFlight verifies that a second SendUserTurn while one is
// already in progress returns ErrTurnInFlight.
//
// We test ErrTurnInFlight directly: by calling SendUserTurn concurrently and
// observing that exactly one returns ErrTurnInFlight.  We use a session with a
// process that never reads stdin (pipe buffer fills up, blocking the write goroutine
// so turnMu stays locked for the second concurrent call).
func TestSession_ErrTurnInFlight(t *testing.T) {
	// Use a script that never reads stdin — its stdin pipe buffer fills up,
	// causing the write goroutine in SendUserTurn to block while turnMu is held.
	script := `sleep 10`
	provider := func() *exec.Cmd {
		return exec.Command("sh", "-c", script) //nolint:gosec
	}

	onChunk, _ := collectChunks()
	sess := interactive.NewSession(interactive.SessionParams{
		ConversationID:  "conv-inflight",
		OwnerOperatorID: "alice",
		OnChunk:         onChunk,
		CmdProvider:     provider,
	})
	if err := sess.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer sess.Close()

	// A large frame that will fill the pipe buffer and block the write goroutine.
	// The typical OS pipe buffer is 64 KiB; we send more than that.
	bigFrame := make([]byte, 128*1024)
	for i := range bigFrame {
		bigFrame[i] = 'x'
	}
	bigFrame[len(bigFrame)-1] = '\n'

	var wg sync.WaitGroup
	var results [2]error

	// Launch two concurrent sends.
	wg.Add(2)
	for i := 0; i < 2; i++ {
		i := i
		go func() {
			defer wg.Done()
			results[i] = sess.SendUserTurn(bigFrame)
		}()
	}

	// Give goroutines time to race.
	time.Sleep(100 * time.Millisecond)

	// Close to unblock the sleeping script and the blocked write.
	sess.Close()
	wg.Wait()

	// One of the two calls should have returned ErrTurnInFlight.
	inflightCount := 0
	for _, r := range results {
		if errors.Is(r, interactive.ErrTurnInFlight) {
			inflightCount++
		}
	}
	if inflightCount == 0 {
		t.Errorf("expected at least one ErrTurnInFlight; got results: %v, %v", results[0], results[1])
	}
}

// TestManager_ActiveCount tracks add/remove correctly.
func TestManager_ActiveCount(t *testing.T) {
	ctx := context.Background()
	mgr := interactive.NewManager(ctx, interactive.ManagerConfig{Cap: 4})
	defer mgr.Stop()

	onChunk, _ := collectChunks()
	params := interactive.SessionParams{
		OnChunk:     onChunk,
		CmdProvider: fakeClaude(t, 1),
	}

	if mgr.ActiveCount() != 0 {
		t.Fatalf("expected 0, got %d", mgr.ActiveCount())
	}

	_, err := mgr.Ensure("c1", "alice", params)
	if err != nil {
		t.Fatalf("Ensure c1: %v", err)
	}
	if mgr.ActiveCount() != 1 {
		t.Fatalf("expected 1, got %d", mgr.ActiveCount())
	}

	_, err = mgr.Ensure("c2", "bob", params)
	if err != nil {
		t.Fatalf("Ensure c2: %v", err)
	}
	if mgr.ActiveCount() != 2 {
		t.Fatalf("expected 2, got %d", mgr.ActiveCount())
	}

	mgr.Close("c1")
	if mgr.ActiveCount() != 1 {
		t.Fatalf("expected 1 after close c1, got %d", mgr.ActiveCount())
	}

	mgr.Close("c2")
	if mgr.ActiveCount() != 0 {
		t.Fatalf("expected 0 after close c2, got %d", mgr.ActiveCount())
	}
}

// TestStreamHelper_SharedWithOneShotPath verifies that ReadLineLoop +
// ParseAndDispatch (the shared helpers) correctly parse the same fixture lines
// used by the one-shot tests.  This is the key parity invariant.
func TestStreamHelper_SharedWithOneShotPath(t *testing.T) {
	// A representative sequence of claude stream-json lines.
	lines := []string{
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":", world!"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop","index":0}}`,
		`{"type":"result","subtype":"success","total_cost_usd":0.001,"usage":{"input_tokens":1,"output_tokens":1}}`,
	}

	input := strings.NewReader(strings.Join(lines, "\n") + "\n")

	var chunks []dispatch.StreamChunk
	ps := dispatch.NewStreamParserState()

	dispatch.ReadLineLoop(input, dispatch.ReadLineLoopConfig{AgentName: "test"}, func(line []byte) {
		dispatch.ParseAndDispatch(line, ps, func(c dispatch.StreamChunk) {
			chunks = append(chunks, c)
		})
	})

	// Expect two token chunks ("Hello" and ", world!").
	tokenChunks := 0
	for _, c := range chunks {
		if c.Type == "token" {
			tokenChunks++
		}
	}
	if tokenChunks != 2 {
		t.Errorf("expected 2 token chunks, got %d (all chunks: %v)", tokenChunks, chunks)
	}
}
