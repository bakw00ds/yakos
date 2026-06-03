package wsbus

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// defaultTokenFile returns the default path for the WS bearer token.
// It is ~/.yakos-state/ws-token (mode 0600).
func defaultTokenFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), ".yakos-ws-token")
	}
	return filepath.Join(home, ".yakos-state", "ws-token")
}

// LoadOrCreateToken reads the token from path, creating it if absent.
// The token is a 256-bit hex string (64 chars) stored with mode 0600.
// If path is empty, the default path is used.
func LoadOrCreateToken(path string) (string, error) {
	if path == "" {
		path = defaultTokenFile()
	}
	data, err := os.ReadFile(path) //nolint:gosec
	if err == nil {
		tok := strings.TrimSpace(string(data))
		if len(tok) == 64 {
			return tok, nil
		}
		// File exists but token is malformed; regenerate.
	}
	return generateToken(path)
}

// RotateToken generates and stores a new token, overwriting any existing one.
// Returns the new token string.
func RotateToken(path string) (string, error) {
	if path == "" {
		path = defaultTokenFile()
	}
	return generateToken(path)
}

// TokenFilePath returns the default token file path.
func TokenFilePath() string {
	return defaultTokenFile()
}

// generateToken creates a 256-bit random token, writes it to path (mode 0600),
// and returns the hex-encoded value.
func generateToken(path string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("wsbus: generate token: %w", err)
	}
	tok := hex.EncodeToString(raw)

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil { //nolint:gosec
		return "", fmt.Errorf("wsbus: mkdir for token: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(tok+"\n"), 0600); err != nil { //nolint:gosec
		return "", fmt.Errorf("wsbus: write token: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("wsbus: rename token: %w", err)
	}
	return tok, nil
}
