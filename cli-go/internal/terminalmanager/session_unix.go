//go:build !windows

package terminalmanager

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// session is the POSIX implementation of a PTY-backed terminal session.
// It owns the PTY master fd and the child process.
//
// ADR-0008 Phase 1 T2 note: the daemon-owned PTY path (newSession) is preserved
// for tests and future use but is NOT invoked in the P1 share-terminal flow.
// The P1 flow uses externalSession instead — start.go owns the PTY, the daemon
// only fans output to browser subscribers.
type session struct {
	id        string
	spec      SpawnSpec
	createdAt time.Time

	ptmx *os.File // PTY master
	cmd  *os.Process
	pgid int // process group id for kill

	// sinks is the set of registered output consumers.
	// outputFn receives raw PTY bytes; exitFn receives the exit code.
	// Keeping them separate ensures the two signals can never be confused.
	sinksMu sync.Mutex
	sinks   map[uint64]sessionSink
	nextKey uint64

	// exitCode is populated after the child process exits.
	exitMu   sync.Mutex
	exitOnce sync.Once
	exitCh   chan struct{}
	code     int

	// lastAct is updated on each PTY read (for idle reaping).
	actMu   sync.Mutex
	lastAct time.Time
}

// sessionSink bundles the two callbacks a subscriber provides.
// outputFn is called with raw PTY bytes; exitFn is called with the exit code.
// Using separate functions eliminates any need to inspect payload content to
// distinguish output from exit events.
type sessionSink struct {
	outputFn func([]byte)
	exitFn   func(int)
}

func newSession(spec SpawnSpec) (*session, error) {
	if len(spec.Argv) == 0 {
		return nil, fmt.Errorf("Argv must not be empty")
	}
	cols := spec.Cols
	if cols == 0 {
		cols = 80
	}
	rows := spec.Rows
	if rows == 0 {
		rows = 24
	}

	id, err := newSessionID()
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...) //nolint:gosec
	cmd.Dir = spec.Cwd
	cmd.Env = spec.Env
	// Run the child in its own process group so we can kill the whole group.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return nil, fmt.Errorf("pty.StartWithSize: %w", err)
	}

	s := &session{
		id:        id,
		spec:      spec,
		createdAt: time.Now(),
		ptmx:      ptmx,
		cmd:       cmd.Process,
		sinks:     make(map[uint64]sessionSink),
		exitCh:    make(chan struct{}),
		lastAct:   time.Now(),
	}

	// Capture the child process group id.
	// Setpgid=true causes the child's pgid == child's pid.
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err == nil {
		s.pgid = pgid
	} else {
		s.pgid = cmd.Process.Pid
	}

	// Wait goroutine: reaps the child and closes exitCh.
	go func() {
		state, _ := cmd.Process.Wait()
		code := 0
		if state != nil {
			code = state.ExitCode()
		}
		s.exitMu.Lock()
		s.code = code
		s.exitMu.Unlock()
		s.exitOnce.Do(func() { close(s.exitCh) })
	}()

	return s, nil
}

// newSessionID generates a random 16-hex-char session identifier.
func newSessionID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("newSessionID: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func (s *session) meta() SessionMeta {
	return SessionMeta{
		SessionID:     s.id,
		WorkspaceRoot: s.spec.WorkspaceRoot,
		CreatedAt:     s.createdAt,
		Argv:          s.spec.Argv,
	}
}

func (s *session) read(buf []byte) (int, error) {
	return s.ptmx.Read(buf)
}

func (s *session) subscribe(outputFn func([]byte), exitFn func(int)) func() {
	s.sinksMu.Lock()
	key := s.nextKey
	s.nextKey++
	s.sinks[key] = sessionSink{outputFn: outputFn, exitFn: exitFn}
	s.sinksMu.Unlock()
	return func() {
		s.sinksMu.Lock()
		delete(s.sinks, key)
		s.sinksMu.Unlock()
	}
}

func (s *session) deliver(chunk []byte) {
	s.sinksMu.Lock()
	snaps := make([]sessionSink, 0, len(s.sinks))
	for _, sk := range s.sinks {
		snaps = append(snaps, sk)
	}
	s.sinksMu.Unlock()
	for _, sk := range snaps {
		if sk.outputFn != nil {
			sk.outputFn(chunk)
		}
	}
}

func (s *session) deliverExit(code int) {
	s.sinksMu.Lock()
	snaps := make([]sessionSink, 0, len(s.sinks))
	for _, sk := range s.sinks {
		snaps = append(snaps, sk)
	}
	s.sinksMu.Unlock()
	for _, sk := range snaps {
		if sk.exitFn != nil {
			sk.exitFn(code)
		}
	}
}

func (s *session) resize(cols, rows uint16) error {
	return pty.Setsize(s.ptmx, &pty.Winsize{Cols: cols, Rows: rows})
}

// writeInput writes raw bytes to the PTY master (stdin of the child process).
// Only the owner-socket attach transport may call this; the browser WS path
// has no access (consoleui never imports this unexported method path).
func (s *session) writeInput(data []byte) error {
	_, err := s.ptmx.Write(data)
	return err
}

func (s *session) close() error {
	// Close PTY master — causes child's stdin/stdout/stderr EOF.
	_ = s.ptmx.Close()
	// Kill the process group to ensure no orphaned children.
	if s.pgid > 0 {
		_ = syscall.Kill(-s.pgid, syscall.SIGKILL)
	} else if s.cmd != nil {
		_ = s.cmd.Kill()
	}
	// Drain the wait goroutine.
	s.exitOnce.Do(func() { close(s.exitCh) })
	return nil
}

func (s *session) waitExit() (int, error) {
	<-s.exitCh
	s.exitMu.Lock()
	code := s.code
	s.exitMu.Unlock()
	return code, nil
}

func (s *session) exitCode() (int, error) {
	select {
	case <-s.exitCh:
		s.exitMu.Lock()
		code := s.code
		s.exitMu.Unlock()
		return code, nil
	default:
		return -1, fmt.Errorf("session still running")
	}
}

func (s *session) touchActivity() {
	s.actMu.Lock()
	s.lastAct = time.Now()
	s.actMu.Unlock()
}

func (s *session) lastActivity() time.Time {
	s.actMu.Lock()
	t := s.lastAct
	s.actMu.Unlock()
	return t
}

// externalSession is defined in external_session.go (no build tag — cross-platform).
