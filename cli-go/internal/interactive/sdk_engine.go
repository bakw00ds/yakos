package interactive

// sdk_engine.go — SDKEngine: an Engine implementation driven by the Node.js
// Agent SDK sidecar (internal/interactive/sidecar/sidecar.bundle.js).
//
// # Architecture
//
// SDKEngine spawns `node <path-to-sidecar.bundle.js>` as a child process.
// Communication is NDJSON over stdin/stdout; stderr is forwarded to slog.
//
// Sidecar startup:
//  1. Start() spawns the node process.
//  2. The readLoop waits for a {"v":1,"kind":"ready"} frame from the sidecar.
//  3. Once received, Start() returns; the caller may immediately SendUserTurn.
//
// Per-turn flow:
//  1. Caller calls SendUserTurn → writes {"v":1,"kind":"user_turn","text":"..."}.
//  2. Sidecar emits token / thinking / tool_use / tool_result frames.
//  3. Sidecar emits {"v":1,"kind":"summary",...} at end of turn.
//  4. readLoop maps each frame to a dispatch.StreamChunk and calls onChunk.
//
// AskUserQuestion flow:
//  1. Sidecar emits {"v":1,"kind":"ask_user_question","toolUseId":"...","questions":[...]}.
//  2. readLoop maps it to StreamChunk{Type:"ask_user_question", AskToolUseID, AskQuestionsJSON}.
//  3. Caller (P2c) calls AnswerQuestion(toolUseID, QuestionAnswer).
//  4. SDKEngine writes the answer frame to sidecar stdin.
//  5. Sidecar resolves its promise; model continues.
//
// Shutdown:
//  1. Close() writes {"v":1,"kind":"shutdown"} to sidecar stdin.
//  2. Closes stdin; kills the process group; waits for exit.
//  3. Closed() channel is closed; IsClosed() returns true.
//
// # Bounded line decode
//
// readLoop uses dispatch.ReadLineLoop which applies the maxStreamLineBytes
// per-line cap (2 MB).  A line exceeding the cap is discarded.
//
// # Version/kind guards
//
// Each incoming frame must have v==1 and a known kind string.  Unknown frames
// are logged and skipped (forward-compat: a newer sidecar might emit new kinds
// that an older Go binary does not understand).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bakw00ds/yakos/internal/dispatch"
)

// sidecarWriteTimeout is the bounded write timeout for sidecar stdin writes.
// Mirrors session.go's stdinWriteTimeout.
const sidecarWriteTimeout = 10 * time.Second

// ---------------------------------------------------------------------------
// NDJSON frame types (sidecar → Go)
// ---------------------------------------------------------------------------

// sidecarFrame is the minimal envelope for all sidecar OUT frames.
type sidecarFrame struct {
	V    int             `json:"v"`
	Kind string          `json:"kind"`
	Raw  json.RawMessage `json:"-"` // full frame stored for field extraction
}

// sidecarTokenFrame carries a token chunk.
type sidecarTokenFrame struct {
	Text string `json:"text"`
}

// sidecarThinkingFrame carries a thinking chunk.
type sidecarThinkingFrame struct {
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
	Redacted  bool   `json:"redacted"`
}

// sidecarToolUseFrame carries a tool_use event.
type sidecarToolUseFrame struct {
	ToolName  string `json:"toolName"`
	ToolInput string `json:"toolInput"`
}

// sidecarToolResultFrame carries a tool_result event.
type sidecarToolResultFrame struct {
	ToolName   string `json:"toolName"`
	ToolOutput string `json:"toolOutput"`
	IsError    bool   `json:"isError"`
}

// sidecarAskFrame carries an ask_user_question event.
type sidecarAskFrame struct {
	ToolUseID string          `json:"toolUseId"`
	Questions json.RawMessage `json:"questions"`
}

// sidecarSummaryFrame carries the terminal summary.
type sidecarSummaryFrame struct {
	TotalCostUsd float64         `json:"totalCostUsd"`
	Usage        json.RawMessage `json:"usage"`
}

// sidecarErrorFrame carries an error from the sidecar.
type sidecarErrorFrame struct {
	Text string `json:"text"`
}

// ---------------------------------------------------------------------------
// NDJSON frame types (Go → sidecar)
// ---------------------------------------------------------------------------

