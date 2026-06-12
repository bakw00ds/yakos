package consoleui

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	_ "embed"

	"github.com/bakw00ds/yakos/internal/dashauth"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/kanban"
	"github.com/bakw00ds/yakos/internal/metricsdash"
	"github.com/bakw00ds/yakos/internal/perfdash"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

//go:embed dist/index.html
var indexHTML []byte

//go:embed dist/app.js
var appJS []byte

//go:embed dist/styles.css
var stylesCSS []byte

//go:embed dist/sw.js
var swJS []byte

// Config holds all configuration for the unified console HTTP server.
type Config struct {
	// Addr is the TCP listen address. Defaults to "127.0.0.1:7890".
	// Must be a loopback address.
	Addr string

	// Token is the console bearer token.
	// Use LoadOrCreateToken to obtain it before constructing the server.
	Token string

	// KanbanBoardPath is the absolute path to kanban.md.
	KanbanBoardPath string

	// KanbanProject is the project name displayed in the kanban UI.
	KanbanProject string

	// MetricsProjectDir is the project dir for the metrics dashboard.
	MetricsProjectDir string

	// PerfWorkDir is the directory containing dispatch-log*.ndjson files.
	// Typically <workspace>/work/current.
	PerfWorkDir string

	// Bus is the shared WebSocket event bus (required for /v1/events).
	Bus *wsbus.Bus

	// DispatchService is the shared dispatch facade. When set, console-originated
	// dispatches (Phase 3+) go through it with identity stamping and governor
	// enforcement. The console mints operatorIDs from connected browser sessions;
	// for Phase 2 the server-level OS-user-derived ID is used as a fallback.
	// This field is wired here so the Phase 3 chat handler can use it without
	// further config changes.
	DispatchService *dispatch.Service

	// Listener, when non-nil, is used directly instead of binding a new socket.
	// Injected in tests to avoid port conflicts.
	Listener net.Listener
}

func (c *Config) addr() string {
	if c.Addr != "" {
		return c.Addr
	}
	return "127.0.0.1:7890"
}

// Server is the unified console HTTP server.
type Server struct {
	cfg      Config
	mux      *http.ServeMux
	httpSrv  *http.Server
	presence *PresenceManager
}

