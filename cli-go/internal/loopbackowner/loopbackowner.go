// Package loopbackowner derives a stable, file-persisted operator identity
// for the loopback (single-user, local-trust) path.
//
// Extracted from internal/consoleui's original loopback_owner.go (round-2
// review R3) so a second consumer — internal/serve's yakos.term.create RPC
// handler (term_methods.go), reached only over the mode-0600, owner-UID
// Unix-domain JSON-RPC socket — can derive the SAME authoritative operator
// ID that consoleui's HTTP/WS server stamps on loopback requests
// (consoleui/loopback_owner.go still wraps this package for its own
// existing callers/tests; both read/write the identical
// <stateDir>/loopback-operator-id file, so they always agree).
//
// This lets RegisterExternalSession record a real owner at session-creation
// time instead of accepting one from RPC params (which a hostile caller on
// the same machine — anyone who can reach the loopback HTTP surface, not
// just the socket owner — could otherwise forge).
package loopbackowner

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

// idFile is the base filename within stateDir.
const idFile = "loopback-operator-id"

// idRe validates a stored loopback operator ID. Must start with a
// letter/digit; allowed chars match the dispatch identity field pattern.
var idRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:\-]{0,127}$`)

// LoadOrCreate returns the stable operator ID for the loopback path. It
// reads <stateDir>/loopback-operator-id if present and valid; otherwise
// derives one from the OS user name (falling back to random hex) and writes
// it to that file.
//
// On any error it returns a best-effort ID derived from the OS user and
// logs a warning. It never returns "".
func LoadOrCreate(stateDir string) string {
	if stateDir == "" {
		return Derive()
	}

	path := filepath.Join(stateDir, idFile)

	if data, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(data))
		if idRe.MatchString(id) {
			return id
		}
		slog.Warn("loopbackowner: loopback-operator-id file contains invalid data; regenerating",
			"path", path)
	}

	id := Derive()
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		slog.Warn("loopbackowner: could not create stateDir for loopback-operator-id",
			"stateDir", stateDir, "err", err)
		return id
	}
	// Write with O_CREATE|O_WRONLY|O_TRUNC, mode 0600 (owner-read-only).
	if err := os.WriteFile(path, []byte(id+"\n"), 0600); err != nil {
		slog.Warn("loopbackowner: could not persist loopback-operator-id",
			"path", path, "err", err)
	}
	return id
}

// Derive builds a stable-ish ID from the OS username, falling back to
// random hex if the username is unavailable or invalid.
func Derive() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
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
		if idRe.MatchString(id) {
			return id
		}
	}
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("lbop-%x", os.Getpid())
	}
	return "lbop-" + hex.EncodeToString(buf)
}
