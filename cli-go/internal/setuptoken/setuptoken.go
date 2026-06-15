// Package setuptoken implements the one-time first-admin setup token
// (ADR-0005, Phase 3c).
//
// # Design
//
// A State holds an in-memory token (32 bytes, base64url-encoded) plus an
// issued-at time.  On a networked start with zero users the daemon calls
// Generate; the caller prints the resulting URL to its own stdout.  The token
// is also written to a 0600 marker file under the stateDir so a daemon restart
// can survive without re-printing (Regenerate restores it from the file or
// generates fresh if the file is absent).
//
// On a successful POST /setup the caller invokes ValidateAndConsume, which
// holds the lock across the entire check-and-zero so only one concurrent
// request can win the token.  A consumed or expired token always returns false.
//
// # Security properties
//
//   - Single-use: ValidateAndConsume zeroes the in-memory token and deletes the
//     file atomically under the mutex — exactly one concurrent winner.
//   - 30-minute expiry: ValidateAndConsume rejects tokens older than TokenTTL.
//   - Zero-users guard: enforced by the caller (/setup handler) via
//     userstore.CreateFirstAdmin which also holds its own lock.
//   - Constant-time compare via dashauth.TokenEqual.
//   - Never logged: callers MUST NOT pass the token to any structured logger.
//   - File at 0600 under 0700 stateDir: reuses userstore/netid file-trust posture.
//   - File-trust on reload: Lstat rejects symlinks and group/world-writable modes
//     (Unix; Windows uses parent-dir ACL trust from the 0700 parent directory).
package setuptoken

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/bakw00ds/yakos/internal/dashauth"
)

// TokenTTL is the maximum lifetime of a setup token.
// A token older than TokenTTL is rejected by Validate.
const TokenTTL = 30 * time.Minute

// tokenBytes is the number of random bytes used to generate the token.
// 32 bytes = 256 bits of entropy.
const tokenBytes = 32

// State holds the in-memory setup token state.
// All methods are safe for concurrent use.
type State struct {
	mu       sync.Mutex
	token    string    // base64url-encoded; "" means consumed or not yet generated
	issuedAt time.Time // zero when no token is active
	filePath string    // <stateDir>/setup-token
	nowFn    func() time.Time
}

// New returns a new State that uses filePath as the marker file.
// filePath should be <stateDir>/setup-token.
// The nowFn is injectable for tests; pass nil to use time.Now.
func New(filePath string, nowFn func() time.Time) *State {
	if nowFn == nil {
		nowFn = time.Now
	}
	return &State{
		filePath: filePath,
		nowFn:    nowFn,
	}
}

// Generate creates a new setup token, writes it to the marker file, and
// returns the raw token string.  If a token already exists in memory (not yet
// consumed), it is replaced.  The caller MUST print the token to daemon stdout
// only — never to a structured/audit log.
//
// The marker file is written atomically (temp+rename) at mode 0600.
// Parent directory is created at 0700 if absent.
func (s *State) Generate() (string, error) {
	tok, err := generate32()
	if err != nil {
		return "", fmt.Errorf("setuptoken: generate: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.nowFn()
	if err := s.writeFileLocked(tok, now); err != nil {
		return "", fmt.Errorf("setuptoken: write marker: %w", err)
	}
	s.token = tok
	s.issuedAt = now
	return tok, nil
}

// LoadOrGenerate loads the token from the marker file if it exists and is not
// yet expired; otherwise it generates a fresh token.  Returns the active token.
//
// This is the recovery path for daemon restarts: the token persisted to disk
// during the original start is reused so operators don't lose the window if
// the daemon restarts within the TTL.
func (s *State) LoadOrGenerate() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tok, issuedAt, err := readFile(s.filePath)
	if err == nil {
		// File exists — check expiry.
		if s.nowFn().Before(issuedAt.Add(TokenTTL)) {
			// Still valid; use it.
			s.token = tok
			s.issuedAt = issuedAt
			return tok, nil
		}
		// Expired; fall through to generate a fresh one.
		// Don't return an error: expiry on restart is expected.
	}
	// No file, unreadable file, or expired — generate fresh.
	newTok, genErr := generate32()
	if genErr != nil {
		return "", fmt.Errorf("setuptoken: generate: %w", genErr)
	}
	now := s.nowFn()
	if writeErr := s.writeFileLocked(newTok, now); writeErr != nil {
		return "", fmt.Errorf("setuptoken: write marker: %w", writeErr)
	}
	s.token = newTok
	s.issuedAt = now
	return newTok, nil
}

// Validate returns true when presented matches the active token and the token
// has not expired.  Uses constant-time comparison.
// Returns false for empty presented, consumed, or expired token.
func (s *State) Validate(presented string) bool {
	if presented == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.token == "" {
		return false // already consumed
	}
	if s.nowFn().After(s.issuedAt.Add(TokenTTL)) {
		return false // expired
	}
	return dashauth.TokenEqual(s.token, presented)
}

// ValidateAndConsume atomically validates presented and, on success, zeroes the
// in-memory token and deletes the marker file — all under a single lock
// acquisition.  This is the correct method to call from a request handler:
// it ensures exactly one concurrent caller wins even if many goroutines present
// the same valid token simultaneously (TOCTOU defence).
//
// Returns true when presented matches the active, unexpired token AND the token
// has not already been consumed.  Returns false in all other cases, including
// when presented is empty, the token is expired, or the token was already used.
//
// Callers MUST call ValidateAndConsume (not Validate + Consume) from mutation
// handlers.  Validate remains available for read-only inspection (e.g. IsActive).
func (s *State) ValidateAndConsume(presented string) bool {
	if presented == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.token == "" {
		return false // already consumed
	}
	if s.nowFn().After(s.issuedAt.Add(TokenTTL)) {
		return false // expired
	}
	if !dashauth.TokenEqual(s.token, presented) {
		return false
	}
	// Token is valid: consume it atomically under the same lock.
	s.token = ""
	s.issuedAt = time.Time{}
	_ = os.Remove(s.filePath)
	return true
}

// Consume invalidates the token: zeroes it in memory and deletes the marker
// file.  Idempotent: safe to call when no token is active.
func (s *State) Consume() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.token = ""
	s.issuedAt = time.Time{}
	_ = os.Remove(s.filePath)
}

