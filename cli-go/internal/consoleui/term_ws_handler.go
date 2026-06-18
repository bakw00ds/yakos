// Package consoleui — term_ws_handler.go
//
// buildTermWSHandler and buildTermWSHandlerNetworked implement the
// /v1/term/<sessionId> WebSocket endpoint (ADR-0008 Phase 1/2).
//
// # Frame format (binary WebSocket frames)
//
// Server→client (output path, Phase 1 unchanged):
//
//	0x00 <raw PTY bytes>                   — PTY output chunk
//	0x01 <4-byte big-endian exit code>     — session closed / process exited
//
// Client→server (input path, Phase 2, RoleAdmin only):
//
//	0x10 <keystroke bytes>                 — browser keystrokes → PTY stdin
//	0x11 <cols uint16 BE><rows uint16 BE>  — browser resize → PTY window size
//
//	Non-admin connections: all inbound frames are silently dropped (fail-closed).
//	Admin connections: 0x10/0x11 are routed to mgr.SendInput / mgr.SendResize.
//	Frames exceeding maxInboundFrameBytes are dropped (log + discard, no panic).
//	Malformed 0x11 frames (< 5 bytes total) are dropped.
//
// # Auth and write-path gates
//
// The role check on line ~94 is the single gate for all inbound frames:
//
//	id.Role.Allows(netid.RoleAdmin) → route 0x10/0x11 to manager
//	otherwise                       → drain and discard all inbound frames
//
// Fail-closed: a non-resolved identity (id.Resolved == false) has
// id.Role == RoleNone, which does NOT pass Allows(RoleAdmin).  Non-admin
// connections can only receive output; they can never inject input.
//
// # Phase 2 write path (full trace)
//
//	browser 0x10/0x11 frame
//	→ WS handler (RoleAdmin gate — this file)
//	→ mgr.SendInput / mgr.SendResize       [terminalmanager/manager.go]
//	→ externalSession.sendToOwner(frame)   [terminalmanager/external_session.go]
//	→ conn.Write(frame)                    [daemon → start via hijacked conn]
//	→ readDaemonFrames goroutine           [start/pump_unix.go]
//	→ ptmx.Write / pty.Setsize             [PTY stdin / window size]
//
// # Auth (WS middleware stack — unchanged from Phase 1)
//
// Reuses the existing WS middleware stack verbatim (same as /v1/events):
//   - Loopback: consoleLoopbackOnly → consoleOriginAllowList → consoleAuthSubprotocol
//   - Networked: consoleOriginAllowListNetworked → consoleAuthSubprotocolOrSession
//
// Additionally requires RoleAdmin (enforced inside the handler, not in the
// outer requireRole wrapper, so the websocket.Server handshake can respond
// with a proper WebSocket close rather than a plain HTTP 403 before upgrade).
package consoleui

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/terminalmanager"
	"golang.org/x/net/websocket"
)

// terminalSessionManager is the subset of *terminalmanager.Manager that the
// WS handler uses.  Extracted as an interface so tests can pass a fake without
// constructing a full Manager.
type terminalSessionManager interface {
	Subscribe(sessionId string, outputFn func([]byte), exitFn func(int)) (func(), error)
	SendInput(sessionId string, data []byte) error
	SendResize(sessionId string, cols, rows uint16) error
}

// maxInboundFrameBytes is the maximum size of an inbound WebSocket frame
// accepted from a browser client.  Frames exceeding this limit are dropped
// (logged and discarded) without panicking.  Applies only to RoleAdmin
// connections; non-admin connections discard all frames regardless.
const maxInboundFrameBytes = 64 * 1024 // 64 KB

// buildTermWSHandler returns an http.Handler for the loopback /v1/term/<sessionId> path.
// termMgr must be non-nil (caller must check before mounting).
func buildTermWSHandler(token string, termMgr *terminalmanager.Manager) http.Handler {
	return buildTermWSHandlerFull(token, termMgr, false, nil)
}

// buildTermWSHandlerNetworked returns an http.Handler for the networked /v1/term/<sessionId> path.
func buildTermWSHandlerNetworked(token string, termMgr *terminalmanager.Manager, externalHosts []string) http.Handler {
	return buildTermWSHandlerFull(token, termMgr, true, externalHosts)
}

