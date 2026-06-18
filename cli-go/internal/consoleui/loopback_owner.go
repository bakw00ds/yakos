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
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
)

// loopbackOwnerIDFile is the base filename within stateDir.
const loopbackOwnerIDFile = "loopback-operator-id"

// loopbackOwnerIDRe validates a stored loopback operator ID.
// Must start with a letter/digit, allowed chars match dispatch identity field.
var loopbackOwnerIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:\-]{0,127}$`)

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
// path.  It reads <stateDir>/loopback-operator-id if present and valid;
// otherwise derives one from the OS user name (falling back to a random
// hex value) and writes it to that file.
//
// On any error the function returns a best-effort ID derived from the OS user
// and logs a warning.  It never returns "".
func loadOrCreateLoopbackOwnerID(stateDir string) string {
	if stateDir == "" {
		return deriveLoopbackOwnerID()
	}

	path := filepath.Join(stateDir, loopbackOwnerIDFile)

	// Try to read an existing valid ID.
	if data, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(data))
		if loopbackOwnerIDRe.MatchString(id) {
			return id
		}
		// File exists but contains invalid data; overwrite below.
		slog.Warn("consoleui: loopback-operator-id file contains invalid data; regenerating",
			"path", path)
	}

	// Create or overwrite with a fresh ID.
	id := deriveLoopbackOwnerID()
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		slog.Warn("consoleui: could not create stateDir for loopback-operator-id",
			"stateDir", stateDir, "err", err)
		return id
	}
	// Write with O_CREATE|O_WRONLY|O_TRUNC, mode 0600 (owner-read-only).
	if err := os.WriteFile(path, []byte(id+"\n"), 0600); err != nil {
		slog.Warn("consoleui: could not persist loopback-operator-id",
			"path", path, "err", err)
	}
	return id
}

// deriveLoopbackOwnerID builds a stable-ish ID from the OS username, falling
// back to random hex if the username is unavailable or invalid.
func deriveLoopbackOwnerID() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		// Sanitize: keep only alphanum, dot, underscore, hyphen.  Replace
		// everything else with '-'.  Prefix with "lbop-" so it is clearly
		// distinct from both random op-XXXX tokens and cert CNs.
		raw := u.Username
		var b strings.Builder
		b.WriteString("lbop-")
		for _, c := range raw {
			if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
				(c >= '0' && c <= '9') || c == '.' || c == '_' || c == '-' {
				b.WriteRune(c)
			} else {
				b.WriteRune('-')
			}
		}
		id := b.String()
		if loopbackOwnerIDRe.MatchString(id) {
			return id
		}
	}
	// Fallback: random hex prefixed so it is valid and starts with a letter.
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("lbop-%x", os.Getpid())
	}
	return "lbop-" + hex.EncodeToString(buf)
}
