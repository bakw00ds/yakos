package interactive

// manager.go — SessionManager: creates, tracks, and cleans up persistent
// multi-turn claude sessions.
//
// Design:
//
//   - Sessions are keyed by conversationID (the stable transcript key).
//   - A bounded cap (defaultSessionCap) prevents memory exhaustion via session
//     accumulation.  Attempts to Ensure() beyond the cap return errCapExceeded.
//   - An idle reaper goroutine closes sessions that have had no SendUserTurn
//     activity for defaultIdleTimeout.
//   - When the readLoop exits unexpectedly (process crash), markDead is called:
//     the entry is removed, and an error SSEEvent is emitted so the browser
//     pane shows an error instead of hanging forever.
//   - Ensure() enforces owner-conflict rejection: a second Ensure() for the
//     same conversationID with a different ownerOperatorID returns
//     ErrOwnerConflict (caller must 403).
//   - Send() enforces the same ownership check and returns ErrTurnInFlight when
//     the session's SendUserTurn returns ErrTurnInFlight (caller must 409).
//
// # Fleet and transcript integration
//
// SessionManager does not write to the fleet registry or the transcript itself;
// that is the chat_handler goroutine's job.  The SSEEvent delivered via onError
// uses the same SSEEvent type as the one-shot path so ChatHub.Route routes it
// to the correct SSE connections.
//
// # Idle reaper
//
// The reaper runs as a long-lived goroutine started by NewManager.  It ticks
// on reaperInterval and closes any session whose lastActivity is older than
// idleTimeout.  The reaper is stopped when the manager's parent context is
// cancelled (or by calling Stop()).

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

const (
	// defaultSessionCap is the maximum number of concurrent interactive sessions.
	// Separate from the dispatch governor (which governs one-shot RunStream calls).
	defaultSessionCap = 4

	// defaultIdleTimeout is how long a session may be idle before the reaper closes it.
	defaultIdleTimeout = 15 * time.Minute

	// defaultReaperInterval is how often the idle reaper scans for stale sessions.
	defaultReaperInterval = 30 * time.Second
)

// ErrOwnerConflict is returned by Ensure and Send when the conversationID is
// already owned by a different operatorID.  The caller must return 403.
var ErrOwnerConflict = errors.New("interactive: session owned by different operator")

// ErrNoSession is returned by Send when the conversationID has no live session.
// The caller must return 404.
var ErrNoSession = errors.New("interactive: no live session for this conversationID")

// ErrCapExceeded is returned by Ensure when the session cap is reached.
// The caller must return 429.
var ErrCapExceeded = errors.New("interactive: session cap exceeded")

// ErrNoPendingQuestion is returned by the answer handler when the supplied
// toolUseID does not match the session's currently-pending question.
// The caller must return 404 (do not leak existence).
var ErrNoPendingQuestion = errors.New("interactive: no pending question matching this toolUseID")

// ErrAnswerAlreadyConsumed is returned by the answer handler when a pending
// question was already answered (single-use enforcement).
// The caller must return 409.
var ErrAnswerAlreadyConsumed = errors.New("interactive: question already answered")

// managerEntry holds one live engine (session) and its associated metadata.
type managerEntry struct {
	session Engine
}

// Manager manages the lifecycle of persistent interactive sessions.
// All exported methods are safe for concurrent use.
type Manager struct {
	mu      sync.Mutex
	entries map[string]*managerEntry // conversationID → entry

	cap            int
	idleTimeout    time.Duration
	reaperInterval time.Duration

	// onError is called when a session's readLoop exits unexpectedly.
	// It emits an error SSE event so the browser pane shows an error.
	// Wired to hub.Route by New().
	onError func(conversationID, sessionID, operatorID, msg string)

	stop     chan struct{}
	stopOnce sync.Once
}

