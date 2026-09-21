package runtime

// env_test.go — tests for the M4 fix: dispatched agent subprocesses must not
// inherit the full parent environment (security-review-2026-09-14.md M4).
// Before this fix, a codex or agy dispatch handed ANTHROPIC_API_KEY to a
// third-party binary, and vice versa.

import (
	"testing"
)

func hasEnvKey(env []string, key string) bool {
	prefix := key + "="
	for _, kv := range env {
		if len(kv) >= len(prefix) && kv[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// TestBuildEnv_Claude_DropsOtherProvidersCredentials is the core M4
// regression for the claude adapter: it must keep its own credential
// (ANTHROPIC_API_KEY) but drop codex's and agy's.
func TestBuildEnv_Claude_DropsOtherProvidersCredentials(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-secret")
	t.Setenv("OPENAI_API_KEY", "sk-openai-secret")
	t.Setenv("CODEX_HOME", "/some/codex/home")
	t.Setenv("ANTIGRAVITY_API_KEY", "agy-secret")
	t.Setenv("GEMINI_API_KEY", "gemini-secret")

	env := buildEnv(DispatchRequest{})

	if !hasEnvKey(env, "ANTHROPIC_API_KEY") {
		t.Error("buildEnv (claude): ANTHROPIC_API_KEY missing; claude's own credential must be forwarded")
	}
	for _, leaked := range []string{"OPENAI_API_KEY", "CODEX_HOME", "ANTIGRAVITY_API_KEY", "GEMINI_API_KEY"} {
		if hasEnvKey(env, leaked) {
			t.Errorf("buildEnv (claude): %s leaked into claude's subprocess env (M4 regression)", leaked)
		}
	}
}

// TestBuildEnvCodex_DropsOtherProvidersCredentials mirrors the claude test
// for the codex adapter.
func TestBuildEnvCodex_DropsOtherProvidersCredentials(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-secret")
	t.Setenv("OPENAI_API_KEY", "sk-openai-secret")
	t.Setenv("CODEX_HOME", "/some/codex/home")
	t.Setenv("ANTIGRAVITY_API_KEY", "agy-secret")
	t.Setenv("GEMINI_API_KEY", "gemini-secret")

	env := buildEnvCodex(DispatchRequest{})

	if !hasEnvKey(env, "OPENAI_API_KEY") || !hasEnvKey(env, "CODEX_HOME") {
		t.Error("buildEnvCodex: OPENAI_API_KEY/CODEX_HOME missing; codex's own credentials must be forwarded")
	}
	for _, leaked := range []string{"ANTHROPIC_API_KEY", "ANTIGRAVITY_API_KEY", "GEMINI_API_KEY"} {
		if hasEnvKey(env, leaked) {
			t.Errorf("buildEnvCodex: %s leaked into codex's subprocess env (M4 regression)", leaked)
		}
	}
}

// TestBuildEnvAgy_DropsOtherProvidersCredentials mirrors the claude test for
// the agy adapter.
func TestBuildEnvAgy_DropsOtherProvidersCredentials(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-secret")
	t.Setenv("OPENAI_API_KEY", "sk-openai-secret")
	t.Setenv("CODEX_HOME", "/some/codex/home")
	t.Setenv("ANTIGRAVITY_API_KEY", "agy-secret")
	t.Setenv("GEMINI_API_KEY", "gemini-secret")

	env := buildEnvAgy(DispatchRequest{})

	if !hasEnvKey(env, "ANTIGRAVITY_API_KEY") || !hasEnvKey(env, "GEMINI_API_KEY") {
		t.Error("buildEnvAgy: ANTIGRAVITY_API_KEY/GEMINI_API_KEY missing; agy's own credentials must be forwarded")
	}
	for _, leaked := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "CODEX_HOME"} {
		if hasEnvKey(env, leaked) {
			t.Errorf("buildEnvAgy: %s leaked into agy's subprocess env (M4 regression)", leaked)
		}
	}
}

// TestFilterEnv_KeepsYakosPrefixedVars verifies that YAKOS_* variables are
// never dropped: lib/hooks/*.sh runs as a child of the claude/codex/agy CLI
// and depends on many of them (YAKOS_ROOT, YAKOS_LIB, YAKOS_RUNTIME,
// YAKOS_DISPATCH_LOG, YAKOS_GATE_*, etc). Filtering the parent env for M4
// must not silently break the hook system.
func TestFilterEnv_KeepsYakosPrefixedVars(t *testing.T) {
	base := []string{
		"YAKOS_ROOT=/home/op/yakos",
		"YAKOS_GATE_VERBOSE=1",
		"SOME_RANDOM_SECRET=leak-me-not",
	}
	got := filterEnv(base, runtimeEnvSpec{})
	if !hasEnvKey(got, "YAKOS_ROOT") {
		t.Error("filterEnv: YAKOS_ROOT was dropped; this breaks lib/hooks/*.sh")
	}
	if !hasEnvKey(got, "YAKOS_GATE_VERBOSE") {
		t.Error("filterEnv: YAKOS_GATE_VERBOSE was dropped; this breaks lib/hooks/*.sh")
	}
	if hasEnvKey(got, "SOME_RANDOM_SECRET") {
		t.Error("filterEnv: an arbitrary, non-allowlisted var was NOT dropped")
	}
}

// TestFilterEnv_KeepsGenericProcessVars verifies the generic vars every CLI
// needs (PATH, HOME, LANG, TMPDIR, TERM, and the LC_*/XDG_* families) are
// preserved.
func TestFilterEnv_KeepsGenericProcessVars(t *testing.T) {
	base := []string{
		"PATH=/usr/bin:/bin",
		"HOME=/home/op",
		"LANG=en_US.UTF-8",
		"LC_ALL=en_US.UTF-8",
		"TMPDIR=/tmp",
		"TERM=xterm-256color",
		"XDG_CONFIG_HOME=/home/op/.config",
		"UNRELATED_APP_SECRET=should-not-pass",
	}
	got := filterEnv(base, runtimeEnvSpec{})
	for _, key := range []string{"PATH", "HOME", "LANG", "LC_ALL", "TMPDIR", "TERM", "XDG_CONFIG_HOME"} {
		if !hasEnvKey(got, key) {
			t.Errorf("filterEnv: generic var %s was dropped", key)
		}
	}
	if hasEnvKey(got, "UNRELATED_APP_SECRET") {
		t.Error("filterEnv: UNRELATED_APP_SECRET should have been dropped")
	}
}

// TestBuildEnv_AllowRootAndModelOverride_StillWork is a non-regression check
// that the M4 refactor did not break the existing dispatch-metadata
// injection (IS_SANDBOX, YAKOS_MODEL_OVERRIDE) for any of the three
// adapters.
func TestBuildEnv_AllowRootAndModelOverride_StillWork(t *testing.T) {
	req := DispatchRequest{AllowRoot: true, ModelOverride: "haiku"}
	for name, env := range map[string][]string{
		"claude": buildEnv(req),
		"codex":  buildEnvCodex(req),
		"agy":    buildEnvAgy(req),
	} {
		if !hasEnvKey(env, "IS_SANDBOX") {
			t.Errorf("%s: IS_SANDBOX missing when AllowRoot=true", name)
		}
		if !hasEnvKey(env, "YAKOS_MODEL_OVERRIDE") {
			t.Errorf("%s: YAKOS_MODEL_OVERRIDE missing", name)
		}
	}
}

// ---- Round-2 review regression tests (R1, R2, R7 prep) --------------------

// TestFilterEnv_WindowsShapedKeysSurvive is the R1 regression: pre-fix,
// isAllowlistedEnvKey was an exact, case-sensitive switch on POSIX-only
// names (PATH, HOME, TMPDIR, ...) with no Windows entries at all, so a
// Windows dispatch (mixed-case Path/SystemRoot/ComSpec/..., no HOME/TMPDIR)
// collapsed to essentially one variable: no PATH to resolve the CLI, no
// SystemRoot for Go's own winsock/DNS resolution, no TEMP/ComSpec/PATHEXT.
// This proves the allowlist now recognizes the natural Windows spellings.
func TestFilterEnv_WindowsShapedKeysSurvive(t *testing.T) {
	base := []string{
		"Path=C:\\Windows\\System32;C:\\Windows",
		"SystemRoot=C:\\Windows",
		"USERPROFILE=C:\\Users\\op",
		"APPDATA=C:\\Users\\op\\AppData\\Roaming",
		"LOCALAPPDATA=C:\\Users\\op\\AppData\\Local",
		"TEMP=C:\\Users\\op\\AppData\\Local\\Temp",
		"TMP=C:\\Users\\op\\AppData\\Local\\Temp",
		"ComSpec=C:\\Windows\\system32\\cmd.exe",
		"PATHEXT=.COM;.EXE;.BAT",
		"UNRELATED_APP_SECRET=should-not-pass",
	}
	got := filterEnv(base, runtimeEnvSpec{})
	for _, key := range []string{"Path", "SystemRoot", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "TEMP", "TMP", "ComSpec", "PATHEXT"} {
		if !hasEnvKey(got, key) {
			t.Errorf("filterEnv: Windows-shaped var %s was dropped (R1 regression); got %v", key, got)
		}
	}
	if hasEnvKey(got, "UNRELATED_APP_SECRET") {
		t.Error("filterEnv: UNRELATED_APP_SECRET should have been dropped")
	}
	if len(got) < 9 {
		t.Fatalf("filterEnv: Windows dispatch env collapsed to %d vars (R1 regression); got %v", len(got), got)
	}
}

// TestFilterEnv_KeyMatchIsCaseInsensitive verifies matching does not depend
// on the exact case of the input key (Windows keys are conventionally
// mixed-case and the OS treats them case-insensitively).
func TestFilterEnv_KeyMatchIsCaseInsensitive(t *testing.T) {
	base := []string{"path=/usr/bin", "Home=/home/op", "xdg_config_home=/home/op/.config"}
	got := filterEnv(base, runtimeEnvSpec{})
	for _, key := range []string{"path", "Home", "xdg_config_home"} {
		if !hasEnvKey(got, key) {
			t.Errorf("filterEnv: lower/mixed-case key %s was dropped", key)
		}
	}
}

// TestFilterEnv_KeepsEgressAndProxyVars is the R2 regression: pre-fix, the
// allowlist dropped ANTHROPIC_BASE_URL, CLAUDE_CODE_USE_BEDROCK/VERTEX,
// HTTPS_PROXY/HTTP_PROXY/NO_PROXY, NODE_EXTRA_CA_CERTS/SSL_CERT_FILE, and
// GIT_*/GH_TOKEN/SSH_AUTH_SOCK. That fails OPEN on data egress: an operator
// routing model traffic through a private gateway or Bedrock/Vertex would
// silently have prompts (their source code) sent direct to the public API
// instead, and a dispatched agent could not git push/gh as this repo's
// workflow requires.
func TestFilterEnv_KeepsEgressAndProxyVars(t *testing.T) {
	base := []string{
		"HTTP_PROXY=http://proxy.internal:8080",
		"HTTPS_PROXY=http://proxy.internal:8080",
		"NO_PROXY=localhost,127.0.0.1",
		"NODE_EXTRA_CA_CERTS=/etc/ssl/corp-ca.pem",
		"SSL_CERT_FILE=/etc/ssl/corp-ca.pem",
		"GIT_AUTHOR_NAME=CI Bot",
		"GIT_SSH_COMMAND=ssh -i /keys/deploy",
		"GH_TOKEN=gho_notarealtoken000000000000000000",
		"SSH_AUTH_SOCK=/tmp/ssh-agent.sock",
	}
	got := filterEnv(base, runtimeEnvSpec{})
	for _, key := range []string{
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE",
		"GIT_AUTHOR_NAME", "GIT_SSH_COMMAND", "GH_TOKEN", "SSH_AUTH_SOCK",
	} {
		if !hasEnvKey(got, key) {
			t.Errorf("filterEnv: %s dropped (R2 regression, data-egress/workflow failure)", key)
		}
	}
}

// TestClaudeEnvSpec_KeepsGatewayAndDeploymentVars is the claude-specific half
// of R2: the per-adapter buildEnv must keep ANTHROPIC_BASE_URL (private
// gateway relocation), ANTHROPIC_AUTH_TOKEN (non-API-key auth), and the
// Bedrock/Vertex deployment switches, not just ANTHROPIC_API_KEY.
func TestClaudeEnvSpec_KeepsGatewayAndDeploymentVars(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-secret")
	t.Setenv("ANTHROPIC_BASE_URL", "https://gateway.internal:9443")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "gw-token")
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")
	t.Setenv("CLAUDE_CONFIG_DIR", "/etc/claude")

	env := buildEnv(DispatchRequest{})
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CONFIG_DIR"} {
		if !hasEnvKey(env, key) {
			t.Errorf("buildEnv (claude): %s missing (R2 regression: silent egress relocation to public API)", key)
		}
	}
	// Still must not leak to codex/agy.
	if hasEnvKey(buildEnvCodex(DispatchRequest{}), "ANTHROPIC_BASE_URL") {
		t.Error("buildEnvCodex: ANTHROPIC_BASE_URL leaked from claude's namespace")
	}
}

