// Package terminalmanager implements ADR-0008 Phase 1 daemon-side PTY ownership.
//
// The Manager maintains a bounded set of TermSessions. Each TermSession owns
// a PTY master (via github.com/creack/pty) and drives a single `claude`
// (or any other) child process under that PTY. A single goroutine fans PTY
// output bytes to all registered sinks (local pump callbacks + browser WS
// viewers).
//
// Phase 1 scope: output-only. Browser viewers receive 0x00 frames (raw PTY
// bytes) and 0x01 frames (close/exit-code). Input path (0x10/0x11 frames) is
// wired for the local pump only (accept resize from the owning pump); browser
// keystroke forwarding is a Phase 2 concern.
//
// # Session cap and idle reaper
//
// Modelled on internal/interactive/manager.go. A bounded cap (defaultCap)
// prevents accumulation. An idle reaper goroutine closes sessions that have
// been inactive for defaultIdleTimeout. Inactivity means no output was
// produced (the child may be waiting for input — this matches the
// interactive/manager.go pattern).
//
// # Process-group kill
//
// The child is started in its own process group (SysProcAttr.Setpgid=true).
// On close, the manager kills the process group (kill(-pgid, SIGKILL)) so no
// zombie sub-processes are left behind.
//
// # Build tags
//
// PTY allocation and process-group kill are POSIX-only. This file is built on
// all platforms; the platform-specific PTY operations are in session_unix.go
// (//go:build !windows) and session_windows.go (//go:build windows).
package terminalmanager

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
)

// ErrCapExceeded is returned by CreateSession when the session cap is reached.
var ErrCapExceeded = errors.New("terminalmanager: session cap exceeded")

// ErrNotFound is returned by Get/Close/Resize when the sessionId is unknown.
var ErrNotFound = errors.New("terminalmanager: session not found")

// ErrNotSupported is returned on platforms that do not support PTY allocation
// (i.e. Windows).
var ErrNotSupported = errors.New("terminalmanager: web terminal is not supported on this platform")

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
	entries map[string]*session // sessionId → session

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

// Get returns the metadata for a session. Returns ErrNotFound if unknown.
func (m *Manager) Get(sessionId string) (SessionMeta, error) {
	m.mu.Lock()
	s, ok := m.entries[sessionId]
	m.mu.Unlock()
	if !ok {
		return SessionMeta{}, ErrNotFound
	}
	return s.meta(), nil
}

// List returns metadata for all active sessions.
func (m *Manager) List() []SessionMeta {
	m.mu.Lock()
	result := make([]SessionMeta, 0, len(m.entries))
	for _, s := range m.entries {
		result = append(result, s.meta())
	}
	m.mu.Unlock()
	return result
}

// Subscribe registers output and exit callbacks for a session.
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
	m.mu.Unlock()
	if !ok {
		return nil, ErrNotFound
	}
	unsub := s.subscribe(outputFn, exitFn)
	return unsub, nil
}

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
	now := time.Now()
	for id, s := range m.entries {
		if now.Sub(s.lastActivity()) > m.idleTimeout {
			stale = append(stale, id)
		}
	}
	for _, id := range stale {
		delete(m.entries, id)
	}
	m.mu.Unlock()

	for _, id := range stale {
		// session.close already handles nil gracefully via the platform stubs.
		slog.Info("terminalmanager: idle reaper closing session", "sessionId", id)
	}
}
