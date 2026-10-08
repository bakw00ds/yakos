package anthropic

// token.go: the gateway token. The gateway attaches the operator's
// ANTHROPIC_API_KEY to what it forwards, so a request must first prove it comes
// from a yakOS-launched client. The proof is a 256-bit random token kept at
// <statepath.Dir()>/gateway-token (0600, owner only). `yakos start --routed`
// reads it and hands it to Claude Code as ANTHROPIC_AUTH_TOKEN, which Claude
// Code sends as `Authorization: Bearer <token>`. The token is rotated on every
// gateway start (RotateToken), so a token captured while the daemon was down is
// worthless after the next start; a daemon restart ends running --routed
// sessions (401 until they are relaunched).

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/bakw00ds/yakos/internal/gateway/gwtoken"
)

const (
	tokenFile  = "gateway-token"
	tokenBytes = gwtoken.Bytes
	// TokenHeader carries the gateway token when Authorization is taken by a
	// subscription OAuth bearer (--gateway-passthrough-subscription).
	TokenHeader = "X-Yakos-Gateway-Token"
)

// store is the gateway token file. The file handling (private file, atomic
// replace, symlink refusal, constant-time compare) lives in gwtoken, shared with
// the OpenAI-compatible endpoint's own token (K-174).
var store = gwtoken.Store{File: tokenFile, Label: "anthropic gateway"}

// ErrNoToken means no usable gateway token exists in the state directory.
var ErrNoToken = errors.New("no gateway token (start the gateway with `yakos serve --gateway` first)")

// TokenPath is where the token lives under stateDir.
func TokenPath(stateDir string) string { return filepath.Join(stateDir, tokenFile) }

// ReadToken returns the existing token. The file must be a regular file owned
// by the current user with no group or other access; anything else is refused
// rather than trusted.
func ReadToken(stateDir string) (string, error) {
	tok, err := store.Read(stateDir)
	if errors.Is(err, gwtoken.ErrNoToken) {
		return "", ErrNoToken
	}
	return tok, err
}

// LoadOrCreateToken returns the token, minting one when none usable exists. A
// token file with loose permissions is treated as exposed and replaced.
func LoadOrCreateToken(stateDir string) (string, error) { return store.LoadOrCreate(stateDir) }

// RotateToken mints a fresh token and atomically replaces the file (0600, same
// path), whatever is there. The gateway calls it on every start; the previous
// token stops working the moment the new daemon serves.
func RotateToken(stateDir string) (string, error) { return store.Rotate(stateDir) }

func validToken(t string) bool { return gwtoken.Valid(t) }

// tokenOK reports whether the request carries the gateway token, as
// `Authorization: Bearer <token>` or in TokenHeader. The comparison is constant
// time whatever the length of the offered value, and it fails closed when the
// configured token is unset or shorter than gwtoken.MinLen, whichever path
// serves the request (Handler() included): an empty offered value must never
// equal an empty configured one.
func (s *Server) tokenOK(h http.Header) bool {
	want := s.cfg.GatewayToken
	if !gwtoken.Usable(want) {
		return false
	}
	ok := false
	for _, v := range h.Values(TokenHeader) {
		ok = gwtoken.Match(want, strings.TrimSpace(v)) || ok
	}
	for _, v := range h.Values("Authorization") {
		if len(v) > 7 && strings.EqualFold(v[:7], "bearer ") {
			ok = gwtoken.Match(want, strings.TrimSpace(v[7:])) || ok
		}
	}
	return ok
}

// isGatewayBearer reports whether an Authorization value is the gateway token.
func (s *Server) isGatewayBearer(v string) bool {
	if len(v) <= 7 || !strings.EqualFold(v[:7], "bearer ") {
		return false
	}
	return s.tokenOK(http.Header{"Authorization": {v}})
}
