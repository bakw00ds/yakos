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

	"github.com/bakw00ds/yakos/internal/workflow"
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

// NewFlowsHandlerForTest builds a flowsHandlers wired with a fake engine
// whose per-node dispatch function is fn. workDir is the <work>/current root.
// Returns the handler (as http.Handler) and the engine for further
// configuration if needed.
//
// The engine has no Svc/Bus/YakosRoot/Project set — fn bypasses all of those.
// This is the handler-level test seam (N3).
func NewFlowsHandlerForTest(t *testing.T, workDir string, fn workflow.EngineRunFn) (http.Handler, *workflow.Engine) {
	t.Helper()
	eng := &workflow.Engine{
		YakosRoot: "/yakos-test",
		Project:   "/project-test",
		WorkDir:   workDir,
	}
	workflow.SetEngineRunFn(eng, fn)
	h := &flowsHandlers{
		engine:    eng,
		workDir:   workDir,
		serverCtx: context.Background(),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/flows/api/workflows", h.handleListWorkflows)
	mux.HandleFunc("/flows/api/workflow", h.handleWorkflowDispatch)
	mux.HandleFunc("/flows/api/run", h.handleRunDispatch)
	mux.HandleFunc("/flows/api/run/node", h.handleGetNodeOutput)
	mux.HandleFunc("/flows/api/resume", h.handleResume)
	return mux, eng
}
