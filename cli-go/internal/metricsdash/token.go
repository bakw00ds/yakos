// Package serve implements the `yakos metrics serve` HTTP dashboard.
//
// The dashboard is a read-only HTTP server that serves a single-page web UI
// and a small JSON API backed by <project>/.yakos/metrics/history.ndjson.
// It runs on a loopback-only address (127.0.0.1:7896 by default) and enforces
// Bearer-token authentication on every /api/* endpoint.
//
// # Authentication
//
// A separate read-only bearer token is used (~/.yakos-state/metrics-token).
// The token is delivered to the browser via the URL fragment (#token=<hex>),
// which is never sent in HTTP requests or server logs.
//
// # Security model
//
//   - Loopback-only binding (127.0.0.1) enforced at Listen time; non-loopback
//     addresses are refused with a loud error.
//   - Dashboard is strictly read-only: no endpoint may write to history.ndjson
//     or any other metrics state.
//
// # Stability: experimental (Phase-3)
package metricsdash

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
	// metricsTokenFile is the filename under stateDir for the metrics dashboard token.
	metricsTokenFile = "metrics-token"

	// tokenBytes is the size of the random token (256 bits = 32 bytes → 64 hex chars).
	tokenBytes = 32
)

// LoadOrCreateMetricsToken reads the metrics dashboard token from stateDir,
// creating a new 256-bit hex token if the file is absent or corrupted.
//
// stateDir is typically ~/.yakos-state.  The token file is stored at mode 0600;
// the directory is created at 0700 if absent.
func LoadOrCreateMetricsToken(stateDir string) (string, error) {
	if err := os.MkdirAll(stateDir, 0700); err != nil { //nolint:gosec
		return "", fmt.Errorf("metrics serve: mkdir %s: %w", stateDir, err)
	}
	path := filepath.Join(stateDir, metricsTokenFile)
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

// MetricsTokenFilePath returns the path to the metrics-token file for stateDir.
func MetricsTokenFilePath(stateDir string) string {
	return filepath.Join(stateDir, metricsTokenFile)
}

// generateToken creates a fresh 256-bit random token, writes it atomically to
// path at mode 0600, and returns the hex-encoded value.
func generateToken(path string) (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("metrics serve: generate token: %w", err)
	}
	tok := hex.EncodeToString(buf)

	// Atomic temp-rename so partial writes are never visible.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(tok+"\n"), 0600); err != nil { //nolint:gosec
		return "", fmt.Errorf("metrics serve: write token tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("metrics serve: rename token: %w", err)
	}
	// Apply Windows NTFS ACL hardening (no-op on non-Windows; 0600 above
	// is the POSIX guard on Unix/macOS).
	if err := winsec.SecureFile(path); err != nil {
		return "", fmt.Errorf("metrics serve: secure token file: %w", err)
	}
	return tok, nil
}

// isValidToken returns true when tok is a 64-character lowercase hex string.
func isValidToken(tok string) bool {
	if len(tok) != tokenBytes*2 {
		return false
	}
	for _, c := range tok {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
