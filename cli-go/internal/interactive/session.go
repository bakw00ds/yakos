// Package interactive implements a persistent multi-turn claude session.
//
// A Session owns a long-running claude CLI process and multiplexes turns over
// its stdin/stdout.  Each user turn is written to stdin as a newline-terminated
// JSON frame; the readLoop drains stdout and dispatches SSE events to a caller-
// supplied onChunk callback.
//
// # Lifecycle
//
//  1. NewSession creates the session but does NOT start the process.
//  2. Start() starts the process and launches the readLoop goroutine.
//  3. SendUserTurn() writes a frame to stdin.  Returns errTurnInFlight if
//     a turn is already in progress (the caller should 409).
//  4. Close() closes stdin (clean exit signal), kills the process group on
//     platforms that support it (unix), and waits for the process to exit.
//     Idempotent.
//
// # Process-group kill
//
// Uses the same pattern as consoleui/bash_exec_{unix,windows}.go:
// Setpgid=true on unix so that killPG (kill -PGID) reaps grandchildren spawned
// by the claude process (tool calls, bash commands).  On Windows, falls back
// to direct kill (no process group support).
//
// # SSE output plane
//
// onChunk is called from the readLoop goroutine — callers must NOT block inside
// it longer than a fast fan-out write (the same contract as dispatch.RunStream).
// The caller (SessionManager) passes ch.hub.Route as onChunk.
package interactive

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"time"

	"github.com/bakw00ds/yakos/internal/dispatch"
)

// stdinWriteTimeout is the bounded write timeout for SendUserTurn.  A stuck
// write (e.g. the claude process is not reading stdin) returns an error after
// this duration rather than wedging the caller goroutine forever.
const stdinWriteTimeout = 10 * time.Second

// Session is one persistent multi-turn claude process.  All exported methods
// are safe for concurrent use.
type Session struct {
	mu sync.Mutex

	// Immutable after construction (set before Start).
	conversationID string
	ownerOperatorID string

	// Process state (written by Start; read after).
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   io.ReadCloser

	// Parser state shared across turns; allocated by NewSession.
	parserState *dispatch.StreamParserState

	// onChunk is the SSE fan-out callback; set at construction.
	onChunk func(dispatch.StreamChunk)

	// closed is closed by Close (idempotent guard).
	closed     chan struct{}
	closeOnce  sync.Once

	// turnInFlight is held by SendUserTurn while a turn is active.
	turnMu sync.Mutex

	// lastActivity is updated on every SendUserTurn call (for idle reaping).
	lastActivity time.Time

	// cmdProvider allows injection of a fake cmd in tests.
	cmdProvider func() *exec.Cmd
}

// SessionParams holds parameters passed to NewSession.
type SessionParams struct {
	ConversationID  string
	OwnerOperatorID string
	// CmdProvider, when non-nil, is called by Start() to obtain the exec.Cmd
	// instead of runtime.InteractiveExecCmd.  Used to inject a fake adapter in
	// tests without requiring the real claude binary.
	CmdProvider func() *exec.Cmd
	// OnChunk is the streaming-chunk callback; called from the readLoop goroutine.
	OnChunk func(dispatch.StreamChunk)
}

// NewSession allocates a Session but does not start the process.
// Call Start() to launch the process and the readLoop goroutine.
func NewSession(p SessionParams) *Session {
	s := &Session{
		conversationID:  p.ConversationID,
		ownerOperatorID: p.OwnerOperatorID,
		parserState:     dispatch.NewStreamParserState(),
		onChunk:         p.OnChunk,
		closed:          make(chan struct{}),
		lastActivity:    time.Now(),
		cmdProvider:     p.CmdProvider,
	}
	return s
}

// ConversationID returns the conversation identifier for this session.
func (s *Session) ConversationID() string { return s.conversationID }

// OwnerOperatorID returns the operator that owns this session.
func (s *Session) OwnerOperatorID() string { return s.ownerOperatorID }

// LastActivity returns the time of the last SendUserTurn call.
func (s *Session) LastActivity() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastActivity
}

// IsClosed reports whether the session has been closed.
func (s *Session) IsClosed() bool {
	select {
	case <-s.closed:
		return true
	default:
		return false
	}
}

// Closed returns the channel that is closed when the session exits.
// Callers may select on this channel to wait for the session to terminate
// (process crash, idle reap, or Close() call).
func (s *Session) Closed() <-chan struct{} { return s.closed }

