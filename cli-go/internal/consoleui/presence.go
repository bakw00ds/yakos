// Package consoleui — presence.go
//
// PresenceManager records which browser clients are currently connected to the
// console and publishes join/leave events onto the bus.
//
// # Design
//
// Each WebSocket connection is identified by a random connID.  On connect the
// client sends a "hello" frame with self-asserted identity fields (operatorId,
// displayName).  The server validates operatorId via dispatch.ValidateIdentityField
// (^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$).  Invalid values fall back to "anon".
//
// Color is ALWAYS server-derived from a SHA-256 hash of the operator ID —
// client-supplied color is ignored.  This prevents one operator from
// impersonating another's color.
//
// Presence data published to the bus (presenceBusPayload) intentionally omits
// all secret/credential fields.  The no-secrets property is gated by
// TestPresencePayload_NoSecretFields in presence_test.go.
package consoleui

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"sync"

	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// displayNameRe is the allow-list for display names:
// allows letters, digits, spaces, hyphens, underscores, dots — max 64 chars.
var displayNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._\-]{0,63}$`)

// HelloMessage is the first frame a browser client sends after WS open.
// All fields are self-asserted (attribution-only; never used for auth).
type HelloMessage struct {
	Type        string `json:"type"`        // must be "hello"
	OperatorID  string `json:"operatorId"`  // attribution label; validated
	DisplayName string `json:"displayName"` // human-readable name; validated
	Color       string `json:"color"`       // IGNORED — server derives color
}

// PresenceRecord is the server-resolved record for one connected operator.
// Returned by GET /api/presence and included in welcome frames.
type PresenceRecord struct {
	ConnID      string `json:"connId"`
	OperatorID  string `json:"operator_id"`
	DisplayName string `json:"display_name"`
	Color       string `json:"color"`  // server-derived #rrggbb
	Status      string `json:"status"` // "online" | "offline"
}

// presenceBusPayload is the payload published to wsbus.TopicPresence.
// It intentionally excludes all secret/credential fields.
// No-secrets property is gated by TestPresencePayload_NoSecretFields in presence_test.go.
//
// ConnID is included so the SPA can key the presence map per-connection rather
// than per-operatorId, preventing multiple unauthenticated clients (all keyed
// "anon") from overwriting each other in the browser's presence map.
// ConnID is a random per-connection discriminator and carries no sensitive data.
type presenceBusPayload struct {
	ConnID      string `json:"conn_id"`
	OperatorID  string `json:"operator_id"`
	DisplayName string `json:"display_name"`
	Color       string `json:"color"`
	Status      string `json:"status"` // "online" | "offline"
}

// PresenceManager tracks connected operators and publishes presence events.
// Safe for concurrent use from multiple goroutines (one per WS connection).
type PresenceManager struct {
	mu    sync.RWMutex
	conns map[string]PresenceRecord // connID → record
	bus   *wsbus.Bus                // may be nil (unit tests)
}

// NewPresenceManager constructs a PresenceManager that publishes events onto
// bus.  Pass nil for bus to disable event publishing (unit tests).
func NewPresenceManager(bus *wsbus.Bus) *PresenceManager {
	return &PresenceManager{
		conns: make(map[string]PresenceRecord),
		bus:   bus,
	}
}

// Join records a new connection, derives the server-side presence record, and
// publishes a join event.  Returns the canonical PresenceRecord that should be
// sent back to the client in the welcome frame.
func (pm *PresenceManager) Join(connID string, hello HelloMessage) PresenceRecord {
	rec := pm.buildRecord(connID, hello, "online")

	pm.mu.Lock()
	pm.conns[connID] = rec
	pm.mu.Unlock()

	pm.publish(rec)
	return rec
}

// Leave removes the connection record and publishes a leave event.
// No-op if connID is not known.
func (pm *PresenceManager) Leave(connID string) {
	pm.mu.Lock()
	rec, ok := pm.conns[connID]
	if ok {
		delete(pm.conns, connID)
	}
	pm.mu.Unlock()

	if !ok {
		return
	}

	// Publish leave with status "offline".
	leaveRec := rec
	leaveRec.Status = "offline"
	pm.publish(leaveRec)
}

// Snapshot returns a copy of all currently-online presence records.
// The slice order is not guaranteed.
func (pm *PresenceManager) Snapshot() []PresenceRecord {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	out := make([]PresenceRecord, 0, len(pm.conns))
	for _, rec := range pm.conns {
		out = append(out, rec)
	}
	return out
}

// buildRecord constructs a canonical PresenceRecord from a hello frame,
// validating and sanitising self-asserted fields.
func (pm *PresenceManager) buildRecord(connID string, hello HelloMessage, status string) PresenceRecord {
	operatorID := sanitiseIdentity(hello.OperatorID)
	displayName := sanitiseDisplayName(hello.DisplayName, operatorID)

	return PresenceRecord{
		ConnID:      connID,
		OperatorID:  operatorID,
		DisplayName: displayName,
		Color:       colorFromOperatorID(operatorID),
		Status:      status,
	}
}

// publish sends a presenceBusPayload for rec to the bus.
// No-op when bus is nil.
func (pm *PresenceManager) publish(rec PresenceRecord) {
	if pm.bus == nil {
		return
	}
	payload := presenceBusPayload{
		ConnID:      rec.ConnID,
		OperatorID:  rec.OperatorID,
		DisplayName: rec.DisplayName,
		Color:       rec.Color,
		Status:      rec.Status,
	}
	pm.bus.Publish(wsbus.TopicPresence, payload)
}

// sanitiseIdentity validates an operator ID using dispatch.ValidateIdentityField
// (the same allow-list used by dispatch.Service — single source of truth).
// Returns "anon" for empty or invalid values.
func sanitiseIdentity(v string) string {
	if v == "" || dispatch.ValidateIdentityField("operator_id", v) != nil {
		return "anon"
	}
	return v
}

// sanitiseDisplayName validates a display name.
// Falls back to operatorID (already sanitised) for empty or invalid values.
func sanitiseDisplayName(v, fallback string) string {
	if v == "" || !displayNameRe.MatchString(v) {
		return fallback
	}
	return v
}

// colorFromOperatorID derives a deterministic #rrggbb color from operatorID.
//
// Algorithm: SHA-256 of the UTF-8 operatorID bytes; take bytes [0:3]; OR each
// byte with 0x80 so all three channels have the high bit set (biases toward
// lighter, more readable colors on dark backgrounds).
func colorFromOperatorID(operatorID string) string {
	h := sha256.Sum256([]byte(operatorID))
	r := h[0] | 0x80
	g := h[1] | 0x80
	b := h[2] | 0x80
	return fmt.Sprintf("#%02x%02x%02x", r, g, b)
}

// marshalPresencePayload is a test helper that JSON-encodes a presenceBusPayload.
// Not exported — used only within this package's tests.
func marshalPresencePayload(p presenceBusPayload) []byte {
	b, _ := json.Marshal(p)
	return b
}