// sidecarInUserTurn is written to sidecar stdin for a new user turn.
type sidecarInUserTurn struct {
	V    int    `json:"v"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// sidecarInAnswer is written to sidecar stdin to answer an AskUserQuestion.
type sidecarInAnswer struct {
	V           int               `json:"v"`
	Kind        string            `json:"kind"`
	ToolUseID   string            `json:"toolUseId"`
	Questions   json.RawMessage   `json:"questions,omitempty"`
	Answers     map[string]string `json:"answers"`
	Response    string            `json:"response,omitempty"`
	Annotations json.RawMessage   `json:"annotations,omitempty"`
}

// sidecarInShutdown is written to sidecar stdin to initiate clean shutdown.
var sidecarInShutdown = []byte(`{"v":1,"kind":"shutdown"}` + "\n")

// ---------------------------------------------------------------------------
// SDKEngineParams
// ---------------------------------------------------------------------------

// SDKEngineParams holds construction parameters for NewSDKEngine.
type SDKEngineParams struct {
	// ConversationID is the stable transcript key for this session.
	ConversationID string

	// OwnerOperatorID is the operator that owns this session.
	OwnerOperatorID string

	// OnChunk is called from the readLoop goroutine for each streaming chunk.
	// Must not block longer than a fast fan-out write.
	OnChunk func(dispatch.StreamChunk)

	// NodePath is the absolute path to the node binary.
	// If empty, SDKEngine uses the value from FindNodeBinary().
	NodePath string

	// BundlePath is the absolute path to sidecar.bundle.js.
	// If empty, SDKEngine uses the value from FindSidecarBundle(yakosRoot).
	BundlePath string

	// YakosRoot is used to locate the bundle when BundlePath is empty.
	// Typically set to the yakos installation root.
	YakosRoot string

	// Model is an optional model override passed to the sidecar as --model <model>.
	Model string

	// cmdProvider, when non-nil, is called by Start() to obtain the exec.Cmd
	// instead of building one from NodePath+BundlePath.  Used in tests to inject
	// a fake sidecar without requiring node or the real bundle.
	cmdProvider func() *exec.Cmd
}

// ---------------------------------------------------------------------------
// SDKEngine
// ---------------------------------------------------------------------------

// SDKEngine implements Engine using the Node.js Agent SDK sidecar.
// All exported methods are safe for concurrent use.
type SDKEngine struct {
	mu sync.Mutex

	// Immutable after construction.
	conversationID  string
	ownerOperatorID string
	onChunk         func(dispatch.StreamChunk)
	params          SDKEngineParams

	// Process state (written by Start; read after).
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	// closed is closed by closeOnce (idempotent guard).
	closed    chan struct{}
	closeOnce sync.Once

	// readyOnce ensures the ready gate fires at most once.
	readyCh   chan struct{}
	readyOnce sync.Once

	// turnMu enforces one-at-a-time SendUserTurn (same pattern as Session).
	turnMu sync.Mutex

	// lastActivity is updated on every SendUserTurn call.
	lastActivity time.Time
}

// NewSDKEngine allocates an SDKEngine but does NOT start the sidecar.
// Call Start() to launch the node process.
//
// Returns an error when node or the bundle cannot be located; callers
// should surface this as a 503 when the feature flag is set.
func NewSDKEngine(p SDKEngineParams) (*SDKEngine, error) {
	// Resolve node binary.
	nodePath := p.NodePath
	if nodePath == "" && p.cmdProvider == nil {
		var err error
		nodePath, err = FindNodeBinary()
		if err != nil {
			return nil, fmt.Errorf("interactive: sdk engine: %w", err)
		}
	}

	// Resolve bundle path.
	bundlePath := p.BundlePath
	if bundlePath == "" && p.cmdProvider == nil {
		var err error
		bundlePath, err = FindSidecarBundle(p.YakosRoot)
		if err != nil {
			return nil, fmt.Errorf("interactive: sdk engine: %w", err)
		}
	}

	e := &SDKEngine{
		conversationID:  p.ConversationID,
		ownerOperatorID: p.OwnerOperatorID,
		onChunk:         p.OnChunk,
		params: SDKEngineParams{
			ConversationID:  p.ConversationID,
			OwnerOperatorID: p.OwnerOperatorID,
			OnChunk:         p.OnChunk,
			NodePath:        nodePath,
			BundlePath:      bundlePath,
			YakosRoot:       p.YakosRoot,
			Model:           p.Model,
			cmdProvider:     p.cmdProvider,
		},
		closed:       make(chan struct{}),
		readyCh:      make(chan struct{}),
		lastActivity: time.Now(),
	}
	return e, nil
}

// ConversationID returns the conversation identifier.
func (e *SDKEngine) ConversationID() string { return e.conversationID }

// OwnerOperatorID returns the operator that owns this engine.
func (e *SDKEngine) OwnerOperatorID() string { return e.ownerOperatorID }

// LastActivity returns the time of the last SendUserTurn call.
func (e *SDKEngine) LastActivity() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastActivity
}

// IsClosed reports whether the engine has been closed.
func (e *SDKEngine) IsClosed() bool {
	select {
	case <-e.closed:
		return true
	default:
		return false
	}
}

// Closed returns a channel that is closed when the engine exits.
func (e *SDKEngine) Closed() <-chan struct{} { return e.closed }

// Start launches the node sidecar process and the readLoop goroutine.
// Must be called exactly once after NewSDKEngine.
//
// Start blocks until the sidecar emits a "ready" frame (indicating it has
// authenticated with the claude CLI keychain) or until ctx is cancelled.
// Returns an error if the process cannot be started or the ready frame is
// not received within a reasonable timeout (readyTimeout).
func (e *SDKEngine) Start(ctx context.Context) error {
	// Guard: already started.
	e.mu.Lock()
	alreadyStarted := e.cmd != nil
	e.mu.Unlock()
	if alreadyStarted {
		return fmt.Errorf("interactive: sdk engine already started")
	}

	var cmd *exec.Cmd
	if e.params.cmdProvider != nil {
		cmd = e.params.cmdProvider()
	} else {
		args := []string{e.params.BundlePath}
		if e.params.Model != "" {
			args = append(args, "--model", e.params.Model)
		}
		cmd = exec.CommandContext(ctx, e.params.NodePath, args...) //nolint:gosec
	}

	// Forward sidecar stderr to our slog at DEBUG level.
	cmd.Stderr = &slogWriter{prefix: "sidecar:" + e.conversationID}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("interactive: sdk engine: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("interactive: sdk engine: stdout pipe: %w", err)
	}

	configureProcAttr(cmd)

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return fmt.Errorf("interactive: sdk engine: start node: %w", err)
	}

	// Store process state under lock; release before blocking.
	e.mu.Lock()
	e.cmd = cmd
	e.stdin = stdin
	e.stdout = stdout
	e.mu.Unlock()

	// Launch the readLoop.  It emits readyOnce when the "ready" frame arrives.
	// The lock is NOT held here — readLoop acquires it separately.
	go e.readLoop()

	// Wait for "ready" (sidecar authenticated) or timeout/cancel.
	// e.mu is NOT held while we wait, so readLoop can proceed.
	const readyTimeout = 30 * time.Second
	select {
	case <-e.readyCh:
		return nil
	case <-e.closed:
		return fmt.Errorf("interactive: sdk engine: sidecar exited before emitting ready")
	case <-ctx.Done():
		e.doClose()
		return fmt.Errorf("interactive: sdk engine: context cancelled waiting for ready: %w", ctx.Err())
	case <-time.After(readyTimeout):
		e.doClose()
		return fmt.Errorf("interactive: sdk engine: sidecar did not emit ready within %s", readyTimeout)
	}
}

// SendUserTurn delivers a user-turn frame to the sidecar.
//
// frame is the raw JSON bytes (as encoded by runtime.EncodeUserTurn) but the
// SDKEngine translates it to the sidecar protocol — we extract the text and
// re-encode as {"v":1,"kind":"user_turn","text":"..."}.
//
// Returns ErrTurnInFlight when a turn is already in progress.
// Returns an error if the engine is closed or the write times out.
func (e *SDKEngine) SendUserTurn(frame []byte) error {
	select {
	case <-e.closed:
		return fmt.Errorf("interactive: sdk engine is closed")
	default:
	}

	// Enforce one-at-a-time.
	if !e.turnMu.TryLock() {
		return ErrTurnInFlight
	}
	defer e.turnMu.Unlock()

	e.mu.Lock()
	e.lastActivity = time.Now()
	stdin := e.stdin
	e.mu.Unlock()

	if stdin == nil {
		return fmt.Errorf("interactive: sdk engine not started")
	}

	// Extract the "text" from the user-turn frame (which is in claude streaming
	// JSON format: {"type":"user","message":{"role":"user","content":[{"type":"text","text":"..."}]}}).
	// An unparseable frame is an error: silently using raw JSON as text would
	// corrupt the conversation without any signal to the caller.
	text, err := extractUserTurnText(frame)
	if err != nil {
		slog.Warn("interactive: sdk engine: malformed user-turn frame; dropping turn",
			"conversationID", e.conversationID,
			"err", err,
			"frame_prefix", truncateForLog(frame, 120),
		)
		return fmt.Errorf("interactive: sdk engine: %w", err)
	}

	out, err := json.Marshal(sidecarInUserTurn{V: 1, Kind: "user_turn", Text: text})
	if err != nil {
		return fmt.Errorf("interactive: sdk engine: marshal user_turn: %w", err)
	}
	out = append(out, '\n')

	return e.writeWithTimeout(stdin, out)
}

// AnswerQuestion delivers the operator's answer to an AskUserQuestion tool call.
//
// toolUseID must match the AskToolUseID from the StreamChunk{Type:"ask_user_question"}
// that was emitted when the question was asked.  answer.Answers maps each
// question string to the chosen option label.
//
// The sidecar is responsible for resolving the parked canUseTool promise and
// allowing the model to continue the turn.
func (e *SDKEngine) AnswerQuestion(toolUseID string, answer QuestionAnswer) error {
	select {
	case <-e.closed:
		return fmt.Errorf("interactive: sdk engine is closed")
	default:
	}

	e.mu.Lock()
	stdin := e.stdin
	e.mu.Unlock()

	if stdin == nil {
		return fmt.Errorf("interactive: sdk engine not started")
	}

	frame := sidecarInAnswer{
		V:         1,
		Kind:      "answer",
		ToolUseID: toolUseID,
		Answers:   answer.Answers,
	}
	if answer.Response != "" {
		frame.Response = answer.Response
	}
	if len(answer.AnnotationsJSON) > 0 {
		frame.Annotations = json.RawMessage(answer.AnnotationsJSON)
	}
	if len(answer.QuestionsJSON) > 0 {
		frame.Questions = json.RawMessage(answer.QuestionsJSON)
	}

	out, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("interactive: sdk engine: marshal answer: %w", err)
	}
	out = append(out, '\n')

	return e.writeWithTimeout(stdin, out)
}

// Close shuts down the SDK engine cleanly.  Idempotent.
func (e *SDKEngine) Close() error {
	e.doClose()
	return nil
}

// doClose performs the actual close logic (called from Close and Start on error).
func (e *SDKEngine) doClose() {
	e.closeOnce.Do(func() {
		e.mu.Lock()
		stdin := e.stdin
		cmd := e.cmd
		e.mu.Unlock()

		// Send graceful shutdown frame, then close stdin.
		if stdin != nil {
			// Best-effort: ignore write errors (process may already be dead).
			done := make(chan struct{})
			go func() {
				_, _ = stdin.Write(sidecarInShutdown)
				_ = stdin.Close()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				_ = stdin.Close()
			}
		}

		// Kill the process group to reap any grandchildren.
		if cmd != nil {
			killPG(cmd)
			_ = cmd.Wait()
		}

		close(e.closed)

		// Unblock any pending readyCh waiter (e.g. Start blocked waiting for ready).
		e.readyOnce.Do(func() {
			close(e.readyCh)
		})

		slog.Debug("interactive: sdk engine closed", "conversationID", e.conversationID)
	})
}

// readLoop drains stdout from the sidecar process and dispatches chunks via onChunk.
// It exits when the sidecar process exits (stdout EOF) and then calls doClose.
func (e *SDKEngine) readLoop() {
	e.mu.Lock()
	stdout := e.stdout
	e.mu.Unlock()

	dispatch.ReadLineLoop(stdout, dispatch.ReadLineLoopConfig{AgentName: "sdk-sidecar"}, func(line []byte) {
		select {
		case <-e.closed:
			return
		default:
		}
		e.dispatchLine(line)
	})

	// stdout EOF → sidecar exited.
	e.doClose()
}

// dispatchLine parses one NDJSON line from the sidecar and calls onChunk.
func (e *SDKEngine) dispatchLine(line []byte) {
	// Fast-path: parse the envelope.
	var env struct {
		V    int    `json:"v"`
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(line, &env); err != nil {
		slog.Debug("interactive: sdk engine: malformed sidecar frame",
			"conversationID", e.conversationID,
			"line", truncateForLog(line, 200),
		)
		return
	}

	// Version guard: must be v:1.
	if env.V != 1 {
		slog.Debug("interactive: sdk engine: unsupported frame version",
			"conversationID", e.conversationID,
			"v", env.V,
		)
		return
	}

	switch env.Kind {
	case "ready":
		e.readyOnce.Do(func() {
			close(e.readyCh)
		})

	case "token":
		var f sidecarTokenFrame
		if err := json.Unmarshal(line, &f); err != nil {
			slog.Debug("interactive: sdk engine: bad token frame", "err", err)
			return
		}
		if e.onChunk != nil {
			e.onChunk(dispatch.StreamChunk{Type: "token", Text: f.Text})
		}

	case "thinking":
		var f sidecarThinkingFrame
		if err := json.Unmarshal(line, &f); err != nil {
			slog.Debug("interactive: sdk engine: bad thinking frame", "err", err)
			return
		}
		if e.onChunk != nil {
			e.onChunk(dispatch.StreamChunk{
				Type:              "thinking",
				Thinking:          f.Text,
				ThinkingTruncated: f.Truncated,
				ThinkingRedacted:  f.Redacted,
			})
		}

	case "tool_use":
		var f sidecarToolUseFrame
		if err := json.Unmarshal(line, &f); err != nil {
			slog.Debug("interactive: sdk engine: bad tool_use frame", "err", err)
			return
		}
		if e.onChunk != nil {
			e.onChunk(dispatch.StreamChunk{
				Type:      "tool_use",
				ToolName:  f.ToolName,
				ToolInput: f.ToolInput,
			})
		}

	case "tool_result":
		var f sidecarToolResultFrame
		if err := json.Unmarshal(line, &f); err != nil {
			slog.Debug("interactive: sdk engine: bad tool_result frame", "err", err)
			return
		}
		if e.onChunk != nil {
			e.onChunk(dispatch.StreamChunk{
				Type:       "tool_result",
				ToolName:   f.ToolName,
				ToolOutput: f.ToolOutput,
				IsError:    f.IsError,
			})
		}

	case "ask_user_question":
		var f sidecarAskFrame
		if err := json.Unmarshal(line, &f); err != nil {
			slog.Debug("interactive: sdk engine: bad ask_user_question frame", "err", err)
			return
		}
		questionsJSON := string(f.Questions)
		if questionsJSON == "" || questionsJSON == "null" {
			questionsJSON = "[]"
		}
		if e.onChunk != nil {
			e.onChunk(dispatch.StreamChunk{
				Type:             "ask_user_question",
				AskToolUseID:     f.ToolUseID,
				AskQuestionsJSON: questionsJSON,
			})
		}

	case "summary":
		var f sidecarSummaryFrame
		if err := json.Unmarshal(line, &f); err != nil {
			slog.Debug("interactive: sdk engine: bad summary frame", "err", err)
			return
		}
		if e.onChunk != nil {
			e.onChunk(dispatch.StreamChunk{
				Type:         "summary",
				TotalCostUSD: f.TotalCostUsd,
				// ExitCode defaults to 0 (success) for SDK sessions.
				// DurationS is not tracked by the sidecar (SDK does not expose it).
				ExitCode: 0,
			})
		}

	case "error":
		var f sidecarErrorFrame
		if err := json.Unmarshal(line, &f); err != nil {
			slog.Debug("interactive: sdk engine: bad error frame", "err", err)
			return
		}
		slog.Warn("interactive: sdk engine: sidecar error",
			"conversationID", e.conversationID,
			"text", f.Text,
		)
		if e.onChunk != nil {
			// Surface as an error chunk so the caller can relay it to the SSE stream.
			e.onChunk(dispatch.StreamChunk{Type: "error", Text: f.Text})
		}

	default:
		// Unknown kind: log and skip (forward-compat).
		slog.Debug("interactive: sdk engine: unknown sidecar frame kind",
			"kind", env.Kind,
			"conversationID", e.conversationID,
		)
	}
}

// writeWithTimeout writes b to w within sidecarWriteTimeout.
// On timeout, doClose() is called to unblock any blocked write goroutine.
func (e *SDKEngine) writeWithTimeout(w io.Writer, b []byte) error {
	done := make(chan error, 1)
	go func() {
		_, err := w.Write(b)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("interactive: sdk engine: stdin write: %w", err)
		}
		return nil
	case <-time.After(sidecarWriteTimeout):
		e.doClose()
		return fmt.Errorf("interactive: sdk engine: stdin write timed out after %s; engine closed", sidecarWriteTimeout)
	case <-e.closed:
		return fmt.Errorf("interactive: sdk engine: engine closed during write")
	}
}

// ---------------------------------------------------------------------------
// QuestionAnswer extended for SDK
// ---------------------------------------------------------------------------

// The QuestionAnswer type declared in engine.go is extended here via usage;
// we add fields that AnswerQuestion needs but that the CLI engine ignores.
// We do NOT change the type declaration (it lives in engine.go); instead we
// access the extra fields via struct embedding / direct access here.
//
// Extended fields used by SDKEngine.AnswerQuestion:
//   - Response (free-text response to include alongside answers)
//   - AnnotationsJSON (raw JSON annotations for each question)
//   - QuestionsJSON (raw JSON questions, echoed back to sidecar for context)
//
// These are zero-valued for the CLI engine path; SDKEngine reads them.
// They are added to QuestionAnswer in engine.go via the patch below.

// ---------------------------------------------------------------------------
// Node binary + bundle discovery
// ---------------------------------------------------------------------------

// minNodeMajor is the minimum Node.js major version required by the sidecar.
// The @anthropic-ai/claude-agent-sdk package requires Node ≥ 18.
const minNodeMajor = 18

// FindNodeBinary locates the `node` binary using exec.LookPath and checks
// that its major version is at least minNodeMajor (18).
//
// Returns a descriptive error when node is not found or is too old, so the
// 503 / startup message names the required version rather than timing out
// with "sidecar did not emit ready".
func FindNodeBinary() (string, error) {
	p, err := exec.LookPath("node")
	if err != nil {
		return "", fmt.Errorf("node binary not found in PATH; install Node.js ≥%d to enable structured questions (SDK engine)", minNodeMajor)
	}

	// Probe the version: `node --version` prints "v18.0.0\n".
	out, err := exec.Command(p, "--version").Output() //nolint:gosec
	if err != nil {
		// If the probe fails we still return the path — the caller will get a
		// clear error when the sidecar itself fails to start.
		return p, nil
	}

	major := parseNodeMajor(strings.TrimSpace(string(out)))
	if major > 0 && major < minNodeMajor {
		return "", fmt.Errorf("node %s is too old (found major=%d, need ≥%d); upgrade Node.js to enable structured questions (SDK engine)",
			strings.TrimSpace(string(out)), major, minNodeMajor)
	}
	return p, nil
}

// parseNodeMajor extracts the major version number from a node --version
// string of the form "v18.12.1".  Returns 0 if the string cannot be parsed.
func parseNodeMajor(version string) int {
	// Strip leading 'v' or 'V'.
	v := strings.TrimLeft(version, "vV")
	// Take everything up to the first '.'.
	dot := strings.IndexByte(v, '.')
	if dot < 0 {
		dot = len(v)
	}
	major := 0
	for _, ch := range v[:dot] {
		if ch < '0' || ch > '9' {
			return 0
		}
		major = major*10 + int(ch-'0')
	}
	return major
}

// FindSidecarBundle returns the absolute path to sidecar.bundle.js, located
// relative to yakosRoot.
//
// Search order:
//  1. <yakosRoot>/cli-go/internal/interactive/sidecar/sidecar.bundle.js
//  2. Alongside the running binary (os.Executable() dir) / sidecar.bundle.js
//     — supports production installs where the bundle is co-installed.
//  3. The directory of this source file at build time (embed path).
//
// Returns an error when no candidate exists.
func FindSidecarBundle(yakosRoot string) (string, error) {
	candidates := []string{}

	// 1. Repo-relative path (dev environment).
	if yakosRoot != "" {
		candidates = append(candidates,
			filepath.Join(yakosRoot, "cli-go", "internal", "interactive", "sidecar", "sidecar.bundle.js"),
		)
	}

	// 2. Co-installed alongside the yakos binary.
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates,
			filepath.Join(filepath.Dir(exe), "sidecar.bundle.js"),
			filepath.Join(filepath.Dir(exe), "sidecar", "sidecar.bundle.js"),
		)
	}

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}

	return "", fmt.Errorf("sidecar.bundle.js not found; looked in: %v — run `make interactive-sidecar` to rebuild it", candidates)
}

// ---------------------------------------------------------------------------
// Factory
// ---------------------------------------------------------------------------

// NewSDKEngineWithProvider creates an SDKEngine using a caller-supplied
// cmdProvider instead of locating node/bundle automatically.  This is the
// primary test seam: pass a fake-sidecar CmdProvider to drive the engine
// against an in-process script without requiring node or the real bundle.
//
// The params.NodePath and params.BundlePath fields are ignored when provider
// is non-nil; the provider is called directly by Start().
func NewSDKEngineWithProvider(params SDKEngineParams, provider func() *exec.Cmd) (*SDKEngine, error) {
	params.cmdProvider = provider
	e := &SDKEngine{
		conversationID:  params.ConversationID,
		ownerOperatorID: params.OwnerOperatorID,
		onChunk:         params.OnChunk,
		params:          params,
		closed:          make(chan struct{}),
		readyCh:         make(chan struct{}),
		lastActivity:    time.Now(),
	}
	return e, nil
}

// SDKEngineFactory is a function that creates an SDKEngine for a session.
// It is used by Manager.EnsureSDK (P2c) to construct SDK engines.
//
// Returns an error when node/bundle are unavailable; callers should surface
// this as a 503 Service Unavailable with the message "SDK engine unavailable".
type SDKEngineFactory func(params SDKEngineParams) (*SDKEngine, error)

// NewSDKEngineFactory returns an SDKEngineFactory pre-configured with the
// resolved node binary and bundle paths.  If either cannot be found, the
// factory function itself returns the error at construction time so callers
// get a clear 503 rather than a silent fallback.
//
// Use this in consoleui/server.go to build the factory once at startup:
//
//	factory, err := interactive.NewSDKEngineFactory(yakosRoot)
//	if err != nil {
//	    slog.Warn("interactive: SDK engine unavailable", "err", err)
//	}
func NewSDKEngineFactory(yakosRoot string) (SDKEngineFactory, error) {
	nodePath, nodeErr := FindNodeBinary()
	bundlePath, bundleErr := FindSidecarBundle(yakosRoot)

	if nodeErr != nil {
		return nil, nodeErr
	}
	if bundleErr != nil {
		return nil, bundleErr
	}

	return func(params SDKEngineParams) (*SDKEngine, error) {
		// Inject resolved paths so NewSDKEngine skips discovery.
		params.NodePath = nodePath
		params.BundlePath = bundlePath
		return NewSDKEngine(params)
	}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// extractUserTurnText extracts the text content from a claude stream-json
// user-turn frame (as encoded by runtime.EncodeUserTurn).
//
// Frame shape:
//
//	{"type":"user","message":{"role":"user","content":[{"type":"text","text":"<TEXT>"}]}}
//
// Returns an error when the frame cannot be parsed or contains no text block,
// so SendUserTurn can surface a clear failure rather than silently corrupting
// the conversation with raw JSON as message text.
func extractUserTurnText(frame []byte) (string, error) {
	var outer struct {
		Message struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(frame, &outer); err != nil {
		return "", fmt.Errorf("malformed user-turn frame: %w", err)
	}
	for _, c := range outer.Message.Content {
		if c.Type == "text" && c.Text != "" {
			return c.Text, nil
		}
	}
	return "", fmt.Errorf("user-turn frame contains no text content block")
}

// truncateForLog truncates a byte slice to at most n bytes for logging.
func truncateForLog(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "...[truncated]"
}

// slogWriter is an io.Writer that writes to slog at DEBUG level.
// Used to capture sidecar stderr without polluting stdout.
//
// exec drains stderr on its own goroutine (one goroutine per writerDescriptor),
// so Write must be goroutine-safe.  mu guards buf across concurrent Write calls.
type slogWriter struct {
	mu     sync.Mutex
	prefix string
	buf    []byte
}

func (w *slogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.buf = append(w.buf, p...)
	// Flush complete lines.
	for {
		idx := -1
		for i, b := range w.buf {
			if b == '\n' {
				idx = i
				break
			}
		}
		if idx < 0 {
			break
		}
		line := string(w.buf[:idx])
		w.buf = w.buf[idx+1:]
		if line != "" {
			slog.Debug(w.prefix, "stderr", line)
		}
	}
	return len(p), nil
}
