package consoleui_test

// testmain_test.go — package-level test setup for the consoleui test binary.
//
// TestMain reduces argon2id cost parameters for the entire test binary so that
// tests that call userstore.Create (argon2id hash) complete in milliseconds
// rather than seconds.  This mirrors the pattern used in userstore_test.go.
//
// The reduced params are (t=1, m=64KiB, p=1) — still a valid argon2id
// derivation, just not production-strength.  VerifyPassword parses params from
// the stored PHC, so round-trip tests are unaffected.
//
// DO NOT call SetArgon2ParamsForTest inside individual tests — it mutates
// package-level globals and races when tests run in parallel.  Set them once
// here.

import (
	"os"
	"testing"

	"github.com/bakw00ds/yakos/internal/userstore"
)

func TestMain(m *testing.M) {
	restore := userstore.SetArgon2ParamsForTest(1, 64, 1)
	defer restore()

	// K-136: an interactive chat turn now writes dispatch_started/finished
	// events (dispatch.Account), and the billing mode of a turn is read from the
	// provider credentials in the environment. Keep every test out of the
	// operator's real state directory and start from no credentials, so a
	// developer's exported API key cannot change what a test sees. A test that
	// wants a log of its own sets YAKOS_DISPATCH_LOG itself.
	stateDir, err := os.MkdirTemp("", "consoleui-test-state-")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("YAKOS_DISPATCH_LOG", stateDir)
	for _, k := range []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN",
		"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY",
		"CODEX_API_KEY", "OPENAI_API_KEY",
		"ANTIGRAVITY_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_GENAI_USE_VERTEXAI",
	} {
		_ = os.Unsetenv(k)
	}
	code := m.Run()
	_ = os.RemoveAll(stateDir)
	os.Exit(code)
}
