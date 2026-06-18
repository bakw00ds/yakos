//go:build windows

package terminalmanager

import (
	"fmt"
	"sync"
	"time"
)

// sessionSink is the Windows stub for the split-sink type defined in session_unix.go.
type sessionSink struct {
	outputFn func([]byte)
	exitFn   func(int)
}

// session is the Windows stub for a PTY-backed terminal session.
// PTY allocation is not supported on Windows; all operations return
// ErrNotSupported.
type session struct {
	id        string
	spec      SpawnSpec
	createdAt time.Time
	exitCh    chan struct{}
	sinksMu   sync.Mutex
	sinks     map[uint64]sessionSink
	nextKey   uint64
	actMu     sync.Mutex
	lastAct   time.Time
	exitMu    sync.Mutex
	exitOnce  sync.Once
	code      int
}

func newSession(spec SpawnSpec) (*session, error) {
	return nil, ErrNotSupported
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
	return 0, ErrNotSupported
}

func (s *session) subscribe(outputFn func([]byte), exitFn func(int)) func() {
	return func() {}
}

func (s *session) deliver(chunk []byte) {}

func (s *session) deliverExit(code int) {}

func (s *session) resize(cols, rows uint16) error {
	return ErrNotSupported
}

func (s *session) writeInput(data []byte) error {
	return ErrNotSupported
}

func (s *session) close() error {
	return ErrNotSupported
}

func (s *session) waitExit() (int, error) {
	return -1, ErrNotSupported
}

func (s *session) exitCode() (int, error) {
	return -1, fmt.Errorf("session still running")
}

func (s *session) touchActivity() {}

func (s *session) lastActivity() time.Time {
	return s.createdAt
}
