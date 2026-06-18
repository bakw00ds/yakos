// Package consoleui — term_ws_handler.go
//
// buildTermWSHandler and buildTermWSHandlerNetworked implement the
// /v1/term/<sessionId> WebSocket endpoint (ADR-0008 Phase 1).
//
// # Frame format (binary WebSocket frames)
//
// Server→client:
//
//	0x00 <raw PTY bytes>   — PTY output chunk
//	0x01 <4-byte big-endian exit code>  — session closed / process exited
//
// Client→server:
//
//	All inbound frames are DROPPED in Phase 1 (output-only).
//	Phase 2 will wire 0x10 (keystrokes) and 0x11 (resize) for RoleAdmin callers.
//
// # Auth
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
func makeTermWSFunc(termMgr *terminalmanager.Manager) websocket.Handler {
	return func(conn *websocket.Conn) {
		defer func() { _ = conn.Close() }()

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

		// ---- 4. Drain inbound frames (Phase 1: DROP all) -------------------
		go func() {
			defer close(disconnCh)
			var discard []byte
			for {
				if err := websocket.Message.Receive(conn, &discard); err != nil {
					return
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
