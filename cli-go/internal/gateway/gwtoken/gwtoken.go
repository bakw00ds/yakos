// Package gwtoken is the one implementation of a gateway bearer token kept as a
// private file in the state directory: 32 random bytes, hex encoded, 0600,
// owner only, replaced atomically, never followed through a symlink, compared in
// constant time. The Anthropic gateway (gateway-token) and the OpenAI-compatible
// endpoint (openai-endpoint-token) each use their own Store, so each is
// independently revocable and neither trusts a token minted for the other.
package gwtoken

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/bakw00ds/yakos/internal/statepath"
	"github.com/bakw00ds/yakos/internal/winsec"
)

const (
	// Bytes is the entropy of a token.
	Bytes = 32
	// MinLen is the shortest configured token a server will compare against. A
	// shorter (or empty) value is a misconfiguration and matches nothing.
	MinLen = 32
)

// ErrNoToken means no usable token exists in the state directory.
var ErrNoToken = errors.New("no usable token file")

// Store names one token file. Label prefixes errors ("anthropic gateway").
type Store struct {
	File  string
	Label string
}

// Path is where the token lives under dir.
func (s Store) Path(dir string) string { return filepath.Join(dir, s.File) }

// Read returns the existing token. The file must be a regular file owned by the
// current user with no group or other access; anything else is refused rather
// than trusted. A missing, unreadable or malformed file is ErrNoToken.
func (s Store) Read(dir string) (string, error) {
	p := s.Path(dir)
	fi, err := os.Lstat(p)
	if err != nil {
		return "", ErrNoToken
	}
	if !fi.Mode().IsRegular() || !statepath.OwnedByCurrentUser(fi) ||
		(runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0) {
		return "", fmt.Errorf("the %s token file is not private to this user; delete it and restart `yakos serve`", s.Label)
	}
	b, err := os.ReadFile(p) //nolint:gosec // path is under the state dir
	if err != nil {
		return "", ErrNoToken
	}
	tok := strings.TrimSpace(string(b))
	if !Valid(tok) {
		return "", ErrNoToken
	}
	return tok, nil
}

// LoadOrCreate returns the token, minting one when none usable exists. A token
// file with loose permissions is treated as exposed and replaced.
func (s Store) LoadOrCreate(dir string) (string, error) {
	if err := statepath.SecureDir(dir); err != nil {
		return "", fmt.Errorf("%s: state dir: %w", s.Label, err)
	}
	if tok, err := s.Read(dir); err == nil {
		return tok, nil
	}
	return s.mint(dir)
}

// Rotate mints a fresh token and atomically replaces the file (0600, same path),
// whatever is there. The previous token stops working at once for any server
// that reads the file per request, and at the next start for one that does not.
func (s Store) Rotate(dir string) (string, error) {
	if err := statepath.SecureDir(dir); err != nil {
		return "", fmt.Errorf("%s: state dir: %w", s.Label, err)
	}
	return s.mint(dir)
}

func (s Store) mint(dir string) (string, error) {
	buf := make([]byte, Bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("%s: generate token: %w", s.Label, err)
	}
	tok := hex.EncodeToString(buf)
	p := s.Path(dir)
	tmp, err := os.CreateTemp(dir, "."+s.File+"-*")
	if err != nil {
		return "", fmt.Errorf("%s: write token: %w", s.Label, err)
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // gone after the rename
	if err := tmp.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		_ = tmp.Close()
		return "", fmt.Errorf("%s: write token: %w", s.Label, err)
	}
	if _, err := tmp.WriteString(tok + "\n"); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("%s: write token: %w", s.Label, err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("%s: write token: %w", s.Label, err)
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return "", fmt.Errorf("%s: write token: %w", s.Label, err)
	}
	if err := winsec.SecureFile(p); err != nil {
		return "", fmt.Errorf("%s: secure token file: %w", s.Label, err)
	}
	return tok, nil
}

// Valid reports whether t has the exact shape of a minted token.
func Valid(t string) bool {
	if len(t) != Bytes*2 {
		return false
	}
	for _, c := range t {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Usable reports whether a configured token is long enough to compare against.
func Usable(want string) bool { return len(want) >= MinLen }

// Match reports whether got equals want. It fails closed when want is unusable
// (unset or shorter than MinLen), so an unconfigured server cannot be opened by
// an empty offered value. Both sides are hashed, so the comparison is constant
// time whatever the length of got.
func Match(want, got string) bool {
	if !Usable(want) {
		return false
	}
	w := sha256.Sum256([]byte(want))
	g := sha256.Sum256([]byte(got))
	return subtle.ConstantTimeCompare(w[:], g[:]) == 1
}
