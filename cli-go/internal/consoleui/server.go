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

	"github.com/bakw00ds/yakos/internal/authsession"
	"github.com/bakw00ds/yakos/internal/dashauth"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/interactive"
	"github.com/bakw00ds/yakos/internal/kanban"
	"github.com/bakw00ds/yakos/internal/metricsdash"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/perfdash"
	"github.com/bakw00ds/yakos/internal/setuptoken"
	"github.com/bakw00ds/yakos/internal/userstore"
	"github.com/bakw00ds/yakos/internal/workflow"
	"github.com/bakw00ds/yakos/internal/worktreemgr"
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
// Mermaid: dist/vendor/mermaid.min.js — retained; no longer loaded by Flows canvas.
// Monaco:  dist/vendor/monaco/ — full min/vs tree + CHECKSUMS.sha256 manifest.
//
//	Includes loader.js, editor/*, basic-languages/* (all language grammars),
//	language/* (TypeScript/JSON/CSS/HTML language services), base/*.
//	The "all:" prefix ensures subdirectories are included by go:embed.
//	The integrity manifest (monaco/CHECKSUMS.sha256) is verified by
//	TestVendorChecksums in vendor_checksum_test.go.
//
// Fonts: dist/vendor/fonts/ — Inter v4 + JetBrains Mono v13 woff2 subsets.
//
//	@font-face declarations in styles.css reference /vendor/fonts/... paths.
//	8 files (latin, 400/500/600/700 for each family); SHA-256 pinned in
//	pinnedFontChecksums in vendor_checksum_test.go. Source + OFL license
//	documented in VENDOR.md §Fonts.  font-src 'self' in cspHeader().
//
// Drawflow: dist/vendor/drawflow/ — vanilla JS drag/connect node editor.
//
//	drawflow.min.js + drawflow.min.css; MIT license; no eval(); no workers.
//	Loads under script-src 'self' with no CSP changes.
//	SHA-256 pinned in pinnedDrawflowChecksums in vendor_checksum_test.go.
//	Documented in VENDOR.md §Drawflow.
//
//go:embed all:dist/vendor/fonts
//go:embed dist/vendor/mermaid.min.js
//go:embed dist/vendor/VENDOR.md
//go:embed all:dist/vendor/monaco
//go:embed all:dist/vendor/drawflow
var vendorFS embed.FS

//go:embed dist/ide-editor.html
var ideEditorHTML []byte

//go:embed dist/ide-editor.js
var ideEditorJS []byte

//go:embed dist/login.html
var loginHTML []byte

//go:embed dist/login.js
var loginJS []byte

//go:embed dist/login.css
var loginCSS []byte

//go:embed dist/setup.html
var setupHTML []byte

//go:embed dist/setup.js
var setupJS []byte

