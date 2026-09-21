package interactive

// sdk_engine_internal_test.go — white-box test for sdkSidecarEnv (round-2
// review R7). The round-1 M4 fix allowlisted the dispatch adapters'
// subprocess environments in internal/runtime but missed this package: the
// Node SDK sidecar spawned by SDKEngine.Start left cmd.Env nil, so Go
// inherited the full parent environment (every other configured runtime's
// credentials included) unfiltered. This test is in-package (not
// interactive_test) because sdkSidecarEnv is unexported.

import "testing"

func hasEnvVar(env []string, key string) bool {
	prefix := key + "="
	for _, kv := range env {
		if len(kv) >= len(prefix) && kv[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// TestSdkSidecarEnv_FiltersOtherRuntimesCredentials proves the sidecar
// environment is allowlisted, not the raw parent environment.
func TestSdkSidecarEnv_FiltersOtherRuntimesCredentials(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-secret")
	t.Setenv("OPENAI_API_KEY", "sk-openai-secret")
	t.Setenv("CODEX_HOME", "/some/codex/home")
	t.Setenv("ANTIGRAVITY_API_KEY", "agy-secret")
	t.Setenv("GEMINI_API_KEY", "gemini-secret")

	env := sdkSidecarEnv()

	if !hasEnvVar(env, "ANTHROPIC_API_KEY") {
		t.Error("sdkSidecarEnv: ANTHROPIC_API_KEY missing; the claude SDK sidecar needs its own credential")
	}
	for _, leaked := range []string{"OPENAI_API_KEY", "CODEX_HOME", "ANTIGRAVITY_API_KEY", "GEMINI_API_KEY"} {
		if hasEnvVar(env, leaked) {
			t.Errorf("sdkSidecarEnv: %s leaked into the SDK sidecar env (R7 regression)", leaked)
		}
	}
}
