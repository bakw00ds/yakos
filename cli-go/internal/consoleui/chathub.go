// Package consoleui — chathub.go
//
// ChatHub routes streaming chunks from RunStream to SSE connections.
//
// # Design
//
// The hub is the authoritative registry for two things:
//
//  1. Live SSE connections, keyed by operatorID.  One operator may have
//     multiple browser tabs open; each tab registers its own sseConn.
//     Connections are added/removed via Register/Unregister.
//
//  2. Session ownership, keyed by sessionID.  Every streaming dispatch
//     "opens" a session owned by a specific operatorID.  The owner receives
//     every chunk from that session.  A session may be promoted to shared
//     (OpenSession shared=true), after which all registered connections
//     receive its chunks.
//
// # Per-operator isolation
//
// Isolation is enforced in Route:
//   - If session.shared is false: chunks are delivered ONLY to connections
//     where conn.operatorID == session.ownerOperatorID.
//   - If session.shared is true: chunks are delivered to ALL registered
//     connections.
//
// Chunks are never delivered cross-operator on unshared sessions.
// Attempting to open a session already owned by a DIFFERENT operator returns
// errSessionOwnerConflict (the caller must return 403 to the browser).
//
// # Concurrency
//
// A single sync.Mutex guards all state.  Chunk delivery is a non-blocking
// send on a buffered channel (sseConn.ch).  A full channel causes the chunk
// to be dropped for that connection; the session is never lost.
//
// # Bounds
//
// maxSSEConnsPerOperator limits the number of simultaneous SSE connections
// per operator.  Attempts to register beyond this cap return
// errTooManyConns.  Each sseConn channel is buffered at sseChBuf events.
package consoleui

import (
	"errors"
	"sync"
	"time"
)

// maxSSEConnsPerOperator is the maximum number of concurrent SSE connections
// a single operator may hold.  Beyond this cap Register returns
// errTooManyConns and the HTTP handler sends 429 Too Many Requests.
const maxSSEConnsPerOperator = 8

// maxTotalSSEConns is the global cap on total live SSE connections across ALL
// operators.  An operator can self-assert any operatorId, so per-operator caps
// alone do not bound the total goroutine count (unlimited operatorIds × 8 conns
// = unbounded).  This global cap closes that gap.
const maxTotalSSEConns = 128

// maxTotalSessions is the global cap on total open sessions across ALL
// operators.  Sessions are cheap but unbounded sessions allow memory exhaustion
// via session squatting.
const maxTotalSessions = 256

// sseChBuf is the per-connection channel buffer depth.  A slow client that
// cannot drain faster than this will drop chunks (never blocks the hub mutex
// or other clients).
const sseChBuf = 256

// SSEEvent is one multiplexed event delivered over the per-operator SSE stream.
// The browser demuxes by sessionID.
type SSEEvent struct {
	// SessionID identifies which chat pane this event belongs to.
	SessionID string `json:"session_id"`

	// Type is "token" | "summary" | "ping" | "tool_use" | "tool_result" | "error".
	// "error" is emitted by the dispatch goroutine when RunStream returns a
	// non-cancel error, so the client UI can show an error instead of hanging.
	Type string `json:"type"`

	// Text is the token text (Type=="token") or full text (Type=="summary").
	Text string `json:"text,omitempty"`

	// ExitCode is set on summary events.  Pointer so exit_code:0 (success) is
	// serialized on summary turns and absent on token/other turns (omitempty on
	// an int zero-value would suppress a legitimate exit code of 0).
	ExitCode *int `json:"exit_code,omitempty"`

	// DurationS is set on summary events.  Pointer for the same reason as ExitCode.
	DurationS *float64 `json:"duration_s,omitempty"`

	// TotalCostUSD is set on summary events.  Pointer for the same reason.
	TotalCostUSD *float64 `json:"total_cost_usd,omitempty"`

	// ModelResolved is set on summary events.
	ModelResolved string `json:"model_resolved,omitempty"`

	// ToolName is the human-readable tool name (Type=="tool_use" or "tool_result").
	// Examples: "Bash", "Read", "Write".
	ToolName string `json:"tool_name,omitempty"`

	// ToolInput is the JSON-encoded tool arguments (Type=="tool_use").
	// For a Bash invocation this is typically {"command":"ls -la"}.
	// Absent on "tool_result" events.
	ToolInput string `json:"tool_input,omitempty"`

	// ToolOutput is the truncated tool result content (Type=="tool_result").
	// Always <= maxToolOutputBytes + len(truncation marker).
	// Absent on "tool_use" events.
	ToolOutput string `json:"tool_output,omitempty"`

	// IsError is true when the tool_result represents a tool-level error
	// (Type=="tool_result" only).
	IsError bool `json:"is_error,omitempty"`

	// TS is the hub-stamped delivery time (RFC3339Nano).
	TS string `json:"ts"`
}