// Config holds all configuration for the unified console HTTP server.
type Config struct {
	// Addr is the TCP listen address. Defaults to "127.0.0.1:7890".
	// When NetworkedMode is false (default), must be a loopback address.
	// When NetworkedMode is true, this is the non-loopback address to bind.
	Addr string

	// TLSConfig, when non-nil, causes the server to serve TLS on Listener /
	// Addr instead of plain HTTP.  Required when NetworkedMode is true.
	//
	// The console networked listener passes mtls.BuildServerTLSConfigHybrid
	// (VerifyClientCertIfGiven) so that password+session users can complete
	// the TLS handshake without a client certificate.  A presented cert is
	// still verified against ClientCAs; an untrusted cert still fails the
	// handshake.  For strict cert-required M2M surfaces use
	// mtls.BuildServerTLSConfig (RequireAndVerifyClientCert) instead.
	//
	// MUST be nil when NetworkedMode is false (loopback path unchanged).
	TLSConfig *tls.Config

	// NetworkedMode, when true, signals that this server is bound to a
	// non-loopback address.  This triggers:
	//   - wss:// origin in CSP and WS Origin allow-list (instead of ws://)
	//   - loopbackTrusted=false in the identity Resolver (certless → RoleNone, Phase 3g)
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

	// AuthSessionStore is the server-side session store used for the
	// password+session-cookie auth regime (ADR-0005 Phase 3).  When non-nil and
	// NetworkedMode is true, it is wired into NewResolverWithSession so that
	// requests carrying a valid session cookie resolve to an authenticated
	// Identity (AuthMethodSession).
	//
	// When nil the session path is not activated — the resolver behaves exactly
	// as before (Phase 3a dormant wiring: no sessions exist, behavior unchanged).
	// serve.go constructs the store and passes it here; callers may inject a
	// custom store for tests.
	AuthSessionStore *authsession.Store

	// UserStore is the user account store used alongside AuthSessionStore to
	// validate session epoch, disabled state, and role.  When nil the session
	// path is not activated regardless of AuthSessionStore.
	//
	// serve.go opens the store from <StateDir>/users/users.json.  An empty or
	// missing file (Count()==0, zero users) is tolerated — all lookups return
	// false so the session path fails closed until Phase 3b wires login.
	UserStore *userstore.Store

	// WorkspaceRoot is the absolute path to the workspace root served by the
	// IDE file API (/api/files/tree and /api/files/content).  When empty,
	// both endpoints return 503 Service Unavailable.  The path is jailed:
	// all requested paths are resolved against WorkspaceRoot and symlink-
	// escaped or traversal attempts are rejected with a generic 400/403.
	WorkspaceRoot string

	// YakosRoot is the yakOS framework root directory (e.g. the yakOS repo root).
	// Required for GET /api/skills to compose the agent roster via
	// agentscompose.Compose.  When empty, /api/skills returns an empty agent
	// list but still serves the static client commands (graceful degradation).
	YakosRoot string

	// SetupToken is the one-time first-admin setup token state (ADR-0005 Phase 3c).
	// When non-nil and NetworkedMode is true, /setup is wired and the
	// unauthenticated edge redirect sends zero-user navigations to /setup
	// instead of /login.
	//
	// When nil (loopback path, or Count()>0 at startup), /setup returns 404/409
	// and all redirects go to /login (Phase 3b behavior unchanged).
	//
	// serve.go constructs the State from <StateDir>/setup-token and passes it
	// here; callers may inject a test State.
	SetupToken *setuptoken.State

	// AllowNetworkedBash, when true, permits POST /api/console/bash from
	// non-loopback connections.  When false (default), that endpoint returns
	// 403 for any request that does not originate from a loopback address.
	//
	// This flag is off by default because the bash endpoint is an intentional
	// RCE surface.  Loopback connections are always permitted regardless of this
	// flag (the loopback network boundary is already trusted).
	//
	// Activated by --console-allow-bash in runServe; serve.go prints a WARNING
	// banner when this flag is set and the console is in networked mode.
	AllowNetworkedBash bool

	// WorktreeManager, when non-nil, enables the IDE diff-review mode.
	// When nil, review-mode dispatch is unavailable (the endpoints return 503).
	//
	// serve.go constructs this via worktreemgr.New(filepath.Join(statepath.Dir(), "ide-worktrees"))
	// and calls PruneOrphans(WorkspaceRoot) at startup if WorkspaceRoot is a git repo.
	// Tests may inject a custom manager.
	WorktreeManager *worktreemgr.Manager

	// InteractiveManager, when non-nil, enables the persistent multi-turn
	// session regime (Interactive-P1).  When nil, POST /api/chat/dispatch with
	// interactive:true returns 503 and POST /api/chat/send returns 503.
	//
	// serve.go constructs this from interactive.NewManager(serverCtx, cfg).
	// Tests may inject a fake manager.
	InteractiveManager *interactive.Manager
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
	files        *filesHandlers
	skills       *skillsHandlers
	fleet        *fleetHandlers
	diff         *diffHandlers      // IDE Phase 3b diff-review endpoints; nil if WorktreeManager is nil
	serverCtx    context.Context    // cancelled on Serve shutdown; dispatch goroutines use this
	serverCancel context.CancelFunc // called by Serve when the server shuts down
}

