// Package terminalmanager implements ADR-0008 Phase 1 terminal session management.
//
// Two session modes are supported:
//
//   - Daemon-owned (CreateSession): the daemon forks the child process under a
//     local PTY. Output is read from the PTY master by a fanOut goroutine and
//     delivered to all subscribers. This path is preserved for tests and future
//     use but is NOT the active P1 share-terminal path.
//
//   - Externally-owned (RegisterExternalSession / T2): the login-shell yakos-start
//     owns the PTY and child process. It pushes PTY output bytes to the daemon
//     over the owner-only Unix socket (PushOutput / PushExit). The daemon fans
//     those bytes to browser /v1/term subscribers. The daemon never fork/execs
//     the child in this mode.
//
// # Session cap and idle reaper
//
// Modelled on internal/interactive/manager.go. A bounded cap (defaultCap)
// prevents accumulation. An idle reaper goroutine closes sessions that have
// been inactive for defaultIdleTimeout.
//
// # Build tags
//
// PTY allocation and process-group kill are POSIX-only. This file is built on
// all platforms; the platform-specific operations are in session_unix.go
// (//go:build !windows) and session_windows.go (//go:build windows).
package terminalmanager

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"
)

const (
	// defaultCap is the maximum number of concurrent terminal sessions.
	defaultCap = 4

	// defaultIdleTimeout is how long a session may be idle (no PTY output)
	// before the reaper closes it.
	defaultIdleTimeout = 30 * time.Minute

	// defaultReaperInterval is how often the idle reaper scans for stale sessions.
	defaultReaperInterval = 60 * time.Second

	// idleReapExitCode is the exit code delivered to subscribers of an
	// externally-owned session that the idle reaper closes. There is no real
	// process exit here (the daemon does not own the PTY for external
	// sessions), but subscribers must still receive a 0x01 frame so browser
	// clients stop believing the terminal is live (M8). 124 is the
	// conventional shell "command timed out" exit code.
	idleReapExitCode = 124

	// maxOwnerFramePayload is the maximum keystroke payload that SendInput will
	// accept.  It must be < math.MaxUint16 - 1 so the uint16 frame-length prefix
	// never overflows.  Kept well below the 64 KB WS bound as defense-in-depth.
	maxOwnerFramePayload = 32 * 1024 // 32 KB
)

// ErrCapExceeded is returned by CreateSession when the session cap is reached.
var ErrCapExceeded = errors.New("terminalmanager: session cap exceeded")

// ErrNotFound is returned by Get/Close/Resize when the sessionId is unknown.
var ErrNotFound = errors.New("terminalmanager: session not found")

// ErrNotSupported is returned on platforms that do not support PTY allocation
// (i.e. Windows).
var ErrNotSupported = errors.New("terminalmanager: web terminal is not supported on this platform")

// ErrOwnerMismatch is returned by ClaimOwner when a different operator
// already owns the session (see ClaimOwner).
var ErrOwnerMismatch = errors.New("terminalmanager: session is owned by a different operator")

// SpawnSpec describes the command to launch under the PTY.
type SpawnSpec struct {
	// Argv is the full argument vector including the binary (argv[0]).
	Argv []string
	// Cwd is the working directory for the child process.
	Cwd string
	// Env is the full environment slice ("KEY=VAL" strings).
	Env []string
	// WorkspaceRoot is recorded as metadata on the session (not passed to child).
	WorkspaceRoot string
	// Cols and Rows are the initial PTY dimensions. Defaults to 80×24.
	Cols uint16
	Rows uint16
}

// SessionMeta is the externally visible metadata for a live session.
type SessionMeta struct {
	SessionID     string    `json:"sessionId"`
	WorkspaceRoot string    `json:"workspaceRoot"`
	CreatedAt     time.Time `json:"createdAt"`
	// Argv is the argument vector (first element is the binary name).
	Argv []string `json:"argv"`
}

