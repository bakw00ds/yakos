package consoleui

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/dashauth"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/kanban"
	"github.com/bakw00ds/yakos/internal/metricsdash"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/perfdash"
	"github.com/bakw00ds/yakos/internal/workflow"
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

// vendorFS embeds the pinned, checksum-verified third-party JS blobs.
// Served at /vendor/<filename> (same-origin 'self'; no CDN dependency).
//
//go:embed dist/vendor/mermaid.min.js
var vendorFS embed.FS

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

	// WorkDir is the <workspace>/work/current directory used for chat transcript
	// persistence (<WorkDir>/chats/<conversationId>.ndjson).  Required for the
	// Phase 3b chat endpoints; when empty the transcript handler will return
	// 503 Service Unavailable.  Also used by the Flows engine for workflow
	// artifacts (<WorkDir>/workflows/).
	WorkDir string

	// WorkflowEngine is the Phase 4/5 DAG executor.  When non-nil, the /flows/*
	// endpoints are fully operational (run + resume launch real goroutines).
	// When nil, list/get/save still work (YAML authoring), but run/resume
	// return 503 Service Unavailable.
	WorkflowEngine *workflow.Engine

	// Listener, when non-nil, is used directly instead of binding a new socket.
	// Injected in tests to avoid port conflicts.
	Listener net.Listener

	// StateDir is the yakOS state directory (e.g. ~/.yakos-state) used to
	// locate the mTLS role-mapping file (mtls/roles.json) for the identity
	// resolver.  When empty, the identity resolver uses an empty stateDir and
	// all authenticated certs default to RoleRead (missing-file-tolerant).
	// Loopback bearer sessions always resolve to admin regardless of StateDir.
	StateDir string
}

func (c *Config) addr() string {
	if c.Addr != "" {
		return c.Addr
	}
	return "127.0.0.1:7890"
}

// Server is the unified console HTTP server.
type Server struct {
	cfg          Config
	mux          *http.ServeMux
	httpSrv      *http.Server
	presence     *PresenceManager
	chatHub      *ChatHub
	chat         *chatHandlers
	flows        *flowsHandlers
	serverCtx    context.Context    // cancelled on Serve shutdown; dispatch goroutines use this
	serverCancel context.CancelFunc // called by Serve when the server shuts down
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
	hub := NewChatHub()
	var transcripts *Transcripts
	if cfg.WorkDir != "" {
		transcripts = NewTranscripts(cfg.WorkDir)
	} else {
		// Fallback: use a temp-like path; the handler will reject reads/writes
		// gracefully via the Transcripts nil-safety check in registerChatRoutes.
		transcripts = NewTranscripts("")
	}
	// serverCtx is cancelled when Serve returns (i.e. on server shutdown).
	// Dispatch goroutines derive their context from serverCtx — NOT from the
	// per-request r.Context() — so they survive the 202 response returning.
	serverCtx, serverCancel := context.WithCancel(context.Background())
	chatH := newChatHandlers(hub, transcripts, cfg.DispatchService, serverCtx)
	flowsH := &flowsHandlers{
		engine:    cfg.WorkflowEngine,
		workDir:   cfg.WorkDir,
		serverCtx: serverCtx,
	}
	s := &Server{
		cfg:          cfg,
		mux:          http.NewServeMux(),
		presence:     pm,
		chatHub:      hub,
		chat:         chatH,
		flows:        flowsH,
		serverCtx:    serverCtx,
		serverCancel: serverCancel,
	}
	s.registerRoutes()
	// Build the identity resolver.  It is purely additive in this PR:
	// it stamps an Identity onto each request's context but does not gate or
	// reject anything.  Enforcement (role checks, operator_id override) is the
	// responsibility of later middleware and the dispatch facade (next PR per
	// ADR-0004).
	//
	// callerLabelFn extracts the cooperative OperatorID from a request.  For
	// loopback bearer sessions the operatorID comes from dispatch.Service's
	// daemon-level mintOperatorID; we have no per-request handle to it here,
	// so we return "" (empty) and let the dispatch facade stamp it as usual.
	// This is intentional: no behaviour change on the loopback path.
	mapper := netid.NewRoleMapper(cfg.StateDir)
	resolver := netid.NewResolver(mapper, func(r *http.Request) string {
		// No per-request cooperative label is available at the edge; the
		// dispatch facade stamps operator_id from its daemon-level opID.
		return ""
	})

	// Wrap with edge auth: Host check + token (with static-asset exemptions)
	// + Content-Type gate for mutations + identity resolution (additive only).
	//
	// Order (outer → inner):
	//   1. RequireLocalHost      — DNS-rebinding defence; loopback-only assertion
	//   2. requireTokenForNonStatic — bearer-token gate for non-static assets
	//   3. requireJSONForMutations  — Content-Type gate; CSRF defence
	//   4. resolver.Middleware      — identity stamping (no enforcement, just context)
	//   5. s.mux                   — route handlers
	protected := dashauth.RequireLocalHost(cfg.addr(),
		requireTokenForNonStatic(cfg.Token, requireJSONForMutations(resolver.Middleware(s.mux))))
	s.httpSrv = &http.Server{
		Addr:        cfg.addr(),
		Handler:     protected,
		ReadTimeout: 30 * time.Second,
		// WriteTimeout intentionally 0: the SSE /api/chat/stream endpoint holds
		// long-lived streaming responses that never complete.  A non-zero
		// WriteTimeout would force-close them after the deadline.  The server
		// is loopback-only (no external exposure), matching the pattern used in
		// wsbus/server.go and mcpserver/streamhttp.go.
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}
	return s
}