// sseConn is one registered SSE connection.
type sseConn struct {
	id         string // random per-connection identifier (for Unregister)
	operatorID string // owner of this connection
	ch         chan SSEEvent
	closed     chan struct{} // closed when Unregister removes this conn
}

// sessionEntry holds ownership metadata for one active session.
type sessionEntry struct {
	ownerOperatorID string
	shared          bool
	conversationID  string // the conversation this session writes to; set by SetConversationID
}

// errSessionOwnerConflict is returned by OpenSession and SetShared when the
// sessionID is already owned by a different operatorID.  The HTTP handler must
// return 403.
var errSessionOwnerConflict = errors.New("chathub: session owned by different operator")

// errConversationOwnerConflict is returned by SetConversationID when the
// conversationID is already bound to a session owned by a DIFFERENT operator.
// The HTTP handler must return 403.  This prevents conversation-binding
// poisoning: an attacker cannot bind their own session to a victim's
// conversationID to manufacture a shared-session entry that bypasses the
// transcript owner check.
var errConversationOwnerConflict = errors.New("chathub: conversationID already bound to another operator's session")

// errSessionNotFound is returned by SetShared when the sessionID does not
// exist in the hub.  The HTTP handler must return 404.
var errSessionNotFound = errors.New("chathub: session not found")

// errTooManyConns is returned by Register when the per-operator or global
// connection cap would be exceeded.  The HTTP handler must return 429.
var errTooManyConns = errors.New("chathub: too many SSE connections for this operator")

// errTooManySessions is returned by OpenSession when the global session cap
// would be exceeded.  The HTTP handler must return 429.
var errTooManySessions = errors.New("chathub: global session cap reached")

// ChatHub is the authoritative router for chat SSE events.
// All exported methods are safe for concurrent use.
type ChatHub struct {
	mu         sync.Mutex
	conns      map[string][]*sseConn   // operatorID → list of connections
	connByID   map[string]*sseConn     // connID → conn (for Unregister)
	sessions   map[string]sessionEntry // sessionID → entry
	totalConns int                     // M3: global SSE connection count across all operators
}

// NewChatHub allocates an empty hub.
func NewChatHub() *ChatHub {
	return &ChatHub{
		conns:    make(map[string][]*sseConn),
		connByID: make(map[string]*sseConn),
		sessions: make(map[string]sessionEntry),
	}
}

// register adds an SSE connection for operatorID.  Returns the connection
// handle (use its ch field for reads; call Unregister with conn.id on
// disconnect).
//
// Returns errTooManyConns when the per-operator cap or the global cap is
// exceeded (M3: operatorId is self-asserted, so unlimited operators × 8 conns
// = unbounded goroutines without the global cap).
//
// Called from the HTTP handler (handleChatStream) and from the test-export
// wrapper (export_test.go).
func (h *ChatHub) register(connID, operatorID string) (*sseConn, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Per-operator cap.
	if len(h.conns[operatorID]) >= maxSSEConnsPerOperator {
		return nil, errTooManyConns
	}
	// M3: global cap — prevents unlimited operatorIds × 8 conns from exhausting
	// goroutines and map memory.
	if h.totalConns >= maxTotalSSEConns {
		return nil, errTooManyConns
	}

	conn := &sseConn{
		id:         connID,
		operatorID: operatorID,
		ch:         make(chan SSEEvent, sseChBuf),
		closed:     make(chan struct{}),
	}
	h.conns[operatorID] = append(h.conns[operatorID], conn)
	h.connByID[connID] = conn
	h.totalConns++
	return conn, nil
}

