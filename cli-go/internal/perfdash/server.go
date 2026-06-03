package perfdash

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "embed"

	"github.com/bakw00ds/yakos/internal/cost"
)

//go:embed dist/index.html
var indexHTML []byte

//go:embed dist/app.js
var appJS []byte

//go:embed dist/styles.css
var stylesCSS []byte

// Config holds all configuration for the perfdash HTTP server.
type Config struct {
	// Addr is the TCP listen address. Defaults to "127.0.0.1:7895".
	// Must be a loopback address.
	Addr string

	// Token is the read-only bearer token for the dashboard.
	// Use LoadOrCreatePerfToken to obtain it before constructing the server.
	Token string

	// WorkDir is the directory containing dispatch-log*.ndjson files.
	// Typically <workspace>/work/current.
	WorkDir string

	// Listener, when non-nil, is used directly instead of binding a new socket.
	// Injected in tests to avoid port conflicts.
	Listener net.Listener
}

func (c *Config) addr() string {
	if c.Addr != "" {
		return c.Addr
	}
	return "127.0.0.1:7895"
}

// Server is the performance dashboard HTTP server.
// It is read-only: no state mutation paths exist.
type Server struct {
	cfg     Config
	mux     *http.ServeMux
	httpSrv *http.Server
}

// New constructs a Server and wires all routes.
func New(cfg Config) *Server {
	s := &Server{cfg: cfg, mux: http.NewServeMux()}
	s.registerRoutes()
	s.httpSrv = &http.Server{
		Addr:         cfg.addr(),
		Handler:      s.mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	return s
}

// Handler returns the underlying http.Handler for use with httptest.NewServer.
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
			return fmt.Errorf("perfdash: listen %s: %w", s.cfg.addr(), err)
		}
		// Enforce loopback-only.
		host, _, _ := net.SplitHostPort(ln.Addr().String())
		ip := net.ParseIP(host)
		if ip != nil && !ip.IsLoopback() {
			_ = ln.Close()
			return fmt.Errorf("perfdash: addr %s is not a loopback address", s.cfg.addr())
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

// registerRoutes wires all perfdash endpoints.
func (s *Server) registerRoutes() {
	// Static assets — no auth required (the token is in the fragment, not in
	// the URL, so the browser will request these without it).
	s.mux.HandleFunc("GET /", s.handleIndex)
	s.mux.HandleFunc("GET /app.js", s.handleAppJS)
	s.mux.HandleFunc("GET /styles.css", s.handleCSS)

	// JSON API — all require Bearer token.
	s.mux.HandleFunc("GET /api/perf/summary", s.requireToken(s.handleSummary))
	s.mux.HandleFunc("GET /api/perf/timeseries", s.requireToken(s.handleTimeseries))
	s.mux.HandleFunc("GET /api/perf/by_axis", s.requireToken(s.handleByAxis))
	s.mux.HandleFunc("GET /api/perf/recent", s.requireToken(s.handleRecent))
}

// ---- auth middleware --------------------------------------------------------

// requireToken enforces Bearer token authentication.
// The token is compared in constant time to prevent timing attacks.
func (s *Server) requireToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := bearerToken(r)
		if tok == "" || !tokenEqual(tok, s.cfg.Token) {
			if tok == "" {
				writeErr(w, http.StatusUnauthorized, "missing Authorization: Bearer token")
			} else {
				writeErr(w, http.StatusForbidden, "invalid token")
			}
			return
		}
		next(w, r)
	}
}

// bearerToken extracts the Bearer token from the Authorization header.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	const prefix = "bearer "
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return h[len(prefix):]
	}
	return ""
}

// tokenEqual compares two strings in O(n) with early-exit only on length
// mismatch.  This is not constant-time for all compilers, but acceptable for a
// local dashboard token (no remote adversaries in the threat model).
func tokenEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

// ---- static asset handlers --------------------------------------------------

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(indexHTML)
}

func (s *Server) handleAppJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(appJS)
}

func (s *Server) handleCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(stylesCSS)
}

// ---- dispatch log helpers ---------------------------------------------------

// loadEvents reads all dispatch_finished events from WorkDir matching the
// since filter.  Empty since means all events.
func (s *Server) loadEvents(since string) ([]cost.Event, error) {
	paths, err := cost.LogFiles(s.cfg.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("perfdash: log files: %w", err)
	}
	ch := cost.StreamFiles(paths, since)
	return collectEvents(ch), nil
}

// queryWindow extracts and parses the ?window= query parameter.
func queryWindow(r *http.Request) time.Duration {
	return parseWindow(r.URL.Query().Get("window"))
}

// ---- /api/perf/summary ------------------------------------------------------

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	window := queryWindow(r)
	since := sinceISO(window)

	events, err := s.loadEvents(since)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "load events: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, ComputeSummary(events, 5))
}

// ---- /api/perf/timeseries ---------------------------------------------------

func (s *Server) handleTimeseries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	window := queryWindow(r)
	bucket := bucketDuration(q.Get("bucket"))
	metric := q.Get("metric")
	if metric == "" {
		metric = "dispatches"
	}
	switch metric {
	case "cost", "latency", "dispatches":
		// valid
	default:
		writeErr(w, http.StatusBadRequest, "metric must be cost|latency|dispatches")
		return
	}

	since := sinceISO(window)
	events, err := s.loadEvents(since)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "load events: "+err.Error())
		return
	}

	points := ComputeTimeseries(events, window, bucket, metric)
	writeJSON(w, http.StatusOK, points)
}

// ---- /api/perf/by_axis ------------------------------------------------------

func (s *Server) handleByAxis(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	axis := q.Get("axis")
	if axis == "" {
		axis = "agent"
	}
	switch axis {
	case "agent", "runtime", "project", "day":
		// valid
	default:
		writeErr(w, http.StatusBadRequest, "axis must be agent|runtime|project|day")
		return
	}

	window := queryWindow(r)
	since := sinceISO(window)

	events, err := s.loadEvents(since)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "load events: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, ComputeByAxis(events, axis))
}

// ---- /api/perf/recent -------------------------------------------------------

func (s *Server) handleRecent(w http.ResponseWriter, r *http.Request) {
	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
			limit = n
		}
	}

	// Recent uses a 7-day window to give a useful default view.
	since := sinceISO(7 * 24 * time.Hour)
	events, err := s.loadEvents(since)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "load events: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, RecentDispatches(events, limit))
}

// ---- response helpers -------------------------------------------------------

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

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// ---- WorkDir resolution helper (exported for integration from serve) ---------

// DefaultWorkDir returns the default work/current directory under workspaceRoot.
func DefaultWorkDir(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, "work", "current")
}

// DefaultStateDir returns the default ~/.yakos-state directory.
func DefaultStateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), ".yakos-state")
	}
	return filepath.Join(home, ".yakos-state")
}