// New constructs a Server and wires all routes.  It returns an error if the
// configuration is invalid.  Specifically, if NetworkedMode is true and either
// AuthSessionStore or UserStore is nil, New fails closed with an error — a
// networked console without the session+user stores would silently skip the
// requireAuthOrRedirect and requireCSRFForSession middleware, leaving the
// surface unauthenticated.
//
// Auth model (three regimes per ADR-0005):
//   - Loopback (NetworkedMode=false): bearer-token cooperative labeling.
//     The identity is Authenticated=false; Role is admin (preserving existing
//     full-access loopback behavior).  The resolver is NewResolver with
//     loopbackTrusted=true.
//   - Networked machines (NetworkedMode=true, mTLS cert present): cert CN →
//     role lookup via RoleMapper.  Identity is Authenticated=true, AuthMethodCert.
//   - Networked humans (NetworkedMode=true, session cookie present):
//     resolved via buildSessionLookupFn from Config.AuthSessionStore +
//     Config.UserStore.  Identity is Authenticated=true, AuthMethodSession.
//
// Edge middleware stack (outer → inner for all three regimes):
//   - Static SPA shell assets (/, /app.js, /styles.css, /sw.js) are served
//     WITHOUT a token requirement so the browser can load them before the
//     token is available.
//   - All other paths require the edge bearer token (or are gated by mTLS on
//     the networked path).
//   - Non-GET requests without Content-Type: application/json receive 415
//     (forces a CORS preflight that a cross-origin attacker cannot satisfy).
//   - The loopback path is additionally wrapped by RequireLocalHost.
//   - Sub-dashboard handlers are mounted via Handler() without their inner
//     per-dashboard Host/token middleware.
//   - /v1/events is mounted from wsbus.Server.Handler() which enforces
//     loopback-only + Origin allow-list (DNS-rebinding defence).
func New(cfg Config) (*Server, error) {
	if cfg.NetworkedMode && (cfg.AuthSessionStore == nil || cfg.UserStore == nil) {
		return nil, fmt.Errorf(
			"consoleui: NetworkedMode requires both AuthSessionStore and UserStore to be non-nil; " +
				"a networked console without stores has no auth middleware")
	}
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
	// Wire yakosRoot + workspaceRoot so the dispatch handler can validate the
	// agent name before returning 202.  These are set after construction to keep
	// newChatHandlers signature stable (it is also called from export_test.go).
	chatH.yakosRoot = cfg.YakosRoot
	chatH.workspaceRoot = cfg.WorkspaceRoot
	// Wire the worktreemgr so review-mode dispatches can provision worktrees.
	chatH.worktreeMgr = cfg.WorktreeManager
	// Wire the interactive manager (Interactive-P1).
	//
	// When cfg.InteractiveManager is non-nil (test injection or explicit caller
	// override), use it as-is.  Otherwise, construct one here now that hub and
	// serverCtx are both available.
	//
	// The OnError callback emits an error SSEEvent routed via hub.Route so the
	// browser pane shows an error (instead of hanging) when a session's process
	// crashes.  It uses the same SSEEvent type as the one-shot path so ChatHub
	// delivers it to the correct owner-scoped SSE connections — i.e. interactive
	// crash events are owner-scoped identically to normal token/summary frames.
	//
	// The manager's reaper goroutine runs until serverCtx is cancelled (daemon
	// shutdown), at which point the goroutine exits cleanly via ctx.Done().
	interactiveMgr := cfg.InteractiveManager
	if interactiveMgr == nil {
		interactiveMgr = interactive.NewManager(serverCtx, interactive.ManagerConfig{
			// Cap and timeouts use package defaults (0 → defaultSessionCap=4,
			// defaultIdleTimeout=15min, defaultReaperInterval=30s).
			OnError: func(conversationID, sessionID, operatorID, msg string) {
				hub.Route(SSEEvent{
					SessionID:      sessionID,
					ConversationID: conversationID,
					Type:           "error",
					Text:           msg,
					TS:             time.Now().UTC().Format(time.RFC3339Nano),
				})
			},
		})
	}
	chatH.interactiveMgr = interactiveMgr
	chatH.interactiveSend = interactiveMgr
	flowsH := &flowsHandlers{
		engine:     cfg.WorkflowEngine,
		workDir:    cfg.WorkDir,
		serverCtx:  serverCtx,
		activeRuns: make(map[string]context.CancelFunc),
	}
	filesH := newFilesHandlers(cfg.WorkspaceRoot)
	skillsH := newSkillsHandlers(cfg.YakosRoot, cfg.WorkspaceRoot)
	// Fleet handler: registry is allocated here and shared with chatHandlers
	// via chatH so the dispatch goroutine can call Add/UpdateStatus/Remove.
	registry := dispatch.NewSessionRegistry()
	chatH.registry = registry
	chatH.bus = cfg.Bus
	fleetH := newFleetHandlers(registry, hub)
	diffH := newDiffHandlers(cfg.WorktreeManager, filesH, cfg.WorkspaceRoot, hub)
	s := &Server{
		cfg:          cfg,
		mux:          http.NewServeMux(),
		presence:     pm,
		chatHub:      hub,
		chat:         chatH,
		flows:        flowsH,
		files:        filesH,
		skills:       skillsH,
		fleet:        fleetH,
		diff:         diffH,
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
	//             Defence-in-depth alongside the TLS layer — even if TLS config
	//             were somehow misconfigured, certless requests NEVER receive admin
	//             on the networked listener.  After Phase 3f, the TLS layer uses
	//             VerifyClientCertIfGiven (not RequireAndVerifyClientCert) so
	//             certless connections reach the HTTP layer; the resolver is the
	//             fail-closed gate for unauthenticated certless requests.
	//
	// callerLabelFn extracts the cooperative OperatorID for loopback bearer sessions.
	// The dispatch facade stamps operator_id from its daemon-level opID on the
	// loopback path; we return "" here and let the facade do it.
	//
	// Session regime (ADR-0005 Phase 3a + Phase 3b):
	//   - Networked path + AuthSessionStore + UserStore set → use
	//     NewResolverWithSession so that requests carrying a valid session cookie
	//     resolve to Identity{AuthMethodSession}.
	//   - Loopback path → always NewResolver (no session path on loopback).
	loopbackTrusted := !cfg.NetworkedMode
	mapper := netid.NewRoleMapper(cfg.StateDir)
	callerLabelFn := func(r *http.Request) string {
		// No per-request cooperative label is available at the edge; the
		// dispatch facade stamps operator_id from its daemon-level opID.
		return ""
	}
	var resolver *netid.Resolver
	if !loopbackTrusted {
		// Networked path: wire the session lookup function.
		// Stores are guaranteed non-nil here: New() fails closed if they are nil.
		// The session fn is guarded by !loopbackTrusted inside the resolver, so
		// even if this branch were somehow reached on loopback, the session path
		// would be skipped (defense-in-depth).
		sessionFn := buildSessionLookupFn(cfg.AuthSessionStore, cfg.UserStore)
		resolver = netid.NewResolverWithSession(mapper, callerLabelFn, loopbackTrusted, sessionFn)
	} else {
		// Loopback path: keep existing NewResolver behavior unchanged.
		resolver = netid.NewResolver(mapper, callerLabelFn, loopbackTrusted)
	}

	// Wire /login (GET page + POST handler) and /logout.
	//
	// /login is registered here (not in registerRoutes) so that a single mux
	// pattern can dispatch on method, avoiding a duplicate-registration panic.
	//
	// On the networked path with stores: GET serves the login page, POST runs
	// the login handler, /logout runs the logout handler.
	//
	// Without stores (loopback path or zero-users first-run): GET /login still
	// serves the placeholder page (useful for debugging); POST /login returns
	// 503 Service Unavailable.
	if cfg.NetworkedMode {
		// secure=true because NetworkedMode uses TLS; the cookie Secure flag must
		// match (ADR-0005 LOW-2 / §Session management).
		// Stores are guaranteed non-nil here: New() fails closed if they are nil.
		authH := newAuthHandlers(cfg.AuthSessionStore, cfg.UserStore, true)
		s.mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodPost:
				authH.handleLogin(w, r)
			case http.MethodGet, http.MethodHead:
				s.handleLoginPage(w, r)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		})
		s.mux.HandleFunc("/logout", authH.handleLogout)

		// Wire /setup (GET page + POST handler) and /setup.js on the networked path.
		// /setup is auth-exempt (users can't be authenticated yet when setting up).
		// The setupHandlers guard Count()==0 internally.
		setupH := newSetupHandlers(cfg.SetupToken, cfg.UserStore)
		s.mux.HandleFunc("/setup", func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodPost:
				setupH.handleSetup(w, r)
			case http.MethodGet, http.MethodHead:
				setupH.handleSetupPage(w, r)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		})
		s.mux.HandleFunc("/setup.js", s.handleSetupJS)
	} else {
		// Loopback path: login page accessible at GET /login for debugging;
		// POST /login is not wired (loopback uses bearer token, not sessions).
		s.mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead:
				s.handleLoginPage(w, r)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		})
	}

	// Build the inner handler chain (shared between loopback and networked paths).
	//
	// Networked-path layer order (outer → inner, matching request processing order):
	//   1. resolver.Middleware        — identity stamping: reads cert/session, sets context
	//   2. requireAuthOrRedirect      — unauthenticated edge: 401 JSON or 302 /login
	//   3. requireCSRFForSession      — double-submit CSRF check for session mutations
	//   4. requireJSONForMutations    — Content-Type gate (second CSRF layer; CORS preflight)
	//   5. s.mux                     — route handlers
	//
	// The resolver MUST run before CSRF and edge-auth checks because both
	// requireAuthOrRedirect and requireCSRFForSession read the resolved
	// netid.Identity from the request context.
	//
	// Loopback-path layer order (unchanged):
	//   1. requireJSONForMutations
	//   2. resolver.Middleware
	//   3. s.mux
	//
	// NOTE: On the loopback path the resolver still runs (inside inner), and
	// the existing loopback tests rely on the resolver.Middleware being between
	// requireJSONForMutations and the mux. Keeping the two paths diverged here
	// avoids any regression on the loopback path.
	var inner http.Handler
	if cfg.NetworkedMode {
		// Wire CSRF middleware and edge redirect on the networked path.
		// Stores are guaranteed non-nil here: New() fails closed if they are nil.
		// Resolver runs first so its output is available to all downstream middleware.
		// requireAuthOrRedirect receives the UserStore so it can redirect to /setup
		// when zero users exist (Phase 3c) vs /login when an admin is already present.
		inner = resolver.Middleware(
			requireAuthOrRedirect(cfg.UserStore,
				requireCSRFForSession(cfg.AuthSessionStore,
					requireJSONForMutations(s.mux))))
	} else {
		inner = requireJSONForMutations(resolver.Middleware(s.mux))
	}

	var protected http.Handler
	if cfg.NetworkedMode {
		// Networked path: the mTLS TLS layer replaces the loopback-only Host check.
		//
		// Auth for regular HTTP requests comes from cert or session cookie — the
		// bearer token is NOT used for regular HTTP on the networked path.  The
		// bearer token is only used for the WS subprotocol on /v1/events (browsers
		// cannot set Authorization headers on WS upgrade requests).
		//
		// For /v1/events WS upgrades: requireTokenForNonStatic still applies (the
		// WS handler itself enforces the subprotocol token; the outer skip lets it
		// reach the handler).  For static assets + login page: token-exempt.  For
		// all other routes: session or cert auth via the inner chain.
		//
		// The RequireLocalHost guard is intentionally NOT applied here — it would
		// reject all legitimate non-loopback traffic.  The HTTP-layer guard is
		// requireAuthOrRedirect (inside inner), which blocks certless+sessionless
		// requests.  After Phase 3f, the TLS layer uses VerifyClientCertIfGiven
		// so certless connections reach the HTTP layer; requireAuthOrRedirect is
		// the fail-closed gate that returns 401/302 for unauthenticated requests.
		protected = requireTokenForNonStaticNetworked(inner)
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
		// networked path (ADR-0005 Phase 3f) uses hybrid TLS + HTTP-edge auth
		// (requireAuthOrRedirect), matching the pattern used in wsbus/server.go
		// and mcpserver/streamhttp.go.
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}
	return s, nil
}

