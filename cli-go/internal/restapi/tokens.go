// Package restapi implements the yakOS daemon REST API.
//
// The REST API is a thin layer over pkg/*, served by the daemon on a TCP
// listener (loopback-only by default) alongside the JSON-RPC socket and
// WebSocket bus.  It is aimed at IDE extensions whose runtime cannot easily
// speak JSON-RPC over a Unix socket (e.g. browser-based / WebView extensions).
//
// # Authentication
//
// Two-token model per Phase 2 design §7 (Q7 decision):
//
//   - Read token  (~/.yakos-state/rest-read-token)  — GET endpoints only
//   - Write token (~/.yakos-state/rest-write-token) — GET + POST + PATCH
//
// Tokens are 32-byte random hex strings (64 hex chars), auto-generated on
// first daemon start and persisted at mode 0600.  Both tokens are accepted
// as Bearer tokens in the Authorization header.
//
// # Idempotency
//
// POST endpoints that mutate state carry an Idempotency-Key header convention
// (documented per-handler).  GET endpoints are idempotent by definition.
// PATCH endpoints are idempotent (move to column is a set operation).
//
// # Rate limiting
//
// All endpoints inherit the project's default rate-limit class (loopback-only,
// not externally exposed in this dispatch).
//
// # Stability: experimental
package restapi

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
	// tokenBytes is the number of random bytes per token (32 bytes → 64 hex chars).
	tokenBytes = 32

	readTokenFile  = "rest-read-token"
	writeTokenFile = "rest-write-token"
)

// Tokens holds the two REST API bearer tokens.
type Tokens struct {
	// Read is the read-only token (accepted by GET endpoints).
	Read string
	// Write is the read-write token (accepted by all endpoints).
	Write string
}

// LoadOrGenerateTokens reads the token files from stateDir, generating and
// persisting them if absent.  stateDir is typically ~/.yakos-state.
//
// Each token file is written at mode 0600.  The directory is created at
// 0700 if absent.
//
// Errors:
//   - returns error on directory creation or file I/O failure
func LoadOrGenerateTokens(stateDir string) (*Tokens, error) {
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return nil, fmt.Errorf("restapi: mkdir %s: %w", stateDir, err)
	}

	read, err := loadOrGenToken(filepath.Join(stateDir, readTokenFile))
	if err != nil {
		return nil, fmt.Errorf("restapi: read token: %w", err)
	}

	write, err := loadOrGenToken(filepath.Join(stateDir, writeTokenFile))
	if err != nil {
		return nil, fmt.Errorf("restapi: write token: %w", err)
	}

	return &Tokens{Read: read, Write: write}, nil
}

// RotateTokens generates fresh tokens and persists them, replacing any
// existing token files.
//
// Errors:
//   - returns error on directory creation or file I/O failure
func RotateTokens(stateDir string) (*Tokens, error) {
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return nil, fmt.Errorf("restapi: mkdir %s: %w", stateDir, err)
	}

	read, err := generateToken(filepath.Join(stateDir, readTokenFile))
	if err != nil {
		return nil, fmt.Errorf("restapi: rotate read token: %w", err)
	}

	write, err := generateToken(filepath.Join(stateDir, writeTokenFile))
	if err != nil {
		return nil, fmt.Errorf("restapi: rotate write token: %w", err)
	}

	return &Tokens{Read: read, Write: write}, nil
}

// loadOrGenToken reads the token at path, or generates and writes a new one.
func loadOrGenToken(path string) (string, error) {
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

// generateToken creates a fresh random token, writes it to path at 0600,
// and returns it.
func generateToken(path string) (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	tok := hex.EncodeToString(buf)

	// Atomic temp-rename so partial writes are not visible.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(tok+"\n"), 0600); err != nil { //nolint:gosec
		return "", fmt.Errorf("write temp %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("rename %s → %s: %w", tmp, path, err)
	}
	// Apply Windows NTFS ACL hardening (no-op on non-Windows; 0600 above
	// is the POSIX guard on Unix/macOS).
	if err := winsec.SecureFile(path); err != nil {
		return "", fmt.Errorf("secure token file %s: %w", path, err)
	}
	return tok, nil
}

// isValidToken returns true when tok is a 64-char lowercase hex string.
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
