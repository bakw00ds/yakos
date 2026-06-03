// Package wsbus implements an in-process WebSocket event bus for yakOS
// multi-dev coordination.
//
// # Architecture
//
// The bus is a publish/subscribe system with topic-based routing.  Publishers
// call [Bus.Publish]; subscribers receive events on a channel obtained from
// [Bus.Subscribe].  The WebSocket server in server.go wraps the bus and
// exposes it over HTTP at /v1/events.
//
// # Topics
//
// Topics use a dot-separated hierarchy: "kanban.added", "dispatch.started",
// "presence".  Subscribers may filter to a single topic; a wildcard ("*" or
// empty) receives all events.
//
// # Event ordering + delivery
//
// Best-effort, at-most-once.  Every event carries a monotonically increasing
// [Event.Seq] so clients can detect gaps.  No replay in Phase 2 (Q8 decision).
//
// # Stability: experimental
package wsbus

import (
	"encoding/json"
	"time"
)

// Topic constants for all Phase-2 event types.
const (
	TopicKanbanAdded      = "kanban.added"
	TopicKanbanMoved      = "kanban.moved"
	TopicDispatchStarted  = "dispatch.started"
	TopicDispatchFinished = "dispatch.finished"
	TopicPresence         = "presence"
)

// Event is an envelope for every message published on the bus.
// Clients receive exactly this structure (JSON-encoded) over the WebSocket.
type Event struct {
	// Seq is a monotonically increasing server-assigned sequence number.
	// Clients use gaps in Seq to detect dropped events.
	Seq int64 `json:"seq"`

	// Topic identifies the event type (e.g. "kanban.added").
	Topic string `json:"topic"`

	// TS is the server-side emission time (RFC 3339 / UTC).
	TS time.Time `json:"ts"`

	// Payload is the topic-specific data, encoded as a JSON object.
	Payload json.RawMessage `json:"payload"`
}

// KanbanAddedPayload is the payload for [TopicKanbanAdded].
type KanbanAddedPayload struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Column string `json:"column"`
}

// KanbanMovedPayload is the payload for [TopicKanbanMoved].
type KanbanMovedPayload struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
}

// DispatchStartedPayload is the payload for [TopicDispatchStarted].
type DispatchStartedPayload struct {
	Agent   string    `json:"agent"`
	Project string    `json:"project"`
	TS      time.Time `json:"ts"`
}

// DispatchFinishedPayload is the payload for [TopicDispatchFinished].
type DispatchFinishedPayload struct {
	Agent    string    `json:"agent"`
	Project  string    `json:"project"`
	ExitCode int       `json:"exit_code"`
	TS       time.Time `json:"ts"`
}

// PresencePayload is the payload for [TopicPresence].
type PresencePayload struct {
	User   string `json:"user"`
	Host   string `json:"host"`
	Status string `json:"status"` // "active" | "idle" | "gone"
}

// MarshalPayload JSON-encodes v and returns the raw message.
// Panics if v cannot be marshalled (caller must ensure v is marshallable).
func MarshalPayload(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic("wsbus: MarshalPayload: " + err.Error())
	}
	return b
}