// TestClaudeEnvSpec_VertexExactNamesNotBroadPrefix is the round-2 review N3
// regression: claude's Vertex support needs GOOGLE_APPLICATION_CREDENTIALS,
// GOOGLE_CLOUD_PROJECT, and CLOUD_ML_REGION specifically, listed as exact
// names — not a bare "GOOGLE_"/"GCLOUD_" prefix, which also captures
// GOOGLE_API_KEY (the Gemini API key). Before the fix, an operator
// configured for Gemini handed that key to the Anthropic CLI on every
// claude dispatch. agy's own GOOGLE_* prefix is unaffected: GOOGLE_API_KEY
// must still reach agy, since that is the runtime that actually reads it.
func TestClaudeEnvSpec_VertexExactNamesNotBroadPrefix(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "gemini-key-should-not-reach-claude")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/etc/gcp/creds.json")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "my-vertex-project")
	t.Setenv("CLOUD_ML_REGION", "us-central1")

	claudeEnv := buildEnv(DispatchRequest{})
	if hasEnvKey(claudeEnv, "GOOGLE_API_KEY") {
		t.Error("buildEnv (claude): GOOGLE_API_KEY leaked (N3 regression: Gemini key reaching the Anthropic CLI)")
	}
	for _, key := range []string{"GOOGLE_APPLICATION_CREDENTIALS", "GOOGLE_CLOUD_PROJECT", "CLOUD_ML_REGION"} {
		if !hasEnvKey(claudeEnv, key) {
			t.Errorf("buildEnv (claude): %s missing (needed for Vertex deployment)", key)
		}
	}

	// GCLOUD_-prefixed vars are dropped outright for claude (nothing in
	// Claude Code's Vertex support reads one).
	t.Setenv("GCLOUD_PROJECT", "should-not-reach-claude")
	if hasEnvKey(buildEnv(DispatchRequest{}), "GCLOUD_PROJECT") {
		t.Error("buildEnv (claude): GCLOUD_PROJECT leaked — GCLOUD_* prefix should be gone from claudeEnvSpec")
	}

	// agy still gets GOOGLE_API_KEY — it is the runtime that reads it.
	if !hasEnvKey(buildEnvAgy(DispatchRequest{}), "GOOGLE_API_KEY") {
		t.Error("buildEnvAgy: GOOGLE_API_KEY missing — agy's GOOGLE_* namespace must be unaffected by the claude-side narrowing")
	}
}

