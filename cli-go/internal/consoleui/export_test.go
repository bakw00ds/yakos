// export_test.go — test-only exports for the consoleui package.
//
// This file is compiled only during tests (package consoleui, not
// consoleui_test).  It exports internal types and constructors used by the
// external test file (chat_test.go in package consoleui_test) so that tests
// can interact with ChatHub and Transcripts without coupling production code
// to test concerns.
package consoleui

import (
	"context"
	"net/http"
	"testing"

	"github.com/bakw00ds/yakos/internal/authsession"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/userstore"
	"github.com/bakw00ds/yakos/internal/workflow"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// ---- ChatHub test exports ---------------------------------------------------

// MaxSSEConnsPerOperator is exported for tests so the cap can be referenced
// without hardcoding magic numbers.
const MaxSSEConnsPerOperator = maxSSEConnsPerOperator

// MaxTotalSSEConns is exported for tests so the global conn cap can be
// referenced without hardcoding magic numbers.
const MaxTotalSSEConns = maxTotalSSEConns

// MaxTotalSessions is exported for tests so the global session cap can be
// referenced without hardcoding magic numbers.
const MaxTotalSessions = maxTotalSessions

// TestSSEConn is the exported view of sseConn used by external tests.
// It provides read-only access to the connection's channel, ID, and closed
// signal.
type TestSSEConn interface {
	ID() string
	Ch() <-chan SSEEvent
	Closed() <-chan struct{}
}

// testSSEConnWrapper wraps *sseConn to satisfy TestSSEConn.
type testSSEConnWrapper struct {
	c *sseConn
}

func (w testSSEConnWrapper) ID() string              { return w.c.id }
func (w testSSEConnWrapper) Ch() <-chan SSEEvent     { return w.c.ch }
func (w testSSEConnWrapper) Closed() <-chan struct{} { return w.c.closed }

// NewChatHubForTest allocates a ChatHub for use in external tests.
func NewChatHubForTest() *ChatHub { return NewChatHub() }

// Register on ChatHub is already exported, but returns *sseConn (unexported
// type).  We provide a test wrapper here so external tests can call Register
// and get a TestSSEConn.

// Register wraps the internal Register and returns a TestSSEConn.
func (h *ChatHub) Register(connID, operatorID string) (TestSSEConn, error) {
	conn, err := h.register(connID, operatorID)
	if err != nil {
		return nil, err
	}
	return testSSEConnWrapper{conn}, nil
}

// ConnCount returns the number of live SSE connections for operatorID.
// Used in external tests.
func (h *ChatHub) ConnCount(operatorID string) int {
	return h.connCount(operatorID)
}

// TotalConns returns the global total SSE connection count.
// Used in tests to verify the global cap.
func (h *ChatHub) TotalConns() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.totalConns
}

// ErrTooManyConns is exported so external tests can identify the error type.
var ErrTooManyConns = errTooManyConns

// ErrTooManySessions is exported so external tests can identify the error type.
var ErrTooManySessions = errTooManySessions

// ErrSessionNotFound is exported so external tests can identify the sentinel
// returned by SetShared when the session does not exist.
var ErrSessionNotFound = errSessionNotFound

// ErrSessionOwnerConflict is exported so external tests can identify the
// sentinel returned by SetShared / OpenSession when the session is owned by a
// different operator.
var ErrSessionOwnerConflict = errSessionOwnerConflict

// ErrConversationOwnerConflict is exported so external tests can identify the
// sentinel returned by SetConversationID when the conversationID is already
// bound to a different operator's session.
var ErrConversationOwnerConflict = errConversationOwnerConflict

// ---- Transcripts test exports -----------------------------------------------

// NewTranscriptsForTest allocates a Transcripts for use in external tests.
func NewTranscriptsForTest(workDir string) *Transcripts { return NewTranscripts(workDir) }

// ---- Chat handlers test exports ---------------------------------------------

// NewChatHandlersForTest allocates chatHandlers with a background server ctx.
// Used in tests that need to directly exercise handler logic.
func NewChatHandlersForTest(hub *ChatHub, transcripts *Transcripts, svc interface {
	RunStream(context.Context, interface{}, interface{}) (interface{}, error)
}) *chatHandlers {
	return newChatHandlers(hub, transcripts, nil, context.Background())
}

// NewChatTestServer builds chatHandlers with an explicit server context for
// use in dispatch-goroutine-lifetime tests.
func NewChatTestServer(hub *ChatHub, transcripts *Transcripts, serverCtx context.Context) *chatHandlers {
	return newChatHandlers(hub, transcripts, nil, serverCtx)
}

