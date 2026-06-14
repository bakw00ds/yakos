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

	"github.com/bakw00ds/yakos/internal/dispatch"
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
		workDir:   workDir,
		serverCtx: context.Background(),
		nodeRunFn: workflow.EngineRunFn(fn),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/flows/api/workflows", h.handleListWorkflows)
	mux.HandleFunc("/flows/api/workflow", h.handleWorkflowDispatch) // GET/POST/DELETE
	mux.HandleFunc("/flows/api/run", h.handleRunDispatch)
	mux.HandleFunc("/flows/api/run/node", h.handleGetNodeOutput)
	mux.HandleFunc("/flows/api/resume", h.handleResume)
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