// TestFilterEnv_PassthroughEscapeHatch verifies YAKOS_DISPATCH_ENV_PASSTHROUGH
// forwards additional operator-named variables (exact name or "PREFIX*")
// regardless of the static allowlist, and that it forwards nothing when unset.
func TestFilterEnv_PassthroughEscapeHatch(t *testing.T) {
	base := []string{"CUSTOM_TOOL_TOKEN=abc123", "CUSTOM_OTHER=xyz", "MY_APP_A=1", "MY_APP_B=2"}

	if got := filterEnv(base, runtimeEnvSpec{}); hasEnvKey(got, "CUSTOM_TOOL_TOKEN") {
		t.Fatal("filterEnv: CUSTOM_TOOL_TOKEN forwarded with no passthrough set")
	}

	t.Setenv("YAKOS_DISPATCH_ENV_PASSTHROUGH", "CUSTOM_TOOL_TOKEN,MY_APP_*")
	got := filterEnv(base, runtimeEnvSpec{})
	if !hasEnvKey(got, "CUSTOM_TOOL_TOKEN") {
		t.Error("filterEnv: exact-name passthrough entry was not forwarded")
	}
	if !hasEnvKey(got, "MY_APP_A") || !hasEnvKey(got, "MY_APP_B") {
		t.Error("filterEnv: prefix passthrough entry (MY_APP_*) was not forwarded")
	}
	if hasEnvKey(got, "CUSTOM_OTHER") {
		t.Error("filterEnv: non-listed var was forwarded despite passthrough being set for other names")
	}
}

