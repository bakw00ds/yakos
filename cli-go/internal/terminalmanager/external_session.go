// Package terminalmanager — external_session.go
//
// externalSession implements the ADR-0008 Phase 1 T2 externally-owned session.
//
// An externalSession has no local PTY or child process.  The owning login-shell
// process (yakos start) spawns the child under its own PTY and pushes output
// bytes to the daemon via Manager.PushOutput.  The daemon fans those bytes to
// browser /v1/term subscribers.
//
// This file is built on all platforms (no build tags) because the subscriber
// fan-out plumbing (sinks, pushOutput, pushExit) has no OS-specific dependencies.

package terminalmanager

import (
	"sync"
	"time"
)

// externalSession is an output-relay session with no local PTY or process.
// Subscribers receive output pushed in from outside via pushOutput/pushExit.
type externalSession struct {
	id        string
	createdAt time.Time
	argv      []string // stored for SessionMeta only
	workspace string

	sinksMu sync.Mutex
	sinks   map[uint64]sessionSink
	nextKey uint64

	exitOnce sync.Once
	exitCh   chan struct{}

	actMu   sync.Mutex
	lastAct time.Time
}

func newExternalSession(id, workspaceRoot string, argv []string) *externalSession {
	return &externalSession{
		id:        id,
		createdAt: time.Now(),
		argv:      argv,
		workspace: workspaceRoot,
		sinks:     make(map[uint64]sessionSink),
		exitCh:    make(chan struct{}),
		lastAct:   time.Now(),
	}
}

func (s *externalSession) meta() SessionMeta {
	return SessionMeta{
		SessionID:     s.id,
		WorkspaceRoot: s.workspace,
		CreatedAt:     s.createdAt,
		Argv:          s.argv,
	}
}

func (s *externalSession) subscribe(outputFn func([]byte), exitFn func(int)) func() {
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

// pushOutput delivers a chunk of PTY output to all registered subscribers.
// Called by the daemon-side push transport when start sends a 0x00 frame.
func (s *externalSession) pushOutput(chunk []byte) {
	s.actMu.Lock()
	s.lastAct = time.Now()
	s.actMu.Unlock()

	s.sinksMu.Lock()
	snaps := make([]sessionSink, 0, len(s.sinks))
	for _, sk := range s.sinks {
		snaps = append(snaps, sk)
	}
	s.sinksMu.Unlock()
	for _, sk := range snaps {
		if sk.outputFn != nil {
			cp := make([]byte, len(chunk))
			copy(cp, chunk)
			sk.outputFn(cp)
		}
	}
}

// pushExit signals session exit to all registered subscribers, then closes exitCh.
// Called by the daemon-side push transport when start sends a 0x01 frame.
func (s *externalSession) pushExit(code int) {
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
	s.exitOnce.Do(func() { close(s.exitCh) })
}

// close signals exit without notifying subscribers (used on unclean disconnect).
func (s *externalSession) close() {
	s.exitOnce.Do(func() { close(s.exitCh) })
}

func (s *externalSession) lastActivity() time.Time {
	s.actMu.Lock()
	t := s.lastAct
	s.actMu.Unlock()
	return t
}
