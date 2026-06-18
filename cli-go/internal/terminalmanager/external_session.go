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

// scrollbackCap is the hard upper bound on buffered PTY bytes retained for
// late-joining browser subscribers.  When the ring buffer exceeds this limit,
// the oldest chunks are evicted until the total is at or below the cap.
const scrollbackCap = 512 * 1024 // 512 KB

// externalSession is an output-relay session with no local PTY or process.
// Subscribers receive output pushed in from outside via pushOutput/pushExit.
//
// # Scrollback ring buffer
//
// scrollback is a slice of owned byte chunks in push order.  scrollbackBytes
// tracks the sum of len(chunk) for all retained chunks.  pushOutput appends to
// it under sinksMu; subscribe replays it to the new outputFn before adding the
// sink — all under a single sinksMu critical section — so no chunk is ever
// lost or double-delivered at join time.
type externalSession struct {
	id        string
	createdAt time.Time
	argv      []string // stored for SessionMeta only
	workspace string

	// sinksMu guards sinks, nextKey, scrollback, and scrollbackBytes.
	sinksMu         sync.Mutex
	sinks           map[uint64]sessionSink
	nextKey         uint64
	scrollback      [][]byte
	scrollbackBytes int

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

// subscribe registers outputFn and exitFn as a new sink.
//
// Before adding the sink to the live set, subscribe replays the entire
// scrollback buffer to outputFn.  The replay and the sink registration both
// happen under a single sinksMu critical section, which guarantees:
//
//   - No chunk pushed concurrently with subscribe is dropped: if pushOutput
//     acquires sinksMu first, the chunk lands in both the live fan-out (to any
//     existing sinks) and the scrollback before subscribe sees the lock; when
//     subscribe then replays, that chunk is included.  If subscribe acquires
//     sinksMu first, the chunk arrives after the new sink is already registered
//     and is delivered via the normal live fan-out.
//
//   - No chunk is double-delivered: the new sink is absent from s.sinks during
//     the replay, so the live fan-out inside pushOutput cannot reach it.
func (s *externalSession) subscribe(outputFn func([]byte), exitFn func(int)) func() {
	s.sinksMu.Lock()

	// 1. Replay scrollback to the new subscriber before it is visible to pushOutput.
	if outputFn != nil {
		for _, chunk := range s.scrollback {
			cp := make([]byte, len(chunk))
			copy(cp, chunk)
			outputFn(cp)
		}
	}

	// 2. Register the sink in the live set.
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

// pushOutput delivers a chunk of PTY output to all registered subscribers and
// appends a copy to the scrollback ring buffer.
// Called by the daemon-side push transport when start sends a 0x00 frame.
func (s *externalSession) pushOutput(chunk []byte) {
	s.actMu.Lock()
	s.lastAct = time.Now()
	s.actMu.Unlock()

	// Store an owned copy before acquiring sinksMu so the copy cost is outside
	// the critical section.
	stored := make([]byte, len(chunk))
	copy(stored, chunk)

	s.sinksMu.Lock()

	// Append to scrollback and evict oldest chunks if the cap is exceeded.
	s.scrollback = append(s.scrollback, stored)
	s.scrollbackBytes += len(stored)
	for s.scrollbackBytes > scrollbackCap && len(s.scrollback) > 0 {
		s.scrollbackBytes -= len(s.scrollback[0])
		s.scrollback[0] = nil // release reference
		s.scrollback = s.scrollback[1:]
	}

	// Snapshot live sinks while still under the lock.
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