// Manager manages the lifecycle of PTY terminal sessions.
// All exported methods are safe for concurrent use.
type Manager struct {
	mu      sync.Mutex
	entries map[string]*session // sessionId → daemon-owned PTY session

	// externals holds externally-owned sessions (ADR-0008 Phase 1 T2).
	// The login-shell process owns the PTY; the daemon only relays output.
	externals map[string]*externalSession

	// owners maps sessionId to the OperatorID that first attached to it as a
	// viewer (see ClaimOwner). It closes the H2 finding
	// (security-review-2026-09-14.md): without it, any RoleAdmin identity
	// could inject keystrokes into, or read the scrollback of, any other
	// admin's live terminal session by guessing/listing its session ID.
	owners map[string]string

	cap            int
	idleTimeout    time.Duration
	reaperInterval time.Duration

	stop     chan struct{}
	stopOnce sync.Once
}

// Config holds configuration for New.
type Config struct {
	// Cap is the maximum number of concurrent sessions. Defaults to defaultCap (4).
	Cap int
	// IdleTimeout is the idle-reap threshold. Defaults to defaultIdleTimeout.
	IdleTimeout time.Duration
	// ReaperInterval is how often the idle reaper scans. Defaults to defaultReaperInterval.
	ReaperInterval time.Duration
}

// New creates a Manager and starts the idle-reaper goroutine.
// The reaper runs until ctx is cancelled or Stop is called.
func New(ctx context.Context, cfg Config) *Manager {
	cap := cfg.Cap
	if cap <= 0 {
		cap = defaultCap
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
		entries:        make(map[string]*session),
		externals:      make(map[string]*externalSession),
		owners:         make(map[string]string),
		cap:            cap,
		idleTimeout:    idleTimeout,
		reaperInterval: reaperInterval,
		stop:           make(chan struct{}),
	}
	go m.reaper(ctx)
	return m
}

// Stop terminates the idle reaper and closes all active sessions.
// Safe to call multiple times; subsequent calls are no-ops.
func (m *Manager) Stop() {
	m.stopOnce.Do(func() {
		close(m.stop)
		m.mu.Lock()
		ids := make([]string, 0, len(m.entries))
		for id := range m.entries {
			ids = append(ids, id)
		}
		m.mu.Unlock()
		for _, id := range ids {
			_ = m.Close(id)
		}
	})
}

// CreateSession spawns a new PTY session from spec and returns its sessionId.
// Returns ErrCapExceeded when the cap is reached.
// Returns ErrNotSupported on Windows.
func (m *Manager) CreateSession(spec SpawnSpec) (string, error) {
	m.mu.Lock()
	if len(m.entries) >= m.cap {
		m.mu.Unlock()
		return "", ErrCapExceeded
	}
	m.mu.Unlock()

	s, err := newSession(spec)
	if err != nil {
		return "", fmt.Errorf("terminalmanager: spawn: %w", err)
	}

	m.mu.Lock()
	// Re-check cap under lock (TOCTOU guard).
	if len(m.entries) >= m.cap {
		m.mu.Unlock()
		_ = s.close()
		return "", ErrCapExceeded
	}
	m.entries[s.id] = s
	m.mu.Unlock()

	// Fan-out goroutine: reads PTY output and delivers to sinks.
	go m.fanOut(s)

	return s.id, nil
}

// Get returns the metadata for a session (daemon-owned or externally-owned).
// Returns ErrNotFound if unknown.
func (m *Manager) Get(sessionId string) (SessionMeta, error) {
	m.mu.Lock()
	s, ok := m.entries[sessionId]
	if !ok {
		ext, extOk := m.externals[sessionId]
		m.mu.Unlock()
		if !extOk {
			return SessionMeta{}, ErrNotFound
		}
		return ext.meta(), nil
	}
	m.mu.Unlock()
	return s.meta(), nil
}

// List returns metadata for all active sessions (daemon-owned and externally-owned).
func (m *Manager) List() []SessionMeta {
	m.mu.Lock()
	result := make([]SessionMeta, 0, len(m.entries)+len(m.externals))
	for _, s := range m.entries {
		result = append(result, s.meta())
	}
	for _, s := range m.externals {
		result = append(result, s.meta())
	}
	m.mu.Unlock()
	return result
}

