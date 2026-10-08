package anthropic

// token.go: the gateway token. The gateway attaches the operator's
// ANTHROPIC_API_KEY to what it forwards, so a request must first prove it comes
// from a yakOS-launched client. The proof is a 256-bit random token kept at
// <statepath.Dir()>/gateway-token (0600, owner only). `yakos start --routed`
// reads it and hands it to Claude Code as ANTHROPIC_AUTH_TOKEN, which Claude
// Code sends as `Authorization: Bearer <token>`. To rotate it, delete the file
// and restart `yakos serve`.

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/bakw00ds/yakos/internal/statepath"
	"github.com/bakw00ds/yakos/internal/winsec"
)

const (
	tokenFile  = "gateway-token"
	tokenBytes = 32
	// TokenHeader carries the gateway token when Authorization is taken by a
	// subscription OAuth bearer (--gateway-passthrough-subscription).
	TokenHeader = "X-Yakos-Gateway-Token"
)

// ErrNoToken means no usable gateway token exists in the state directory.
var ErrNoToken = errors.New("no gateway token (start the gateway with `yakos serve --gateway` first)")

// TokenPath is where the token lives under stateDir.
func TokenPath(stateDir string) string { return filepath.Join(stateDir, tokenFile) }

// ReadToken returns the existing token. The file must be a regular file owned
// by the current user with no group or other access; anything else is refused
// rather than trusted.
func ReadToken(stateDir string) (string, error) {
	p := TokenPath(stateDir)
	fi, err := os.Lstat(p)
	if err != nil {
		return "", ErrNoToken
	}
	if !fi.Mode().IsRegular() || !statepath.OwnedByCurrentUser(fi) ||
		(runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0) {
		return "", errors.New("the gateway token file is not private to this user; delete it and restart `yakos serve --gateway`")
	}
	b, err := os.ReadFile(p) //nolint:gosec // path is under the state dir
	if err != nil {
		return "", ErrNoToken
	}
	tok := strings.TrimSpace(string(b))
	if !validToken(tok) {
		return "", ErrNoToken
	}
	return tok, nil
}

// LoadOrCreateToken returns the token, minting one when none usable exists. A
// token file with loose permissions is treated as exposed and replaced.
func LoadOrCreateToken(stateDir string) (string, error) {
	if err := statepath.SecureDir(stateDir); err != nil {
		return "", fmt.Errorf("anthropic gateway: state dir: %w", err)
	}
	if tok, err := ReadToken(stateDir); err == nil {
		return tok, nil
	}
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("anthropic gateway: generate token: %w", err)
	}
	tok := hex.EncodeToString(buf)
	p := TokenPath(stateDir)
	tmp, err := os.CreateTemp(stateDir, ".gateway-token-*")
	if err != nil {
		return "", fmt.Errorf("anthropic gateway: write token: %w", err)
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // gone after the rename
	if err := tmp.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		_ = tmp.Close()
		return "", fmt.Errorf("anthropic gateway: write token: %w", err)
	}
	if _, err := tmp.WriteString(tok + "\n"); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("anthropic gateway: write token: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("anthropic gateway: write token: %w", err)
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return "", fmt.Errorf("anthropic gateway: write token: %w", err)
	}
	if err := winsec.SecureFile(p); err != nil {
		return "", fmt.Errorf("anthropic gateway: secure token file: %w", err)
	}
	return tok, nil
}

func validToken(t string) bool {
	if len(t) != tokenBytes*2 {
		return false
	}
	for _, c := range t {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// tokenOK reports whether the request carries the gateway token, as
// `Authorization: Bearer <token>` or in TokenHeader. Both sides are hashed so
// the comparison is constant-time whatever the length of the offered value.
func (s *Server) tokenOK(h http.Header) bool {
	want := sha256.Sum256([]byte(s.cfg.GatewayToken))
	ok := 0
	check := func(got string) {
		g := sha256.Sum256([]byte(got))
		ok |= subtle.ConstantTimeCompare(want[:], g[:])
	}
	for _, v := range h.Values(TokenHeader) {
		check(strings.TrimSpace(v))
	}
	for _, v := range h.Values("Authorization") {
		if len(v) > 7 && strings.EqualFold(v[:7], "bearer ") {
			check(strings.TrimSpace(v[7:]))
		}
	}
	return ok == 1
}

// isGatewayBearer reports whether an Authorization value is the gateway token.
func (s *Server) isGatewayBearer(v string) bool {
	if len(v) <= 7 || !strings.EqualFold(v[:7], "bearer ") {
		return false
	}
	return s.tokenOK(http.Header{"Authorization": {v}})
}