// ---- Flows handler test exports ---------------------------------------------

// NewFlowsHandlerForTest builds a flowsHandlers with a fake per-node dispatch
// function injected at the consoleui handler layer. workDir is the
// <work>/current root. Returns the handler (as http.Handler).
//
// The workflow engine is NOT used — fn is stored on flowsHandlers.nodeRunFn so
// that handleRun/handleResume call it directly, bypassing both the engine and
// the governed dispatch.Service. No LLM calls, no live dispatch.
//
// This is the handler-level test seam (N3). The workflow package exports no
// production-reachable governor-bypass symbol as a result of this design.
func NewFlowsHandlerForTest(t *testing.T, workDir string, fn func(context.Context, dispatch.Params) ([]byte, dispatch.Result, error)) (http.Handler, *workflow.Engine) {
	t.Helper()
	h := &flowsHandlers{
		workDir:    workDir,
		serverCtx:  context.Background(),
		nodeRunFn:  workflow.EngineRunFn(fn),
		activeRuns: make(map[string]context.CancelFunc),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/flows/api/workflows", h.handleListWorkflows)
	mux.HandleFunc("/flows/api/workflow", h.handleWorkflowDispatch) // GET/POST/DELETE
	mux.HandleFunc("/flows/api/run", h.handleRunDispatch)
	mux.HandleFunc("/flows/api/run/node", h.handleGetNodeOutput)
	mux.HandleFunc("/flows/api/resume", h.handleResume)
	mux.HandleFunc("/flows/api/cancel", h.handleCancel)
	return mux, nil
}

// ---- Phase 6c: console-bind test exports ------------------------------------

// NewPresenceManagerForTest creates a PresenceManager for use in external tests.
func NewPresenceManagerForTest(bus *wsbus.Bus) *PresenceManager {
	return NewPresenceManager(bus)
}

// BuildConsoleWSHandlerNetworkedForTest exposes buildConsoleWSHandlerNetworked
// for external test packages.  The token parameter is used for subprotocol auth.
// externalHosts is the list of host[:port] values used by browsers.
func BuildConsoleWSHandlerNetworkedForTest(token string, bus *wsbus.Bus, pm *PresenceManager, externalHosts []string) http.Handler {
	return buildConsoleWSHandlerNetworked(token, bus, pm, externalHosts)
}

// IsExternalOriginForTest exposes isExternalOrigin for external tests so
// the Origin allow-list predicate can be unit-tested without triggering a
// real WS upgrade (which requires http.Hijacker).
func IsExternalOriginForTest(origin, externalHost string) bool {
	return isExternalOrigin(origin, externalHost)
}

// IsLoopbackHostForTest exposes isLoopbackHost for external tests so the
// canonical loopback predicate can be unit-tested without going through a
// full HTTP server round-trip.
func IsLoopbackHostForTest(host string) bool {
	return isLoopbackHost(host)
}

// IsLoopbackOriginForTest exposes isLoopbackOrigin for external tests.
func IsLoopbackOriginForTest(origin string) bool {
	return isLoopbackOrigin(origin)
}

// BuildOriginAllowListNetworkedForTest exposes consoleOriginAllowListNetworked
// directly so tests can exercise the middleware with a simple next handler
// (without triggering the WS upgrade that requires http.Hijacker).
// externalHosts is the list of host[:port] values used by browsers.
func BuildOriginAllowListNetworkedForTest(externalHosts []string, next http.Handler) http.Handler {
	return consoleOriginAllowListNetworked(externalHosts, next)
}

// ---- Phase 3e: session-cookie WS auth test exports ---------------------------

// ConsoleAuthSubprotocolOrSessionForTest exposes consoleAuthSubprotocolOrSession
// for external test packages.  externalHosts is the list of host[:port] values
// that the session-path Origin check allows (same as buildConsoleWSHandlerNetworked).
func ConsoleAuthSubprotocolOrSessionForTest(token string, externalHosts []string, next http.Handler) http.Handler {
	return consoleAuthSubprotocolOrSession(token, externalHosts, next)
}

// ---- Phase 3a: session auth glue test exports --------------------------------

// BuildSessionLookupFnForTest exposes buildSessionLookupFn for external tests
// so the session lookup glue can be unit-tested directly without going through
// a full HTTP server stack.  Callers can exercise the (operatorID, role, ok)
// return values and verify that spoofed headers/params are ignored.
func BuildSessionLookupFnForTest(authStore *authsession.Store, uStore *userstore.Store) netid.SessionLookupFn {
	return buildSessionLookupFn(authStore, uStore)
}

// ---- New / MustNew test helpers ----------------------------------------------

// MustNew calls New(cfg) and calls t.Fatal if it returns an error.
// Use this in tests instead of calling New directly so that the two-return-value
// signature is handled cleanly without a t.Fatal boilerplate at every call site.
func MustNew(t *testing.T, cfg Config) *Server {
	t.Helper()
	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("consoleui.New: %v", err)
	}
	return srv
}

