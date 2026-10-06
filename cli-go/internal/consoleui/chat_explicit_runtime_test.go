package consoleui_test

// chat_explicit_runtime_test.go — a console pane set to a specific runtime means
// that runtime. If it cannot run, the turn fails with the reason and the
// fallbacks it did not use; it is never answered by another vendor (K-132).

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/dispatch"
)

func TestChatDispatch_ExplicitRuntimePaneIsNotAnsweredByAnotherRuntime(t *testing.T) {
	root := routingYakosRoot(t)
	fake := installFakeChatClaude(t)
	// Only the stub claude (and system tools) on PATH: no real codex or agy can
	// be reached, so codex is not installed as far as dispatch can tell.
	stub := strings.SplitN(os.Getenv("PATH"), string(os.PathListSeparator), 2)[0]
	t.Setenv("PATH", stub+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CODEX_HOME", t.TempDir())

	// The service's project lists claude as a fallback for everything.
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".yakos.yml"), []byte("default-fallback: [claude]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := dispatch.NewService(dispatch.ServiceConfig{YakosRoot: root, WorkspaceRoot: project})
	ts, tok, workDir, _ := newRoutingServer(t, root, svc)
	store := consoleui.NewTranscripts(workDir)
	const conv = "conv-explicit"

	// A pane set to codex: the request itself is valid, so it is accepted...
	if code, body := postDispatch(t, ts, tok, map[string]any{"runtime": "codex", "agent": "backend", "conversationId": conv, "sessionId": "s-explicit"}); code != http.StatusAccepted {
		t.Fatalf("status %d %s", code, body)
	}
	// ...and the turn then fails with the explicit-runtime error.
	var errText string
	waitUntil(t, "the turn to fail", func() bool {
		entries, _ := store.Read(conv, "alice")
		for _, e := range entries {
			if e.Role == consoleui.RoleError {
				errText = e.Text
				return true
			}
		}
		return false
	})
	for _, want := range []string{"runtime codex was requested explicitly", "CLI not found on PATH", "Not falling back to claude"} {
		if !strings.Contains(errText, want) {
			t.Errorf("the pane's error %q does not contain %q", errText, want)
		}
	}
	if calls := fake.calls(t); len(calls) != 0 {
		t.Errorf("claude answered a codex pane: %v", calls)
	}

	// An auto pane is not a named runtime: the same project and agent run on
	// claude, which is the agent's own runtime.
	fake.set(t, "ok", "sess-auto")
	if code, body := postDispatch(t, ts, tok, map[string]any{"runtime": "auto", "agent": "backend", "conversationId": "conv-auto", "sessionId": "s-auto"}); code != http.StatusAccepted {
		t.Fatalf("auto status %d %s", code, body)
	}
	waitForTurns(t, store, "conv-auto", 1)
	if calls := fake.calls(t); len(calls) != 1 {
		t.Errorf("the auto pane should have run claude once, got %d calls", len(calls))
	}
}