// Subscribe registers output and exit callbacks for a session (daemon-owned or
// externally-owned).
//
// outputFn is called with each raw PTY output chunk (no framing tags; just the
// bytes the child process wrote to the terminal).  It must not retain the slice
// across the call — copy if needed.
//
// exitFn is called exactly once with the child's exit code when the process
// exits.  It is called via a DIFFERENT code path than outputFn so PTY output
// bytes can never be confused with an exit notification, regardless of their
// content.
//
// Either callback may be nil; a nil callback is silently skipped.
//
// Returns ErrNotFound if the session does not exist.
func (m *Manager) Subscribe(sessionId string, outputFn func([]byte), exitFn func(int)) (func(), error) {
	m.mu.Lock()
	s, ok := m.entries[sessionId]
	if !ok {
		ext, extOk := m.externals[sessionId]
		m.mu.Unlock()
		if !extOk {
			return nil, ErrNotFound
		}
		unsub := ext.subscribe(outputFn, exitFn)
		return unsub, nil
	}
	m.mu.Unlock()
	unsub := s.subscribe(outputFn, exitFn)
	return unsub, nil
}

// ClaimOwner records operatorID as the owner of sessionId the first time it
// is called for that session, and verifies the match on every subsequent
// call (e.g. a browser reconnect from the same operator).
//
// Callers (the /v1/term WS handler) must call this BEFORE Subscribe, and
// must deny the attach entirely — never subscribing, never replaying
// scrollback — when it returns ErrOwnerMismatch. This closes H2
// (security-review-2026-09-14.md): without an owner lock, any RoleAdmin
// identity could list session IDs via GET /api/term and then attach to any
// other admin's live session, injecting keystrokes into a
// bypassPermissions shell and reading up to 512 KB of that admin's prior
// terminal output (tokens, env dumps, etc.) via the scrollback replay.
//
// This intentionally has no "takeover" path: the codebase has no existing
// admin-override concept for terminal sessions, so a mismatch is a hard
// deny (fail closed) rather than silently displacing the current owner.
//
// Returns ErrNotFound if the session does not exist (daemon-owned or
// externally-owned), ErrOwnerMismatch if a different operator already
// owns it, or nil if operatorID is now (or already was) the owner.
func (m *Manager) ClaimOwner(sessionId, operatorID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.entries[sessionId]; !ok {
		if _, ok := m.externals[sessionId]; !ok {
			return ErrNotFound
		}
	}
	if existing, has := m.owners[sessionId]; has {
		if existing != operatorID {
			return ErrOwnerMismatch
		}
		return nil
	}
	m.owners[sessionId] = operatorID
	return nil
}

// ---- externally-owned session API (ADR-0008 Phase 1 T2) ----------------------

// RegisterExternalSession registers a new externally-owned session with the
// given sessionId.  The caller (start.go) owns the PTY and child process; the
// daemon only fans output pushed via PushOutput to browser subscribers.
//
// Returns ErrCapExceeded when the combined (daemon + external) session count
// reaches cap.  Returns an error if sessionId is already registered.
func (m *Manager) RegisterExternalSession(sessionId, workspaceRoot string, argv []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.entries)+len(m.externals) >= m.cap {
		return ErrCapExceeded
	}
	if _, dup := m.externals[sessionId]; dup {
		return fmt.Errorf("terminalmanager: session %q already registered", sessionId)
	}
	if _, dup := m.entries[sessionId]; dup {
		return fmt.Errorf("terminalmanager: session %q already registered", sessionId)
	}
	m.externals[sessionId] = newExternalSession(sessionId, workspaceRoot, argv)
	return nil
}

// PushOutput delivers a PTY output chunk for an externally-owned session to all
// registered subscribers.  Called by the daemon-side push transport when start
// sends a 0x00 frame.  Returns ErrNotFound if the session is unknown.
func (m *Manager) PushOutput(sessionId string, chunk []byte) error {
	m.mu.Lock()
	ext, ok := m.externals[sessionId]
	m.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	ext.pushOutput(chunk)
	return nil
}

// PushExit signals exit for an externally-owned session and removes it from the
// manager.  Called by the daemon-side push transport when start sends a 0x01
// frame.  Returns ErrNotFound if the session is unknown.
func (m *Manager) PushExit(sessionId string, exitCode int) error {
	m.mu.Lock()
	ext, ok := m.externals[sessionId]
	if ok {
		delete(m.externals, sessionId)
		delete(m.owners, sessionId)
	}
	m.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	ext.pushExit(exitCode)
	slog.Debug("terminalmanager: external session exited", "sessionId", sessionId, "exitCode", exitCode)
	return nil
}