// ManagerConfig holds configuration for NewManager.
type ManagerConfig struct {
	// Cap is the maximum number of concurrent interactive sessions.
	// Defaults to defaultSessionCap (4) when 0.
	Cap int

	// IdleTimeout is the idle-reap threshold.
	// Defaults to defaultIdleTimeout (15 min) when 0.
	IdleTimeout time.Duration

	// ReaperInterval is how often the idle reaper scans for stale sessions.
	// Defaults to defaultReaperInterval (30s) when 0.
	// Set to a small value in tests to speed up idle-reaper assertions.
	ReaperInterval time.Duration

	// OnError is called when a session's process crashes or exits unexpectedly.
	// The parameters are: conversationID, sessionID, ownerOperatorID, errorMessage.
	// Callers should route an error SSEEvent to the browser via hub.Route.
	// May be nil (no error callback).
	OnError func(conversationID, sessionID, operatorID, msg string)
}

// NewManager creates a SessionManager and starts the idle-reaper goroutine.
// The reaper runs until ctx is cancelled or Stop() is called.
func NewManager(ctx context.Context, cfg ManagerConfig) *Manager {
	cap := cfg.Cap
	if cap <= 0 {
		cap = defaultSessionCap
	}
	idleTimeout := cfg.IdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = defaultIdleTimeout
	}
	reaperInterval := cfg.ReaperInterval
	if reaperInterval <= 0 {
		reaperInterval = defaultReaperInterval
	}
	m := &Manager{
		entries:        make(map[string]*managerEntry),
		cap:            cap,
		idleTimeout:    idleTimeout,
		reaperInterval: reaperInterval,
		onError:        cfg.OnError,
		stop:           make(chan struct{}),
	}
	go m.reaper(ctx)
	return m
}

// Stop halts the idle-reaper goroutine.  Idempotent.
// In-flight sessions are NOT closed; callers that want a clean shutdown should
// close each session individually via Close(conversationID).
func (m *Manager) Stop() {
	m.stopOnce.Do(func() { close(m.stop) })
}

// EnsureSDK returns (or creates) the live SDK engine for conversationID.
//
// If no engine exists, a new SDKEngine is created via factory and started.
// If an engine exists and ownerOperatorID matches, the existing engine is
// returned.  If an engine exists but is owned by a different operator, returns
// ErrOwnerConflict.
//
// All lifecycle semantics (cap, idle-reap, owner-conflict, turn-in-flight,
// crash-detection/OnError, group-kill) are identical to Ensure — the only
// difference is the engine type: SDKEngine instead of the CLI Session.
//
// factory must not be nil.  params.ConversationID and params.OwnerOperatorID
// are always overwritten by the conversationID and ownerOperatorID arguments.
func (m *Manager) EnsureSDK(conversationID, ownerOperatorID string, params SDKEngineParams, factory SDKEngineFactory) (Engine, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if entry, ok := m.entries[conversationID]; ok {
		if entry.session.OwnerOperatorID() != ownerOperatorID {
			return nil, ErrOwnerConflict
		}
		if entry.session.IsClosed() {
			delete(m.entries, conversationID)
		} else {
			return entry.session, nil
		}
	}

	if len(m.entries) >= m.cap {
		return nil, ErrCapExceeded
	}

	params.ConversationID = conversationID
	params.OwnerOperatorID = ownerOperatorID

	eng, err := factory(params)
	if err != nil {
		return nil, fmt.Errorf("interactive: EnsureSDK factory: %w", err)
	}

	if err := eng.Start(context.Background()); err != nil {
		return nil, fmt.Errorf("interactive: EnsureSDK start: %w", err)
	}

	m.entries[conversationID] = &managerEntry{session: eng}

	go func() {
		<-eng.Closed()

		m.mu.Lock()
		_, stillPresent := m.entries[conversationID]
		if stillPresent {
			delete(m.entries, conversationID)
		}
		m.mu.Unlock()

		if stillPresent {
			slog.Warn("interactive: SDK engine exited unexpectedly",
				"conversationID", conversationID,
				"owner", ownerOperatorID,
			)
			if m.onError != nil {
				m.onError(conversationID, conversationID, ownerOperatorID,
					"interactive SDK session exited unexpectedly")
			}
		}
	}()

	return eng, nil
}

