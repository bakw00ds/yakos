package wsbus

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/websocket"
)

// ServerConfig holds configuration for the WebSocket HTTP server.
type ServerConfig struct {
	// Addr is the TCP address to listen on (e.g. "127.0.0.1:7891").
	// If empty, the server defaults to "127.0.0.1:0" (OS-assigned port).
	Addr string

	// Bus is the event bus to subscribe from.  Required.
	Bus *Bus

	// Token is the bearer token clients must present for authentication.
	// Required; obtain via [LoadOrCreateToken].
	Token string
}

// Server is a WebSocket HTTP server that streams bus events to authenticated
// loopback-only clients.
type Server struct {
	cfg      ServerConfig
	ln       net.Listener
	httpSrv  *http.Server
	addrCh   chan struct{} // closed once ln is bound
}

// NewServer creates a Server from cfg but does not start it.
// Call [Server.Serve] to start accepting connections.
func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.Bus == nil {
		return nil, fmt.Errorf("wsbus: Bus is required")
	}
	if cfg.Token == "" {
		return nil, fmt.Errorf("wsbus: Token is required")
	}
	s := &Server{
		cfg:    cfg,
		addrCh: make(chan struct{}),
	}
	return s, nil
}

// Serve starts the HTTP listener and blocks until ctx is cancelled.
// The listener is closed when Serve returns.
func (s *Server) Serve(ctx context.Context) error {
	addr := s.cfg.Addr
	if addr == "" {
		addr = "127.0.0.1:0"
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("wsbus: listen %s: %w", addr, err)
	}
	s.ln = ln
	close(s.addrCh)

	mux := http.NewServeMux()
	mux.Handle("/v1/events", s.loopbackOnly(s.authenticate(websocket.Handler(s.handleWS))))

	s.httpSrv = &http.Server{
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // streaming; no write timeout
		IdleTimeout:  60 * time.Second,
	}

	// Shutdown when ctx is cancelled.
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.httpSrv.Shutdown(shutCtx)
	}()

	err = s.httpSrv.Serve(ln)
	// http.ErrServerClosed is expected on clean shutdown.
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// Addr returns the bound address once Serve has started.
// Blocks until the listener is ready or ctx is cancelled.
func (s *Server) Addr(ctx context.Context) (string, error) {
	select {
	case <-s.addrCh:
		return s.ln.Addr().String(), nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// loopbackOnly rejects connections whose remote address is not 127.0.0.1 or ::1.
func (s *Server) loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			http.Error(w, "wsbus: bad remote addr", http.StatusForbidden)
			return
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			http.Error(w, "wsbus: non-loopback connection rejected (mTLS required for cross-machine; Q2)", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authenticate checks the Authorization: Bearer <token> header or ?token= query param.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := extractToken(r)
		if tok == "" || tok != s.cfg.Token {
			http.Error(w, "wsbus: unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// extractToken extracts the bearer token from the Authorization header or
// the ?token= query parameter.
func extractToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		const prefix = "Bearer "
		if strings.HasPrefix(auth, prefix) {
			return strings.TrimSpace(auth[len(prefix):])
		}
	}
	return r.URL.Query().Get("token")
}

// handleWS is the core WebSocket handler.  It subscribes to all topics on the
// bus and streams events to the client until the connection closes or ctx ends.
func (s *Server) handleWS(conn *websocket.Conn) {
	defer func() { _ = conn.Close() }()

	sub := s.cfg.Bus.Subscribe("") // "" = all topics
	defer sub.Unsubscribe()

	ctx := conn.Request().Context()

	// Heartbeat: send a PING frame every 15s; client must pong within 5s.
	// golang.org/x/net/websocket handles pong automatically at the protocol
	// level, so we use a simple write-side ping by sending a JSON ping event.
	pingTicker := time.NewTicker(15 * time.Second)
	defer pingTicker.Stop()

	enc := json.NewEncoder(conn)

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-sub.C():
			if !ok {
				return
			}
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
			if err := enc.Encode(ev); err != nil {
				slog.Debug("wsbus: write error; dropping client", "err", err)
				return
			}
		case <-pingTicker.C:
			// Send a synthetic "ping" event so clients can detect stale connections.
			pingEv := Event{
				Seq:     s.cfg.Bus.seq.Load(),
				Topic:   "ping",
				TS:      time.Now().UTC(),
				Payload: json.RawMessage(`{}`),
			}
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
			if err := enc.Encode(pingEv); err != nil {
				return
			}
		}
	}
}
