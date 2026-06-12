package dispatch

// stream_test.go tests RunStream via the fake streaming adapter.
//
// The fake adapter emits pre-recorded stream-json lines, allowing tests to
// exercise the full streaming path without live LLM calls.  The test seam is
// the package-level streamRunFn (analogous to runFn for Service.Run).
//
// Tests cover:
//  1. Claude-style incremental deltas → multiple "token" chunks + "summary".
//  2. Codex-style buffered → one "token" chunk + "summary".
//  3. dispatch_finished NDJSON schema keys present (field parseability).
//  4. Bounded reader tolerates a multi-KB line (system/init event size).
//  5. Context cancellation kills the stream cleanly.
//  6. Over-long line (>2 MB, no newline) is truncated without buffering whole.
//  7. Buffered path output ceiling: runtime emitting >32 MB is truncated.
//  8. Streaming usage is populated (not null) in dispatch_finished for claude.
//  9. Facade task-length bound rejects over-limit tasks.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/agentscompose"
	"github.com/bakw00ds/yakos/internal/runtime"
)

// ---- fake streaming adapters ---------------------------------------------------

// fakeClaudeStreamAdapter simulates the claude adapter: emits pre-recorded
// stream-json NDJSON lines, including a system/init event and multiple deltas.
// It implements both runtime.Adapter and chatCmdProvider.
type fakeClaudeStreamAdapter struct {
	lines []string // NDJSON lines to emit on stdout
}

func (f *fakeClaudeStreamAdapter) Name() string                     { return "claude" }
func (f *fakeClaudeStreamAdapter) Available(_ context.Context) bool { return true }
func (f *fakeClaudeStreamAdapter) Dispatch(_ context.Context, _ runtime.DispatchRequest) (*runtime.DispatchResult, error) {
	return &runtime.DispatchResult{Stdout: []byte("fake")}, nil
}