// Handler returns the underlying http.Handler for mounting in tests.
// Neither the Host-header middleware nor the token middleware is applied here —
// the caller supplies them.
func (s *Server) Handler() http.Handler { return s.mux }

// ChatHub returns the chat routing hub, exposed for testing.
func (s *Server) ChatHub() *ChatHub { return s.chatHub }

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
		// Cancel the server-lifetime context so all dispatch goroutines
		// that are still running receive their cancellation signal.
		s.serverCancel()
		return <-errCh
	case err := <-errCh:
		s.serverCancel()
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

	// Vendored JS blobs — pinned, checksum-verified, served same-origin.
	// Token-exempt: static assets that carry no secrets.
	// The files live under dist/vendor/ in the embed.FS; strip the
	// /vendor/ prefix so the FS path is dist/vendor/<filename>.
	// Wrapped to set Cross-Origin-Resource-Policy: same-origin, consistent
	// with the other static asset handlers (handleIndex, handleAppJS, etc.).
	vendorSub, _ := fs.Sub(vendorFS, "dist/vendor")
	vendorHandler := http.StripPrefix("/vendor/", http.FileServer(http.FS(vendorSub)))
	s.mux.Handle("/vendor/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		vendorHandler.ServeHTTP(w, r)
	}))

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

	// ---- Phase 3b: Chat SSE + dispatch + transcript (token-gated at edge) ---
	// GET  /api/chat/stream      — per-operator SSE stream (multiplexed by sessionID)
	// POST /api/chat/dispatch    — start a streaming dispatch; returns {sessionId}
	// POST /api/chat/cancel      — cancel an in-flight dispatch (idempotent)
	// GET  /api/chat/transcript  — fetch persisted transcript for a conversationId
	//
	// These paths are NOT static assets, so the edge requireTokenForNonStatic
	// middleware enforces Authorization: Bearer on all of them.
	s.mux.HandleFunc("/api/chat/stream", s.chat.handleChatStream)
	s.mux.HandleFunc("/api/chat/dispatch", s.chat.handleChatDispatch)
	s.mux.HandleFunc("/api/chat/cancel", s.chat.handleChatCancel)
	s.mux.HandleFunc("/api/chat/transcript", s.chat.handleChatTranscript)
	// POST /api/chat/share — flip shared flag; owner-gated.
	s.mux.HandleFunc("/api/chat/share", s.chat.handleChatShare)

	// ---- Phase 5: Flows UI endpoints (token-gated at edge) ------------------
	// GET  /flows/api/workflows          — list workflow names
	// GET  /flows/api/workflow?name=<n>  — get workflow YAML + version stamp
	// POST /flows/api/workflow           — save workflow (optimistic-concurrency)
	// POST /flows/api/run?name=<n>       — start a new run; returns {run_id}
	// POST /flows/api/resume             — resume a prior run; returns {new_run_id}
	// GET  /flows/api/run?id=<runId>     — poll run state (run.json)
	// GET  /flows/api/run/node?id=<r>&node=<n> — node stdout
	s.mux.HandleFunc("/flows/api/workflows", s.flows.handleListWorkflows)
	s.mux.HandleFunc("/flows/api/workflow", s.flows.handleWorkflowDispatch)
	s.mux.HandleFunc("/flows/api/run/node", s.flows.handleGetNodeOutput)
	s.mux.HandleFunc("/flows/api/run", s.flows.handleRunDispatch)
	s.mux.HandleFunc("/flows/api/resume", s.flows.handleResume)
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
// The assets /, /app.js, /styles.css, /sw.js, and vendored blobs under /vendor/
// carry no secrets and must be accessible before the browser can obtain and
// present the bearer token.
func isStaticAsset(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	switch r.URL.Path {
	case "/", "/app.js", "/styles.css", "/sw.js":
		return true
	}
	// Vendored pinned blobs are same-origin static assets; no token required.
	if strings.HasPrefix(r.URL.Path, "/vendor/") {
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
