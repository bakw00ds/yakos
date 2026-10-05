package runtime

// sdk_env_test.go — K-137: the Node Agent-SDK sidecar starts only with an
// Anthropic API key and never sees subscription OAuth material.
//
// Anthropic's terms (2026-02-19) allow Pro/Max OAuth only in Claude Code and
// claude.ai, not in the Agent SDK. Before K-137 the sidecar ran with whatever
// the allowlisted environment held and, with no key at all, let the SDK fall
// back to the operator's claude.ai login.

import (
	"errors"
	"strings"
	"testing"
)

// oauthSentinel is a made-up OAuth-shaped value. Nothing in an error text or a
// returned environment may contain the SECRET part.
const (
	oauthPrefix   = "sk-ant-oat01-"
	oauthSecret   = "SECRETSENTINEL0123456789"
	oauthToken    = oauthPrefix + oauthSecret
	refreshToken  = "sk-ant-ort01-" + oauthSecret
	fakeAPIKey    = "sk-ant-api03-fakekeyforunittests"
	otherProvider = "sk-openai-must-not-leak"
)

func envHas(env []string, key string) bool { return hasEnvKey(env, key) }

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return strings.TrimPrefix(kv, prefix)
		}
	}
	return ""
}

func TestSDKSidecarEnv_RequiresAnAPIKey(t *testing.T) {
	cases := []struct {
		name string
		base []string
	}{
		{"unset", []string{"PATH=/usr/bin", "HOME=/home/u"}},
		{"empty", []string{"PATH=/usr/bin", "ANTHROPIC_API_KEY="}},
		{"blank", []string{"PATH=/usr/bin", "ANTHROPIC_API_KEY=   "}},
		{"only a subscription token", []string{"PATH=/usr/bin", "CLAUDE_CODE_OAUTH_TOKEN=" + oauthToken}},
		{"only a gateway token", []string{"PATH=/usr/bin", "ANTHROPIC_AUTH_TOKEN=gateway-token"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, err := SDKSidecarEnv(tc.base)
			if !errors.Is(err, ErrSDKAPIKeyRequired) {
				t.Fatalf("err = %v, want ErrSDKAPIKeyRequired", err)
			}
			if env != nil {
				t.Errorf("a refused start must not hand back an environment, got %d entries", len(env))
			}
		})
	}
}

func TestSDKSidecarEnv_RefusalNamesTheVariableAndTheCLIEngine(t *testing.T) {
	_, err := SDKSidecarEnv([]string{"PATH=/usr/bin"})
	if err == nil {
		t.Fatal("want an error")
	}
	msg := err.Error()
	for _, want := range []string{"ANTHROPIC_API_KEY", "CLI engine", "login"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal %q must mention %q", msg, want)
		}
	}
}

func TestSDKSidecarEnv_RefusesAnOAuthTokenInTheAPIKeySlot(t *testing.T) {
	for _, token := range []string{oauthToken, refreshToken, "  " + strings.ToUpper(oauthToken), "Bearer " + oauthToken} {
		_, err := SDKSidecarEnv([]string{"PATH=/usr/bin", "ANTHROPIC_API_KEY=" + token})
		if !errors.Is(err, ErrSDKAPIKeyIsOAuthToken) {
			t.Errorf("ANTHROPIC_API_KEY=%q: err = %v, want ErrSDKAPIKeyIsOAuthToken", token[:14]+"...", err)
			continue
		}
		if strings.Contains(err.Error(), oauthSecret) || strings.Contains(strings.ToUpper(err.Error()), oauthSecret) {
			t.Errorf("the refusal echoed token material: %q", err.Error())
		}
		if !strings.Contains(err.Error(), "CLI engine") {
			t.Errorf("the refusal must point at the CLI engine: %q", err.Error())
		}
	}
}

func TestSDKSidecarEnv_PassesTheKeyAndTheAllowlistedEnvironment(t *testing.T) {
	base := []string{
		"PATH=/usr/bin", "HOME=/home/u", "ANTHROPIC_API_KEY=" + fakeAPIKey,
		"ANTHROPIC_BASE_URL=https://gateway.example", "OPENAI_API_KEY=" + otherProvider,
		"GEMINI_API_KEY=gem", "CODEX_HOME=/c",
	}
	env, err := SDKSidecarEnv(base)
	if err != nil {
		t.Fatalf("a key is set, want a start: %v", err)
	}
	if got := envValue(env, "ANTHROPIC_API_KEY"); got != fakeAPIKey {
		t.Errorf("the sidecar must receive the key unchanged")
	}
	for _, want := range []string{"PATH", "HOME", "ANTHROPIC_BASE_URL"} {
		if !envHas(env, want) {
			t.Errorf("%s was dropped; the allowlist keeps it", want)
		}
	}
	for _, leaked := range []string{"OPENAI_API_KEY", "GEMINI_API_KEY", "CODEX_HOME"} {
		if envHas(env, leaked) {
			t.Errorf("%s leaked into the sidecar environment", leaked)
		}
	}
}