// TestFilterEnvFor_AppliesPerRuntimeNamespace is the R7 regression surface:
// FilterEnvFor must exist and apply the same per-runtime namespacing as the
// dispatch adapters, so internal/start and internal/interactive can reuse it
// instead of forwarding os.Environ() unfiltered.
func TestFilterEnvFor_AppliesPerRuntimeNamespace(t *testing.T) {
	base := []string{
		"ANTHROPIC_API_KEY=sk-ant-secret",
		"OPENAI_API_KEY=sk-openai-secret",
		"PATH=/usr/bin",
	}
	claudeEnv := FilterEnvFor("claude", base)
	if !hasEnvKey(claudeEnv, "ANTHROPIC_API_KEY") {
		t.Error("FilterEnvFor(claude): ANTHROPIC_API_KEY missing")
	}
	if hasEnvKey(claudeEnv, "OPENAI_API_KEY") {
		t.Error("FilterEnvFor(claude): OPENAI_API_KEY leaked")
	}
	codexEnv := FilterEnvFor("codex", base)
	if hasEnvKey(codexEnv, "ANTHROPIC_API_KEY") {
		t.Error("FilterEnvFor(codex): ANTHROPIC_API_KEY leaked")
	}
	// Unknown runtime name: generic allowlist only, no provider namespace.
	unknownEnv := FilterEnvFor("some-future-runtime", base)
	if hasEnvKey(unknownEnv, "ANTHROPIC_API_KEY") || hasEnvKey(unknownEnv, "OPENAI_API_KEY") {
		t.Error("FilterEnvFor(unknown): must not forward any provider namespace")
	}
	if !hasEnvKey(unknownEnv, "PATH") {
		t.Error("FilterEnvFor(unknown): generic PATH must still be forwarded")
	}
}
