package runtime

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/bakw00ds/yakos/internal/codexhome"
)

func TestCodexBypassHookTrustOnlyWithInstalledProfileHooks(t *testing.T) {
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
	a := &CodexAdapter{}
	has := func(resume bool) bool {
		req := DispatchRequest{AgentName: "x", Task: "t", Project: t.TempDir()}
		if resume {
			req.ConversationID = "thread-1"
		}
		return slices.Contains(a.ExecCmd(context.Background(), req).Args, "--dangerously-bypass-hook-trust")
	}
	if has(false) || has(true) {
		t.Fatal("flag added without an installed hooks.json")
	}
	if err := os.WriteFile(filepath.Join(prof, codexhome.HooksFileName), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !has(false) || !has(true) {
		t.Fatal("flag missing with installed profile hooks.json")
	}
	// Ambient CODEX_HOME hooks must never trigger the bypass: only the profile.
	if err := os.Remove(filepath.Join(prof, "auth.json")); err != nil {
		t.Fatal(err)
	}
	amb := t.TempDir()
	_ = os.WriteFile(filepath.Join(amb, "hooks.json"), []byte("{}"), 0o600)
	t.Setenv("CODEX_HOME", amb)
	if has(false) {
		t.Fatal("flag added for a non-yakOS CODEX_HOME")
	}
}