func TestSDKSidecarEnv_NeverForwardsSubscriptionOAuthMaterial(t *testing.T) {
	base := []string{
		"PATH=/usr/bin",
		"ANTHROPIC_API_KEY=" + fakeAPIKey,
		"CLAUDE_CODE_OAUTH_TOKEN=" + oauthToken,
		"CLAUDE_CODE_OAUTH_REFRESH_TOKEN=" + refreshToken,
		"CLAUDE_CODE_OAUTH_SCOPES=user:inference",
		"ANTHROPIC_AUTH_TOKEN=" + oauthToken, // an OAuth token mis-filed as a gateway token
		"ANTHROPIC_CUSTOM_HEADERS=Authorization: Bearer " + oauthToken,
		"ANTHROPIC_BASE_URL=https://gateway.example",
	}
	env, err := SDKSidecarEnv(base)
	if err != nil {
		t.Fatalf("the API key is set: %v", err)
	}
	for _, name := range []string{
		"CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_OAUTH_REFRESH_TOKEN", "CLAUDE_CODE_OAUTH_SCOPES",
		"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_CUSTOM_HEADERS",
	} {
		if envHas(env, name) {
			t.Errorf("%s reached the Agent SDK sidecar", name)
		}
	}
	if strings.Contains(strings.Join(env, "\n"), oauthSecret) {
		t.Error("OAuth token material is still present in the sidecar environment")
	}
	if !envHas(env, "ANTHROPIC_API_KEY") || !envHas(env, "ANTHROPIC_BASE_URL") {
		t.Error("the API key and the base URL must survive the OAuth strip")
	}
}

func TestSDKSidecarEnv_KeepsANonOAuthGatewayToken(t *testing.T) {
	env, err := SDKSidecarEnv([]string{
		"PATH=/usr/bin", "ANTHROPIC_API_KEY=" + fakeAPIKey,
		"ANTHROPIC_AUTH_TOKEN=gateway-bearer", "ANTHROPIC_BASE_URL=https://gateway.example",
	})
	if err != nil {
		t.Fatal(err)
	}
	if envValue(env, "ANTHROPIC_AUTH_TOKEN") != "gateway-bearer" {
		t.Error("a gateway bearer that is not an OAuth token must still reach the sidecar")
	}
}

func TestSDKSidecarEnv_TheEscapeHatchCannotReAddOAuthMaterial(t *testing.T) {
	t.Setenv(passthroughEnvVar, "CLAUDE_CODE_OAUTH_TOKEN,SOME_OAT_VAR")
	env, err := SDKSidecarEnv([]string{
		"PATH=/usr/bin", "ANTHROPIC_API_KEY=" + fakeAPIKey,
		"CLAUDE_CODE_OAUTH_TOKEN=" + oauthToken, "SOME_OAT_VAR=" + oauthToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	if envHas(env, "CLAUDE_CODE_OAUTH_TOKEN") || envHas(env, "SOME_OAT_VAR") {
		t.Error("YAKOS_DISPATCH_ENV_PASSTHROUGH re-added OAuth material to the Agent SDK sidecar")
	}
}

func TestSDKSidecarEnv_MatchesNamesLikeTheAllowlistDoes(t *testing.T) {
	// Windows spells variables in any case; the allowlist is case-insensitive,
	// so the gate and the strip must be too.
	env, err := SDKSidecarEnv([]string{"Path=C:\\Windows", "Anthropic_Api_Key=" + fakeAPIKey, "Claude_Code_Oauth_Token=" + oauthToken})
	if err != nil {
		t.Fatalf("a mixed-case API key name still counts: %v", err)
	}
	if envHas(env, "Claude_Code_Oauth_Token") {
		t.Error("a mixed-case OAuth variable name slipped past the strip")
	}
}

func TestCheckSDKAPIKey_ReadsThroughTheGetter(t *testing.T) {
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if err := CheckSDKAPIKey(get(map[string]string{"ANTHROPIC_API_KEY": fakeAPIKey})); err != nil {
		t.Errorf("a key is set: %v", err)
	}
	if err := CheckSDKAPIKey(get(nil)); !errors.Is(err, ErrSDKAPIKeyRequired) {
		t.Errorf("no key: err = %v, want ErrSDKAPIKeyRequired", err)
	}
	if err := CheckSDKAPIKey(get(map[string]string{"ANTHROPIC_API_KEY": oauthToken})); !errors.Is(err, ErrSDKAPIKeyIsOAuthToken) {
		t.Errorf("OAuth-shaped key: err = %v, want ErrSDKAPIKeyIsOAuthToken", err)
	}
}

// TestFilterEnvFor_StillForwardsClaudeVariablesToTheClaudeCLI pins the other
// half of the K-137 decision: the allowlist itself is unchanged, because the
// claude CLI legitimately runs on CLAUDE_CODE_OAUTH_TOKEN. Only the Agent SDK
// sidecar strips it.
func TestFilterEnvFor_StillForwardsClaudeVariablesToTheClaudeCLI(t *testing.T) {
	env := FilterEnvFor("claude", []string{"PATH=/usr/bin", "CLAUDE_CODE_OAUTH_TOKEN=" + oauthToken})
	if !envHas(env, "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Error("the claude CLI adapter lost CLAUDE_CODE_OAUTH_TOKEN; only the SDK sidecar may strip it")
	}
}
