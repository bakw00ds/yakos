//go:build !windows

// Package serve — term_transport_unix.go
//
// Two transport functions are defined here:
//
//  1. runPushTransport — ADR-0008 Phase 1/2 T2 (active path).
//     Data flows start → daemon: start sends length-prefixed 0x00/0x01 frames;
//     daemon calls Manager.PushOutput / Manager.PushExit to fan output to
//     browser /v1/term subscribers.  The daemon NEVER fork/execs the child
//     process in this path.
//
//     Phase 2 adds a back-channel (daemon → start): after the session is
//     registered, the hijacked conn is recorded via mgr.SetAttachConn so that
//     mgr.SendInput / mgr.SendResize can write 0x10/0x11 frames back to start.
//     The start-side readDaemonFrames goroutine (pump_unix.go) reads these
//     frames and applies them to the PTY.
//
//  2. runAttachTransport — T1 daemon-owned PTY path (preserved for tests).
//     Data flows daemon → client: daemon reads PTY output and sends it over the
//     connection.  NOT used in the active P1/P2 share-terminal flow; only
//     invoked by the test suite.
//
// # Wire protocol for runPushTransport (T2, both directions)
//
//	[ 2-byte big-endian total length ] [ tag byte ] [ payload ]
//
//	start → daemon:  0x00 <raw PTY bytes>          — output chunk
//	                 0x01 <4-byte BE exit code>      — session exited (clean)
//
//	daemon → start:  0x10 <keystroke bytes>          — browser keystrokes
//	                 0x11 <cols uint16 BE><rows uint16 BE> — browser resize
//
// # Security scope
//
// Both transports are reachable only through the daemon's Unix socket (mode
// 0600, owner's UID).  The OS kernel enforces the ownership check.
//
// The browser WebSocket path (consoleui/term_ws_handler.go) gates all input on
// RoleAdmin before calling mgr.SendInput / mgr.SendResize.  The gate is
// fail-closed: non-admin inbound frames are silently dropped; only RoleAdmin
// frames reach the Manager.
//
// Go net.Conn allows concurrent reads and writes (separate kernel paths), so
// runPushTransport's read loop (start → daemon) and SendInput/SendResize's
// Write calls (daemon → start) can run concurrently on the same conn without
// a mutex.  Only the conn pointer itself is guarded (by attachMu in
// externalSession) to protect against nil-deref on transport exit.

package serve

import (
	"encoding/binary"
	"io"
	"log/slog"
	"net"

	termmanager "github.com/bakw00ds/yakos/internal/terminalmanager"
)

// outEvent is the typed union carried on outCh from the subscriber callbacks
// (used by runAttachTransport — the T1 daemon-owned PTY path preserved for tests).
type outEvent struct {
	kind     outEventKind
	payload  []byte // for kindOutput: raw PTY bytes (no tag)
	exitCode int    // for kindExit
}

type outEventKind uint8

const (
	kindOutput outEventKind = iota
	kindExit
)

// ---- runPushTransport: ADR-0008 Phase 1 T2 (active path) --------------------
//
// start → daemon direction: start sends length-prefixed binary frames carrying
// PTY output (0x00) and exit notification (0x01).  The daemon fans them to
// browser /v1/term subscribers via Manager.PushOutput and Manager.PushExit.
//
// Frame format (inbound from start):
//
//	[ 2-byte big-endian total length ] [ tag byte ] [ payload ]
//	  0x00: raw PTY bytes (payload is the PTY output)
//	  0x01: exit (payload is a 4-byte big-endian exit code)

func runPushTransport(conn net.Conn, sessionID string, mgr *termmanager.Manager) {
	// Phase 2: record the attach conn so mgr.SendInput / mgr.SendResize can
	// write 0x10/0x11 frames back to start via the same hijacked connection.
	// SetAttachConn is a no-op if the session is already gone (race with
	// CloseExternal from a concurrent idle-reaper tick).
	_ = mgr.SetAttachConn(sessionID, conn)

	defer func() {
		// Clear the attach conn before closing so sendToOwner returns an error
		// rather than writing to a closed connection.
		_ = mgr.SetAttachConn(sessionID, nil)
		conn.Close()
		// If the connection dropped without a clean 0x01 close, remove the
		// session from the manager so subscribers are not left dangling.
		_ = mgr.CloseExternal(sessionID)
	}()

	hdr := make([]byte, 2)
	for {
		if _, err := io.ReadFull(conn, hdr); err != nil {
			// Connection closed or reset — treat as unclean exit.
			break
		}
		frameLen := binary.BigEndian.Uint16(hdr)
		if frameLen == 0 {
			continue
		}
		buf := make([]byte, frameLen)
		if _, err := io.ReadFull(conn, buf); err != nil {
			break
		}
		tag := buf[0]
		payload := buf[1:]

		switch tag {
		case 0x00: // PTY output chunk
			if len(payload) > 0 {
				if err := mgr.PushOutput(sessionID, payload); err != nil {
					// Session already removed (race with idle reaper or duplicate close).
					slog.Debug("term.push: PushOutput on unknown session", "sessionId", sessionID)
					return
				}
			}
		case 0x01: // exit notification
			code := 0
			if len(payload) >= 4 {
				code = int(binary.BigEndian.Uint32(payload[0:4]))
			}
			// PushExit removes the session from the manager after notifying subscribers.
			_ = mgr.PushExit(sessionID, code)
			return
		default:
			// Unknown tag — ignore and continue.
			slog.Debug("term.push: unknown frame tag", "tag", tag, "sessionId", sessionID)
		}
	}
}

