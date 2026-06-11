package metricsdash

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "embed"

	"github.com/bakw00ds/yakos/internal/metrics"
)

//go:embed dist/index.html
var indexHTML []byte

// Config holds all configuration for the metrics dashboard HTTP server.
type Config struct {
	// Addr is the TCP listen address. Defaults to "127.0.0.1:7896".
	// Must be a loopback address.
	Addr string

	// Token is the read-only bearer token for the dashboard.
	// Use LoadOrCreateMetricsToken to obtain it before constructing the server.
	Token string

	// ProjectDir is the project whose history.ndjson is served.
	// The single-project API uses ReadHistory(ProjectDir).
	ProjectDir string

	// AllProjects, when true, enables GET /api/metrics/projects rollup.
	// The server discovers projects via ~/agent-control/*/.project-path.
	AllProjects bool

	// HomeDir is used for agent-control discovery in AllProjects mode.
	// Defaults to os.UserHomeDir().
	HomeDir string

	// Listener, when non-nil, is used directly instead of binding a new socket.
	// Injected in tests to avoid port conflicts.
	Listener net.Listener
}

func (c *Config) addr() string {
	if c.Addr != "" {
		return c.Addr
	}
	return "127.0.0.1:7896"
}

// Server is the metrics dashboard HTTP server.
// It is strictly read-only: no endpoint may mutate history.ndjson.
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
			return fmt.Errorf("metrics serve: listen %s: %w", s.cfg.addr(), err)
		}
		// Enforce loopback-only.
		host, _, _ := net.SplitHostPort(ln.Addr().String())
		ip := net.ParseIP(host)
		if ip != nil && !ip.IsLoopback() {
			_ = ln.Close()
			return fmt.Errorf("metrics serve: addr %s is not a loopback address — refusing to bind publicly", s.cfg.addr())
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

// ValidateAddr returns an error if addr is a non-loopback bind address.
// Call this before Serve to get an early loud error.
func ValidateAddr(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// Might be host-only, try parsing directly.
		host = addr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil // hostname — let OS resolve; we check again at Listen time
	}
	if !ip.IsLoopback() {
		return fmt.Errorf("metrics serve: --host %s is not a loopback address; refusing to expose dashboard publicly (use 127.0.0.1 or ::1)", host)
	}
	return nil
}

// registerRoutes wires all metrics dashboard endpoints.
func (s *Server) registerRoutes() {
	// Static assets — no auth required (token is in URL fragment, never sent
	// as a request header by the browser for initial page load).
	s.mux.HandleFunc("GET /", s.handleIndex)

	// JSON API — all require Bearer token.
	s.mux.HandleFunc("GET /api/metrics/snapshot", s.requireToken(s.handleSnapshot))
	s.mux.HandleFunc("GET /api/metrics/trend", s.requireToken(s.handleTrend))
	s.mux.HandleFunc("GET /api/metrics/compare", s.requireToken(s.handleCompare))
	s.mux.HandleFunc("GET /api/metrics/history", s.requireToken(s.handleHistory))
	if s.cfg.AllProjects {
		s.mux.HandleFunc("GET /api/metrics/projects", s.requireToken(s.handleProjects))
	}
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

// tokenEqual compares two strings with constant-time-like behaviour.
// Length mismatch exits early which is acceptable for a local dashboard.
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

// ---- history loader (READ-ONLY) ---------------------------------------------

// loadHistory reads snapshots for the configured project. It never writes.
func (s *Server) loadHistory() ([]metrics.Snapshot, error) {
	if s.cfg.ProjectDir == "" {
		return nil, nil
	}
	return metrics.ReadHistory(s.cfg.ProjectDir)
}

// ---- GET /api/metrics/snapshot ----------------------------------------------

func (s *Server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	snaps, err := s.loadHistory()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "read history: "+err.Error())
		return
	}
	latest := LatestSnapshot(snaps)
	if latest == nil {
		writeJSON(w, http.StatusOK, map[string]string{"message": "no snapshots yet — run: yakos metrics collect"})
		return
	}
	writeJSON(w, http.StatusOK, latest)
}

// ---- GET /api/metrics/history -----------------------------------------------

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	snaps, err := s.loadHistory()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "read history: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, HistoryHeaders(snaps))
}

// ---- GET /api/metrics/trend -------------------------------------------------

func (s *Server) handleTrend(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	metricPath := q.Get("metric")
	if metricPath == "" {
		metricPath = "efficiency.total_cost_usd"
	}
	lastN := 10
	if v := q.Get("last"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			lastN = n
		}
	}
	sinceTS := q.Get("since")

	snaps, err := s.loadHistory()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "read history: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, TrendSeries(snaps, metricPath, lastN, sinceTS))
}

// ---- GET /api/metrics/compare -----------------------------------------------

func (s *Server) handleCompare(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	shaA := q.Get("a")
	shaB := q.Get("b")
	if shaA == "" || shaB == "" {
		writeErr(w, http.StatusBadRequest, "compare requires ?a=<sha>&b=<sha>")
		return
	}

	snaps, err := s.loadHistory()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "read history: "+err.Error())
		return
	}
	snapA, snapB := CompareSnapshots(snaps, shaA, shaB)
	if snapA == nil {
		writeErr(w, http.StatusNotFound, "snapshot for commit "+shaA+" not found")
		return
	}
	if snapB == nil {
		writeErr(w, http.StatusNotFound, "snapshot for commit "+shaB+" not found")
		return
	}
	writeJSON(w, http.StatusOK, CompareDiff{A: snapA, B: snapB})
}

// ---- GET /api/metrics/projects (--all-projects) ----------------------------

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	home := s.cfg.HomeDir
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "home dir: "+err.Error())
			return
		}
	}

	summaries := discoverProjects(home)
	writeJSON(w, http.StatusOK, summaries)
}

// discoverProjects walks ~/agent-control/*/.project-path to find known projects
// and returns per-project headline summaries. Lightweight: reads only the last
// snapshot from each history file.
func discoverProjects(home string) []ProjectSummary {
	acDir := filepath.Join(home, "agent-control")
	entries, err := fs.ReadDir(os.DirFS(acDir), ".")
	if err != nil {
		// agent-control dir absent — return empty list (not an error).
		return []ProjectSummary{}
	}

	var results []ProjectSummary
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ppFile := filepath.Join(acDir, e.Name(), ".project-path")
		data, err := os.ReadFile(ppFile) //nolint:gosec
		if err != nil {
			continue
		}
		projectPath := strings.TrimRight(string(data), "\r\n ")
		if projectPath == "" {
			continue
		}

		histPath := metrics.HistoryPath(projectPath)
		snaps, err := metrics.ReadHistory(projectPath)
		if err != nil {
			// History unreadable — include entry with zero snapshots.
			results = append(results, ProjectSummary{
				Project:       e.Name(),
				HistoryPath:   histPath,
				SnapshotCount: 0,
				Latest:        nil,
			})
			continue
		}
		results = append(results, ProjectSummary{
			Project:       e.Name(),
			HistoryPath:   histPath,
			SnapshotCount: len(snaps),
			Latest:        LatestSnapshot(snaps),
		})
	}
	return results
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