// CloseExternal removes an externally-owned session without sending an exit
// notification (used on transport disconnect without a clean 0x01 close).
// Returns ErrNotFound if unknown.
func (m *Manager) CloseExternal(sessionId string) error {
	m.mu.Lock()
	ext, ok := m.externals[sessionId]
	if ok {
		delete(m.externals, sessionId)
		delete(m.owners, sessionId)
	}
	m.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	ext.close()
	return nil
}

// ---- Phase 2 back-channel: browser input → start PTY ------------------------

// SetAttachConn records (or clears) the hijacked attach conn for an
// externally-owned session.  Called by runPushTransport after the session is
// registered (conn != nil) and again on transport exit (conn == nil).
//
// Returns ErrNotFound if the session is unknown.
func (m *Manager) SetAttachConn(sessionId string, conn net.Conn) error {
	m.mu.Lock()
	ext, ok := m.externals[sessionId]
	m.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	ext.setAttachConn(conn)
	return nil
}

// SendInput delivers browser keystroke bytes to the session's owning start
// process via the attach conn back-channel.
//
// Owner-scoping: only externally-owned sessions (m.externals) are reachable.
// Daemon-owned sessions (m.entries) are NOT reachable from the browser input
// path — the browser writer must not be able to inject keystrokes into a
// daemon-owned PTY.  Callers that need to write to a daemon-owned session
// must use Manager.WriteInput (the owner-only attach path).
//
// Frame format sent to start:
//
//	[ 2-byte BE total length ] [ 0x10 ] [ keystroke bytes ]
//
// Returns ErrNotFound if the session is unknown.
func (m *Manager) SendInput(sessionId string, data []byte) error {
	if len(data) > maxOwnerFramePayload {
		return fmt.Errorf("terminalmanager: SendInput payload too large: %d bytes (max %d)", len(data), maxOwnerFramePayload)
	}
	m.mu.Lock()
	ext, ok := m.externals[sessionId]
	m.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	// Build: [2-byte BE total len][tag=0x10][payload]
	total := uint16(1 + len(data))
	frame := make([]byte, 2+int(total))
	binary.BigEndian.PutUint16(frame[0:2], total)
	frame[2] = 0x10
	copy(frame[3:], data)
	return ext.sendToOwner(frame)
}

// SendResize delivers a browser resize event to the session's owning start
// process via the attach conn back-channel.
//
// Owner-scoping: only externally-owned sessions (m.externals) are reachable
// (same isolation guarantee as SendInput).
//
// Frame format sent to start:
//
//	[ 2-byte BE total length=5 ] [ 0x11 ] [ cols uint16 BE ] [ rows uint16 BE ]
//
// Last-writer-wins for resize: local SIGWINCH and browser 0x11 can both call
// pty.Setsize on start's side; the last one wins — correct behavior for a
// shared terminal.
//
// Returns ErrNotFound if the session is unknown.
func (m *Manager) SendResize(sessionId string, cols, rows uint16) error {
	m.mu.Lock()
	ext, ok := m.externals[sessionId]
	m.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	// Build: [2-byte BE total len=5][tag=0x11][cols uint16 BE][rows uint16 BE]
	frame := make([]byte, 7) // 2 (len prefix) + 5 (tag + 4 payload)
	binary.BigEndian.PutUint16(frame[0:2], 5)
	frame[2] = 0x11
	binary.BigEndian.PutUint16(frame[3:5], cols)
	binary.BigEndian.PutUint16(frame[5:7], rows)
	return ext.sendToOwner(frame)
}

// ---- daemon-owned input path (preserved for tests / future use) --------------