// Unregister removes the connection with the given connID and closes its
// closed channel to signal the SSE handler that it should stop.
// No-op if connID is not known.
func (h *ChatHub) Unregister(connID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	conn, ok := h.connByID[connID]
	if !ok {
		return
	}
	delete(h.connByID, connID)

	list := h.conns[conn.operatorID]
	for i, c := range list {
		if c.id == connID {
			h.conns[conn.operatorID] = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(h.conns[conn.operatorID]) == 0 {
		delete(h.conns, conn.operatorID)
	}
	if h.totalConns > 0 {
		h.totalConns--
	}
	close(conn.closed)
}

// OpenSession registers ownerOperatorID as the session owner.
//
// If the sessionID already exists:
//   - Same owner: idempotent (returns nil); updates shared flag.
//   - Different owner: returns errSessionOwnerConflict (caller returns 403).
//
// Returns errTooManySessions when the global session cap is reached (M3).
//
// shared controls whether non-owner operators' SSE connections also receive
// chunks from this session.
func (h *ChatHub) OpenSession(sessionID, ownerOperatorID string, shared bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if entry, ok := h.sessions[sessionID]; ok {
		if entry.ownerOperatorID != ownerOperatorID {
			return errSessionOwnerConflict
		}
		// Same owner: idempotent; update shared flag.
		entry.shared = shared
		h.sessions[sessionID] = entry
		return nil
	}
	// M3: global session cap.
	if len(h.sessions) >= maxTotalSessions {
		return errTooManySessions
	}
	h.sessions[sessionID] = sessionEntry{
		ownerOperatorID: ownerOperatorID,
		shared:          shared,
	}
	return nil
}

// CloseSession removes the session entry.  No-op if not found.
// Called after the RunStream goroutine exits and the summary chunk has been
// delivered.
func (h *ChatHub) CloseSession(sessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.sessions, sessionID)
}

// SetShared atomically updates the shared flag for an existing session.
//
// Unlike the SessionOwner+OpenSession pair, this method holds the mutex for
// the entire check-and-update operation, eliminating the TOCTOU window where
// the dispatch goroutine's deferred CloseSession could fire between the two
// calls and cause OpenSession to recreate a session entry that nothing ever
// closes (permanent leak toward maxTotalSessions=256).
//
// Returns:
//   - errSessionNotFound (→ 404) if the session does not exist.
//   - errSessionOwnerConflict (→ 403) if the session is owned by a different
//     operatorID.
//   - nil on success.
func (h *ChatHub) SetShared(sessionID, ownerOperatorID string, shared bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	entry, ok := h.sessions[sessionID]
	if !ok {
		return errSessionNotFound
	}
	if entry.ownerOperatorID != ownerOperatorID {
		return errSessionOwnerConflict
	}
	entry.shared = shared
	h.sessions[sessionID] = entry
	return nil
}

// Route delivers ev to the appropriate SSE connections.
//
// Delivery rules:
//   - If no session entry exists for ev.SessionID, the event is dropped.
//   - Private session (shared=false): only connections owned by the session
//     owner receive the event.
//   - Shared session (shared=true): all registered connections receive it.
//
// Delivery is non-blocking: a full per-connection channel causes that chunk
// to be silently dropped for that connection.  The session is never stalled.
func (h *ChatHub) Route(ev SSEEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()

	entry, ok := h.sessions[ev.SessionID]
	if !ok {
		return // unknown session — drop
	}

	if ev.TS == "" {
		ev.TS = time.Now().UTC().Format(time.RFC3339Nano)
	}

	deliver := func(conn *sseConn) {
		select {
		case conn.ch <- ev:
		default: // channel full — drop for this connection only
		}
	}

	if entry.shared {
		// Deliver to all registered connections across all operators.
		for _, list := range h.conns {
			for _, conn := range list {
				deliver(conn)
			}
		}
	} else {
		// Deliver only to the owner's connections.
		for _, conn := range h.conns[entry.ownerOperatorID] {
			deliver(conn)
		}
	}
}

// SessionOwner returns the ownerOperatorID for the given sessionID, and
// whether the session exists.  Used by the dispatch handler to verify that a
// new dispatch for an existing session comes from the same operator.
func (h *ChatHub) SessionOwner(sessionID string) (ownerOperatorID string, exists bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e, ok := h.sessions[sessionID]
	return e.ownerOperatorID, ok
}

// IsShared returns whether the session with the given sessionID has been
// promoted to shared (visible to all operators).  Returns false if the session
// does not exist.  Used by the fleet handler to apply the same scoping rules
// as ChatHub.Route without exposing the unexported sessionEntry type.
func (h *ChatHub) IsShared(sessionID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	e, ok := h.sessions[sessionID]
	if !ok {
		return false
	}
	return e.shared
}

// SetConversationID records which conversationID a session is writing to.
// Called by the dispatch handler immediately after conversationID is resolved
// (defaulting to sessionID when the caller supplies none).
//
// Security invariant: returns errConversationOwnerConflict (→ 403) when
// conversationID is already bound to an OPEN session owned by a DIFFERENT
// operator.  This prevents conversation-binding poisoning: an attacker cannot
// bind their own session to a victim's conversationID, share their own session,
// and then have IsConversationShared return true for the victim's conversation.
//
// Same-operator re-binding (e.g. a second dispatch for the same conversation
// under a new sessionID) is allowed.
//
// No-op (returns nil) when sessionID does not exist in the hub (race with
// CloseSession at teardown is benign — the session is already gone).
func (h *ChatHub) SetConversationID(sessionID, conversationID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	e, ok := h.sessions[sessionID]
	if !ok {
		return nil // session already closed — no-op
	}

	// Check whether conversationID is already bound to a different operator's
	// session.  We scan ALL sessions so we detect even sessions that have
	// already written to the conversation from a different sessionID.
	for sid, existing := range h.sessions {
		if sid == sessionID {
			continue // skip the session we are about to update
		}
		if existing.conversationID == conversationID &&
			existing.ownerOperatorID != e.ownerOperatorID {
			return errConversationOwnerConflict
		}
	}

	e.conversationID = conversationID
	h.sessions[sessionID] = e
	return nil
}

// ConversationForSession returns the conversationID bound to sessionID, or ""
// when the session does not exist or has no conversation bound yet.
// Used by the transcript handler for defense-in-depth: if a caller supplies a
// sessionId that is bound to a different conversationID than the one being
// requested, the request is rejected (403) before reaching the owner check.
func (h *ChatHub) ConversationForSession(sessionID string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sessions[sessionID].conversationID
}

// IsConversationShared returns true iff a currently-open, SHARED session that
// owns conversationID is also owned by expectedOwnerOperatorID.
//
// Owner-anchoring: the lookup matches on BOTH conversationID and session owner.
// An attacker who bound their own session to the same conversationID will have
// a different ownerOperatorID, so their shared session cannot satisfy this
// check.  Combined with SetConversationID's cross-operator rejection, this
// provides two independent layers of defence.
//
// expectedOwnerOperatorID is the operator established by the transcript file's
// first user turn (M1 logic).  Passing "" disables the owner check (internal
// daemon calls where no operatorID is known).
//
// Returns false when no matching session is found or it is not shared.
func (h *ChatHub) IsConversationShared(conversationID, expectedOwnerOperatorID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, e := range h.sessions {
		if e.conversationID != conversationID {
			continue
		}
		// Owner-anchored: the session owner must match the transcript's
		// established owner.  "" disables this check (internal calls).
		if expectedOwnerOperatorID != "" && e.ownerOperatorID != expectedOwnerOperatorID {
			continue
		}
		return e.shared
	}
	return false
}

// connCount returns the number of live SSE connections for operatorID.
// Used in tests and diagnostics.
func (h *ChatHub) connCount(operatorID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.conns[operatorID])
}