// New constructs a Server and wires all routes.
//
// Auth model:
//   - The static SPA shell assets (/, /app.js, /styles.css, /sw.js) are
//     served WITHOUT a token requirement so the browser can load them and
//     register the Service Worker before the token is available.
//   - All other paths (sub-dashboards /kanban/, /cost/, /perf/, /v1/events,
//     and any future API paths) require the edge bearer token.
//   - Non-GET requests without Content-Type: application/json receive 415
//     (forces a CORS preflight that a cross-origin attacker cannot satisfy).
//   - The entire mux is wrapped by RequireLocalHost so only loopback
//     connections are accepted.
//   - Sub-dashboard handlers are mounted via Handler() without their inner
//     per-dashboard Host/token middleware.
//   - /v1/events is mounted from wsbus.Server.Handler() which enforces
//     loopback-only + Origin allow-list (DNS-rebinding defence).
func New(cfg Config) *Server {
	pm := NewPresenceManager(cfg.Bus)
	s := &Server{cfg: cfg, mux: http.NewServeMux(), presence: pm}
	s.registerRoutes()
	// Wrap with edge auth: Host check + token (with static-asset exemptions)
	// + Content-Type gate for mutations.
	protected := dashauth.RequireLocalHost(cfg.addr(),
		requireTokenForNonStatic(cfg.Token, requireJSONForMutations(s.mux)))
	s.httpSrv = &http.Server{
		Addr:         cfg.addr(),
		Handler:      protected,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	return s
}

// Handler returns the underlying http.Handler for mounting in tests.
// Neither the Host-header middleware nor the token middleware is applied here —
// the caller supplies them.
func (s *Server) Handler() http.Handler { return s.mux }

// Serve starts the HTTP server and blocks until ctx is cancelled.
// Returns nil on clean shutdown (http.ErrServerClosed treated as nil).
func (s *Server) Serve(ctx context.Context) error {
	var ln net.Listener
	if s.cfg.Listener != nil {
		ln = s.cfg.Listener
	} else {
		var err error
		ln, err = net.Listen("tcp", s.cfg.addr())
		if err != nil {
			return fmt.Errorf("consoleui: listen %s: %w", s.cfg.addr(), err)
		}
		// Enforce loopback-only.
		host, _, _ := net.SplitHostPort(ln.Addr().String())
		ip := net.ParseIP(host)
		if ip != nil && !ip.IsLoopback() {
			_ = ln.Close()
			return fmt.Errorf("consoleui: addr %s is not a loopback address", s.cfg.addr())
		}
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

// registerRoutes wires all console endpoints.
func (s *Server) registerRoutes() {
	// ---- Static SPA shell (token-exempt — token arrives via fragment) ----------
	// Use method-neutral patterns to avoid the Go 1.22 method-specificity
	// conflict with the path-prefix handlers below (which are also method-neutral).
	s.mux.HandleFunc("/", s.handleIndex)
	s.mux.HandleFunc("/app.js", s.handleAppJS)
	s.mux.HandleFunc("/styles.css", s.handleCSS)
	// Service Worker served from a real same-origin path so browsers accept
	// registration at scope '/'.  Blob-URL registration is rejected by
	// Chrome/Firefox at scope '/'.  /sw.js is token-exempt (it carries no
	// secrets; the token is delivered via postMessage only).
	s.mux.HandleFunc("/sw.js", s.handleSW)

	// ---- Kanban sub-dashboard -----------------------------------------------
	// Mount kanban.Handler() under /kanban/. The kanban handler's own
	// Host-allowlist is NOT invoked (we used Handler() not Serve()).
	kanbanSrv := kanban.NewKanbanServer(s.cfg.KanbanBoardPath, s.cfg.KanbanProject, "")
	s.mux.Handle("/kanban/", http.StripPrefix("/kanban", kanbanSrv.Handler()))

	// ---- Metrics (cost) sub-dashboard ---------------------------------------
	metricsSrv := metricsdash.New(metricsdash.Config{
		Token:      s.cfg.Token, // same token; auth is at edge
		ProjectDir: s.cfg.MetricsProjectDir,
	})
	s.mux.Handle("/cost/", http.StripPrefix("/cost", metricsSrv.Handler()))

	// ---- Performance sub-dashboard ------------------------------------------
	perfSrv := perfdash.New(perfdash.Config{
		Token:   s.cfg.Token, // same token; auth is at edge
		WorkDir: s.cfg.PerfWorkDir,
	})
	s.mux.Handle("/perf/", http.StripPrefix("/perf", perfSrv.Handler()))

	// ---- WebSocket event stream at console origin ----------------------------
	// Phase 2.5: the console WS uses Sec-WebSocket-Protocol subprotocol auth
	// ("yakos-bearer, <token>") instead of the Authorization header — browsers
	// cannot set Authorization on WS upgrade requests.  buildConsoleWSHandler
	// applies loopbackOnly + Origin allow-list + subprotocol token validation
	// (constant-time via dashauth.TokenEqual) + presence join/leave lifecycle.
	if s.cfg.Bus != nil {
		wsHandler := buildConsoleWSHandler(s.cfg.Token, s.cfg.Bus, s.presence)
		s.mux.Handle("/v1/events", wsHandler)
	}

	// ---- Presence snapshot (token-gated via edge middleware) -----------------
	s.mux.HandleFunc("/api/presence", s.handlePresence)
}

// handlePresence returns the current online operator presence snapshot as a
// JSON array of PresenceRecord.  Auth is enforced by the edge middleware
// (RequireToken) — no re-check here.
func (s *Server) handlePresence(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	snap := s.presence.Snapshot()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(snap); err != nil {
		slog.Error("consoleui: handlePresence encode", "err", err)
	}
}

// ---- static handlers --------------------------------------------------------

// cspHeader returns the Content-Security-Policy value for the given bind addr.
// Serving the CSP as a response header (rather than a <meta> tag) ensures the
// connect-src ws:// origin is correct for any --console-addr value.
func cspHeader(addr string) string {
	// Derive the ws:// origin from the bound address.
	// addr is "host:port" (e.g. "127.0.0.1:7890").
	wsOrigin := "ws://" + addr
	return strings.Join([]string{
		"default-src 'self'",
		// app.js and sw.js are same-origin; no blob: needed.
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		// Allow WS connection to the console itself (for /v1/events).
		"connect-src 'self' " + wsOrigin,
		"frame-src 'self'",
		"base-uri 'none'",
		"form-action 'none'",
		"object-src 'none'",
		"frame-ancestors 'none'",
	}, "; ")
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	// Only serve the root path; let sub-paths 404 so they're handled by sub-muxes.
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	// Emit CSP as a response header so the ws:// origin matches --console-addr.
	w.Header().Set("Content-Security-Policy", cspHeader(s.cfg.addr()))
	// Cross-Origin-Resource-Policy: same-origin — prevents cross-origin reads.
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	_, _ = w.Write(indexHTML)
}

func (s *Server) handleAppJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	_, _ = w.Write(appJS)
}

func (s *Server) handleCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	_, _ = w.Write(stylesCSS)
}

func (s *Server) handleSW(w http.ResponseWriter, r *http.Request) {
	// Service-Worker-Allowed: / is required for a SW at /sw.js to claim scope '/'.
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Service-Worker-Allowed", "/")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	_, _ = w.Write(swJS)
}

// ---- auth helpers -----------------------------------------------------------

// isStaticAsset reports whether the request is for a token-exempt static asset.
// The assets /, /app.js, /styles.css, and /sw.js carry no secrets and must be
// accessible before the browser can obtain and present the bearer token.
func isStaticAsset(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	switch r.URL.Path {
	case "/", "/app.js", "/styles.css", "/sw.js":
		return true
	}
	return false
}

// RequireTokenForNonStatic wraps next with RequireToken for every path EXCEPT
// the token-exempt static assets (/, /app.js, /styles.css, /sw.js).
// All API paths, sub-dashboard paths, and /v1/events require the token.
//
// Exported so tests can replicate the production edge middleware without
// importing internal details.
func RequireTokenForNonStatic(token string, next http.Handler) http.Handler {
	return requireTokenForNonStatic(token, next)
}

func requireTokenForNonStatic(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isStaticAsset(r) {
			next.ServeHTTP(w, r)
			return
		}
		dashauth.RequireToken(token, next.ServeHTTP)(w, r)
	})
}

// RequireJSONForMutations wraps next with a Content-Type check for non-GET
// methods.  Any mutating request that does not declare Content-Type:
// application/json receives 415 Unsupported Media Type.
//
// This forces a CORS preflight on cross-origin mutation attempts because
// "application/json" is not a CORS-simple content type.  A cross-origin
// attacker whose preflight is rejected cannot replay the mutation.
// Token-exempt static assets (all GET) are not affected.
//
// Exported so tests can replicate the production edge middleware.
func RequireJSONForMutations(next http.Handler) http.Handler {
	return requireJSONForMutations(next)
}

func requireJSONForMutations(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			ct := r.Header.Get("Content-Type")
			// Accept "application/json" with or without charset suffix.
			if !strings.HasPrefix(ct, "application/json") {
				http.Error(w, "415 Unsupported Media Type: Content-Type must be application/json", http.StatusUnsupportedMediaType)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// DefaultAddr returns the default console listen address.
func DefaultAddr() string { return "127.0.0.1:7890" }