// Handler returns the underlying http.Handler for mounting in tests.
// Neither the Host-header middleware nor the token middleware is applied here —
// the caller supplies them.
func (s *Server) Handler() http.Handler { return s.mux }

// FullHandler returns the full protected handler chain (identical to what
// Serve would use internally), including:
//   - requireTokenForNonStatic
//   - requireAuthOrRedirect (networked path with session store)
//   - requireCSRFForSession (networked path with session store)
//   - requireJSONForMutations
//   - resolver.Middleware
//   - the route mux
//
// Use this in tests that need to exercise the complete middleware stack
// (CSRF, edge auth redirect/401) without spinning up a real TLS listener.
// The Host-header / RequireLocalHost guard is NOT applied (tests use
// httptest and don't need that constraint).
func (s *Server) FullHandler() http.Handler { return s.httpSrv.Handler }

// ChatHub returns the chat routing hub, exposed for testing.
func (s *Server) ChatHub() *ChatHub { return s.chatHub }

// Serve starts the HTTP server and blocks until ctx is cancelled.
// Returns nil on clean shutdown (http.ErrServerClosed treated as nil).
//
// Loopback path (NetworkedMode=false): unchanged from prior phases.
//   - Binds a plain TCP listener; enforces loopback-only via IsLoopback check.
//   - Any non-loopback address returns an error before opening the listener.
//
// Networked path (NetworkedMode=true): hybrid TLS listener (ADR-0005 Phase 3f).
//   - The caller MUST have set cfg.TLSConfig (via mtls.BuildServerTLSConfigHybrid)
//     and must have verified mTLS material is available before calling Serve.
//   - The plain TCP listener is wrapped with tls.NewListener before Serve.
//   - The loopback-only assertion is NOT applied (intentional: the HTTP-layer
//     requireAuthOrRedirect is the fail-closed gate for certless+sessionless
//     requests; the TLS layer uses VerifyClientCertIfGiven so password+session
//     users can complete the handshake without a client cert).
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

	// ---- Auth pages (login) — token-gated on loopback; auth-or-redirect on networked.
	// GET /login.js — login form JS (script-src 'self'; no inline scripts).
	// GET /login.css — login form CSS (style-src 'self'; no unsafe-inline).
	//
	// After Phase 3f, certless connections can reach /login (TLS uses
	// VerifyClientCertIfGiven).  The requireAuthOrRedirect middleware exempts
	// /login and /setup via isStaticAsset so unauthenticated users can reach
	// the login form without being redirect-looped.
	//
	// Note: /login is registered in New() (after the auth handlers are built)
	// so that GET and POST can share a single mux pattern with method dispatch.
	// This avoids a duplicate-pattern panic when both registerRoutes and New()
	// would otherwise register "/login".
	s.mux.HandleFunc("/login.js", s.handleLoginJS)
	s.mux.HandleFunc("/login.css", s.handleLoginCSS)

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
	// frame.  On the networked path, the shell is reachable by certless browsers
	// (Phase 3f: TLS uses VerifyClientCertIfGiven); authentication is required
	// to reach file-content endpoints (requireAuthOrRedirect).  On the loopback
	// path the server is loopback-only by construction.  Putting a RoleRead gate
	// on the shell itself conflicted with navigation reality: browser top-level
	// navigations cannot carry an Authorization header, so a gated shell always
	// returns 401 on direct open (the same reason / is exempt — it is a shell,
	// not a data endpoint).
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
	// HandlerNoToken() is used instead of Handler() so that session-cookie-
	// authenticated requests (networked console, no #token= fragment) are not
	// blocked by the sub-dashboard's inner Bearer-token check.  Auth is
	// enforced at the console edge: requireRole(RoleRead) + the outer
	// requireAuthOrRedirect / requireTokenForNonStatic middleware.
	//
	// DispatchLogDir is set to PerfWorkDir (the yakOS state dir) so the Cost
	// tab shows live LLM spend from the dispatch-log without requiring a manual
	// `yakos metrics collect`.  The same directory is used by perfdash so
	// both dashboards read from the same canonical location.
	metricsSrv := metricsdash.New(metricsdash.Config{
		Token:          s.cfg.Token, // retained for standalone Serve(); unused when mounted here
		ProjectDir:     s.cfg.MetricsProjectDir,
		DispatchLogDir: s.cfg.PerfWorkDir,
	})
	s.mux.Handle("/cost/", requireRole(netid.RoleRead, http.StripPrefix("/cost", metricsSrv.HandlerNoToken())))

	// ---- Performance sub-dashboard ------------------------------------------
	// Same rationale as /cost/: use HandlerNoToken() so session-cookie auth
	// works when the iframe is served without a #token= fragment.
	perfSrv := perfdash.New(perfdash.Config{
		Token:   s.cfg.Token, // retained for standalone Serve(); unused when mounted here
		WorkDir: s.cfg.PerfWorkDir,
	})
	s.mux.Handle("/perf/", requireRole(netid.RoleRead, http.StripPrefix("/perf", perfSrv.HandlerNoToken())))

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

	// ---- Phase 2 fleet panel: active dispatch session snapshot ----------------
	// GET /api/fleet — returns the caller-scoped snapshot of active dispatch
	// sessions: owned sessions + shared sessions.  Live updates arrive via the
	// fleet.started / fleet.finished topics on /v1/events.
	// RoleRead: read-only; no state mutation.
	// Cache-Control: no-store (enforced inside handleFleet).
	if s.fleet != nil {
		s.mux.HandleFunc("/api/fleet", requireRoleFunc(netid.RoleRead, s.fleet.handleFleet))
	}

	// ---- Phase 3b: Chat SSE + dispatch + transcript (token-gated at edge) ---
	// GET  /api/chat/stream      — per-operator SSE stream (multiplexed by sessionID)
	// POST /api/chat/dispatch    — start a streaming dispatch; returns {sessionId}
	// POST /api/chat/cancel      — cancel an in-flight dispatch (idempotent)
	// GET  /api/chat/transcript  — fetch persisted transcript for a conversationId
	//
	// These paths are NOT static assets, so the edge requireTokenForNonStatic
	// middleware enforces Authorization: Bearer on all of them.
	//
	// Role policy (Phase 3 session-attach):
	//   - /api/chat/stream requires RoleRead (Phase 3 watch mode: a RoleRead
	//     operator may subscribe to a shared session's live SSE frames; the hub
	//     already enforces per-session ownership/shared scoping in Route).
	//   - /api/chat/dispatch, /api/chat/cancel, /api/chat/share
	//     require RoleDispatch (start/cancel dispatches; flip share ownership).
	//   - /api/chat/transcript requires RoleRead (read-only; owner + shared watchers).
	//
	// Security note: lowering /api/chat/stream to RoleRead does NOT weaken
	// dispatch isolation — the hub's Route method only delivers frames to a
	// connection when (a) it is the session owner, OR (b) the session is shared.
	// A RoleRead watcher connecting to the SSE stream will only ever see frames
	// for sessions that have been explicitly shared by their owner.  Dispatch
	// (POST /api/chat/dispatch) remains gated at RoleDispatch, so a RoleRead
	// operator can watch but not start new dispatches.
	s.mux.HandleFunc("/api/chat/stream", requireRoleFunc(netid.RoleRead, s.chat.handleChatStream))
	s.mux.HandleFunc("/api/chat/dispatch", requireRoleFunc(netid.RoleDispatch, s.chat.handleChatDispatch))
	s.mux.HandleFunc("/api/chat/cancel", requireRoleFunc(netid.RoleDispatch, s.chat.handleChatCancel))
	s.mux.HandleFunc("/api/chat/transcript", requireRoleFunc(netid.RoleRead, s.chat.handleChatTranscript))
	// POST /api/chat/share — flip shared flag; owner-gated.
	s.mux.HandleFunc("/api/chat/share", requireRoleFunc(netid.RoleDispatch, s.chat.handleChatShare))
	// POST /api/chat/send — deliver a follow-up turn to a persistent interactive
	// session started with interactive:true in /api/chat/dispatch.
	// RoleDispatch: only the session owner may drive the session; watchers (RoleRead)
	// may subscribe to the SSE stream but cannot send turns.
	// CSRF + JSON-mutation gates applied by the global middleware in New().
	s.mux.HandleFunc("/api/chat/send", requireRoleFunc(netid.RoleDispatch, s.chat.handleChatSend))

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
	// POST /flows/api/cancel?id=<runId> — cancel an in-flight run (RoleFlowsRun).
	// Cancel is always a mutation so it is gated at RoleFlowsRun at the route
	// level (unlike /flows/api/run which uses RoleRead-at-edge + per-method check
	// because GET is also routed there).
	s.mux.HandleFunc("/flows/api/cancel", requireRoleFunc(netid.RoleFlowsRun, s.flows.handleCancel))

	// ---- Phase 5 (ADR-0005 §D6): Users management API -------------------------
	// All /api/users/* require RoleAdmin; /api/account/* require RoleRead.
	// CSRF and JSON-mutation gating are applied globally via the middleware stack
	// in New(); no re-check is needed in handler bodies.
	// AuthSessionStore may be nil on the loopback path (no session mutations).
	usersH := newUsersHandlers(s.cfg.UserStore, s.cfg.AuthSessionStore)
	// GET /api/users — list users; POST /api/users — create user.
	// Both methods are dispatched from a single pattern to avoid duplicate
	// registration. Method-specific sub-paths are registered separately and take
	// precedence over the bare /api/users pattern in Go's ServeMux (longer
	// patterns win).
	s.mux.HandleFunc("/api/users", requireRoleFunc(netid.RoleAdmin, usersH.handleUsersRoot))
	// POST /api/users/role — set role (bumps epoch → invalidates sessions)
	s.mux.HandleFunc("/api/users/role", requireRoleFunc(netid.RoleAdmin, usersH.handleSetRole))
	// POST /api/users/reset-password — admin reset (sets passwordResetReq)
	s.mux.HandleFunc("/api/users/reset-password", requireRoleFunc(netid.RoleAdmin, usersH.handleResetPassword))
	// POST /api/users/disable — disable user (purges sessions)
	s.mux.HandleFunc("/api/users/disable", requireRoleFunc(netid.RoleAdmin, usersH.handleDisableUser))
	// POST /api/users/enable — re-enable user
	s.mux.HandleFunc("/api/users/enable", requireRoleFunc(netid.RoleAdmin, usersH.handleEnableUser))
	// POST /api/users/delete — delete user (purges sessions first)
	s.mux.HandleFunc("/api/users/delete", requireRoleFunc(netid.RoleAdmin, usersH.handleDeleteUser))
	// GET /api/account — whoami (any authenticated user)
	// POST /api/account/password — self-service password change (any authenticated user)
	// Both require RoleRead (any authenticated user; lower bound enforced here).
	s.mux.HandleFunc("/api/account", requireRoleFunc(netid.RoleRead, usersH.handleAccount))
	s.mux.HandleFunc("/api/account/password", requireRoleFunc(netid.RoleRead, usersH.handleChangePassword))

	// ---- Phase 7 (IDE): File API (read: RoleRead, write: RoleDispatch) ---------
	// GET  /api/files/tree?dir=<relpath>     — JSON directory tree (RoleRead)
	// GET  /api/files/content?path=<relpath> — file content + version (RoleRead)
	// POST /api/files/write                  — atomic write with OCC (RoleDispatch)
	//
	// All routes are jailed to Config.WorkspaceRoot.
	// Secret files are omitted from tree, refused at content, and refused at write.
	// See files_handler.go for the full security model.
	//
	// NOTE: PR #175 (feat/ide-monaco-spike) is still open and also edits
	// registerRoutes.  Whichever of #175 / #176 merges second will have a
	// trivial rebase conflict on this section of registerRoutes; the resolution
	// is to include both sets of route registrations.
	s.mux.HandleFunc("/api/files/tree", requireRoleFunc(netid.RoleRead, s.files.handleFilesTree))
	s.mux.HandleFunc("/api/files/content", requireRoleFunc(netid.RoleRead, s.files.handleFilesContent))
	// POST /api/files/write requires RoleDispatch at the route level.
	// The handler also enforces RoleDispatch internally (per-handler check mirrors
	// flows_handler.go handleSaveWorkflow pattern), providing defence in depth.
	// requireJSONForMutations (applied globally in New()) enforces Content-Type:
	// application/json for this POST, providing CSRF defence.
	s.mux.HandleFunc("/api/files/write", requireRoleFunc(netid.RoleDispatch, s.files.handleFilesWrite))

	// ---- IDE Phase 3b: diff-review endpoints ------------------------------------
	// GET  /api/files/diff             — per-file structured diff (RoleRead, owner-scoped)
	// POST /api/files/diff/accept      — promote hunk to real tree (RoleDispatch, owner-scoped)
	// POST /api/files/diff/reject      — discard hunk from worktree (RoleDispatch, owner-scoped)
	// GET  /api/git/status             — git status for session worktree (RoleRead, owner-scoped)
	// POST /api/git/commit             — commit promoted changes in real tree (RoleDispatch)
	//
	// All mutating endpoints: CSRF + JSON gate (via global middleware in New()) +
	// RoleDispatch + owner-scoped to the caller's session + path jailed under
	// WorkspaceRoot.  WorkDirOverride paths are server-derived, never from request body.
	//
	// Idempotency-Key: not required for diff/accept or diff/reject because they
	// operate on exact hunk identity (same hunk_id + same session = idempotent in
	// effect after first apply; subsequent apply returns 409 not a duplicate).
	// POST /api/git/commit: callers MUST deduplicate via the returned SHA.
	if s.diff != nil {
		s.mux.HandleFunc("/api/files/diff", requireRoleFunc(netid.RoleRead, s.diff.handleFilesDiff))
		s.mux.HandleFunc("/api/files/diff/accept", requireRoleFunc(netid.RoleDispatch, s.diff.handleDiffAccept))
		s.mux.HandleFunc("/api/files/diff/reject", requireRoleFunc(netid.RoleDispatch, s.diff.handleDiffReject))
		s.mux.HandleFunc("/api/git/status", requireRoleFunc(netid.RoleRead, s.diff.handleGitStatus))
		s.mux.HandleFunc("/api/git/commit", requireRoleFunc(netid.RoleDispatch, s.diff.handleGitCommit))
	}

	// ---- Phase 1 (REPL slash-skills): agent + command catalog ------------------
	// GET /api/skills — read-only roster of composed agents + static client commands.
	// Used by the chat REPL "/" popover to present a filterable agent/command list.
	// RoleRead: reading the catalog requires no elevated privilege.
	s.mux.HandleFunc("/api/skills", requireRoleFunc(netid.RoleRead, s.skills.handleSkills))

	// ---- Console bash pass-through (RoleAdmin; loopback or --console-allow-bash) -
	// POST /api/console/bash — executes an arbitrary shell command (sh -c) and
	// returns {stdout, stderr, exit_code, truncated}.
	//
	// Gating (in addition to requireRoleFunc(RoleAdmin)):
	//   - Loopback connections: always permitted.
	//   - Non-loopback connections: require Config.AllowNetworkedBash; else 403.
	//
	// CSRF + JSON-mutation gates are applied by the global middleware stack in
	// New() (requireCSRFForSession + requireJSONForMutations). mTLS M2M clients
	// (AuthMethodCert) are CSRF-exempt because they do not use session cookies;
	// the CSRF middleware already handles this — no special case is needed here.
	//
	// Idempotency-Key: not declared — shell commands are inherently side-effecting
	// and cannot be made retry-safe. Callers must not retry without human review.
	bashH := newBashHandlers(s.cfg)
	s.mux.HandleFunc("/api/console/bash", requireRoleFunc(netid.RoleAdmin, bashH.handleBash))
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
		// data: needed for inline SVG used as CSS background-image (e.g. the
		// <select> dropdown-arrow glyph); background-images fall under img-src.
		"img-src 'self' data:",
		// Vendored fonts served same-origin under /vendor/fonts/.
		// 'self' is already covered by default-src but listed explicitly
		// here so the intent is auditable and the directive is present
		// even when browser fallback handling differs by UA.
		"font-src 'self'",
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