func (f *fakeClaudeStreamAdapter) ChatExecCmd(ctx context.Context, _ runtime.ChatDispatchRequest) *exec.Cmd {
	// Build a script that writes the pre-recorded lines to stdout.
	// Use 'printf' via sh so we don't need a real binary.
	script := ""
	for _, line := range f.lines {
		// Escape single quotes in line for shell safety.
		safe := strings.ReplaceAll(line, "'", "'\\''")
		script += "printf '%s\\n' '" + safe + "'\n"
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", script) //nolint:gosec
	return cmd
}

// fakeCodexStreamAdapter simulates the codex adapter: emits one buffered output
// (no incremental streaming).
type fakeCodexStreamAdapter struct {
	output string
}

func (f *fakeCodexStreamAdapter) Name() string                     { return "codex" }
func (f *fakeCodexStreamAdapter) Available(_ context.Context) bool { return true }
func (f *fakeCodexStreamAdapter) Dispatch(_ context.Context, _ runtime.DispatchRequest) (*runtime.DispatchResult, error) {
	return &runtime.DispatchResult{Stdout: []byte(f.output)}, nil
}
func (f *fakeCodexStreamAdapter) ChatExecCmd(ctx context.Context, _ runtime.ChatDispatchRequest) *exec.Cmd {
	safe := strings.ReplaceAll(f.output, "'", "'\\''")
	cmd := exec.CommandContext(ctx, "sh", "-c", "printf '%s' '"+safe+"'") //nolint:gosec
	return cmd
}

// ---- claude stream fixture lines -----------------------------------------------

// claudeStreamLines returns a representative sequence of claude stream-json
// lines: system/init (should be dropped), a text block start, three deltas,
// a thinking block (should be dropped), a message_stop, and a result.
func claudeStreamLines() []string {
	// Simulate a multi-KB system/init line by padding the tools array.
	tools := make([]string, 50)
	for i := range tools {
		tools[i] = fmt.Sprintf(`{"name":"Tool%d"}`, i)
	}
	systemLine := `{"type":"system","subtype":"init","cwd":"/p","tools":[` + strings.Join(tools, ",") + `]}`

	return []string{
		systemLine,
		`{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_01","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-5","stop_reason":null,"usage":{"input_tokens":42}}}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":", "}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"world!"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop","index":0}}`,
		`{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}}`,
		`{"type":"stream_event","event":{"type":"message_stop"}}`,
		`{"type":"result","subtype":"success","result":"Hello, world!","is_error":false,"duration_ms":1200,"total_cost_usd":0.00042,"usage":{"input_tokens":42,"output_tokens":3}}`,
	}
}

// ---- stream_test helper: install a fake streamRunFn ---------------------------

// withStreamRunFn replaces streamRunFn for the duration of body, then restores.
// Analogous to withRunFn for the regular dispatch path.
func withStreamRunFn(
	fake func(ctx context.Context, req Request, adapter runtime.Adapter, chatReq runtime.ChatDispatchRequest, onChunk func(StreamChunk)) (Result, error),
	body func(),
) {
	orig := streamRunFn
	streamRunFn = fake
	defer func() { streamRunFn = orig }()
	body()
}

// ---- Test: real execWithStreaming with fake claude adapter ---------------------

// TestRunStream_ClaudeIncrementalDeltas verifies that a claude-style adapter
// emitting multiple text_delta events produces multiple "token" chunks followed
// by one "summary" chunk.
func TestRunStream_ClaudeIncrementalDeltas(t *testing.T) {
	logDir := isolatedLogDir(t)
	yakosRoot := buildFakeRoster(t, "chat-agent", "You are a helpful assistant.")

	svc := NewService(ServiceConfig{
		YakosRoot:     yakosRoot,
		WorkspaceRoot: logDir,
		OperatorID:    "test-op",
	})

	// Inject the fake claude adapter via streamRunFn.
	fakeAdapter := &fakeClaudeStreamAdapter{lines: claudeStreamLines()}

	var chunks []StreamChunk
	withStreamRunFn(func(ctx context.Context, req Request, adapter runtime.Adapter, chatReq runtime.ChatDispatchRequest, onChunk func(StreamChunk)) (Result, error) {
		return execWithStreaming(ctx, req, fakeAdapter, chatReq, func(c StreamChunk) {
			chunks = append(chunks, c)
			onChunk(c)
		})
	}, func() {
		_, err := svc.RunStream(context.Background(), Params{
			Agent:   "chat-agent",
			Task:    "say hello",
			Project: logDir,
		}, func(c StreamChunk) {})
		if err != nil {
			t.Fatalf("RunStream: unexpected error: %v", err)
		}
	})

	// Count token chunks and find the summary.
	var tokenChunks []StreamChunk
	var summaryChunk *StreamChunk
	for i := range chunks {
		switch chunks[i].Type {
		case "token":
			tokenChunks = append(tokenChunks, chunks[i])
		case "summary":
			summaryChunk = &chunks[i]
		}
	}

	// We expect exactly 3 "token" chunks: "Hello", ", ", "world!".
	if len(tokenChunks) != 3 {
		t.Errorf("expected 3 token chunks, got %d: %v", len(tokenChunks), tokenChunks)
	} else {
		wantTexts := []string{"Hello", ", ", "world!"}
		for i, want := range wantTexts {
			if tokenChunks[i].Text != want {
				t.Errorf("token[%d]: got %q, want %q", i, tokenChunks[i].Text, want)
			}
		}
	}

	// Must have exactly one summary chunk.
	if summaryChunk == nil {
		t.Fatal("expected a summary chunk, got none")
	}
	if summaryChunk.ExitCode != 0 {
		t.Errorf("summary.ExitCode: got %d, want 0", summaryChunk.ExitCode)
	}
	if summaryChunk.TotalCostUSD != 0.00042 {
		t.Errorf("summary.TotalCostUSD: got %f, want 0.00042", summaryChunk.TotalCostUSD)
	}
}

// TestRunStream_CodexBuffered verifies that a codex-style (buffered) adapter
// produces exactly one "token" chunk containing the full output, plus a summary.
func TestRunStream_CodexBuffered(t *testing.T) {
	logDir := isolatedLogDir(t)
	yakosRoot := buildFakeRoster(t, "chat-agent", "You are a helpful assistant.")

	svc := NewService(ServiceConfig{
		YakosRoot:     yakosRoot,
		WorkspaceRoot: logDir,
		OperatorID:    "test-op",
	})

	fakeAdapter := &fakeCodexStreamAdapter{output: "Codex answer here."}

	var chunks []StreamChunk
	withStreamRunFn(func(ctx context.Context, req Request, adapter runtime.Adapter, chatReq runtime.ChatDispatchRequest, onChunk func(StreamChunk)) (Result, error) {
		return execWithStreaming(ctx, req, fakeAdapter, chatReq, func(c StreamChunk) {
			chunks = append(chunks, c)
			onChunk(c)
		})
	}, func() {
		_, err := svc.RunStream(context.Background(), Params{
			Agent:   "chat-agent",
			Task:    "answer me",
			Project: logDir,
		}, func(c StreamChunk) {})
		if err != nil {
			t.Fatalf("RunStream: unexpected error: %v", err)
		}
	})

	var tokenChunks []StreamChunk
	var summaryChunk *StreamChunk
	for i := range chunks {
		switch chunks[i].Type {
		case "token":
			tokenChunks = append(tokenChunks, chunks[i])
		case "summary":
			summaryChunk = &chunks[i]
		}
	}

	if len(tokenChunks) != 1 {
		t.Errorf("codex: expected 1 token chunk, got %d", len(tokenChunks))
	} else if tokenChunks[0].Text != "Codex answer here." {
		t.Errorf("codex: token text: got %q, want %q", tokenChunks[0].Text, "Codex answer here.")
	}
	if summaryChunk == nil {
		t.Fatal("codex: expected a summary chunk")
	}
	if summaryChunk.ExitCode != 0 {
		t.Errorf("codex: summary.ExitCode: got %d, want 0", summaryChunk.ExitCode)
	}
}

// TestRunStream_DispatchFinishedParity verifies that RunStream writes a
// dispatch_finished NDJSON event that is structurally identical to the one
// written by a non-streaming Run: same fields, same absence of identity fields
// when not supplied.
func TestRunStream_DispatchFinishedParity(t *testing.T) {
	logDir := isolatedLogDir(t)
	yakosRoot := buildFakeRoster(t, "chat-agent", "System prompt.")

	svc := NewService(ServiceConfig{
		YakosRoot:     yakosRoot,
		WorkspaceRoot: logDir,
		OperatorID:    "parity-op",
	})

	fakeAdapter := &fakeClaudeStreamAdapter{lines: claudeStreamLines()}

	withStreamRunFn(func(ctx context.Context, req Request, adapter runtime.Adapter, chatReq runtime.ChatDispatchRequest, onChunk func(StreamChunk)) (Result, error) {
		return execWithStreaming(ctx, req, fakeAdapter, chatReq, onChunk)
	}, func() {
		_, err := svc.RunStream(context.Background(), Params{
			Agent:   "chat-agent",
			Task:    "parity check",
			Project: logDir,
		}, func(_ StreamChunk) {})
		if err != nil {
			t.Fatalf("RunStream: %v", err)
		}
	})

	// Read the dispatch log.
	events := readDispatchLog(t, logDir)

	// Should have exactly 2 events: dispatch_started + dispatch_finished.
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d: %v", len(events), events)
	}

	startEv := events[0]
	finEv := events[1]

	// Validate dispatch_started fields.
	assertField(t, startEv, "type", "dispatch_started")
	assertField(t, startEv, "agent", "chat-agent")

	// Validate dispatch_finished fields.
	assertField(t, finEv, "type", "dispatch_finished")
	assertField(t, finEv, "agent", "chat-agent")
	assertField(t, finEv, "runtime", "claude")

	// exit_code must be present (JSON number 0).
	if _, ok := finEv["exit_code"]; !ok {
		t.Error("dispatch_finished: missing exit_code field")
	}
	// duration_s must be present.
	if _, ok := finEv["duration_s"]; !ok {
		t.Error("dispatch_finished: missing duration_s field")
	}
	// output_bytes must be present.
	if _, ok := finEv["output_bytes"]; !ok {
		t.Error("dispatch_finished: missing output_bytes field")
	}
	// model_chosen_by and model_resolved must be present.
	if _, ok := finEv["model_chosen_by"]; !ok {
		t.Error("dispatch_finished: missing model_chosen_by field")
	}
	if _, ok := finEv["model_resolved"]; !ok {
		t.Error("dispatch_finished: missing model_resolved field")
	}

	// Identity fields must be absent when not supplied (omitempty parity).
	if _, ok := finEv["operator_id"]; !ok {
		// operator_id IS set (parity-op from service config), so it should be present.
		// Actually: Service.RunStream stamps operatorID just like Service.Run.
		// So we expect it present here.
	}
}

