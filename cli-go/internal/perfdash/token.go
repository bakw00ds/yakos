// Package perfdash implements the yakOS performance dashboard.
//
// The dashboard is a read-only HTTP server that serves a single-page web UI
// and a small JSON API for dispatch-log analytics.  It runs on a dedicated
// port (default 127.0.0.1:7895) alongside the JSON-RPC, WebSocket, and REST
// API servers managed by internal/serve.
//
// # Authentication
//
// A separate read-only bearer token is used (~/.yakos-state/perf-token), per
// Phase 2 design decision Q7.  The dashboard URL is the most likely URL to be
// shared with a colleague; reusing the REST or WS token would risk leaking
// write access.  The token is delivered to the browser via the URL fragment
// (#token=<hex>), which is never sent in HTTP requests or server logs.
//
// # Security model
//
// - Loopback-only binding (127.0.0.1) in this dispatch; cross-machine access
//   requires mTLS (Phase 2 parallel scope, not included here).
// - Dashboard is strictly read-only: no state mutation paths exist.
// - Host header validated against an allowlist (DNS-rebinding defence).
//
// # Stability: experimental
package perfdash

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
	// tokenFile is the filename under stateDir for the perf dashboard token.
	tokenFile = "perf-token"

	// tokenBytes is the size of the random token (256 bits = 32 bytes → 64 hex chars).
	tokenBytes = 32
)

// LoadOrCreatePerfToken reads the perf dashboard token from stateDir, creating
// a new 256-bit hex token if the file is absent or corrupted.
//
// stateDir is typically ~/.yakos-state.  The token file is stored at mode 0600;
// the directory is created at 0700 if absent.
//
// NOTE: On Windows with NTFS ACLs the file mode bits are advisory; the NTFS
// ACL (restricted to the creating SID) provides the primary access control.
//
// Errors:
//   - returns error on directory creation or I/O failure
func LoadOrCreatePerfToken(stateDir string) (string, error) {
	if err := os.MkdirAll(stateDir, 0700); err != nil { //nolint:gosec
		return "", fmt.Errorf("perfdash: mkdir %s: %w", stateDir, err)
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

// RotatePerfToken generates and stores a new token, overwriting any existing
// token file.  Returns the new token string.
//
// Errors:
//   - returns error on directory creation or I/O failure
func RotatePerfToken(stateDir string) (string, error) {
	if err := os.MkdirAll(stateDir, 0700); err != nil { //nolint:gosec
		return "", fmt.Errorf("perfdash: mkdir %s: %w", stateDir, err)
	}
	return generateToken(filepath.Join(stateDir, tokenFile))
}

// PerfTokenFilePath returns the path to the perf-token file for stateDir.
func PerfTokenFilePath(stateDir string) string {
	return filepath.Join(stateDir, tokenFile)
}

// defaultStateDir returns the default ~/.yakos-state directory.
func defaultStateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), ".yakos-state")
	}
	return filepath.Join(home, ".yakos-state")
}

// generateToken creates a fresh 256-bit random token, writes it atomically to
// path at mode 0600, and returns the hex-encoded value.
func generateToken(path string) (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("perfdash: generate token: %w", err)
	}
	tok := hex.EncodeToString(buf)

	// Atomic temp-rename so partial writes are never visible.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(tok+"\n"), 0600); err != nil { //nolint:gosec
		return "", fmt.Errorf("perfdash: write token tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("perfdash: rename token: %w", err)
	}
	// Apply Windows NTFS ACL hardening (no-op on non-Windows; 0600 above
	// is the POSIX guard on Unix/macOS).
	if err := winsec.SecureFile(path); err != nil {
		return "", fmt.Errorf("perfdash: secure token file: %w", err)
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