// handleLoginPage serves GET /login — the minimal server-rendered login page.
// CSP: script-src 'self' (login.js is a same-origin file, no inline scripts).
// This is the Phase 3b placeholder; the polished UI lands in the next PR.
func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		// POST /login is handled by authHandlers.handleLogin (wired in New).
		// Any other method is not allowed.
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	// CSP: script-src 'self' covers /login.js (same-origin).  No inline scripts.
	// style-src 'self' covers /login.css (external CSS; no 'unsafe-inline').
	// form-action 'self' allows the form POST to /login.
	w.Header().Set("Content-Security-Policy", strings.Join([]string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self'",
		"connect-src 'self'",
		"font-src 'self'",
		"img-src 'self'",
		"frame-ancestors 'none'",
		"base-uri 'none'",
		"object-src 'none'",
		"form-action 'self'",
	}, "; "))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	_, _ = w.Write(loginHTML)
}

// handleLoginJS serves GET /login.js — the minimal login form JS.
// Token-exempt on the networked path (exempted by isStaticAsset).
func (s *Server) handleLoginJS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	_, _ = w.Write(loginJS)
}

// handleLoginCSS serves GET /login.css — the external stylesheet for the login page.
// Token-exempt on the networked path (exempted by isStaticAsset).
// Serving CSS as an external file eliminates the need for 'unsafe-inline' in the
// login page's style-src CSP directive (ADR-0005 Phase 3b followup #4).
func (s *Server) handleLoginCSS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	_, _ = w.Write(loginCSS)
}