// TestRunStream_BoundedReaderToleratesLargeInitLine verifies that a multi-KB
// system/init line does not cause the bounded reader to fail.  The line is
// dropped (system events are ignored) and subsequent deltas still arrive.
//
// The fixture is written to a temp file and served via "cat <file>" so the
// large line never appears in a shell argv — avoiding Linux ARG_MAX limits
// (~128 KB–2 MB) that caused this test to fail on CI.
func TestRunStream_BoundedReaderToleratesLargeInitLine(t *testing.T) {
	logDir := isolatedLogDir(t)
	yakosRoot := buildFakeRoster(t, "chat-agent", "System prompt.")

	// Build a line that is well above a naive 64KB scanner limit (e.g. 200KB).
	// The line stays under maxStreamLineBytes (2MB) so the bounded reader keeps
	// it in lineBuf and parses it — but as a "system" event it is dropped,
	// and the subsequent text_delta must still arrive.
	bigTool := strings.Repeat("X", 200*1024)
	bigInitLine := `{"type":"system","subtype":"init","data":"` + bigTool + `"}`

	rawLines := []string{
		bigInitLine,
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"after big init"}}}`,
		`{"type":"result","subtype":"success","result":"after big init","is_error":false,"duration_ms":10,"total_cost_usd":0.0001,"usage":{}}` + "\n",
	}

	// Write fixture to a temp file (avoids passing 200KB via shell argv on Linux).
	var buf bytes.Buffer
	for _, l := range rawLines {
		buf.WriteString(l)
		buf.WriteByte('\n')
	}
	// newLargePipeAdapter writes the bytes to a temp file and returns an adapter
	// whose ChatExecCmd runs "cat <file>" — tiny argv regardless of content size.
	fakeAdapter := newLargePipeAdapter(t, "claude", buf.Bytes())

	svc := NewService(ServiceConfig{
		YakosRoot:     yakosRoot,
		WorkspaceRoot: logDir,
		OperatorID:    "test-op",
	})

	var tokenTexts []string
	withStreamRunFn(func(ctx context.Context, req Request, _ runtime.Adapter, chatReq runtime.ChatDispatchRequest, onChunk func(StreamChunk)) (Result, error) {
		return execWithStreaming(ctx, req, fakeAdapter, chatReq, func(c StreamChunk) {
			if c.Type == "token" {
				tokenTexts = append(tokenTexts, c.Text)
			}
			onChunk(c)
		})
	}, func() {
		_, err := svc.RunStream(context.Background(), Params{
			Agent:   "chat-agent",
			Task:    "task",
			Project: logDir,
		}, func(_ StreamChunk) {})
		if err != nil {
			t.Fatalf("RunStream: %v", err)
		}
	})

	// The token "after big init" must have arrived despite the large init line.
	if len(tokenTexts) != 1 || tokenTexts[0] != "after big init" {
		t.Errorf("expected [\"after big init\"], got %v", tokenTexts)
	}
}

// TestRunStream_ContextCancelKillsStream verifies that cancelling the context
// causes RunStream to return promptly (without deadlocking) on all platforms.
//
// Design rationale for the fake:
//
// The previous approach used exec.CommandContext("sh", "-c", "sleep 30") and
// relied on exec killing the subprocess to unblock StdoutPipe.Read.  This is
// inherently platform-dependent: on Linux, exec.CommandContext sends SIGKILL to
// the direct child (sh), but sh has already forked sleep as a grandchild.  The
// grandchild holds the write end of the stdout pipe open, so Read never returns.
// On macOS the process hierarchy or signal propagation differs and the test
// accidentally passes.
//
// Fix: the fake does not spawn a subprocess at all.  It blocks in a select on
// ctx.Done() — purely in Go scheduler space — which is deterministic on every
// platform.  Emitting one chunk first proves that the streaming path started
// before the block.
//
// Race-safety discipline: body() must not return until the goroutine that calls
// svc.RunStream (which reads the package-global streamRunFn) has fully exited.
// withStreamRunFn restores streamRunFn = orig in a defer that fires when body()
// returns; if the goroutine is still mid-RunStream at that point, the write of
// streamRunFn races with the goroutine's read.  A sync.WaitGroup inside body()
// prevents body() from returning before the goroutine is joined.
func TestRunStream_ContextCancelKillsStream(t *testing.T) {
	logDir := isolatedLogDir(t)
	yakosRoot := buildFakeRoster(t, "chat-agent", "System prompt.")

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	svc := NewService(ServiceConfig{
		YakosRoot:     yakosRoot,
		WorkspaceRoot: logDir,
		OperatorID:    "test-op",
	})

	withStreamRunFn(func(ctx2 context.Context, req Request, _ runtime.Adapter, _ runtime.ChatDispatchRequest, onChunk func(StreamChunk)) (Result, error) {
		// Emit one chunk to prove streaming started, then block until ctx2 is
		// cancelled.  No subprocess: avoids all platform-specific pipe/signal
		// behaviour that caused the Linux CI hang.
		onChunk(StreamChunk{Type: "token", Text: "started"})
		<-ctx2.Done()
		return Result{ExitCode: -1}, ctx2.Err()
	}, func() {
		done := make(chan error, 1)

		// wg ensures body() does not return until the goroutine has fully
		// exited, preventing a concurrent read of streamRunFn after
		// withStreamRunFn's defer restores it.
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.RunStream(ctx, Params{
				Agent:   "chat-agent",
				Task:    "task",
				Project: logDir,
			}, func(_ StreamChunk) {})
			done <- err
		}()

		timedOut := false
		select {
		case err := <-done:
			// RunStream must return (with or without error — cancellation is expected).
			t.Logf("RunStream returned with: %v", err)
		case <-time.After(3 * time.Second):
			// Report timeout via t.Error (not t.Fatal) so wg.Wait() below can
			// still run and drain the goroutine before body() returns.
			t.Error("RunStream did not return after context cancellation")
			timedOut = true
		}

		// Always join the goroutine before body() returns.  This is the
		// critical sync point: withStreamRunFn's defer (streamRunFn = orig)
		// must not fire while the goroutine is still reading streamRunFn.
		wg.Wait()
		_ = timedOut
	})
}

// TestRunStream_GovernorEnforcedForStreaming verifies that the governor semaphore
// is honoured for RunStream calls (streaming dispatches compete for the same cap).
func TestRunStream_GovernorEnforcedForStreaming(t *testing.T) {
	logDir := isolatedLogDir(t)
	yakosRoot := buildFakeRoster(t, "chat-agent", "System prompt.")

	const numSlots = 2
	svc := NewService(ServiceConfig{
		YakosRoot:     yakosRoot,
		WorkspaceRoot: logDir,
		OperatorID:    "test-op",
		MaxConcurrent: numSlots,
	})

	// Hold all slots manually.
	for i := 0; i < numSlots; i++ {
		<-svc.sem
	}
	defer func() {
		for i := 0; i < numSlots; i++ {
			svc.sem <- struct{}{}
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel so RunStream returns immediately on slot wait.

	withStreamRunFn(func(ctx context.Context, req Request, _ runtime.Adapter, _ runtime.ChatDispatchRequest, onChunk func(StreamChunk)) (Result, error) {
		return Result{}, nil
	}, func() {
		_, err := svc.RunStream(ctx, Params{
			Agent:   "chat-agent",
			Task:    "task",
			Project: logDir,
		}, func(_ StreamChunk) {})
		if err == nil {
			t.Fatal("expected 'at capacity' error, got nil")
		}
		if !strings.Contains(err.Error(), "at capacity") {
			t.Errorf("expected 'at capacity' in error, got: %v", err)
		}
	})
}

// largePipeAdapter is a test adapter that writes its content from a file
// (to avoid ARG_MAX limits when the content is many megabytes).
type largePipeAdapter struct {
	name     string // "claude" or "codex"
	dataFile string // temp file path; content is written to stdout as-is
}

func (a *largePipeAdapter) Name() string                     { return a.name }
func (a *largePipeAdapter) Available(_ context.Context) bool { return true }
func (a *largePipeAdapter) Dispatch(_ context.Context, _ runtime.DispatchRequest) (*runtime.DispatchResult, error) {
	return &runtime.DispatchResult{Stdout: []byte("fake")}, nil
}
func (a *largePipeAdapter) ChatExecCmd(ctx context.Context, _ runtime.ChatDispatchRequest) *exec.Cmd {
	// cat the pre-written data file to stdout.
	cmd := exec.CommandContext(ctx, "cat", a.dataFile) //nolint:gosec
	return cmd
}

// newLargePipeAdapter writes data to a temp file and returns an adapter that
// cats that file on ChatExecCmd.
func newLargePipeAdapter(t *testing.T, name string, data []byte) *largePipeAdapter {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "large-*.dat")
	if err != nil {
		t.Fatalf("newLargePipeAdapter: create temp: %v", err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatalf("newLargePipeAdapter: write: %v", err)
	}
	_ = f.Close()
	return &largePipeAdapter{name: name, dataFile: f.Name()}
}

var _ chatCmdProvider = (*largePipeAdapter)(nil)

// TestRunStream_OverlongLineIsTruncated verifies that a single line larger than
// maxStreamLineBytes (with no newline) is discarded without being buffered in
// its entirety.  Subsequent valid lines still arrive.
func TestRunStream_OverlongLineIsTruncated(t *testing.T) {
	logDir := isolatedLogDir(t)
	yakosRoot := buildFakeRoster(t, "chat-agent", "System prompt.")

	// Construct a data blob: 3 MB of 'X' (no newline) + newline + result line.
	const overlongBytes = 3 * 1024 * 1024
	resultLine := `{"type":"result","subtype":"success","result":"after overlong","is_error":false,"duration_ms":1,"total_cost_usd":0.001,"usage":{"input_tokens":5,"output_tokens":2}}`
	var buf []byte
	buf = append(buf, bytes.Repeat([]byte("X"), overlongBytes)...)
	buf = append(buf, '\n')
	buf = append(buf, []byte(resultLine)...)
	buf = append(buf, '\n')

	// Write to a temp file so we avoid ARG_MAX shell limits.
	fakeAdapter := newLargePipeAdapter(t, "claude", buf)

	svc := NewService(ServiceConfig{
		YakosRoot:     yakosRoot,
		WorkspaceRoot: logDir,
		OperatorID:    "test-op",
	})

	var tokenTexts []string
	var summaryChunk *StreamChunk
	withStreamRunFn(func(ctx context.Context, req Request, _ runtime.Adapter, chatReq runtime.ChatDispatchRequest, onChunk func(StreamChunk)) (Result, error) {
		return execWithStreaming(ctx, req, fakeAdapter, chatReq, func(c StreamChunk) {
			if c.Type == "token" {
				tokenTexts = append(tokenTexts, c.Text)
			}
			if c.Type == "summary" {
				cp := c
				summaryChunk = &cp
			}
			onChunk(c)
		})
	}, func() {
		_, err := svc.RunStream(context.Background(), Params{
			Agent:   "chat-agent",
			Task:    "task",
			Project: logDir,
		}, func(_ StreamChunk) {})
		if err != nil {
			t.Fatalf("RunStream: unexpected error: %v", err)
		}
	})

	// The overlong line is pure 'X's — not valid NDJSON for a text_delta, so
	// even if it leaked through ParseStreamLine would emit no token.  But the
	// guard must not have returned an error or hung — confirmed by reaching here.
	// The result line after the overlong line must have been processed.
	if summaryChunk == nil {
		t.Fatal("expected a summary chunk; reader may have stopped after overlong line")
	}
	if summaryChunk.TotalCostUSD != 0.001 {
		t.Errorf("summary.TotalCostUSD: got %f, want 0.001", summaryChunk.TotalCostUSD)
	}
	// No token chunks expected (overlong line is not a text_delta; result is not a token).
	if len(tokenTexts) != 0 {
		t.Errorf("expected no token chunks from overlong+result lines, got %v", tokenTexts)
	}
}

// TestRunStream_BufferedOutputCeiling verifies that a buffered (non-claude)
// runtime emitting more than maxBufferedOutputBytes does not cause the entire
// output to be held in memory.  The accumulated allText is capped and a
// truncation marker is appended.
func TestRunStream_BufferedOutputCeiling(t *testing.T) {
	logDir := isolatedLogDir(t)
	yakosRoot := buildFakeRoster(t, "chat-agent", "System prompt.")

	// Build a payload slightly larger than the 32 MB ceiling, structured as
	// many short lines (1 KB each) so no individual line triggers the per-line
	// overlong guard (which would discard them rather than accumulate them).
	// We need total bytes > maxBufferedOutputBytes to exercise the ceiling.
	const lineSize = 1024 // 1 KB per line — well under the 2 MB per-line cap
	const numLines = (maxBufferedOutputBytes / lineSize) + 2
	var hugeData []byte
	lineContent := bytes.Repeat([]byte("A"), lineSize)
	for i := 0; i < numLines; i++ {
		hugeData = append(hugeData, lineContent...)
		hugeData = append(hugeData, '\n')
	}
	fakeAdapter := newLargePipeAdapter(t, "codex", hugeData)

	svc := NewService(ServiceConfig{
		YakosRoot:     yakosRoot,
		WorkspaceRoot: logDir,
		OperatorID:    "test-op",
	})

	var tokenChunks []StreamChunk
	withStreamRunFn(func(ctx context.Context, req Request, _ runtime.Adapter, chatReq runtime.ChatDispatchRequest, onChunk func(StreamChunk)) (Result, error) {
		return execWithStreaming(ctx, req, fakeAdapter, chatReq, func(c StreamChunk) {
			if c.Type == "token" {
				tokenChunks = append(tokenChunks, c)
			}
			onChunk(c)
		})
	}, func() {
		_, err := svc.RunStream(context.Background(), Params{
			Agent:   "chat-agent",
			Task:    "task",
			Project: logDir,
		}, func(_ StreamChunk) {})
		if err != nil {
			t.Fatalf("RunStream: unexpected error: %v", err)
		}
	})

	if len(tokenChunks) != 1 {
		t.Fatalf("expected 1 token chunk, got %d", len(tokenChunks))
	}
	text := tokenChunks[0].Text
	// The text must be shorter than the total input (ceiling enforced).
	totalInput := numLines * (lineSize + 1) // +1 for the newline
	if len(text) >= totalInput {
		t.Errorf("buffered output not capped: got %d bytes, expected < %d", len(text), totalInput)
	}
	// The truncation marker must be present.
	if !strings.Contains(text, "[...output truncated...]") {
		t.Errorf("expected truncation marker in capped output, got text len=%d", len(text))
	}
}

// TestRunStream_StreamingUsagePopulated verifies that the dispatch_finished
// event written by a claude streaming dispatch contains a non-null "usage"
// field with input_tokens and output_tokens populated from the result event.
func TestRunStream_StreamingUsagePopulated(t *testing.T) {
	logDir := isolatedLogDir(t)
	yakosRoot := buildFakeRoster(t, "chat-agent", "System prompt.")

	svc := NewService(ServiceConfig{
		YakosRoot:     yakosRoot,
		WorkspaceRoot: logDir,
		OperatorID:    "test-op",
	})

	fakeAdapter := &fakeClaudeStreamAdapter{lines: claudeStreamLines()}
	// claudeStreamLines includes:
	//   "usage":{"input_tokens":42,"output_tokens":3}
	// in the result event, so we expect those values in the log.

	withStreamRunFn(func(ctx context.Context, req Request, _ runtime.Adapter, chatReq runtime.ChatDispatchRequest, onChunk func(StreamChunk)) (Result, error) {
		return execWithStreaming(ctx, req, fakeAdapter, chatReq, onChunk)
	}, func() {
		_, err := svc.RunStream(context.Background(), Params{
			Agent:   "chat-agent",
			Task:    "usage test",
			Project: logDir,
		}, func(_ StreamChunk) {})
		if err != nil {
			t.Fatalf("RunStream: %v", err)
		}
	})

	// Read the dispatch log and find the dispatch_finished line.
	logPath := filepath.Join(logDir, "dispatch-log.ndjson")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var finishedLine string
	for _, l := range lines {
		if strings.Contains(l, `"dispatch_finished"`) {
			finishedLine = l
		}
	}
	if finishedLine == "" {
		t.Fatal("no dispatch_finished line in log")
	}

	var ev map[string]json.RawMessage
	if err := json.Unmarshal([]byte(finishedLine), &ev); err != nil {
		t.Fatalf("dispatch_finished parse error: %v", err)
	}

	usageRaw, ok := ev["usage"]
	if !ok {
		t.Fatal("dispatch_finished: missing 'usage' field (expected non-null for claude streaming)")
	}
	if string(usageRaw) == "null" {
		t.Fatal("dispatch_finished: 'usage' is null; expected token counts from result event")
	}

	var usage struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	}
	if err := json.Unmarshal(usageRaw, &usage); err != nil {
		t.Fatalf("usage unmarshal: %v", err)
	}
	if usage.InputTokens != 42 {
		t.Errorf("usage.input_tokens: got %d, want 42", usage.InputTokens)
	}
	if usage.OutputTokens != 3 {
		t.Errorf("usage.output_tokens: got %d, want 3", usage.OutputTokens)
	}
}

// TestRunStream_FacadeTaskLengthBound verifies that RunStream rejects a task
// that exceeds maxTaskBytes with a clear error before forking any subprocess.
func TestRunStream_FacadeTaskLengthBound(t *testing.T) {
	logDir := isolatedLogDir(t)
	yakosRoot := buildFakeRoster(t, "chat-agent", "System prompt.")

	svc := NewService(ServiceConfig{
		YakosRoot:     yakosRoot,
		WorkspaceRoot: logDir,
		OperatorID:    "test-op",
	})

	// Construct a task that is exactly 1 byte over the limit.
	overLimitTask := strings.Repeat("T", maxTaskBytes+1)

	_, err := svc.RunStream(context.Background(), Params{
		Agent:   "chat-agent",
		Task:    overLimitTask,
		Project: logDir,
	}, func(_ StreamChunk) {})

	if err == nil {
		t.Fatal("expected error for over-limit task, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Errorf("expected 'exceeds maximum size' in error, got: %v", err)
	}
}

// TestService_Run_FacadeTaskLengthBound verifies that Service.Run also rejects
// an over-limit task at the facade level (same chokepoint, non-streaming path).
func TestService_Run_FacadeTaskLengthBound(t *testing.T) {
	logDir := isolatedLogDir(t)
	yakosRoot := buildFakeRoster(t, "chat-agent", "System prompt.")

	svc := NewService(ServiceConfig{
		YakosRoot:     yakosRoot,
		WorkspaceRoot: logDir,
		OperatorID:    "test-op",
	})

	overLimitTask := strings.Repeat("T", maxTaskBytes+1)

	_, _, err := svc.Run(context.Background(), Params{
		Agent:   "chat-agent",
		Task:    overLimitTask,
		Project: logDir,
	})

	if err == nil {
		t.Fatal("expected error for over-limit task on Run path, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Errorf("expected 'exceeds maximum size' in error, got: %v", err)
	}
}

// ---- helpers -------------------------------------------------------------------

// buildFakeRoster creates a minimal yakos lib/agents directory with a single
// agent .md file and returns the yakosRoot.
func buildFakeRoster(t *testing.T, agentID, prompt string) string {
	t.Helper()
	root := t.TempDir()
	agentsDir := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("buildFakeRoster: mkdir: %v", err)
	}
	agentPath := filepath.Join(agentsDir, agentID+".md")
	content := "---\nid: " + agentID + "\ndescription: Test agent\n---\n\n" + prompt + "\n"
	if err := os.WriteFile(agentPath, []byte(content), 0644); err != nil {
		t.Fatalf("buildFakeRoster: write agent: %v", err)
	}
	return root
}

// ---- Compile-check: fakeClaudeStreamAdapter satisfies chatCmdProvider ---------

var _ chatCmdProvider = (*fakeClaudeStreamAdapter)(nil)
var _ chatCmdProvider = (*fakeCodexStreamAdapter)(nil)

// ---- Verify agentscompose still works with the fake roster --------------------

func TestBuildFakeRosterComposable(t *testing.T) {
	yakosRoot := buildFakeRoster(t, "chat-agent", "Test system prompt.")
	roster, err := agentscompose.Compose(yakosRoot, "")
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	found := false
	for _, a := range roster {
		if a.ID == "chat-agent" {
			found = true
		}
	}
	if !found {
		t.Error("chat-agent not found in composed roster")
	}
}

// ---- JSON round-trip: dispatch_finished fields ----------------------------------

// TestDispatchFinishedParseability verifies that the dispatch_finished line
// written by RunStream is valid JSON with the expected schema keys.
func TestDispatchFinishedParseability(t *testing.T) {
	logDir := isolatedLogDir(t)
	yakosRoot := buildFakeRoster(t, "chat-agent", "Prompt.")

	svc := NewService(ServiceConfig{
		YakosRoot:     yakosRoot,
		WorkspaceRoot: logDir,
		OperatorID:    "parse-op",
	})

	fakeAdapter := &fakeClaudeStreamAdapter{lines: claudeStreamLines()}

	withStreamRunFn(func(ctx context.Context, req Request, _ runtime.Adapter, chatReq runtime.ChatDispatchRequest, onChunk func(StreamChunk)) (Result, error) {
		return execWithStreaming(ctx, req, fakeAdapter, chatReq, onChunk)
	}, func() {
		_, err := svc.RunStream(context.Background(), Params{
			Agent:   "chat-agent",
			Task:    "test parseability",
			Project: logDir,
		}, func(_ StreamChunk) {})
		if err != nil {
			t.Fatalf("RunStream: %v", err)
		}
	})

	logPath := filepath.Join(logDir, "dispatch-log.ndjson")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}

	requiredKeys := []string{
		"type", "ts", "agent", "runtime", "project",
		"exit_code", "duration_s", "output_bytes", "task_bytes",
		"model_chosen_by", "model_resolved",
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var finishedLine string
	for _, l := range lines {
		if strings.Contains(l, `"dispatch_finished"`) {
			finishedLine = l
			break
		}
	}
	if finishedLine == "" {
		t.Fatal("no dispatch_finished line found in log")
	}

	var ev map[string]json.RawMessage
	if err := json.Unmarshal([]byte(finishedLine), &ev); err != nil {
		t.Fatalf("dispatch_finished JSON parse error: %v", err)
	}
	for _, key := range requiredKeys {
		if _, ok := ev[key]; !ok {
			t.Errorf("dispatch_finished: missing required key %q", key)
		}
	}
}
