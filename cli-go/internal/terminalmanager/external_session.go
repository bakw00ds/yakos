// Package terminalmanager — external_session.go
//
// externalSession implements the ADR-0008 Phase 1 T2 externally-owned session,
// extended in Phase 2 with a bidirectional back-channel (attachConn) that lets
// the daemon forward browser input (0x10 keystrokes) and resize events (0x11)
// back to the start process that owns the PTY.
//
// An externalSession has no local PTY or child process.  The owning login-shell
// process (yakos start) spawns the child under its own PTY and pushes output
// bytes to the daemon via Manager.PushOutput.  The daemon fans those bytes to
// browser /v1/term subscribers.
//
// Phase 2 back-channel write path:
//
//	browser 0x10/0x11 frame
//	→ WS handler (RoleAdmin gate)
//	→ mgr.SendInput / mgr.SendResize
//	→ externalSession.sendToOwner(frame)
//	→ attachConn.Write(frame)         [daemon side of net.Conn]
//	→ start readDaemonFrames goroutine [reads from conn]
//	→ ptmx.Write / pty.Setsize         [PTY stdin / window size]
//
// Thread safety: attachMu guards the conn pointer (set/clear/nil-check) only.
// sendMu serializes concurrent Write calls so that multiple RoleAdmin
// goroutines writing to the same attachConn cannot interleave
// length-prefixed frames (Fix 1 — HIGH, ADR-0008 P2 security review).
// Lock ordering is deadlock-free: attachMu is acquired, the conn pointer is
// copied, attachMu is released, THEN sendMu is acquired.  No code path holds
// both locks simultaneously.
//
// This file is built on all platforms (no build tags) because the subscriber
// fan-out plumbing (sinks, pushOutput, pushExit) has no OS-specific dependencies.

package terminalmanager

import (
	"fmt"
	"net"
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
//
// # Phase 2 back-channel
//
// attachConn is the hijacked net.Conn from runPushTransport.  It is set once
// after the session registers and before any 0x00/0x01 frames are read.  It is
// cleared (set to nil) when runPushTransport exits so that sendToOwner returns
// an error rather than writing to a closed connection.
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

	// attachMu guards the attachConn pointer only — not the Write call.
	// See sendMu for write serialization.
	attachMu   sync.Mutex
	attachConn net.Conn // the hijacked conn from term.attach; nil until registered

	// sendMu serializes conn.Write calls so that N concurrent RoleAdmin
	// goroutines calling sendToOwner cannot interleave length-prefixed frames.
	// It is NEVER held at the same time as attachMu: attachMu is released
	// before sendMu is acquired (deadlock-free by construction).
	sendMu sync.Mutex
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

// setAttachConn records (or clears) the hijacked conn from runPushTransport.
// Called once after session registration (conn != nil) and again on transport
// exit (conn == nil) to prevent stale writes after the session is gone.
func (s *externalSession) setAttachConn(conn net.Conn) {
	s.attachMu.Lock()
	s.attachConn = conn
	s.attachMu.Unlock()
}

// sendToOwner writes a fully-formed length-prefixed frame to the attach conn
// (the start process).  The frame must already include the 2-byte big-endian
// length prefix, tag byte, and payload — matching the wire format used by
// runPushTransport (see term_transport_unix.go).
//
// Returns an error if the conn is nil (not yet registered, or already cleared
// after transport exit) or if the Write fails.
//
// Thread safety: attachMu guards the pointer read; sendMu serializes the Write
// so that N concurrent callers (N RoleAdmin browser goroutines on the same
// session) cannot interleave frames.  Lock ordering: attachMu → released →
// sendMu acquired.  The two locks are never held simultaneously.
func (s *externalSession) sendToOwner(frame []byte) error {
	s.attachMu.Lock()
	conn := s.attachConn
	s.attachMu.Unlock()

	if conn == nil {
		return fmt.Errorf("terminalmanager: sendToOwner: no attach conn for session %q", s.id)
	}
	s.sendMu.Lock()
	_, err := conn.Write(frame)
	s.sendMu.Unlock()
	return err
}
