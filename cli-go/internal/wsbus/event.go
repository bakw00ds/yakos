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

// Topic constants for Phase-4 workflow events.
// Invariant: no token or node content is published on these topics.
// Only run/node lifecycle metadata is carried (IDs, status, timestamps).
const (
	TopicWorkflowRunStarted    = "workflow.run.started"
	TopicWorkflowRunFinished   = "workflow.run.finished"
	TopicWorkflowNodeStarted   = "workflow.node.started"
	TopicWorkflowNodeFinished  = "workflow.node.finished"
	TopicWorkflowNodeTruncated = "workflow.node.truncated"
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

// WorkflowRunStartedPayload is the payload for [TopicWorkflowRunStarted].
// Invariant: does not carry node content or tokens.
type WorkflowRunStartedPayload struct {
	RunID    string    `json:"run_id"`
	Workflow string    `json:"workflow"`
	TS       time.Time `json:"ts"`
}

// WorkflowRunFinishedPayload is the payload for [TopicWorkflowRunFinished].
type WorkflowRunFinishedPayload struct {
	RunID    string    `json:"run_id"`
	Workflow string    `json:"workflow"`
	Status   string    `json:"status"` // "completed" | "failed"
	TS       time.Time `json:"ts"`
}

// WorkflowNodeStartedPayload is the payload for [TopicWorkflowNodeStarted].
type WorkflowNodeStartedPayload struct {
	RunID    string    `json:"run_id"`
	Workflow string    `json:"workflow"`
	NodeID   string    `json:"node_id"`
	Agent    string    `json:"agent"`
	TS       time.Time `json:"ts"`
}

// WorkflowNodeFinishedPayload is the payload for [TopicWorkflowNodeFinished].
// Invariant: does not carry node content or tokens.
// CostUSD is the cost of this node's dispatch (from dispatch.Result.Usage.TotalCostUSD).
// It is omitted when cost is unknown (non-claude runtimes, or claude without usage data).
// Clients must treat absent cost_usd as "unavailable", not "$0".
type WorkflowNodeFinishedPayload struct {
	RunID    string    `json:"run_id"`
	Workflow string    `json:"workflow"`
	NodeID   string    `json:"node_id"`
	Status   string    `json:"status"` // "completed" | "failed" | "skipped"
	ExitCode int       `json:"exit_code"`
	CostUSD  *float64  `json:"cost_usd,omitempty"` // nil = cost unavailable for this node
	TS       time.Time `json:"ts"`
}

// WorkflowNodeTruncatedPayload is the payload for [TopicWorkflowNodeTruncated].
// Published when a node's output is tail-truncated before being substituted
// into downstream prompts. Carries only byte counts, never content.
type WorkflowNodeTruncatedPayload struct {
	RunID       string    `json:"run_id"`
	Workflow    string    `json:"workflow"`
	NodeID      string    `json:"node_id"`
	OriginalLen int       `json:"original_len"`
	TruncatedTo int       `json:"truncated_to"`
	TS          time.Time `json:"ts"`
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
