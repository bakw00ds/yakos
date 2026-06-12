// Package consoleui implements the yakOS unified console — a single-origin
// tabbed web UI that mounts the kanban, metricsdash, and perfdash dashboards
// plus the WebSocket event stream behind one loopback bearer token.
//
// # Authentication
//
// One bearer token guards the entire console (127.0.0.1:7890 default).
// The token is stored at ~/.yakos-state/console-token (mode 0600) and
// delivered to the browser via the URL fragment (#token=...) which is
// never sent in HTTP requests or server logs.
//
// # Security model
//
//   - Loopback-only binding (127.0.0.1).
//   - RequireLocalHost + RequireToken applied ONCE at the edge, before any
//     StripPrefix routing.  Inner per-dashboard Host/token middleware is NOT
//     applied (Handler() paths skip it).
//   - Token uses crypto/rand 32 bytes, atomic temp-rename, mode 0600.
//
// # Stability: experimental
package consoleui

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bakw00ds/yakos/internal/winsec"
)

const (
	// tokenFile is the filename under stateDir for the console token.
	tokenFile = "console-token"

	// tokenBytes is the size of the random token (256 bits = 32 bytes → 64 hex chars).
	tokenBytes = 32
)

// LoadOrCreateToken reads the console token from stateDir, creating a new
// 256-bit hex token if the file is absent or corrupted.
//
// stateDir is typically ~/.yakos-state.  The token file is stored at mode
// 0600; the directory is created at 0700 if absent.
func LoadOrCreateToken(stateDir string) (string, error) {
	if err := os.MkdirAll(stateDir, 0700); err != nil { //nolint:gosec
		return "", fmt.Errorf("consoleui: mkdir %s: %w", stateDir, err)
	}
	path := filepath.Join(stateDir, tokenFile)
	data, err := os.ReadFile(path) //nolint:gosec
	if err == nil {
		tok := strings.TrimSpace(string(data))
		if isValidToken(tok) {
			return tok, nil
		}
		// Corrupted / empty — regenerate.
	}
	return generateToken(path)
}

// RotateToken generates and stores a new token, overwriting any existing one.
// Returns the new token string.
func RotateToken(stateDir string) (string, error) {
	if err := os.MkdirAll(stateDir, 0700); err != nil { //nolint:gosec
		return "", fmt.Errorf("consoleui: mkdir %s: %w", stateDir, err)
	}
	return generateToken(filepath.Join(stateDir, tokenFile))
}

// TokenFilePath returns the path to the console token file for stateDir.
func TokenFilePath(stateDir string) string {
	return filepath.Join(stateDir, tokenFile)
}

// generateToken creates a fresh 256-bit random token, writes it atomically to
// path at mode 0600, and returns the hex-encoded value.
func generateToken(path string) (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("consoleui: generate token: %w", err)
	}
	tok := hex.EncodeToString(buf)

	// Atomic temp-rename so partial writes are never visible.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(tok+"\n"), 0600); err != nil { //nolint:gosec
		return "", fmt.Errorf("consoleui: write token tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("consoleui: rename token: %w", err)
	}
	// Apply Windows NTFS ACL hardening (no-op on non-Windows).
	if err := winsec.SecureFile(path); err != nil {
		return "", fmt.Errorf("consoleui: secure token file: %w", err)
	}
	return tok, nil
}

// isValidToken returns true when tok is a 64-character lowercase hex string.
func isValidToken(tok string) bool {
	if len(tok) != tokenBytes*2 {
		return false
	}
	for _, c := range tok {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