// Ensure returns (or creates) the live engine for conversationID.
//
// If no engine exists, a new Session (CLI engine) is created and started.
// If an engine exists and ownerOperatorID matches, the existing engine is
// returned.  If an engine exists but is owned by a different operator, returns
// ErrOwnerConflict.
//
// params.CmdProvider is used to build the exec.Cmd for the CLI engine.  For
// production callers, pass a closure over runtime.InteractiveExecCmd.  For
// tests, inject a fake.
//
// Note: params.ConversationID and params.OwnerOperatorID are always overwritten
// by the conversationID and ownerOperatorID arguments; values set in params for
// those fields are ignored.
func (m *Manager) Ensure(conversationID, ownerOperatorID string, params SessionParams) (Engine, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if entry, ok := m.entries[conversationID]; ok {
		// Engine exists.
		if entry.session.OwnerOperatorID() != ownerOperatorID {
			return nil, ErrOwnerConflict
		}
		if entry.session.IsClosed() {
			// Engine was closed (crash or idle timeout): remove and recreate below.
			delete(m.entries, conversationID)
		} else {
			return entry.session, nil
		}
	}

	// Cap check before creating.
	if len(m.entries) >= m.cap {
		return nil, ErrCapExceeded
	}

	// Build the session parameters.
	params.ConversationID = conversationID
	params.OwnerOperatorID = ownerOperatorID

	// Construct the CLI engine.  The crash-detection goroutine below waits for
	// the engine's Closed() channel and calls the OnError callback.
	sess := NewSession(params)

	// Start the process in a background goroutine-compatible context.
	// We pass context.Background() here because the engine outlives the HTTP
	// request context.  The engine's own Closed() channel and Close() handle
	// lifetime management.
	if err := sess.Start(context.Background()); err != nil {
		return nil, fmt.Errorf("interactive: start session: %w", err)
	}

	m.entries[conversationID] = &managerEntry{session: sess}

	// Crash-detection goroutine: waits for the engine's Closed() channel.
	// When it fires, removes the entry and emits an error SSE event.
	go func() {
		<-sess.Closed()

		m.mu.Lock()
		_, stillPresent := m.entries[conversationID]
		if stillPresent {
			delete(m.entries, conversationID)
		}
		m.mu.Unlock()

		if stillPresent {
			slog.Warn("interactive: session process exited unexpectedly",
				"conversationID", conversationID,
				"owner", ownerOperatorID,
			)
			if m.onError != nil {
				// sessionID is the same as conversationID on the interactive path
				// (the manager does not separately track sessionID).
				m.onError(conversationID, conversationID, ownerOperatorID,
					"interactive session process exited unexpectedly")
			}
		}
	}()

	return sess, nil
}

// IsOwner reports whether a live session exists for conversationID and, if so,
// whether ownerOperatorID is its owner.
//
// This is a non-consuming read-only check intended for pre-flight ownership
// validation BEFORE calling ValidateAndConsume on the pending-question store.
// By checking ownership first, a non-owner attempt cannot consume (burn) the
// legitimate owner's pending-question entry.
//
// # Lock-free field read safety
//
// IsOwner takes the manager lock to look up the entry pointer, then drops it
// before reading entry.session fields.  This is safe because managerEntry.session
// is write-once: it is set at construction time and never mutated; only the map
// entry itself is replaced (by reaper or crash-detection goroutine removing it).
// Once IsOwner holds a stable pointer to *managerEntry, OwnerOperatorID() and
// IsClosed() read immutable / goroutine-safe fields on the Engine interface.
//
// The same stable-pointer reasoning applies to Send and AnswerQuestion; this
// comment documents the invariant for all three callers.
func (m *Manager) IsOwner(conversationID, operatorID string) (exists bool, owned bool) {
	m.mu.Lock()
	entry, ok := m.entries[conversationID]
	m.mu.Unlock()

	if !ok {
		return false, false
	}
	if entry.session.IsClosed() {
		return false, false
	}
	return true, entry.session.OwnerOperatorID() == operatorID
}

