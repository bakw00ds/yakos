// Package dispatch — registry.go
//
// SessionRegistry is a concurrency-safe in-process registry of active console
// dispatch sessions (browser chat panes that have an in-flight RunStream goroutine).
// It is the source of truth for the /api/fleet endpoint and the fleet.started /
// fleet.finished WS topics.
//
// Design constraints:
//   - TaskPreview is hard-truncated to <=120 chars at Add time.
//     It must NEVER be included in WS fleet.* payloads (metadata-only invariant).
//     It is exposed only via the REST snapshot endpoint, scoped to the caller.
//   - The registry holds no token text, no LLM tokens, no transcript content.
//   - Status values: "running" | "finished" | "failed".
//   - All methods are safe for concurrent use (single RWMutex).
package dispatch

import (
	"sync"
	"time"
	"unicode/utf8"
)

// maxTaskPreview is the maximum rune length of a stored task preview.
const maxTaskPreview = 120

// SessionStatus is the lifecycle state of a registry entry.
type SessionStatus string

const (
	// StatusRunning indicates the RunStream goroutine is still active.
	StatusRunning SessionStatus = "running"
	// StatusFinished indicates the RunStream goroutine exited cleanly (exit_code 0).
	StatusFinished SessionStatus = "finished"
	// StatusFailed indicates the RunStream goroutine exited with a non-zero exit code.
	StatusFailed SessionStatus = "failed"
)

// SessionEntry is one record in the SessionRegistry.
// TaskPreview is intentionally excluded from WS payloads — see fleet_handler.go
// and wsbus event payload types for the no-task-text invariant.
type SessionEntry struct {
	SessionID   string        `json:"session_id"`
	Agent       string        `json:"agent"`
	Runtime     string        `json:"runtime"`
	OperatorID  string        `json:"operator_id"`
	TaskPreview string        `json:"task_preview"` // <=120 chars; REST-only, never in WS
	StartedAt   time.Time     `json:"started_at"`
	Status      SessionStatus `json:"status"`
}

// SessionRegistry is a concurrency-safe map of sessionID → SessionEntry.
// Lifecycle: Add on goroutine start, UpdateStatus on summary chunk or error,
// Remove on goroutine exit.
type SessionRegistry struct {
	mu       sync.RWMutex
	sessions map[string]SessionEntry
}

// NewSessionRegistry allocates an empty SessionRegistry.
func NewSessionRegistry() *SessionRegistry {
	return &SessionRegistry{
		sessions: make(map[string]SessionEntry),
	}
}

// Add registers a new session entry.  If the sessionID already exists, Add is
// a no-op (callers must Remove before re-adding, matching the chatState pattern).
// TaskPreview is truncated to maxTaskPreview runes at entry.
func (r *SessionRegistry) Add(e SessionEntry) {
	e.TaskPreview = truncateRunes(e.TaskPreview, maxTaskPreview)
	if e.Status == "" {
		e.Status = StatusRunning
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sessions[e.SessionID]; exists {
		return // no-op: already registered
	}
	r.sessions[e.SessionID] = e
}

// UpdateStatus changes the Status field of an existing entry.
// No-op if the sessionID is not registered.
func (r *SessionRegistry) UpdateStatus(sessionID string, status SessionStatus) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.sessions[sessionID]
	if !ok {
		return
	}
	e.Status = status
	r.sessions[sessionID] = e
}

// Remove deletes the entry for sessionID.  No-op if not found.
func (r *SessionRegistry) Remove(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, sessionID)
}

// Snapshot returns a copy of all entries in the registry.
// The returned slice is a snapshot; mutations to the registry after this call
// do not affect it.
func (r *SessionRegistry) Snapshot() []SessionEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]SessionEntry, 0, len(r.sessions))
	for _, e := range r.sessions {
		out = append(out, e)
	}
	return out
}

// Get returns a copy of the entry for sessionID and whether it exists.
func (r *SessionRegistry) Get(sessionID string) (SessionEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.sessions[sessionID]
	return e, ok
}

// truncateRunes returns s truncated to at most n runes.
func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}
