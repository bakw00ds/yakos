package restapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/dispatch"
)

// Config holds all REST server configuration.
type Config struct {
	// Addr is the TCP listen address (default "127.0.0.1:7892").
	// Must be a loopback address in production use.
	Addr string

	// Tokens holds the read/write bearer tokens.
	Tokens *Tokens

	// WorkspaceRoot is the workspace path forwarded to handler helpers.
	WorkspaceRoot string

	// YakosRoot is the framework root used by refresh/agent handlers.
	YakosRoot string

	// StateDir is the directory used for token persistence
	// (typically ~/.yakos-state).
	StateDir string

	// DispatchService is the shared dispatch facade. Must be non-nil for
	// POST /v1/dispatches; a nil value causes the handler to return HTTP 500
	// (no ephemeral fallback — all requests share the global governor).
	DispatchService *dispatch.Service
}

// authLevel describes what level of access a token grants.
type authLevel int

const (
	authNone  authLevel = 0
	authRead  authLevel = 1
	authWrite authLevel = 2
)

// Server is the REST API HTTP server.
type Server struct {
	cfg     Config
	mux     *http.ServeMux
	httpSrv *http.Server
}

// New constructs a Server wiring all routes.
func New(cfg Config) *Server {
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:7892"
	}

	s := &Server{cfg: cfg, mux: http.NewServeMux()}
	s.registerRoutes()

	s.httpSrv = &http.Server{
		Addr:         cfg.Addr,
		Handler:      s.mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	return s
}

// Handler returns the underlying http.Handler, suitable for use with
// httptest.NewServer in tests.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// ListenAndServe starts the HTTP server on cfg.Addr and blocks until ctx is
// cancelled or a fatal listen error occurs.
//
// Errors:
//   - returns http.ErrServerClosed on clean shutdown (treat as nil)
//   - returns a wrapped error on listen failure
func (s *Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("restapi: listen %s: %w", s.cfg.Addr, err)
	}
	// Enforce loopback-only.
	host, _, _ := net.SplitHostPort(ln.Addr().String())
	ip := net.ParseIP(host)
	if ip != nil && !ip.IsLoopback() {
		_ = ln.Close()
		return fmt.Errorf("restapi: addr %s is not a loopback address", s.cfg.Addr)
	}

	errCh := make(chan error, 1)
	go func() {
		err := s.httpSrv.Serve(ln)
		if err != nil && err != http.ErrServerClosed {
			errCh <- err
		} else {
			errCh <- nil
		}
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.httpSrv.Shutdown(shutCtx)
		return <-errCh
	case err := <-errCh:
		return err
	}
}

// Serve runs ListenAndServe and converts http.ErrServerClosed to nil.
func (s *Server) Serve(ctx context.Context) error {
	err := s.ListenAndServe(ctx)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// registerRoutes wires all REST endpoints to the ServeMux.
func (s *Server) registerRoutes() {
	// No-auth endpoint
	s.mux.HandleFunc("GET /v1/version", s.handleVersion)

	// Read-token endpoints
	s.mux.HandleFunc("GET /v1/kanban", s.requireRead(s.handleKanbanList))
	s.mux.HandleFunc("GET /v1/dispatches", s.requireRead(s.handleDispatchesList))
	s.mux.HandleFunc("GET /v1/cost", s.requireRead(s.handleCost))
	s.mux.HandleFunc("GET /v1/status", s.requireRead(s.handleStatus))
	s.mux.HandleFunc("GET /v1/supervise/pending", s.requireRead(s.handleSupervisePending))

	// Write-token endpoints
	s.mux.HandleFunc("POST /v1/kanban/items", s.requireWrite(s.handleKanbanCreate))
	s.mux.HandleFunc("PATCH /v1/kanban/items/{id}", s.requireWrite(s.handleKanbanPatch))
	s.mux.HandleFunc("POST /v1/dispatches", s.requireWrite(s.handleDispatchCreate))
}

// ---- auth middleware --------------------------------------------------------

// requireRead returns an http.HandlerFunc that enforces read-level auth.
func (s *Server) requireRead(next http.HandlerFunc) http.HandlerFunc {
	return s.requireAuth(authRead, next)
}

// requireWrite returns an http.HandlerFunc that enforces write-level auth.
func (s *Server) requireWrite(next http.HandlerFunc) http.HandlerFunc {
	return s.requireAuth(authWrite, next)
}

// requireAuth extracts the Bearer token from the Authorization header and
// compares it against the configured tokens.  The write token grants read
// access; the read token does not grant write access.
func (s *Server) requireAuth(level authLevel, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := bearerToken(r)
		if tok == "" {
			writeError(w, http.StatusUnauthorized, "missing Authorization: Bearer token")
			return
		}

		toks := s.cfg.Tokens
		if toks == nil {
			writeError(w, http.StatusInternalServerError, "token configuration missing")
			return
		}

		granted := authNone
		if tok == toks.Write {
			granted = authWrite
		} else if tok == toks.Read {
			granted = authRead
		}

		if granted < level {
			writeError(w, http.StatusForbidden, "insufficient token permissions")
			return
		}

		next(w, r)
	}
}

// bearerToken extracts the Bearer token from an Authorization header.
// Returns "" if absent or malformed.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	// Case-insensitive prefix match per RFC 7235.
	const prefix = "bearer "
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return h[len(prefix):]
	}
	return ""
}

// ---- response helpers -------------------------------------------------------

// writeJSON marshals v and writes it with Content-Type application/json.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, `{"error":"marshal failed"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// writeError writes a JSON error envelope.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// decodeBody JSON-decodes the request body into dst.
// Returns false and writes an appropriate HTTP error if decoding fails.
func decodeBody(w http.ResponseWriter, r *http.Request, dst interface{}) bool {
	if r.Body == nil || r.ContentLength == 0 {
		writeError(w, http.StatusBadRequest, "request body required")
		return false
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MiB limit
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body")
		return false
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return false
	}
	return true
}
