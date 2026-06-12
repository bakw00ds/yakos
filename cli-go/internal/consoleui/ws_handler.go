// Package consoleui — ws_handler.go
//
// buildConsoleWSHandler wires the browser WebSocket for the unified console:
//   - Sec-WebSocket-Protocol subprotocol token auth (browsers cannot set
//     Authorization on WS upgrade; the Service Worker cannot intercept WS)
//   - loopbackOnly + originAllowList (DNS-rebinding defence)
//   - hello-frame presence join/leave via PresenceManager
//   - bus event fan-out
//
// # Sec-WebSocket-Protocol token auth
//
// Browsers send: Sec-WebSocket-Protocol: yakos-bearer, <token>
//
// The auth middleware (consoleAuthSubprotocol) validates the token with
// constant-time comparison (dashauth.TokenEqual) before the WS upgrade.
// Because golang.org/x/net/websocket requires exactly ONE protocol in the
// handshake response, a Handshake func reduces config.Protocol to
// ["yakos-bearer"] (dropping the token slot) before AcceptHandshake runs.
//
// WebSocket connection lifecycle:
//
//  1. Client: new WebSocket(url, ['yakos-bearer', '<token>'])
//  2. consoleAuthSubprotocol validates token; sets a request-scoped flag.
//  3. websocket.Server{Handshake} selects protocol = ["yakos-bearer"].
//  4. Server enters makeConsoleWSFunc.
//  5. Server reads the first JSON frame (hello) — 500ms deadline.
//  6. PresenceManager.Join() records the operator, publishes join event.
//  7. Bus events streamed to client until disconnect/context cancel.
//  8. PresenceManager.Leave() publishes leave event on defer.
package consoleui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/dashauth"
	"github.com/bakw00ds/yakos/internal/wsbus"
	"golang.org/x/net/websocket"
)

// consoleSubprotocol is the Sec-WebSocket-Protocol value negotiated for token
// delivery.  The browser sends: "yakos-bearer, <token>".
// The server responds: "yakos-bearer" (accepted subprotocol).
const consoleSubprotocol = "yakos-bearer"

// contextKey is an unexported type for context keys in this package.
type contextKey int

// authedKey marks a request as having passed subprotocol token auth.
const authedKey contextKey = 1

// buildConsoleWSHandler returns an http.Handler mounted at /v1/events that
// implements the full console WebSocket stack described in the package doc.
func buildConsoleWSHandler(token string, bus *wsbus.Bus, pm *PresenceManager) http.Handler {
	// The websocket.Server selects the "yakos-bearer" protocol from the list,
	// dropping the token slot.  Token validity was already checked by the
	// middleware layer (consoleAuthSubprotocol) before the upgrade happens.
	wsSrv := &websocket.Server{
		Handshake: func(config *websocket.Config, r *http.Request) error {
			// Verify the auth middleware ran and succeeded.
			if r.Context().Value(authedKey) != true {
				return fmt.Errorf("consoleui: unauthenticated WebSocket connection")
			}
			// Reduce protocol list to exactly ["yakos-bearer"].
			// AcceptHandshake requires exactly 0 or 1 protocol.
			config.Protocol = []string{consoleSubprotocol}
			return nil
		},
		Handler: makeConsoleWSFunc(bus, pm),
	}

	mux := http.NewServeMux()
	mux.Handle("/v1/events",
		consoleLoopbackOnly(
			consoleOriginAllowList(
				consoleAuthSubprotocol(token, wsSrv),
			),
		),
	)
	return mux
}

// makeConsoleWSFunc returns the websocket.Handler func that drives the full
// hello → presence → bus-streaming lifecycle for one browser connection.
func makeConsoleWSFunc(bus *wsbus.Bus, pm *PresenceManager) websocket.Handler {
	return func(conn *websocket.Conn) {
		defer func() { _ = conn.Close() }()

		connID := newConnID()
		ctx := conn.Request().Context()

		// ---- 1. Read hello and detect disconnect via a single reader goroutine --
		//
		// IMPORTANT: we must NOT use conn.SetReadDeadline.
		// golang.org/x/net/websocket leaves the underlying TCP connection in a
		// broken state after a read deadline fires, making subsequent writes fail.
		//
		// A single goroutine owns all reads from conn for the lifetime of the
		// connection (avoiding concurrent read races).  It:
		//   a) First reads one frame as HelloMessage (optional; 500ms timeout).
		//   b) Then drains remaining frames (client shouldn't send more, but
		//      we need to read to detect EOF / disconnect).
		//   c) Closes disconnCh on any read error (EOF = client gone).
		helloCh := make(chan HelloMessage, 1)
		disconnCh := make(chan struct{})
		go func() {
			defer close(disconnCh)
			// a) Try to read hello.
			var h HelloMessage
			if err := websocket.JSON.Receive(conn, &h); err == nil {
				helloCh <- h
			}
			// b) Drain remaining frames for disconnect detection.
			var discard json.RawMessage
			for {
				if err := websocket.JSON.Receive(conn, &discard); err != nil {
					return // EOF or any read error = client gone
				}
			}
		}()

		helloTimer := time.NewTimer(500 * time.Millisecond)
		defer helloTimer.Stop()

		var hello HelloMessage
		select {
		case h := <-helloCh:
			hello = h
		case <-disconnCh:
			// Client disconnected before sending hello — handler will exit below.
			hello = HelloMessage{Type: "hello"}
		case <-helloTimer.C:
			slog.Debug("consoleui: no hello frame within 500ms; using anon")
			hello = HelloMessage{Type: "hello"}
		}
		if hello.Type != "hello" {
			hello = HelloMessage{Type: "hello"}
		}

		// ---- 2. Presence join -----------------------------------------------
		rec := pm.Join(connID, hello)
		defer pm.Leave(connID)

		// ---- 3. Send welcome back (server-resolved presence record) ----------
		welcomeMsg := map[string]interface{}{
			"type":     "welcome",
			"connId":   connID,
			"presence": rec,
		}
		_ = websocket.JSON.Send(conn, welcomeMsg) // best-effort

		// ---- 4. Subscribe to bus, replay missed events, then stream live ----
		//
		// Subscribe FIRST so we don't miss events published between replay
		// completion and subscription setup (same pattern as wsbus.Server).
		sub := bus.Subscribe("")
		defer sub.Unsubscribe()

		// Replay: if ?since=<seq> is present, send buffered events the client
		// missed during a disconnect before joining the live stream.
		if sinceStr := conn.Request().URL.Query().Get("since"); sinceStr != "" {
			if sinceSeq, err := parseSinceSeq(sinceStr); err == nil {
				for _, ev := range bus.History(sinceSeq) {
					conn.SetWriteDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
					if err := websocket.JSON.Send(conn, ev); err != nil {
						slog.Debug("consoleui: replay write error; dropping client", "err", err)
						return
					}
				}
			}
		}

		pingTicker := time.NewTicker(15 * time.Second)
		defer pingTicker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-disconnCh:
				return
			case ev, ok := <-sub.C():
				if !ok {
					return
				}
				conn.SetWriteDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
				if err := websocket.JSON.Send(conn, ev); err != nil {
					slog.Debug("consoleui: WS write error; dropping client", "err", err)
					return
				}
			case <-pingTicker.C:
				pingEv := wsbus.Event{
					Topic:   "ping",
					Payload: json.RawMessage(`{}`),
				}
				conn.SetWriteDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
				if err := websocket.JSON.Send(conn, pingEv); err != nil {
					return
				}
			}
		}
	}
}

