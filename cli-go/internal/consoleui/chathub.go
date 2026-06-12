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

// sseChBuf is the per-connection channel buffer depth.  A slow client that
// cannot drain faster than this will drop chunks (never blocks the hub mutex
// or other clients).
const sseChBuf = 256

// SSEEvent is one multiplexed event delivered over the per-operator SSE stream.
// The browser demuxes by sessionID.
type SSEEvent struct {
	// SessionID identifies which chat pane this event belongs to.
	SessionID string `json:"session_id"`

	// Type is "token" | "summary" | "ping".
	Type string `json:"type"`

	// Text is the token text (Type=="token") or full text (Type=="summary").
	Text string `json:"text,omitempty"`

	// ExitCode is set on summary events.
	ExitCode int `json:"exit_code,omitempty"`

	// DurationS is set on summary events.
	DurationS float64 `json:"duration_s,omitempty"`

	// TotalCostUSD is set on summary events.
	TotalCostUSD float64 `json:"total_cost_usd,omitempty"`

	// ModelResolved is set on summary events.
	ModelResolved string `json:"model_resolved,omitempty"`

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
}

// errSessionOwnerConflict is returned by OpenSession when the sessionID is
// already owned by a different operatorID.  The HTTP handler must return 403.
var errSessionOwnerConflict = errors.New("chathub: session owned by different operator")

// errTooManyConns is returned by Register when the per-operator connection cap
// would be exceeded.  The HTTP handler must return 429.
var errTooManyConns = errors.New("chathub: too many SSE connections for this operator")

// ChatHub is the authoritative router for chat SSE events.
// All exported methods are safe for concurrent use.
type ChatHub struct {
	mu       sync.Mutex
	conns    map[string][]*sseConn   // operatorID → list of connections
	connByID map[string]*sseConn     // connID → conn (for Unregister)
	sessions map[string]sessionEntry // sessionID → entry
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
// disconnect).  Returns errTooManyConns when the per-operator cap is exceeded.
//
// Called from the HTTP handler (handleChatStream) and from the test-export
// wrapper (export_test.go).
func (h *ChatHub) register(connID, operatorID string) (*sseConn, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(h.conns[operatorID]) >= maxSSEConnsPerOperator {
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
	close(conn.closed)
}

// OpenSession registers ownerOperatorID as the session owner.
//
// If the sessionID already exists:
//   - Same owner: idempotent (returns nil); updates shared flag.
//   - Different owner: returns errSessionOwnerConflict (caller returns 403).
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

// connCount returns the number of live SSE connections for operatorID.
// Used in tests and diagnostics.
func (h *ChatHub) connCount(operatorID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.conns[operatorID])
}
