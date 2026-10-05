package runtime

import (
	"errors"
	"strings"
)

// sdk_env.go — the environment contract of the Node Agent-SDK sidecar (K-137).
//
// Anthropic's terms (2026-02-19) allow a Pro or Max subscription's OAuth only in
// Claude Code and claude.ai, not in "any other product, including the Agent
// SDK". The interactive SDK engine (internal/interactive, sidecar.mjs) is the
// Agent SDK. Before K-137 it ran with no API key and let the SDK fall back to
// the operator's claude.ai login. It now starts only with an Anthropic API key
// in its environment, and the environment it gets carries no subscription OAuth
// material. The claude CLI engine stays the interactive path for subscription
// users: it is Claude Code itself.
//
// This file owns both halves so the daemon (interactive.SDKEngine.Start), the
// console error text and `yakos doctor --policy` agree on one rule. The
// allowlist in env.go is deliberately unchanged: the claude CLI adapters
// legitimately run on CLAUDE_CODE_OAUTH_TOKEN, and only the SDK sidecar must
// not.

// SDKAPIKeyEnv names the variable the SDK sidecar requires.
const SDKAPIKeyEnv = "ANTHROPIC_API_KEY"

// ErrSDKAPIKeyRequired is returned when the SDK sidecar would start with no
// API key. It names the variable and the supported alternative, and carries no
// credential material.
var ErrSDKAPIKeyRequired = errors.New(
	"ANTHROPIC_API_KEY is not set: the Agent SDK engine does not run on a claude.ai subscription login " +
		"(Anthropic does not allow that in the Agent SDK). Set an API key in the daemon's environment, " +
		"or use the CLI engine (interactive chat without structured questions), which runs the claude CLI under your own login")

// ErrSDKAPIKeyIsOAuthToken is returned when ANTHROPIC_API_KEY holds a
// subscription OAuth token (sk-ant-oat..., or a refresh token sk-ant-ort...)
// instead of an API key. The Agent SDK must not run on one either.
var ErrSDKAPIKeyIsOAuthToken = errors.New(
	"ANTHROPIC_API_KEY holds a subscription OAuth token, not an API key: Anthropic does not allow those in the Agent SDK. " +
		"Set an API key from the Anthropic Console, or use the CLI engine (interactive chat without structured questions), " +
		"which runs the claude CLI under your own login")

// oauthTokenMarkers are the prefixes of Anthropic subscription OAuth tokens:
// access tokens (sk-ant-oat...) and refresh tokens (sk-ant-ort...). A value
// that contains one anywhere is treated as OAuth material, so a token pasted
// into a Bearer header or a custom-headers variable is caught too.
var oauthTokenMarkers = []string{"sk-ant-oat", "sk-ant-ort"}

// oauthEnvNamePrefix covers CLAUDE_CODE_OAUTH_TOKEN and its siblings
// (refresh token, scopes, client id).
const oauthEnvNamePrefix = "CLAUDE_CODE_OAUTH"

// yakosEnvNamePrefix marks yakOS's own variables. They are never judged by
// their value: the composed agent roster (YAKOS_AGENTS_JSON in the bash
// runtime's hand-off) can mention a token prefix in prose, and the yakOS hooks
// run by the bundled CLI read many YAKOS_ variables (env.go). Nothing reads one
// as a credential. The match is exact-case: a name that only contains YAKOS_,
// or spells it in lowercase, is an ordinary name.
const yakosEnvNamePrefix = "YAKOS_"

func isOAuthTokenValue(v string) bool {
	lower := strings.ToLower(v)
	for _, m := range oauthTokenMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

func isOAuthEnvName(name string) bool {
	return strings.HasPrefix(strings.ToUpper(name), oauthEnvNamePrefix)
}

// carriesOAuthMaterial reports whether the variable is subscription OAuth
// material: a CLAUDE_CODE_OAUTH* name, or a value holding a token marker under
// any name that is not one of yakOS's own.
func carriesOAuthMaterial(name, val string) bool {
	if isOAuthEnvName(name) {
		return true
	}
	if strings.HasPrefix(name, yakosEnvNamePrefix) {
		return false
	}
	return isOAuthTokenValue(val)
}

// checkAPIKeyValue classifies the value of ANTHROPIC_API_KEY.
func checkAPIKeyValue(v string) error {
	v = strings.TrimSpace(v)
	switch {
	case v == "":
		return ErrSDKAPIKeyRequired
	case isOAuthTokenValue(v):
		return ErrSDKAPIKeyIsOAuthToken
	}
	return nil
}

// CheckSDKAPIKey reports whether getenv holds a usable API key for the SDK
// sidecar: nil, ErrSDKAPIKeyRequired or ErrSDKAPIKeyIsOAuthToken. It reads only
// ANTHROPIC_API_KEY and never returns its value.
func CheckSDKAPIKey(getenv func(string) string) error {
	return checkAPIKeyValue(getenv(SDKAPIKeyEnv))
}

// SDKSidecarEnv returns the environment for the Node Agent-SDK sidecar built
// from base (typically os.Environ()), or an error when the sidecar must not
// start.
//
// The environment is the claude allowlist (FilterEnvFor, which keeps the other
// runtimes' credentials out: M4/R7), minus every variable that carries
// subscription OAuth material: any CLAUDE_CODE_OAUTH* name, and any variable
// whose value contains an OAuth token marker, whatever its name except yakOS's
// own YAKOS_ names (see yakosEnvNamePrefix). The strip runs after the
// allowlist, so YAKOS_DISPATCH_ENV_PASSTHROUGH cannot re-add them.
//
// It then requires ANTHROPIC_API_KEY. A missing, blank or OAuth-shaped key
// returns ErrSDKAPIKeyRequired or ErrSDKAPIKeyIsOAuthToken and no environment:
// the caller must not spawn the sidecar.
func SDKSidecarEnv(base []string) ([]string, error) {
	filtered := FilterEnvFor("claude-sdk", base)
	out := make([]string, 0, len(filtered))
	apiKey := ""
	for _, kv := range filtered {
		name, val, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if strings.EqualFold(name, SDKAPIKeyEnv) {
			apiKey = val // judged below, even when the value is dropped as OAuth-shaped
		}
		if carriesOAuthMaterial(name, val) {
			continue
		}
		out = append(out, kv)
	}
	if err := checkAPIKeyValue(apiKey); err != nil {
		return nil, err
	}
	return out, nil
}