// consoleAuthSubprotocol validates the bearer token from the
// Sec-WebSocket-Protocol header BEFORE the WebSocket upgrade.
// Sets authedKey=true in the request context on success so the
// websocket.Server Handshake func can verify it ran.
//
// Token is validated with dashauth.TokenEqual (constant-time).
// The ?token= query parameter is NOT accepted (tokens in query strings
// appear in server logs — explicitly forbidden by the security plan).
func consoleAuthSubprotocol(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protos := r.Header.Get("Sec-WebSocket-Protocol")
		if protos == "" {
			http.Error(w, "consoleui: missing Sec-WebSocket-Protocol (expected 'yakos-bearer, <token>')", http.StatusForbidden)
			return
		}
		parts := strings.SplitN(protos, ",", 2)
		if len(parts) < 2 {
			http.Error(w, "consoleui: invalid Sec-WebSocket-Protocol (expected 'yakos-bearer, <token>')", http.StatusForbidden)
			return
		}
		prefix := strings.TrimSpace(parts[0])
		tok := strings.TrimSpace(parts[1])
		if prefix != consoleSubprotocol {
			http.Error(w, "consoleui: unsupported WS subprotocol prefix (expected 'yakos-bearer')", http.StatusForbidden)
			return
		}
		if tok == "" || !dashauth.TokenEqual(tok, token) {
			http.Error(w, "consoleui: unauthorized (invalid subprotocol token)", http.StatusForbidden)
			return
		}
		// Mark request as authenticated; pass to the websocket.Server.
		ctx := context.WithValue(r.Context(), authedKey, true)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// consoleLoopbackOnly rejects non-loopback remote addresses.
func consoleLoopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := splitHostAddr(r.RemoteAddr)
		if err != nil {
			http.Error(w, "consoleui: bad remote addr", http.StatusForbidden)
			return
		}
		if !isLoopbackHost(host) {
			http.Error(w, "consoleui: non-loopback connection rejected", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// consoleOriginAllowList rejects requests with a non-loopback Origin header.
// Requests without an Origin header (e.g. CLI tools) pass through.
func consoleOriginAllowList(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && !isLoopbackOrigin(origin) {
			http.Error(w, "consoleui: Origin not in allow-list", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// newConnID generates a random 8-byte hex connection identifier.
func newConnID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// parseSinceSeq parses a ?since= query value as a non-negative int64.
func parseSinceSeq(s string) (int64, error) {
	var n int64
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return 0, err
	}
	if n < 0 {
		n = 0
	}
	return n, nil
}

// splitHostAddr splits "host:port" (with IPv6 bracket support).
func splitHostAddr(addr string) (string, string, error) {
	if addr == "" {
		return "", "", fmt.Errorf("consoleui: empty addr")
	}
	if addr[0] == '[' {
		end := strings.LastIndex(addr, "]")
		if end < 0 {
			return "", "", fmt.Errorf("consoleui: malformed IPv6 addr: %s", addr)
		}
		host := addr[1:end]
		rest := addr[end+1:]
		port := ""
		if len(rest) > 1 && rest[0] == ':' {
			port = rest[1:]
		}
		return host, port, nil
	}
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return addr, "", nil
	}
	return addr[:i], addr[i+1:], nil
}

// isLoopbackHost returns true when host is a loopback address.
func isLoopbackHost(host string) bool {
	switch host {
	case "::1", "localhost", "127.0.0.1":
		return true
	}
	return strings.HasPrefix(host, "127.")
}

// isLoopbackOrigin returns true when origin is a loopback console HTTP origin.
func isLoopbackOrigin(origin string) bool {
	const (
		pfx4  = "http://127.0.0.1"
		pfxLH = "http://localhost"
		pfx6  = "http://[::1]"
	)
	o := strings.TrimRight(origin, "/")
	for _, pfx := range []string{pfx4, pfxLH, pfx6} {
		if o == pfx || strings.HasPrefix(o, pfx+":") {
			return true
		}
	}
	return false
}