// IsActive returns true when a non-consumed, non-expired token is held.
func (s *State) IsActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.token == "" {
		return false
	}
	return !s.nowFn().After(s.issuedAt.Add(TokenTTL))
}

// ---- file helpers -----------------------------------------------------------

// fileTokenSep separates the base64url token from the unix-nano timestamp.
// We use a newline so the file is human-readable.
const fileTokenSep = "\n"

// writeFileLocked writes token and issuedAt to s.filePath atomically.
// Caller must hold s.mu.
func (s *State) writeFileLocked(token string, issuedAt time.Time) error {
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("mkdir %q: %w", dir, err)
	}
	// Format: "<token>\n<unix-nano>\n"
	content := fmt.Sprintf("%s%s%d\n", token, fileTokenSep, issuedAt.UnixNano())
	tmp := s.filePath + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0600); err != nil { //nolint:gosec
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, s.filePath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	// Belt-and-suspenders: enforce 0600 in case umask is 0.
	if err := os.Chmod(s.filePath, 0600); err != nil { //nolint:gosec
		return fmt.Errorf("chmod: %w", err)
	}
	return nil
}

// readFile reads the marker file with file-trust hardening and returns the
// token and issuedAt.
//
// Trust model (mirrors userstore.Open):
//   - Symlinks are rejected: a symlink swap could point the read at an
//     attacker-controlled file containing a known token value.
//   - Non-regular files (devices, pipes) are rejected.
//   - Group- or world-writable permission bits are rejected on Unix
//     (markerPermOK from perm_unix.go / perm_windows.go).
//
// Returns an error when the file is absent, unreadable, untrusted, or malformed.
func readFile(filePath string) (string, time.Time, error) {
	fi, err := os.Lstat(filePath) //nolint:gosec
	if err != nil {
		return "", time.Time{}, err
	}
	// Reject symlinks.
	if fi.Mode()&os.ModeSymlink != 0 {
		return "", time.Time{}, fmt.Errorf("setuptoken: %q is a symlink; refusing to use it", filePath)
	}
	// Reject non-regular files (devices, named pipes, sockets, etc.).
	if !fi.Mode().IsRegular() {
		return "", time.Time{}, fmt.Errorf("setuptoken: %q is not a regular file", filePath)
	}
	// Reject unsafe permissions (Unix only; Windows stub always returns true).
	if !markerPermOK(fi) {
		return "", time.Time{}, fmt.Errorf("setuptoken: %q has unsafe permissions (must not be group- or world-writable)", filePath)
	}

	data, err := os.ReadFile(filePath) //nolint:gosec
	if err != nil {
		return "", time.Time{}, err
	}
	var token string
	var nanos int64
	n, _ := fmt.Sscanf(string(data), "%s\n%d", &token, &nanos)
	if n < 2 || token == "" {
		return "", time.Time{}, fmt.Errorf("setuptoken: malformed marker file")
	}
	return token, time.Unix(0, nanos), nil
}

// generate32 generates 32 random bytes and encodes them as base64url (no padding).
func generate32() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
