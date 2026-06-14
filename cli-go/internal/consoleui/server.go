package consoleui

import (
	"context"
	"crypto/tls"
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
// Served at /vendor/<path> (same-origin 'self'; no CDN dependency).
//
// Mermaid: dist/vendor/mermaid.min.js — Flows UI DAG renderer.
// Monaco:  dist/vendor/monaco/ — full min/vs tree + CHECKSUMS.sha256 manifest.
//
//	Includes loader.js, editor/*, basic-languages/* (all language grammars),
//	language/* (TypeScript/JSON/CSS/HTML language services), base/*.
//	The "all:" prefix ensures subdirectories are included by go:embed.
//	The integrity manifest (monaco/CHECKSUMS.sha256) is verified by
//	TestVendorChecksums in vendor_checksum_test.go.
//
//go:embed dist/vendor/mermaid.min.js
//go:embed dist/vendor/VENDOR.md
//go:embed all:dist/vendor/monaco
var vendorFS embed.FS

//go:embed dist/ide-editor.html
var ideEditorHTML []byte

//go:embed dist/ide-editor.js
var ideEditorJS []byte

// Config holds all configuration for the unified console HTTP server.
type Config struct {
	// Addr is the TCP listen address. Defaults to "127.0.0.1:7890".
	// When NetworkedMode is false (default), must be a loopback address.
	// When NetworkedMode is true, this is the non-loopback address to bind.
	Addr string

	// TLSConfig, when non-nil, causes the server to serve TLS on Listener /
	// Addr instead of plain HTTP.  Required when NetworkedMode is true (the
	// caller sets this to the mTLS config from mtls.BuildServerTLSConfig).
	// MUST be nil when NetworkedMode is false (loopback path unchanged).
	TLSConfig *tls.Config

	// NetworkedMode, when true, signals that this server is bound to a
	// non-loopback address.  This triggers:
	//   - wss:// origin in CSP and WS Origin allow-list (instead of ws://)
	//   - loopbackTrusted=false in the identity Resolver (certless → RoleRead)
	//   - Removal of the loopback-only assertion in Serve()
	//   - Admission of the external origin in the WS Origin allow-list
	// When false (default), all loopback-path behaviour is completely
	// unchanged.
	NetworkedMode bool

	// ExternalHost is the host[:port] that browsers use to reach this
	// non-loopback server.  Derived from Addr when empty.  Used to build
	// the wss:// allowed Origin in the WS allow-list.
	// Ignored when NetworkedMode is false.
	//
	// Deprecated: use ExternalHosts (slice) for multi-host support.
	// When both are set, ExternalHosts takes precedence.
	ExternalHost string

	// ExternalHosts is the list of host[:port] values that browsers may use to
	// reach this non-loopback server.  Each entry becomes an allowed Origin in
	// the WS allow-list and a SAN in the server cert.  The first entry is used
	// for the CSP wss:// directive and the startup banner URL.
	// Ignored when NetworkedMode is false.
	ExternalHosts []string

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

// externalHosts returns the effective list of external host:port values.
// ExternalHosts takes precedence over ExternalHost (single string).
// When neither is set, falls back to the bind address.
// Returns nil when NetworkedMode is false.
func (c *Config) externalHosts() []string {
	if !c.NetworkedMode {
		return nil
	}
	if len(c.ExternalHosts) > 0 {
		return c.ExternalHosts
	}
	if c.ExternalHost != "" {
		return []string{c.ExternalHost}
	}
	return []string{c.addr()}
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
	// Build the identity resolver.
	//
	// loopbackTrusted controls the no-cert fallback:
	//   - true  (loopback path): certless requests → RoleAdmin / Authenticated=false.
	//             Preserves today's cooperative-labeling bearer-token behaviour exactly.
	//   - false (networked path): certless requests → RoleRead / Authenticated=false.
	//             Defence-in-depth alongside RequireAndVerifyClientCert in the TLS
	//             layer — even if TLS config were somehow misconfigured, certless
	//             requests NEVER receive admin on the networked listener.
	//
	// callerLabelFn extracts the cooperative OperatorID for loopback bearer sessions.
	// The dispatch facade stamps operator_id from its daemon-level opID on the
	// loopback path; we return "" here and let the facade do it.
	loopbackTrusted := !cfg.NetworkedMode
	mapper := netid.NewRoleMapper(cfg.StateDir)
	resolver := netid.NewResolver(mapper, func(r *http.Request) string {
		// No per-request cooperative label is available at the edge; the
		// dispatch facade stamps operator_id from its daemon-level opID.
		return ""
	}, loopbackTrusted)

	// Build the inner handler chain (shared between loopback and networked paths).
	//
	// Order (outer → inner):
	//   3. requireJSONForMutations  — Content-Type gate; CSRF defence
	//   4. resolver.Middleware      — identity stamping (no enforcement, just context)
	//   5. s.mux                   — route handlers
	inner := requireJSONForMutations(resolver.Middleware(s.mux))

	var protected http.Handler
	if cfg.NetworkedMode {
		// Networked path: the mTLS TLS layer replaces the loopback-only Host check.
		// We still apply the token middleware for the bearer-token subprotocol on WS
		// (browser WS connections still use Sec-WebSocket-Protocol token auth),
		// and the Content-Type gate for CSRF defence.
		//
		// The RequireLocalHost guard is intentionally NOT applied here — it would
		// reject all legitimate non-loopback traffic.  Instead, TLS
		// RequireAndVerifyClientCert provides the equivalent network-layer guard.
		protected = requireTokenForNonStatic(cfg.Token, inner)
	} else {
		// Loopback path (default): wrap with edge auth unchanged.
		//   1. RequireLocalHost      — DNS-rebinding defence; loopback-only assertion
		//   2. requireTokenForNonStatic — bearer-token gate for non-static assets
		protected = dashauth.RequireLocalHost(cfg.addr(),
			requireTokenForNonStatic(cfg.Token, inner))
	}

	s.httpSrv = &http.Server{
		Addr:    cfg.addr(),
		Handler: protected,
		// TLSConfig is set here only for the networked path; Serve() uses
		// tls.NewListener so http.Server.ServeTLS is not needed.
		TLSConfig:   cfg.TLSConfig,
		ReadTimeout: 30 * time.Second,
		// WriteTimeout intentionally 0: the SSE /api/chat/stream endpoint holds
		// long-lived streaming responses that never complete.  A non-zero
		// WriteTimeout would force-close them after the deadline.  The server
		// is loopback-only (no external exposure) on the default path; the
		// networked path is guarded by mTLS, matching the pattern used in
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
//
// Loopback path (NetworkedMode=false): unchanged from prior phases.
//   - Binds a plain TCP listener; enforces loopback-only via IsLoopback check.
//   - Any non-loopback address returns an error before opening the listener.
//
// Networked path (NetworkedMode=true): mTLS TLS listener.
//   - The caller MUST have set cfg.TLSConfig (via mtls.BuildServerTLSConfig) and
//     must have verified mTLS material is available before calling Serve.
//   - The plain TCP listener is wrapped with tls.NewListener before Serve.
//   - The loopback-only assertion is NOT applied (intentional: the mTLS layer
//     is the network-boundary guard for the networked path).
func (s *Server) Serve(ctx context.Context) error {
	var ln net.Listener
	if s.cfg.Listener != nil {
		// Fail-closed: even with an injected listener, the networked path
		// requires TLSConfig.  Without it we would silently serve plain HTTP
		// on a listener the caller intended to be TLS-protected.
		if s.cfg.NetworkedMode && s.cfg.TLSConfig == nil {
			_ = s.cfg.Listener.Close()
			return fmt.Errorf("consoleui: NetworkedMode requires TLSConfig (mTLS material not provided)")
		}
		ln = s.cfg.Listener
		// For the networked path with an injected listener (e.g. tests), wrap
		// with TLS when TLSConfig is set.  Callers that pre-wrap with TLS
		// should set TLSConfig=nil to skip the double-wrap.
		if s.cfg.TLSConfig != nil {
			ln = tls.NewListener(ln, s.cfg.TLSConfig)
		}
	} else {
		var err error
		tcpLn, err := net.Listen("tcp", s.cfg.addr())
		if err != nil {
			return fmt.Errorf("consoleui: listen %s: %w", s.cfg.addr(), err)
		}

		if !s.cfg.NetworkedMode {
			// Enforce loopback-only on the plain HTTP (loopback) path.
			host, _, _ := net.SplitHostPort(tcpLn.Addr().String())
			ip := net.ParseIP(host)
			if ip != nil && !ip.IsLoopback() {
				_ = tcpLn.Close()
				return fmt.Errorf("consoleui: addr %s is not a loopback address; use --console-bind with mTLS for non-loopback", s.cfg.addr())
			}
			ln = tcpLn
		} else {
			// Networked path: wrap with TLS.  TLSConfig must be non-nil (caller
			// validates this before calling Serve).
			if s.cfg.TLSConfig == nil {
				_ = tcpLn.Close()
				return fmt.Errorf("consoleui: NetworkedMode requires TLSConfig (mTLS material not provided)")
			}
			ln = tls.NewListener(tcpLn, s.cfg.TLSConfig)
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

	// IDE editor bootstrap script — token-exempt so the /ide/editor iframe
	// can load it before the SW has injected the Authorization header.
	// All Monaco initialisation code lives here; ide-editor.html contains
	// only the DOM skeleton and a <script src="/ide-editor.js">.
	s.mux.HandleFunc("/ide-editor.js", s.handleIDEEditorJS)

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

	// ---- IDE editor spike: isolated host document ----------------------------
	// GET /ide/editor — serves dist/ide-editor.html as a TOKEN-EXEMPT static
	// shell, consistent with / (index.html) and /app.js.
	//
	// Security rationale: the shell itself carries NO workspace content.
	// File content is served only by the RoleRead-gated /api/files/* endpoints
	// and delivered into the editor via postMessage from the authenticated parent
	// frame.  On the networked path, access is still gated at the transport layer
	// (mTLS RequireAndVerifyClientCert).  On the loopback path the server is
	// loopback-only by construction.  Putting a RoleRead gate here conflicted
	// with navigation reality: browser top-level navigations cannot carry an
	// Authorization header, so a gated shell always returns 401 on direct open
	// (the same reason / is exempt — it is a shell, not a data endpoint).
	//
	// The scoped ideEditorCSP() is preserved: wasm-unsafe-eval and worker-src
	// blob: apply ONLY to this route.  The MAIN console CSP (cspHeader) is
	// unchanged.
	s.mux.HandleFunc("/ide/editor", s.handleIDEEditor)

	// ---- Kanban sub-dashboard -----------------------------------------------
	// Mount kanban.Handler() under /kanban/. The kanban handler's own
	// Host-allowlist is NOT invoked (we used Handler() not Serve()).
	kanbanSrv := kanban.NewKanbanServer(s.cfg.KanbanBoardPath, s.cfg.KanbanProject, "")
	s.mux.Handle("/kanban/", requireRole(netid.RoleRead, http.StripPrefix("/kanban", kanbanSrv.Handler())))

	// ---- Metrics (cost) sub-dashboard ---------------------------------------
	metricsSrv := metricsdash.New(metricsdash.Config{
		Token:      s.cfg.Token, // same token; auth is at edge
		ProjectDir: s.cfg.MetricsProjectDir,
	})
	s.mux.Handle("/cost/", requireRole(netid.RoleRead, http.StripPrefix("/cost", metricsSrv.Handler())))

	// ---- Performance sub-dashboard ------------------------------------------
	perfSrv := perfdash.New(perfdash.Config{
		Token:   s.cfg.Token, // same token; auth is at edge
		WorkDir: s.cfg.PerfWorkDir,
	})
	s.mux.Handle("/perf/", requireRole(netid.RoleRead, http.StripPrefix("/perf", perfSrv.Handler())))

	// ---- WebSocket event stream at console origin ----------------------------
	// Phase 2.5: the console WS uses Sec-WebSocket-Protocol subprotocol auth
	// ("yakos-bearer, <token>") instead of the Authorization header — browsers
	// cannot set Authorization on WS upgrade requests.
	//
	// Phase 6c: when NetworkedMode is true, the WS handler skips the loopback
	// RemoteAddr guard (TLS RequireAndVerifyClientCert handles that) and
	// extends the Origin allow-list to include the configured external host.
	// The WS scheme switches from ws:// to wss:// in CSP and Origin matching.
	if s.cfg.Bus != nil {
		var wsHandler http.Handler
		if s.cfg.NetworkedMode {
			externalHosts := s.cfg.externalHosts()
			wsHandler = buildConsoleWSHandlerNetworked(s.cfg.Token, s.cfg.Bus, s.presence, externalHosts)
		} else {
			wsHandler = buildConsoleWSHandler(s.cfg.Token, s.cfg.Bus, s.presence)
		}
		s.mux.Handle("/v1/events", requireRole(netid.RoleRead, wsHandler))
	}

	// ---- Presence snapshot (token-gated via edge middleware) -----------------
	// RoleRead: presence is read-only (who's online).
	s.mux.HandleFunc("/api/presence", requireRoleFunc(netid.RoleRead, s.handlePresence))

	// ---- Phase 3b: Chat SSE + dispatch + transcript (token-gated at edge) ---
	// GET  /api/chat/stream      — per-operator SSE stream (multiplexed by sessionID)
	// POST /api/chat/dispatch    — start a streaming dispatch; returns {sessionId}
	// POST /api/chat/cancel      — cancel an in-flight dispatch (idempotent)
	// GET  /api/chat/transcript  — fetch persisted transcript for a conversationId
	//
	// These paths are NOT static assets, so the edge requireTokenForNonStatic
	// middleware enforces Authorization: Bearer on all of them.
	//
	// Role policy (Phase 6b):
	//   - /api/chat/stream, /api/chat/dispatch, /api/chat/cancel, /api/chat/share
	//     require RoleDispatch (start/cancel dispatches; flip share ownership).
	//   - /api/chat/transcript requires RoleRead (read-only; public-ish for shared).
	//
	// Note: /api/chat/stream requires RoleDispatch, so a RoleRead operator cannot
	// receive live SSE for a shared session.  This is intentional — RoleRead operators
	// view shared sessions via the transcript GET (RoleRead) endpoint, not live SSE.
	// Requiring RoleDispatch for SSE is the more restrictive, safer default; relaxing
	// to RoleRead for shared-session SSE is deferred to a future PR.
	s.mux.HandleFunc("/api/chat/stream", requireRoleFunc(netid.RoleDispatch, s.chat.handleChatStream))
	s.mux.HandleFunc("/api/chat/dispatch", requireRoleFunc(netid.RoleDispatch, s.chat.handleChatDispatch))
	s.mux.HandleFunc("/api/chat/cancel", requireRoleFunc(netid.RoleDispatch, s.chat.handleChatCancel))
	s.mux.HandleFunc("/api/chat/transcript", requireRoleFunc(netid.RoleRead, s.chat.handleChatTranscript))
	// POST /api/chat/share — flip shared flag; owner-gated.
	s.mux.HandleFunc("/api/chat/share", requireRoleFunc(netid.RoleDispatch, s.chat.handleChatShare))

	// ---- Phase 5: Flows UI endpoints (token-gated at edge) ------------------
	// GET  /flows/api/workflows          — list workflow names (RoleRead)
	// GET  /flows/api/workflow?name=<n>  — get workflow YAML + version stamp (RoleRead)
	// POST /flows/api/workflow           — save workflow (RoleFlowsRun; per-method in handler)
	// POST /flows/api/run?name=<n>       — start a new run (RoleFlowsRun; per-method in handler)
	// POST /flows/api/resume             — resume a prior run (RoleFlowsRun)
	// GET  /flows/api/run?id=<runId>     — poll run state (RoleRead; per-method in handler)
	// GET  /flows/api/run/node?id=<r>&node=<n> — node stdout (RoleRead)
	//
	// Outer route wrapping uses RoleRead for GET-only paths.  Mixed GET/POST
	// paths (workflow, run) are wrapped at RoleRead here; the individual POST
	// handler functions add a per-method RoleFlowsRun check internally.
	s.mux.HandleFunc("/flows/api/workflows", requireRoleFunc(netid.RoleRead, s.flows.handleListWorkflows))
	s.mux.HandleFunc("/flows/api/workflow", requireRoleFunc(netid.RoleRead, s.flows.handleWorkflowDispatch))
	s.mux.HandleFunc("/flows/api/run/node", requireRoleFunc(netid.RoleRead, s.flows.handleGetNodeOutput))
	s.mux.HandleFunc("/flows/api/run", requireRoleFunc(netid.RoleRead, s.flows.handleRunDispatch))
	s.mux.HandleFunc("/flows/api/resume", requireRoleFunc(netid.RoleFlowsRun, s.flows.handleResume))
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
// connect-src origin is correct for any --console-addr / --console-bind value.
//
// networked: when true, uses wss:// (mandatory for non-loopback mTLS listeners).
// When false, uses ws:// (loopback path; unchanged).
func cspHeader(addr string, networked bool) string {
	// Derive the ws:// or wss:// origin from the bound address.
	// addr is "host:port" (e.g. "127.0.0.1:7890" or "10.0.0.1:7890").
	scheme := "ws"
	if networked {
		scheme = "wss"
	}
	wsOrigin := scheme + "://" + addr
	return strings.Join([]string{
		"default-src 'self'",
		// app.js and sw.js are same-origin; no blob: needed.
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		// Allow WS connection to the console itself (for /v1/events).
		// For the networked path this is wss:// (TLS WebSocket).
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
	// Emit CSP as a response header so the ws:// / wss:// origin matches the
	// actual bind address and protocol (loopback → ws://; networked → wss://).
	// For the networked path, use the first external host for the wss:// directive
	// (the browser connects to a specific host, not the wildcard bind address).
	wsAddr := s.cfg.addr()
	if s.cfg.NetworkedMode {
		if hosts := s.cfg.externalHosts(); len(hosts) > 0 {
			wsAddr = hosts[0]
		}
	}
	w.Header().Set("Content-Security-Policy", cspHeader(wsAddr, s.cfg.NetworkedMode))
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

// handleIDEEditorJS serves the Monaco bootstrap script (/ide-editor.js).
// Token-exempt (listed in isStaticAsset) so the /ide/editor iframe can load
// it before the Service Worker has injected the Authorization header.
// script-src 'self' covers this same-origin path; no 'unsafe-inline' needed.
func (s *Server) handleIDEEditorJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	_, _ = w.Write(ideEditorJS)
}

// ideEditorCSP returns the Content-Security-Policy value for the /ide/editor
// route ONLY.  This is a deliberately scoped relaxation to allow Monaco editor
// to run: the AMD loader requires wasm-unsafe-eval (for its regex-engine
// internals) and Monaco workers are loaded via the standard blob: URL wrapper
// pattern (see dist/ide-editor.html MonacoEnvironment.getWorkerUrl).
//
// IMPORTANT: This function is entirely separate from cspHeader.  The main
// console CSP (cspHeader) must remain unchanged — no wasm-unsafe-eval, no blob:
// worker-src.  ideEditorCSP is only applied on /ide/editor.
func ideEditorCSP() string {
	return strings.Join([]string{
		"default-src 'self'",
		// Monaco AMD loader requires wasm-unsafe-eval for its regex engine.
		// 'self' covers loader.js, editor.main.js, workerMain.js.
		"script-src 'self' 'wasm-unsafe-eval'",
		// Monaco web workers use the standard blob:-wrapper pattern:
		// the host creates a Blob URL pointing to the same-origin workerMain.js.
		// blob: is required here; 'self' covers same-origin worker scripts directly.
		"worker-src blob: 'self'",
		// Monaco injects inline styles for theming and decorations.
		"style-src 'self' 'unsafe-inline'",
		// No external connections; only same-origin (loader may use fetch for
		// lazy module loading in future, but all assets are same-origin here).
		"connect-src 'self'",
		// Explicit font-src so codicon.ttf is served from same-origin rather
		// than relying on the default-src fallback.  data: covers any inline
		// base64 font data Monaco may embed in editor.main.css.
		"font-src 'self' data:",
		// img-src: Monaco uses data: URIs for some inline images in its UI.
		"img-src 'self' data:",
		// Prevent this document from being framed by anything other than 'self'
		// (parent console page).
		"frame-ancestors 'self'",
		"base-uri 'none'",
		"object-src 'none'",
	}, "; ")
}

func (s *Server) handleIDEEditor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	// Scoped CSP: only applies to this route.  The main cspHeader is untouched.
	w.Header().Set("Content-Security-Policy", ideEditorCSP())
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(ideEditorHTML)
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
	case "/", "/app.js", "/styles.css", "/sw.js", "/ide-editor.js", "/ide/editor":
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

// requireRole returns an http.Handler that enforces minimum role when the
// identity has been resolved (Identity.Resolved==true).
//
// The Resolved gate is critical: tests that bypass the resolver middleware by
// calling srv.Handler() directly receive the zero-value Identity
// (Resolved=false).  Without the gate, those tests would receive spurious 403s
// on any route that needs more than RoleRead — breaking the loopback invariant.
//
// Production paths always run through resolver.Middleware (set up in New()),
// which stamps Resolved=true on every identity; enforcement fires normally.
func requireRole(required netid.Role, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := netid.IdentityFrom(r.Context())
		// Only enforce when the resolver has explicitly resolved the identity.
		if id.Resolved && !id.Role.Allows(required) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireRoleFunc wraps an http.HandlerFunc with requireRole.
func requireRoleFunc(required netid.Role, fn http.HandlerFunc) http.HandlerFunc {
	return requireRole(required, fn).ServeHTTP
}