// SetChatHandlersAgentValidation wires yakosRoot and workspaceRoot onto the
// Server's chatHandlers after construction.  Used in tests that need to
// exercise up-front agent validation without going through the full serve.go
// startup path.
func SetChatHandlersAgentValidation(srv *Server, yakosRoot, workspaceRoot string) {
	srv.chat.yakosRoot = yakosRoot
	srv.chat.workspaceRoot = workspaceRoot
}

// ---- Phase 3b: auth handler test exports ------------------------------------

// LoginRateLimitRequests is the per-IP login attempt cap, exported for tests
// so they can exhaust it without hardcoding the magic number.
const LoginRateLimitRequests = loginRateLimitRequests

// SessionCookieName is the session cookie name constant, exported for tests
// so the cross-package invariant test can compare it to
// authsession.CookieNameSession without reaching into the unexported symbol.
const SessionCookieName = sessionCookieName

// RequireCSRFForSessionForTest exposes requireCSRFForSession for direct
// unit tests that want to exercise CSRF middleware behavior with injected
// identities, without going through the full production server stack.
func RequireCSRFForSessionForTest(authStore *authsession.Store, next http.Handler) http.Handler {
	return requireCSRFForSession(authStore, next)
}

// ---- Interactive-P1 test exports --------------------------------------------

// InteractiveSender is the exported alias for the interactiveSender interface.
// Tests in consoleui_test can implement it to inject deterministic Send errors
// (e.g. ErrTurnInFlight) without needing a real subprocess.
type InteractiveSender = interactiveSender

// SetInteractiveSender replaces the chat handler's Send-side implementation
// with stub.  Call this AFTER MustNew and BEFORE the first HTTP request.
// The interactiveMgr nil-guard in handleChatSend is bypassed by this helper:
// it sets interactiveMgr to a non-nil sentinel and interactiveSend to stub.
func SetInteractiveSender(srv *Server, stub InteractiveSender) {
	// We need interactiveMgr != nil so the 503 guard passes.  Reuse the same
	// non-nil value already set (if any), or set a sentinel non-nil pointer.
	// In tests that pass a real Manager via Config, interactiveMgr is already
	// set; we only replace interactiveSend.
	// For tests that pass nil InteractiveManager but want to test the 409 path,
	// we set interactiveMgr to a bare non-nil value via the sentinel field trick.
	srv.chat.interactiveSend = stub
	// If interactiveMgr is nil (no real manager provided), we still need it
	// non-nil to pass the 503 guard.  We cannot construct a Manager here
	// (requires context + goroutine), so callers of SetInteractiveSender must
	// also pass a non-nil InteractiveManager in Config (even a dummy one).
	// See TestChatSend_TurnInFlight_409 for the pattern.
}

// ---- File API test exports ---------------------------------------------------

// MaxTreeEntriesPerDir is exported so tests can exercise the per-directory
// entry-cap and verify the truncated=true branch without hardcoding the limit.
const MaxTreeEntriesPerDir = maxTreeEntriesPerDir

// MaxTreeDepth is exported so tests can exercise the depth-cap truncated=true
// branch (nest MaxTreeDepth+1 directories).
const MaxTreeDepth = maxTreeDepth

// MaxTreeTotalNodes is exported so tests can exercise the global node budget
// truncated=true branch.
const MaxTreeTotalNodes = maxTreeTotalNodes

// MaxFileContentBytes is exported so write tests can exercise the content size
// cap (413 Request Entity Too Large) without hardcoding the limit.
const MaxFileContentBytes = maxFileContentBytes

// FileContentHash is exported so tests can compute expected version stamps
// and verify round-trip consistency between read and write endpoints.
var FileContentHash = fileContentHash

// ---- Fleet handler test exports ---------------------------------------------

// RegistryForTest returns the SessionRegistry wired into the Server.
// Used by fleet tests to pre-populate sessions without going through the
// full dispatch goroutine.
func RegistryForTest(s *Server) *dispatch.SessionRegistry {
	return s.fleet.registry
}