// Start launches the claude process and the readLoop goroutine.
// Must be called exactly once after NewSession.
//
// ctx is the session-lifetime context.  When ctx is cancelled, the session
// transitions to closed state (Close is called automatically from within the
// goroutine).
func (s *Session) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cmd != nil {
		return fmt.Errorf("interactive: session already started")
	}

	var cmd *exec.Cmd
	if s.cmdProvider != nil {
		cmd = s.cmdProvider()
	} else {
		return fmt.Errorf("interactive: no cmdProvider set (use SessionParams.CmdProvider or call via SessionManager)")
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("interactive: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("interactive: stdout pipe: %w", err)
	}

	configureProcAttr(cmd)

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return fmt.Errorf("interactive: start: %w", err)
	}

	s.cmd = cmd
	s.stdin = stdin
	s.stdout = stdout

	// Launch the read loop in a goroutine.  It exits when the process exits
	// (stdout EOF) and then calls Close to mark the session dead.
	go s.readLoop(ctx)

	return nil
}

// readLoop drains stdout from the claude process and dispatches chunks via
// onChunk.  It uses the shared dispatch.ReadLineLoop + dispatch.ParseAndDispatch
// helpers so the interactive path and the one-shot path share one parser
// implementation.
//
// When the process exits (stdout EOF), readLoop calls Close() so the Session
// is marked dead and the SessionManager can detect the crash and emit an error
// SSE frame.
func (s *Session) readLoop(ctx context.Context) {
	// Read from stdout using the shared helper.
	s.mu.Lock()
	stdout := s.stdout
	ps := s.parserState
	s.mu.Unlock()

	dispatch.ReadLineLoop(stdout, dispatch.ReadLineLoopConfig{AgentName: "interactive"}, func(line []byte) {
		// Check for session close before processing each line.
		select {
		case <-s.closed:
			return
		default:
		}
		dispatch.ParseAndDispatch(line, ps, s.onChunk)
	})

	// stdout EOF means the process has exited.  Close the session so the manager
	// can detect the crash and emit an error SSE frame.
	s.closeOnce.Do(func() {
		// Wait for the process to fully exit to collect exit status.
		if s.cmd != nil {
			_ = s.cmd.Wait()
		}
		slog.Debug("interactive: session readLoop exited", "conversationID", s.conversationID)
		close(s.closed)
	})
}

// SendUserTurn writes a user-turn frame to the claude process's stdin.
//
// Returns errTurnInFlight when a turn is already in progress (the caller should
// return 409 Conflict).  Returns an error if the session is closed or the write
// times out (stdinWriteTimeout).
//
// The frame is encoded by runtime.EncodeUserTurn and written atomically with
// a bounded write timeout.
func (s *Session) SendUserTurn(frame []byte) error {
	select {
	case <-s.closed:
		return fmt.Errorf("interactive: session is closed")
	default:
	}

	// Enforce one-at-a-time: TryLock to get the turn mutex without blocking.
	if !s.turnMu.TryLock() {
		return ErrTurnInFlight
	}
	defer s.turnMu.Unlock()

	s.mu.Lock()
	stdin := s.stdin
	s.lastActivity = time.Now()
	s.mu.Unlock()

	if stdin == nil {
		return fmt.Errorf("interactive: session not started")
	}

	// Bounded write: use a goroutine + channel to enforce the timeout.
	done := make(chan error, 1)
	go func() {
		_, err := stdin.Write(frame)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("interactive: stdin write: %w", err)
		}
		return nil
	case <-time.After(stdinWriteTimeout):
		return fmt.Errorf("interactive: stdin write timed out after %s", stdinWriteTimeout)
	case <-s.closed:
		return fmt.Errorf("interactive: session closed during write")
	}
}

// Close shuts down the session: closes stdin (clean exit signal to the claude
// CLI), kills the process group (to reap any grandchildren), and waits for the
// process to exit.  Idempotent — safe to call multiple times.
func (s *Session) Close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		stdin := s.stdin
		cmd := s.cmd
		s.mu.Unlock()

		// Close stdin first — this signals the claude process to exit cleanly.
		if stdin != nil {
			_ = stdin.Close()
		}

		// Kill the process group to reap any grandchildren (bash commands, etc.)
		// spawned by the claude process.  The same pattern as consoleui bash_exec.
		if cmd != nil {
			killPG(cmd)
			// Wait for the process to fully exit so we don't leak a zombie.
			_ = cmd.Wait()
		}

		close(s.closed)
		slog.Debug("interactive: session closed", "conversationID", s.conversationID)
	})
}

// ErrTurnInFlight is returned by SendUserTurn when another turn is already in
// progress on this session.
var ErrTurnInFlight = fmt.Errorf("interactive: turn already in flight")
