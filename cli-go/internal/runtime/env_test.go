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
	got := filterEnv(base)
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
	got := filterEnv(base)
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
