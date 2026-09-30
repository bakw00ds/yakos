package contextthreshold_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
			t.Fatalf("severity=%v decision=%v, want WARN", rec["severity"], rec["decision"])
		}
	})
	t.Run("env-only id is ignored: probe unavailable", func(t *testing.T) {
		rec := run(t, map[string]any{}, map[string]string{"CLAUDE_SESSION_ID": "sess-real"})
		if rec["decision"] != "probe_unavailable" {
			t.Fatalf("decision=%v severity=%v, want probe_unavailable", rec["decision"], rec["severity"])
		}
	})
}

// K-110: the payload's transcript_path is used as given, even when neither the
// session id nor the project directory would derive it. A path that does not
// name a file falls back to the derived location.
func TestTranscriptPathFromPayload(t *testing.T) {
	run := func(t *testing.T, payload func(elsewhere string) map[string]any, derived bool) map[string]any {
		work, home, proj, state := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
		writeSettings(t, state, 1, 90)
		elsewhere := filepath.Join(t.TempDir(), "renamed.jsonl")
		writeFile(t, elsewhere, 800_000) // ~10% of the window
		if derived {
			writeClaudeTranscript(t, home, proj, "sess-real", 800_000)
		}
		h := &contextthreshold.Hook{WorkCurrentDir: work, HomeDir: home, StateDir: state, NowFn: fixedNow}
		env := map[string]string{"YAKOS_RUNTIME": "claude", "CLAUDE_PROJECT_DIR": proj}
		if _, err := h.Run(context.Background(), hooktype.HookInput{Payload: payload(elsewhere), Env: env}); err != nil {
			t.Fatal(err)
		}
		return readLastLog(t, filepath.Join(work, "logs", "context-threshold.ndjson"))
	}

	t.Run("payload path alone finds the transcript", func(t *testing.T) {
		rec := run(t, func(p string) map[string]any {
			return map[string]any{"session_id": "unknown-sess", "transcript_path": p}
		}, false)
		if rec["severity"] != "WARN" {
			t.Fatalf("severity=%v decision=%v, want WARN", rec["severity"], rec["decision"])
		}
	})
	t.Run("payload path works with no session id", func(t *testing.T) {
		rec := run(t, func(p string) map[string]any { return map[string]any{"transcript_path": p} }, false)
		if rec["severity"] != "WARN" {
			t.Fatalf("severity=%v decision=%v, want WARN", rec["severity"], rec["decision"])
		}
	})
	t.Run("missing payload path falls back to the derived transcript", func(t *testing.T) {
		rec := run(t, func(string) map[string]any {
			return map[string]any{"session_id": "sess-real", "transcript_path": "/nonexistent/x.jsonl"}
		}, true)
		if rec["severity"] != "WARN" {
			t.Fatalf("severity=%v decision=%v, want WARN", rec["severity"], rec["decision"])
		}
	})
}

func writeFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Repeat("x", size)), 0o644); err != nil {
		t.Fatal(err)
	}
}
