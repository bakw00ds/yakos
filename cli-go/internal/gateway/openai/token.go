package openai

// token.go: the endpoint's own bearer token (K-174). It lives at
// <state dir>/openai-endpoint-token (0600, owner only), separate from the REST
// write token, so a leak from a chat client (Open WebUI's database, a Continue
// config) is revoked by rotating this file alone and grants none of the REST or
// MCP write surfaces. The file handling and the constant-time compare are the
// Anthropic gateway's, shared through gwtoken.

import (
	"github.com/bakw00ds/yakos/internal/gateway/gwtoken"
)

// TokenFile is the token's file name under the state directory.
const TokenFile = "openai-endpoint-token"

var tokenStore = gwtoken.Store{File: TokenFile, Label: "openai endpoint"}

// TokenPath is where the token lives under stateDir.
func TokenPath(stateDir string) string { return tokenStore.Path(stateDir) }

// ReadToken returns the existing token. The file must be a regular file owned by
// the current user with no group or other access; a symlink or a loose mode is
// refused rather than trusted.
func ReadToken(stateDir string) (string, error) { return tokenStore.Read(stateDir) }

// LoadOrCreateToken returns the token, minting one when none usable exists. The
// daemon calls it at start, so the first start creates the file.
func LoadOrCreateToken(stateDir string) (string, error) { return tokenStore.LoadOrCreate(stateDir) }

// RotateToken mints a fresh token and atomically replaces the file. A running
// endpoint reads the file per request, so the old token stops working at once.
func RotateToken(stateDir string) (string, error) { return tokenStore.Rotate(stateDir) }

// FileToken returns a Config.Token func that reads the token file under stateDir
// on every request. A missing, loose, symlinked or malformed file yields "", which
// admits nobody (fail closed).
func FileToken(stateDir string) func() string {
	return func() string {
		tok, err := ReadToken(stateDir)
		if err != nil {
			return ""
		}
		return tok
	}
}