// AnswerQuestion delivers the operator's answer to an AskUserQuestion tool call
// on the session identified by conversationID.
//
// Returns ErrNoSession (→ 404) when no live session exists.
// Returns ErrOwnerConflict (→ 403) when the conversationID is owned by a
// different operator.
// Returns interactive.ErrAnswerUnsupported (→ 501) when the engine does not
// support answering (CLI engine).
// Returns ErrNoPendingQuestion (→ 404) when toolUseID does not match the
// session's currently pending question.
// Returns ErrAnswerAlreadyConsumed (→ 409) when the pending question was
// already answered.
// R4 invariant: the ownership check below reads entry.session fields after
// dropping the lock.  This is safe because managerEntry.session is write-once
// (set at construction, never mutated; only the map entry is replaced).
// See IsOwner for the full stable-pointer explanation.
func (m *Manager) AnswerQuestion(conversationID, ownerOperatorID, toolUseID string, answer QuestionAnswer) error {
	m.mu.Lock()
	entry, ok := m.entries[conversationID]
	m.mu.Unlock()

	if !ok {
		return ErrNoSession
	}
	if entry.session.OwnerOperatorID() != ownerOperatorID {
		return ErrOwnerConflict
	}
	if entry.session.IsClosed() {
		return ErrNoSession
	}
	return entry.session.AnswerQuestion(toolUseID, answer)
}

// Send delivers a user-turn frame to the session identified by conversationID.
//
// Returns ErrNoSession (→ 404) when no live session exists.
// Returns ErrOwnerConflict (→ 403) when the conversationID is owned by a
// different operator.
// Returns ErrTurnInFlight (→ 409) when a turn is already in progress.
// R4 invariant: the ownership check below reads entry.session fields after
// dropping the lock.  managerEntry.session is write-once (set at construction,
// never mutated); reading OwnerOperatorID() / IsClosed() off the stable pointer
// is safe without holding the lock.  See IsOwner for the full explanation.
func (m *Manager) Send(conversationID, ownerOperatorID string, frame []byte) error {
	m.mu.Lock()
	entry, ok := m.entries[conversationID]
	m.mu.Unlock()

	if !ok {
		return ErrNoSession
	}

	if entry.session.OwnerOperatorID() != ownerOperatorID {
		return ErrOwnerConflict
	}

	if entry.session.IsClosed() {
		m.mu.Lock()
		delete(m.entries, conversationID)
		m.mu.Unlock()
		return ErrNoSession
	}

	return entry.session.SendUserTurn(frame)
}

// Close closes the session for conversationID and removes it from the manager.
// No-op if no session exists.
func (m *Manager) Close(conversationID string) {
	m.mu.Lock()
	entry, ok := m.entries[conversationID]
	if ok {
		delete(m.entries, conversationID)
	}
	m.mu.Unlock()

	if ok {
		_ = entry.session.Close()
	}
}

// ActiveCount returns the number of currently tracked sessions.
func (m *Manager) ActiveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}

// reaper scans for idle sessions and closes them.
func (m *Manager) reaper(ctx context.Context) {
	ticker := time.NewTicker(m.reaperInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stop:
			return
		case <-ticker.C:
			m.reapOnce()
		}
	}
}

// reapOnce closes all sessions whose lastActivity is older than m.idleTimeout.
func (m *Manager) reapOnce() {
	cutoff := time.Now().Add(-m.idleTimeout)

	// Collect stale entries under the lock; close them outside.
	m.mu.Lock()
	var stale []struct {
		convID string
		sess   Engine
	}
	for convID, e := range m.entries {
		if e.session.LastActivity().Before(cutoff) {
			stale = append(stale, struct {
				convID string
				sess   Engine
			}{convID, e.session})
			delete(m.entries, convID)
		}
	}
	m.mu.Unlock()

	for _, s := range stale {
		slog.Info("interactive: reaping idle session",
			"conversationID", s.convID,
			"idle_since", s.sess.LastActivity(),
		)
		_ = s.sess.Close()
	}
}
