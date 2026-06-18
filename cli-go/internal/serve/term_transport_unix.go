//go:build !windows

// Package serve — term_transport_unix.go
//
// runAttachTransport is the daemon-side binary pump for an owner-attached
// terminal session (ADR-0008 Phase 1).
//
// # How the attach protocol works
//
// After yakos.term.attach is acknowledged over the NDJSON socket, the connection
// is hijacked (via jsonrpc.HijackChanFromCtx) and handed to this function.
// The connection then speaks a binary frame protocol instead of NDJSON:
//
//	Client → daemon:  0x10 <N bytes>                  — keystrokes to PTY stdin
//	                  0x11 <2-byte cols> <2-byte rows> — resize event
//	Daemon → client:  0x00 <raw PTY bytes>             — PTY output
//	                  0x01 <4-byte exit code>           — session exited
//
// Inbound frames are length-prefixed:
//
//	[ 2-byte big-endian total length ] [ tag byte ] [ payload ]
//
// Outbound frames are NOT length-prefixed (plain binary, consumed by pump_unix.go
// which parses by tag).
//
// # Security scope
//
// This transport is reachable only through the daemon's Unix socket, which is
// mode 0600 and owned by the operator's UID.  The operator's yakos-start process
// dials this socket; the OS kernel enforces the ownership check.
//
// The browser WebSocket path (consoleui/term_ws_handler.go) is write-isolated:
//
//  1. Manager.WriteInput has exactly one caller — runAttachTransport — which is
//     reachable only via the owner-only Unix-socket attach hijack.
//
//  2. The browser WS handler calls only Manager.Subscribe and Manager.Get; it
//     discards every inbound WebSocket frame (Phase 1: output-only).  WriteInput
//     does not appear in any file under internal/consoleui/.
//
// Phase 2 note: browser keystroke ingestion requires a separate security review
// before any write path is exposed to the WS handler.

package serve

import (
	"encoding/binary"
	"io"
	"net"

	termmanager "github.com/bakw00ds/yakos/internal/terminalmanager"
)

// outEvent is the typed union carried on outCh from the subscriber callbacks.
// Using an explicit kind field means the outbound goroutine dispatches purely
// on ev.kind — it never inspects payload bytes to determine event type.
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

// runAttachTransport runs the bidirectional binary frame pump over conn for
// the session identified by sessionID.  It blocks until the session exits or
// the connection closes, then returns.  conn is closed before returning.
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
