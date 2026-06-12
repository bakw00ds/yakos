package consoleui

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	_ "embed"

	"github.com/bakw00ds/yakos/internal/dashauth"
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
	cfg     Config
	mux     *http.ServeMux
	httpSrv *http.Server
}

// New constructs a Server and wires all routes.
//
// Auth model:
//   - The ENTIRE mux (including static assets at / /app.js /styles.css) is
//     protected by RequireLocalHost + RequireToken at the edge EXCEPT for
//     GET / which serves the SPA shell unauthenticated (the browser needs to
//     load the shell before it can read the fragment token and authenticate).
//   - Sub-dashboard handlers are mounted via Handler() without their inner
//     per-dashboard Host/token middleware.
//   - /v1/events is mounted from wsbus.Server.Handler() which retains only
//     the loopbackOnly middleware (token auth is handled at the edge).
func New(cfg Config) *Server {
	s := &Server{cfg: cfg, mux: http.NewServeMux()}
	s.registerRoutes()
	// Wrap with edge auth: Host check + single token.
	// GET / is exempt so the browser can load the SPA shell from the fragment URL.
	protected := dashauth.RequireLocalHost(cfg.addr(), requireTokenExceptRoot(cfg.Token, s.mux))
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
	// ---- Static SPA shell (unauthenticated — token arrives via fragment) ----
	// Use method-neutral patterns to avoid the Go 1.22 method-specificity
	// conflict with the path-prefix handlers below (which are also method-neutral).
	s.mux.HandleFunc("/", s.handleIndex)
	s.mux.HandleFunc("/app.js", s.handleAppJS)
	s.mux.HandleFunc("/styles.css", s.handleCSS)

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
	// wsbus.Server.Handler() mounts /v1/events with loopbackOnly middleware.
	// The token is validated at the console edge (RequireToken above), so we
	// build the wsbus.Server without a token requirement for the inner handler.
	// We create a synthetic wsbus.Server that exposes just the WS handler.
	if s.cfg.Bus != nil {
		wsSrv, err := wsbus.NewServer(wsbus.ServerConfig{
			// Token is intentionally set to the console token so the wsbus
			// inner authenticate middleware also enforces it. The edge token
			// check already validated it, but defence-in-depth is cheap.
			Token: s.cfg.Token,
			Bus:   s.cfg.Bus,
		})
		if err == nil {
			// Mount the WS handler. The wsbus Handler() includes loopbackOnly
			// but NOT the authenticate middleware (that's in Serve's mux).
			// Here we use the full Handler() which includes authenticate so
			// that the WS upgrade also validates the token in both directions.
			s.mux.Handle("/v1/events", wsSrv.Handler())
		}
	}
}

// ---- static handlers --------------------------------------------------------

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	// Only serve the root path; let sub-paths 404 so they're handled by sub-muxes.
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
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

// ---- auth helpers -----------------------------------------------------------

// requireTokenExceptRoot wraps next with RequireToken for every path EXCEPT
// GET / (the SPA shell).  The SPA shell must be served without a token so the
// browser can load it and read the #token fragment.
//
// All API paths, sub-dashboard paths, /app.js, /styles.css, and /v1/events
// require the token.  The SPA itself does not contain sensitive data.
func requireTokenExceptRoot(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			next.ServeHTTP(w, r)
			return
		}
		dashauth.RequireToken(token, next.ServeHTTP)(w, r)
	})
}

// DefaultAddr returns the default console listen address.
func DefaultAddr() string { return "127.0.0.1:7890" }
