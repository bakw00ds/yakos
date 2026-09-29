package contextthreshold_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooks/contextthreshold"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

// bash locates the transcript with hi_session_id (payload .session_id). A
// CLAUDE_SESSION_ID env var must neither supply the id nor override it.
func TestSessionIDFromPayloadDecidesThreshold(t *testing.T) {
	run := func(t *testing.T, payload map[string]any, env map[string]string) map[string]any {
		work, home, proj, state := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
		writeSettings(t, state, 1, 90)
		writeClaudeTranscript(t, home, proj, "sess-real", 800_000) // ~10% of the window
		h := &contextthreshold.Hook{WorkCurrentDir: work, HomeDir: home, StateDir: state, NowFn: fixedNow}
		env["YAKOS_RUNTIME"] = "claude"
		env["CLAUDE_PROJECT_DIR"] = proj
		if _, err := h.Run(context.Background(), hooktype.HookInput{Payload: payload, Env: env}); err != nil {
			t.Fatal(err)
		}
		return readLastLog(t, filepath.Join(work, "logs", "context-threshold.ndjson"))
	}

	t.Run("payload id finds the transcript and crosses the notice threshold", func(t *testing.T) {
		rec := run(t, map[string]any{"session_id": "sess-real"}, map[string]string{"CLAUDE_SESSION_ID": "sess-other"})
		if rec["severity"] != "WARN" {
			t.Fatalf("severity=%v action=%v, want WARN", rec["severity"], rec["action"])
		}
	})
	t.Run("env-only id is ignored: probe unavailable", func(t *testing.T) {
		rec := run(t, map[string]any{}, map[string]string{"CLAUDE_SESSION_ID": "sess-real"})
		if rec["action"] != "probe_unavailable" {
			t.Fatalf("action=%v severity=%v, want probe_unavailable", rec["action"], rec["severity"])
		}
	})
}