// ---- runAttachTransport: T1 daemon-owned PTY path (preserved for tests) -----
//
// Daemon → client direction: daemon subscribes to PTY output (from
// Manager.CreateSession / newSession) and sends 0x00/0x01 frames to the
// connected client.  Client can send 0x10 keystroke frames and 0x11 resize
// frames back.
//
// NOT used in the active P1 share-terminal flow.  The daemon no longer spawns
// a PTY in the share-terminal path (that is T2 / runPushTransport).  This
// function remains callable from the test suite for regression coverage of
// the daemon-owned session subscriber path.
func runAttachTransport(conn net.Conn, sessionID string, mgr *termmanager.Manager) {
	defer conn.Close()

	// outCh carries typed events from the subscriber callbacks to the outbound
	// writer goroutine.  PTY output goes through outputFn → kindOutput; exit
	// goes through exitFn → kindExit.  The two paths are structurally separate —
	// payload content is never inspected to determine event kind.
	outCh := make(chan outEvent, 256)

	outputFn := func(chunk []byte) {
		cp := make([]byte, len(chunk))
		copy(cp, chunk)
		select {
		case outCh <- outEvent{kind: kindOutput, payload: cp}:
		default: // drop if the pump is too slow
		}
	}
	exitFn := func(code int) {
		select {
		case outCh <- outEvent{kind: kindExit, exitCode: code}:
		default:
		}
	}

	// Subscribe to PTY output and exit notification.
	unsub, err := mgr.Subscribe(sessionID, outputFn, exitFn)
	if err != nil {
		// Session doesn't exist (already exited between the attach ack and hijack).
		return
	}

	// connClosed is closed when the inbound loop exits so the outbound goroutine
	// knows the connection is gone.
	connClosed := make(chan struct{})

	// outDone is closed when the outbound goroutine finishes.
	outDone := make(chan struct{})

	// --- outbound goroutine: deliver PTY output (0x00) and exit (0x01) --------
	go func() {
		defer close(outDone)
		for {
			select {
			case ev, ok := <-outCh:
				if !ok {
					return
				}
				switch ev.kind {
				case kindExit:
					// Build and send the 5-byte exit frame: 0x01 + 4-byte BE code.
					c := uint32(ev.exitCode)
					exitFrame := []byte{
						0x01,
						byte(c >> 24), byte(c >> 16), byte(c >> 8), byte(c),
					}
					_, _ = conn.Write(exitFrame)
					unsub()
					// Close conn to unblock the inbound io.ReadFull loop.
					conn.Close()
					return
				case kindOutput:
					if len(ev.payload) == 0 {
						continue
					}
					// Prepend 0x00 output tag.
					frame := make([]byte, 1+len(ev.payload))
					frame[0] = 0x00
					copy(frame[1:], ev.payload)
					if _, err := conn.Write(frame); err != nil {
						unsub()
						return
					}
				}
			case <-connClosed:
				// Inbound loop exited; stop forwarding output.
				unsub()
				return
			}
		}
	}()

	// --- inbound loop: read length-prefixed frames from conn ------------------
	hdr := make([]byte, 2)
	for {
		// Read 2-byte frame-length header.
		if _, err := io.ReadFull(conn, hdr); err != nil {
			break
		}
		frameLen := binary.BigEndian.Uint16(hdr)
		if frameLen == 0 {
			continue
		}
		buf := make([]byte, frameLen)
		if _, err := io.ReadFull(conn, buf); err != nil {
			break
		}
		tag := buf[0]
		payload := buf[1:]

		switch tag {
		case 0x10: // keystrokes → PTY stdin
			if len(payload) > 0 {
				if err := mgr.WriteInput(sessionID, payload); err != nil {
					goto done // session exited
				}
			}
		case 0x11: // resize
			if len(payload) >= 4 {
				cols := binary.BigEndian.Uint16(payload[0:2])
				rows := binary.BigEndian.Uint16(payload[2:4])
				_ = mgr.Resize(sessionID, cols, rows)
			}
		}
	}
done:
	close(connClosed)
	// Drain the outbound goroutine so we don't leak it.
	for {
		select {
		case <-outCh:
		case <-outDone:
			return
		}
	}
}