// WriteInput writes raw bytes to the PTY master (stdin of the child process).
//
// This is the owner-only input path.  WriteInput has exactly one caller —
// runAttachTransport in serve/term_transport_unix.go — which is reachable only
// via the owner-only Unix-socket attach hijack (mode-0600 socket, operator UID).
//
// The browser WebSocket handler (consoleui/term_ws_handler.go) is write-isolated:
// it calls only Manager.Subscribe and Manager.Get; it discards every inbound
// WebSocket frame (Phase 1: output-only).  WriteInput does not appear in any
// file under internal/consoleui/.
//
// Returns ErrNotFound if the session is unknown.
// Returns ErrNotSupported on Windows.
func (m *Manager) WriteInput(sessionId string, data []byte) error {
	m.mu.Lock()
	s, ok := m.entries[sessionId]
	m.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	return s.writeInput(data)
}

// Resize sends a resize event to the PTY master for the given session.
// Only the owning pump should call this (P1); browser resize is Phase 2.
// Returns ErrNotFound if the session is unknown.
func (m *Manager) Resize(sessionId string, cols, rows uint16) error {
	m.mu.Lock()
	s, ok := m.entries[sessionId]
	m.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	return s.resize(cols, rows)
}

// Close terminates the session (kills the child process group) and removes it
// from the manager. Returns ErrNotFound if already closed.
func (m *Manager) Close(sessionId string) error {
	m.mu.Lock()
	s, ok := m.entries[sessionId]
	if ok {
		delete(m.entries, sessionId)
		delete(m.owners, sessionId)
	}
	m.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	return s.close()
}

// ExitCode returns the exit code for a completed session.
// Returns ErrNotFound if the session is unknown or still running.
func (m *Manager) ExitCode(sessionId string) (int, error) {
	m.mu.Lock()
	s, ok := m.entries[sessionId]
	m.mu.Unlock()
	if !ok {
		return -1, ErrNotFound
	}
	return s.exitCode()
}

// fanOut reads PTY output and delivers it to all registered sinks until the
// session closes.
func (m *Manager) fanOut(s *session) {
	buf := make([]byte, 32*1024)
	for {
		n, err := s.read(buf)
		if n > 0 {
			s.touchActivity()
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			s.deliver(chunk)
		}
		if err != nil {
			break
		}
	}
	// PTY EOF: the child process has exited (or PTY was closed).
	// Deliver the exit frame to all sinks.
	code, _ := s.waitExit()
	s.deliverExit(code)
	slog.Debug("terminalmanager: session exited", "sessionId", s.id, "exitCode", code)

	// Remove from the manager.
	m.mu.Lock()
	delete(m.entries, s.id)
	m.mu.Unlock()
}

// reaper closes sessions that have been idle for longer than idleTimeout.
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
			m.reapIdle()
		}
	}
}

func (m *Manager) reapIdle() {
	m.mu.Lock()
	var stale []string
	var staleSessions []*session
	var staleExt []string
	var staleExtSessions []*externalSession
	now := time.Now()
	for id, s := range m.entries {
		if now.Sub(s.lastActivity()) > m.idleTimeout {
			stale = append(stale, id)
			staleSessions = append(staleSessions, s)
		}
	}
	for id, s := range m.externals {
		if now.Sub(s.lastActivity()) > m.idleTimeout {
			staleExt = append(staleExt, id)
			staleExtSessions = append(staleExtSessions, s)
		}
	}
	for _, id := range stale {
		delete(m.entries, id)
		delete(m.owners, id)
	}
	for _, id := range staleExt {
		delete(m.externals, id)
		delete(m.owners, id)
	}
	m.mu.Unlock()

	// SECURITY/CORRECTNESS (M8): actually close each reaped session instead
	// of only deleting the map entry. Before this fix, daemon-owned PTYs and
	// their child process groups leaked (the fanOut goroutine and the child
	// process ran forever), and external subscribers never received the 0x01
	// exit frame, so browser clients kept believing the terminal was live.
	// Mirrors what Close()/CloseExternal() do for an explicit close, done
	// here without holding m.mu since close() may block briefly on I/O.
	for i, id := range stale {
		slog.Info("terminalmanager: idle reaper closing session", "sessionId", id)
		_ = staleSessions[i].close()
	}
	for i, id := range staleExt {
		slog.Info("terminalmanager: idle reaper closing external session", "sessionId", id)
		staleExtSessions[i].pushExit(idleReapExitCode)
	}
}