// buildTermWSHandlerFull is the shared implementation.
func buildTermWSHandlerFull(token string, termMgr *terminalmanager.Manager, networked bool, externalHosts []string) http.Handler {
	wsSrv := &websocket.Server{
		Handshake: func(config *websocket.Config, r *http.Request) error {
			if r.Context().Value(authedKey) != true {
				return fmt.Errorf("consoleui: unauthenticated WebSocket connection")
			}
			if r.Context().Value(sessionAuthedKey) == true {
				config.Protocol = nil
				return nil
			}
			config.Protocol = []string{consoleSubprotocol}
			return nil
		},
		Handler: makeTermWSFunc(termMgr),
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract sessionId from the URL path: /v1/term/<sessionId>
		// The mux is registered as /v1/term/ so the remainder is the sessionId.
		sessionId := strings.TrimPrefix(r.URL.Path, "/v1/term/")
		sessionId = strings.Trim(sessionId, "/")
		if sessionId == "" {
			http.Error(w, "consoleui: missing sessionId in path", http.StatusBadRequest)
			return
		}
		// Stash the sessionId in the URL so the websocket.Server handler can read it.
		// We use a copy of the request with the path trimmed to exactly the sessionId.
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/v1/term/" + sessionId
		wsSrv.ServeHTTP(w, r2)
	})
}

