package dispatch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/codexhome"
	"github.com/bakw00ds/yakos/internal/hooksinstall"
)

// K-145: a codex run whose profile hooks.json is not the installed one is
// marked hooks_untrusted in the dispatch log row.
func TestFinishedEventMarksUntrustedCodexHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("OPENAI_API_KEY", "")
	prof := codexhome.ProfileDir(home)
	if err := os.MkdirAll(prof, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prof, "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	ev := func(rt string) finishedEvent {
		return buildFinished(Request{AgentName: "a", Runtime: rt}, Result{}, time.Now())
	}
	if ev("codex").HooksUntrusted {
		t.Error("no hooks file: must not be marked")
	}
	if err := os.WriteFile(filepath.Join(prof, "hooks.json"), []byte(`{"hooks":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !ev("codex").HooksUntrusted {
		t.Error("tampered hooks file not marked")
	}
	if ev("claude").HooksUntrusted {
		t.Error("non-codex run marked")
	}
	if _, _, err := hooksinstall.InstallShape("codex", prof, ""); err != nil {
		t.Fatal(err)
	}
	if ev("codex").HooksUntrusted {
		t.Error("installed hooks file marked")
	}
}
