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
	"sync"
	"time"

	_ "embed"

	"github.com/bakw00ds/yakos/internal/cost"
	"github.com/bakw00ds/yakos/internal/dashauth"
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

// eventsCache is an mtime+size-keyed cache for parsed dispatch-log events.
// It avoids re-parsing all log files on every API request; the cache is
// invalidated when any log file's mtime, size, or the since filter changes.
// This mirrors the historyCache pattern in internal/metricsdash/server.go.
type eventsCache struct {
	mu        sync.Mutex
	since     string    // ISO-8601 lower-bound filter at time of last load
	maxMtime  time.Time // latest mtime across all log files
	totalSize int64     // sum of sizes of all log files
	events    []cost.Event
}

// load returns cached events when the log files and since filter are unchanged;
// otherwise it re-reads and re-parses all matching log files.
func (c *eventsCache) load(workDir, since string) ([]cost.Event, error) {
	if workDir == "" {
		return nil, nil
	}
	paths, err := cost.LogFiles(workDir)
	if err != nil {
		return nil, fmt.Errorf("perfdash: log files: %w", err)
	}

	// Compute aggregate mtime and size across all log files.
	var maxMtime time.Time
	var totalSize int64
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			// Non-fatal: a file may have been rotated away between LogFiles and Stat.
			continue
		}
		if fi.ModTime().After(maxMtime) {
			maxMtime = fi.ModTime()
		}
		totalSize += fi.Size()
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Cache hit: files unchanged and same since filter.
	if since == c.since && maxMtime.Equal(c.maxMtime) && totalSize == c.totalSize {
		return c.events, nil
	}

	// Cache miss: re-parse.
	ch := cost.StreamFiles(paths, since)
	evs := collectEvents(ch)
	c.since = since
	c.maxMtime = maxMtime
	c.totalSize = totalSize
	c.events = evs
	return evs, nil
}

// Server is the performance dashboard HTTP server.
// It is read-only: no state mutation paths exist.
type Server struct {
	cfg     Config
	mux     *http.ServeMux
	httpSrv *http.Server
	cache   eventsCache
}

// New constructs a Server and wires all routes.
func New(cfg Config) *Server {
	s := &Server{cfg: cfg, mux: http.NewServeMux()}
	s.registerRoutes()
	// Wrap the mux with Host-header / DNS-rebinding defense.
	// The middleware rejects any request whose Host is not one of the
	// loopback addresses for the configured port.
	protected := dashauth.RequireLocalHost(cfg.addr(), s.mux)
	s.httpSrv = &http.Server{
		Addr:         cfg.addr(),
		Handler:      protected,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	return s
}

// Handler returns the underlying http.Handler for use with httptest.NewServer.
// Note: the Host-header middleware is NOT applied here because httptest uses
// a random port and the Host header would not match the configured addr.
// Tests that specifically exercise Host-header rejection should build a
// request with req.Host set and use RequireLocalHost directly.
func (s *Server) Handler() http.Handler { return s.mux }

// HandlerNoToken returns a handler that serves the same routes as Handler but
// without the inner per-endpoint Bearer-token check.  Use this when mounting
// the performance dashboard inside a host server that already enforces
// authentication at the edge (e.g. the session console, where the session
// cookie is the auth credential and no #token= fragment is present in the
// iframe URL).
//
// The host server is responsible for ensuring that only authenticated requests
// reach this handler.
func (s *Server) HandlerNoToken() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /app.js", s.handleAppJS)
	mux.HandleFunc("GET /styles.css", s.handleCSS)
	mux.HandleFunc("GET /api/perf/summary", s.handleSummary)
	mux.HandleFunc("GET /api/perf/timeseries", s.handleTimeseries)
	mux.HandleFunc("GET /api/perf/by_axis", s.handleByAxis)
	mux.HandleFunc("GET /api/perf/recent", s.handleRecent)
	return mux
}

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

// requireToken enforces Bearer token authentication using dashauth.
// Constant-time comparison via crypto/subtle is handled in dashauth.TokenEqual.
func (s *Server) requireToken(next http.HandlerFunc) http.HandlerFunc {
	return dashauth.RequireToken(s.cfg.Token, next)
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

// loadEvents returns dispatch_finished events from WorkDir matching since.
// Results are served from the mtime+size cache when the log files are unchanged
// and the since filter is the same as the last request; otherwise re-parsed.
// Empty since means all events.
func (s *Server) loadEvents(since string) ([]cost.Event, error) {
	return s.cache.load(s.cfg.WorkDir, since)
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

// DefaultStateDir returns the yakOS state directory that the dispatch daemon
// writes to.  Resolution order (matches dispatch/events.go dispatchLogPath):
//
//  1. YAKOS_DISPATCH_LOG env var — the writer uses this as an override directory.
//  2. ~/.yakos-state — the default.
//
// Using the same precedence as the writer ensures the dashboard reads from
// exactly the same location the daemon writes to.
func DefaultStateDir() string {
	if v := os.Getenv("YAKOS_DISPATCH_LOG"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), ".yakos-state")
	}
	return filepath.Join(home, ".yakos-state")
}