// makeTermWSFunc returns the websocket.Handler func for one PTY viewer connection.
func makeTermWSFunc(termMgr terminalSessionManager) websocket.Handler {
	return func(conn *websocket.Conn) {
		defer func() { _ = conn.Close() }()

		// Bound inbound WebSocket message size before the first Receive call so the
		// library rejects oversized frames at the read level, not after buffering them.
		conn.MaxPayloadBytes = maxInboundFrameBytes

		// ---- 1. Role gate: require RoleAdmin ----------------------------------
		id := netid.IdentityFrom(conn.Request().Context())
		if id.Resolved && !id.Role.Allows(netid.RoleAdmin) {
			// Send a close frame before disconnecting; best-effort.
			_ = sendWSBinary(conn, []byte{0x01, 0x00, 0x00, 0x00, 0x01}) // exit code 1
			slog.Warn("consoleui: /v1/term: insufficient role", "role", id.Role)
			return
		}

		// ---- 2. Extract sessionId from URL path ----------------------------
		sessionId := strings.TrimPrefix(conn.Request().URL.Path, "/v1/term/")
		sessionId = strings.Trim(sessionId, "/")
		if sessionId == "" {
			return
		}

		// ---- 3. Subscribe to PTY output and exit notification -----------------
		disconnCh := make(chan struct{})
		outputCh := make(chan []byte, 64) // buffered to avoid blocking the fanOut goroutine
		exitCh := make(chan int, 1)       // receives exit code when session ends

		// outputFn and exitFn are called from separate code paths in the Manager:
		// outputFn receives only raw PTY bytes; exitFn receives only the exit code.
		// No content inspection is needed to distinguish the two event types.
		outputFn := func(chunk []byte) {
			select {
			case outputCh <- chunk:
			default:
				slog.Debug("consoleui: /v1/term: slow viewer; dropping chunk", "sessionId", sessionId, "bytes", len(chunk))
			}
		}
		exitFn := func(code int) {
			select {
			case exitCh <- code:
			default:
			}
		}

		unsub, err := termMgr.Subscribe(sessionId, outputFn, exitFn)
		if err != nil {
			// Session not found — send error close frame.
			_ = sendWSBinary(conn, []byte{0x01, 0x00, 0x00, 0x00, 0x01})
			slog.Debug("consoleui: /v1/term: session not found", "sessionId", sessionId, "err", err)
			return
		}
		defer unsub()

		// ---- 4. Inbound frame router (Phase 2) ------------------------------
		//
		// RoleAdmin connections: route 0x10 (keystrokes) and 0x11 (resize)
		// frames to the manager's back-channel.  All other frames are dropped.
		//
		// Non-admin connections: drain and discard all inbound frames silently
		// (fail-closed — same behavior as Phase 1 DROP-all).
		//
		// Fail-closed invariant: a non-resolved identity (Resolved==false) has
		// Role==RoleNone < RoleAdmin, so isAdmin is false for unresolved
		// identities.  The gate is the single decision point for all writes.
		isAdmin := id.Resolved && id.Role.Allows(netid.RoleAdmin)
		go func() {
			// This goroutine handles adversarial network input and is detached from
			// net/http's request goroutine.  Any unrecovered panic here would crash
			// the process.  Log and close instead.
			defer func() {
				if r := recover(); r != nil {
					slog.Error("consoleui: /v1/term: inbound goroutine panic (closing connection)", "recover", r, "sessionId", sessionId)
					_ = conn.Close()
				}
			}()
			defer close(disconnCh)
			// firstWrite gates the one-time audit log entry for the first accepted
			// write frame from a RoleAdmin connection.  Emitted exactly once per
			// connection regardless of how many frames follow.
			// (Fix 3 — audit log, ADR-0008 P2 security review.)
			firstWrite := false
			var frame []byte
			for {
				if err := websocket.Message.Receive(conn, &frame); err != nil {
					return
				}
				if !isAdmin {
					// Non-admin: silently discard all inbound frames.
					continue
				}
				// Admin: bound check and route.
				if len(frame) > maxInboundFrameBytes {
					slog.Warn("consoleui: /v1/term: inbound frame exceeds max size; dropping",
						"sessionId", sessionId, "size", len(frame), "max", maxInboundFrameBytes)
					continue
				}
				if len(frame) == 0 {
					continue
				}
				tag := frame[0]
				// One-time audit log on the first accepted write frame per connection.
				if !firstWrite && (tag == 0x10 || tag == 0x11) {
					slog.Info("term: admin input session", "sessionId", sessionId, "operatorId", id.OperatorID)
					firstWrite = true
				}
				switch tag {
				case 0x10: // keystrokes → PTY stdin
					payload := frame[1:]
					if len(payload) == 0 {
						continue
					}
					if err := termMgr.SendInput(sessionId, payload); err != nil {
						slog.Debug("consoleui: /v1/term: SendInput error", "sessionId", sessionId, "err", err)
					}
				case 0x11: // resize → PTY window size
					// Requires exactly 4 payload bytes: cols uint16 BE + rows uint16 BE.
					payload := frame[1:]
					if len(payload) < 4 {
						slog.Debug("consoleui: /v1/term: malformed 0x11 resize frame; dropping",
							"sessionId", sessionId, "payloadLen", len(payload))
						continue
					}
					cols := binary.BigEndian.Uint16(payload[0:2])
					rows := binary.BigEndian.Uint16(payload[2:4])
					if err := termMgr.SendResize(sessionId, cols, rows); err != nil {
						slog.Debug("consoleui: /v1/term: SendResize error", "sessionId", sessionId, "err", err)
					}
				default:
					// Unknown tag — drop silently; do not panic.
					slog.Debug("consoleui: /v1/term: unknown inbound frame tag; dropping",
						"sessionId", sessionId, "tag", tag)
				}
			}
		}()

		// ---- 5. Stream output frames to browser ----------------------------
		ctx := conn.Request().Context()
		for {
			select {
			case <-ctx.Done():
				return
			case <-disconnCh:
				return
			case code := <-exitCh:
				// Session exited: send 0x01 exit frame and close.
				conn.SetWriteDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
				_ = sendWSBinary(conn, encodeExitFrame(code))
				return
			case chunk, ok := <-outputCh:
				if !ok {
					return
				}
				// PTY output frame: prepend 0x00 tag.
				frame := make([]byte, 1+len(chunk))
				frame[0] = 0x00
				copy(frame[1:], chunk)
				conn.SetWriteDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
				if err := sendWSBinary(conn, frame); err != nil {
					slog.Debug("consoleui: /v1/term: write error", "sessionId", sessionId, "err", err)
					return
				}
			}
		}
	}
}

// sendWSBinary sends a binary WebSocket frame.
func sendWSBinary(conn *websocket.Conn, data []byte) error {
	return websocket.Message.Send(conn, data)
}

// encodeExitFrame encodes a 0x01 close frame with a 4-byte big-endian exit code.
// Exported for use in tests.
func encodeExitFrame(exitCode int) []byte {
	frame := make([]byte, 5)
	frame[0] = 0x01
	binary.BigEndian.PutUint32(frame[1:], uint32(exitCode))
	return frame
}

// decodeExitFrame decodes a 0x01 close frame, returning the exit code.
// Returns an error if the frame is malformed.
func decodeExitFrame(frame []byte) (int, error) {
	if len(frame) < 5 || frame[0] != 0x01 {
		return -1, fmt.Errorf("not a valid exit frame")
	}
	code := binary.BigEndian.Uint32(frame[1:5])
	return int(code), nil
}