// ---- auth helpers -----------------------------------------------------------

// isStaticAsset reports whether the request is for a token-exempt static asset.
// The assets /, /app.js, /styles.css, /sw.js, and vendored blobs under /vendor/
// carry no secrets and must be accessible before the browser can obtain and
// present the bearer token.
//
// /login and /login.js are ALSO token-exempt because:
//   - On the networked path, browsers must reach /login (POST and GET) without
//     a bearer token — the user is authenticating, so by definition they don't
//     yet hold a token.  The TLS layer (mTLS on networked) is the network-level
//     gate; the bearer token is a loopback-path cooperative label.
//   - On the loopback path, the login flow is not used (bearer token is always
//     available).  /login is reachable token-free but is useless on loopback —
//     harmless, and consistent with the networked behavior.
func isStaticAsset(r *http.Request) bool {
	switch r.URL.Path {
	case "/login", "/login.js", "/login.css":
		// Login page, its script, and its stylesheet: always accessible without a
		// bearer token.  GET/POST for /login; GET/HEAD for /login.js and /login.css.
		// The handler enforces its own method check; the token gate is not the right
		// place to restrict these.
		return true
	case "/setup", "/setup.js":
		// Setup page and its script: auth-exempt (unauthenticated operators must
		// reach /setup to create the first admin; by definition they have no token).
		// GET/POST for /setup; GET/HEAD for /setup.js.
		return true
	}
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

// requireTokenForNonStaticNetworked is the networked-path variant of
// requireTokenForNonStatic.  On the networked path, regular HTTP requests
// (API calls, page loads) are authenticated via session cookie or cert —
// NOT via the bearer token.  The bearer token is only required for the
// /v1/events WebSocket subprotocol (browsers cannot set Authorization headers
// on WS upgrade requests, so the subprotocol carries the token instead;
// the WS handler itself validates it via consoleAuthSubprotocol middleware).
//
// This function lets ALL non-WS, non-static requests through without a bearer
// token check; auth is enforced downstream by the resolver + requireAuthOrRedirect.
// Static assets (/, /app.js, /login, etc.) bypass the bearer check as before.
func requireTokenForNonStaticNetworked(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Static assets (including login page): pass through.
		if isStaticAsset(r) {
			next.ServeHTTP(w, r)
			return
		}
		// /v1/events WebSocket upgrade: require bearer token via subprotocol.
		// The downstream consoleAuthSubprotocol middleware validates the token;
		// we must let the request through the outer gate so it can reach that.
		if r.URL.Path == "/v1/events" && strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			next.ServeHTTP(w, r)
			return
		}
		// All other requests on the networked path: no bearer token required.
		// Auth is session cookie or cert — enforced downstream.
		next.ServeHTTP(w, r)
	})
}

func requireTokenForNonStatic(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isStaticAsset(r) {
			next.ServeHTTP(w, r)
			return
		}
		// WebSocket upgrade requests cannot carry an Authorization header —
		// browsers forbid setting it on the WS upgrade.  Scope the bypass
		// strictly to /v1/events: a spoofed Upgrade: websocket header on any
		// other path (e.g. /api/files/content) must NOT skip the token check.
		// The downstream consoleAuthSubprotocol middleware gates /v1/events
		// itself via the Sec-WebSocket-Protocol subprotocol token.
		if r.URL.Path == "/v1/events" && strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
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
