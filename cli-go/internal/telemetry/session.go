package telemetry

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"sync"
)

var (
	sessionHashOnce sync.Once
	sessionHash     string
)

// SessionHash returns the sha256 hex digest of CLAUDE_SESSION_ID.
//
// If CLAUDE_SESSION_ID is not set, a 32-byte random value is generated once
// per process and hashed, giving a per-run token that does not persist across
// invocations.  The result is memoised — the same value is returned for every
// call within a single process.
//
// The hash is irreversible: no username, hostname, or identity can be derived
// from it.
func SessionHash() string {
	sessionHashOnce.Do(func() {
		var src []byte
		if id := os.Getenv("CLAUDE_SESSION_ID"); id != "" {
			src = []byte(id)
		} else {
			// Fall back to a random per-process nonce so each CLI invocation
			// that lacks a session ID is still distinguishable from others
			// but cannot be linked to an identity.
			nonce := make([]byte, 32)
			if _, err := rand.Read(nonce); err != nil {
				// rand.Read should never fail on any supported platform.
				// Use a fixed fallback that signals the failure without
				// leaking anything.
				sessionHash = "0000000000000000000000000000000000000000000000000000000000000000"
				return
			}
			src = nonce
		}
		sum := sha256.Sum256(src)
		sessionHash = hex.EncodeToString(sum[:])
	})
	return sessionHash
}
