// Package consoleui — fleet_handler.go
//
// GET /api/fleet — REST snapshot of active console dispatch sessions.
//
// # Purpose
//
// The fleet endpoint gives the browser's REPL side-panel a seed view of all
// in-flight dispatch sessions that the caller is allowed to see.  After the
// initial fetch, the client patches its local copy live using fleet.started /
// fleet.finished events on the existing /v1/events WS bus.
//
// # Scoping (security requirement)
//
// An operator MUST NOT see another operator's unshared sessions.
// Visibility rules mirror ChatHub.Route:
//   - Owned session (ownerOperatorID == caller): always visible.
//   - Shared session (hub entry has shared=true): visible to all callers.
//   - Other operator's unshared session: NOT returned.
//
// The `attachable` field signals whether the caller may re-attach to the session
// (true = owned or shared).  On the loopback path, all entries are attachable
// because there is only one cooperative operator.  On the mTLS/session path,
// only owned+shared entries are attachable.
//
// # Auth
//
// Mounted under requireRoleFunc(netid.RoleRead, ...) in registerRoutes.
// Auth is enforced at middleware level.  No re-check in handler body.
//
// # Cache
//
// Cache-Control: no-store (the snapshot is live state; stale caches produce
// phantom sessions in the UI).
//
// # Idempotency
//
// GET /api/fleet is idempotent (read-only); no Idempotency-Key needed.
package consoleui

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/netid"
)

// FleetSession is one row in the /api/fleet response.
// TaskPreview comes from the SessionRegistry (truncated at Add time to <=120 chars).
// It is included here (REST-only) so the browser panel can show a hint of what
// the agent is doing without the caller needing to fetch the transcript.
// TaskPreview is intentionally absent from WS fleet.* payloads (metadata-only
// invariant there); the REST snapshot is the only channel that carries it.
type FleetSession struct {
	SessionID   string    `json:"session_id"`
	Agent       string    `json:"agent"`
	Runtime     string    `json:"runtime"`
	Status      string    `json:"status"`
	StartedAt   time.Time `json:"started_at"`
	TaskPreview string    `json:"task_preview"` // <=120 chars; REST-only
	Attachable  bool      `json:"attachable"`   // true = caller may re-attach (owned || shared)
	Owned       bool      `json:"owned"`        // true = caller is the session owner (can interject)
}

// FleetResponse is the JSON body returned by GET /api/fleet.
type FleetResponse struct {
	Sessions []FleetSession `json:"sessions"`
}

// fleetHandlers holds dependencies for the fleet endpoint.
// Constructed in Server.registerRoutes alongside chatHandlers.
type fleetHandlers struct {
	registry *dispatch.SessionRegistry
	hub      *ChatHub
}

// newFleetHandlers allocates a fleetHandlers.
func newFleetHandlers(registry *dispatch.SessionRegistry, hub *ChatHub) *fleetHandlers {
	return &fleetHandlers{registry: registry, hub: hub}
}

// handleFleet serves GET /api/fleet.
//
// Scoping logic (mirrors ChatHub.Route per-operator isolation):
//   - For each registry entry, look up the corresponding ChatHub session entry
//     to determine whether it is owned by the caller or shared.
//   - Entries owned by a different operator that are not shared are excluded.
//   - The ChatHub.SessionOwner and the registry may be momentarily inconsistent
//     (the goroutine removes from the registry before closing the hub session).
//     In that case the entry has already exited and would have been removed, so
//     the race window produces a missed entry, not a security leak.
func (fh *fleetHandlers) handleFleet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Resolve the caller's effective operator ID (dual-regime: cert CN or body).
	// On the loopback path the identity is Authenticated=false with RoleAdmin;
	// we use an empty string as the caller ID, which means every session is
	// considered "owned" (loopback = trusted boundary, single operator).
	id := netid.IdentityFrom(r.Context())
	callerOperatorID := ""
	if id.Authenticated {
		callerOperatorID = id.OperatorID
	}
	loopbackPath := !id.Resolved || !id.Authenticated

	snapshot := fh.registry.Snapshot()
	rows := make([]FleetSession, 0, len(snapshot))

	for _, e := range snapshot {
		// Determine visibility using a single atomic hub lock acquisition.
		// e.OperatorID (from the registry) is the authoritative owner; IsShared
		// is a single lock acquisition that returns false for unknown sessions.
		// This avoids the TOCTOU window from the previous SessionOwner+IsShared
		// two-step where a CloseSession racing between the two calls could yield
		// asymmetric visibility.
		shared := fh.hub.IsShared(e.SessionID)
		owned := (e.OperatorID == callerOperatorID) || loopbackPath

		// Exclude foreign unshared sessions.
		if !owned && !shared {
			continue
		}

		rows = append(rows, FleetSession{
			SessionID:   e.SessionID,
			Agent:       e.Agent,
			Runtime:     e.Runtime,
			Status:      string(e.Status),
			StartedAt:   e.StartedAt,
			TaskPreview: e.TaskPreview,
			Attachable:  owned || shared,
			Owned:       owned,
		})
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(FleetResponse{Sessions: rows}); err != nil {
		slog.Error("consoleui: fleet encode error", "err", err)
	}
}
