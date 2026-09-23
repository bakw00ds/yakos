package consoleui

// loopback_owner.go — stable loopback operator identity.
//
// On the loopback (single-user) path, the browser historically minted a new
// random "op-XXXX" token per localStorage scope.  That token changed whenever
// the browser's localStorage was cleared, the port changed, or a new browser
// profile was used — causing "only the session owner can…" / 403 errors on
// reconnect even though the same human was operating the same machine.
//
// The fix: derive a STABLE operator ID for the loopback path from the OS user
// name, persisted to <stateDir>/loopback-operator-id (0600) on first use.
// This file survives daemon restarts and port changes; the same human always
// presents the same ID to the server.
//
// Security:
//   - The stable ID is only used on the loopback path (NetworkedMode=false),
//     where the network boundary itself is the trust boundary.
//   - On the networked path the cert CN / session cookie is the identity source;
//     this file is not consulted.
//   - The file is created 0600 (owner-read-only) so other OS users cannot read
//     or spoof it.  (On loopback, network isolation already limits who can
//     reach the endpoint; file perms are defense-in-depth.)

import (
	"strings"

	"github.com/bakw00ds/yakos/internal/loopbackowner"
)

// isLegacyRandomToken returns true when id looks like a random browser-minted
// loopback token ("op-" followed by 12 lower-case hex chars).  These tokens
// cannot correspond to any authenticated networked identity (authenticated
// identities are cert CNs or usernames, not op-<hex>), so a conversation
// whose recorded owner is such a token may be safely adopted by the
// now-stable loopback identity or a legitimate authenticated user.
func isLegacyRandomToken(id string) bool {
	// Pattern: "op-" + exactly 12 lower-case hex chars.
	// randomHex(12) in app.js produces exactly 12 hex chars.
	if !strings.HasPrefix(id, "op-") {
		return false
	}
	rest := id[len("op-"):]
	if len(rest) != 12 {
		return false
	}
	for _, c := range rest {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// loadOrCreateLoopbackOwnerID returns the stable operator ID for the loopback
// path. Delegates to internal/loopbackowner (round-2 review R3), which is
// also consumed directly by internal/serve's yakos.term.create handler so
// both packages derive the identical ID from the identical
// <stateDir>/loopback-operator-id file.
func loadOrCreateLoopbackOwnerID(stateDir string) string {
	return loopbackowner.LoadOrCreate(stateDir)
}
