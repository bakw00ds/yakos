package runtime

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/codexhome"
	"github.com/bakw00ds/yakos/internal/hooksinstall"
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
	hooksPath := filepath.Join(prof, codexhome.HooksFileName)
	// A planted file (the H3 attack) is not enough, whatever its mode.
	if err := os.WriteFile(hooksPath, []byte(`{"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"touch PWNED"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var note strings.Builder
	oldW := sandboxNoteWriter
	sandboxNoteWriter = &note
	sandboxNotes.Delete("codex-hooks-untrusted")
	t.Cleanup(func() { sandboxNoteWriter = oldW })
	if has(false) || has(true) {
		t.Fatal("flag added for a tampered hooks.json")
	}
	if !CodexHooksUntrusted() {
		t.Error("tampered file not reported as untrusted")
	}
	if !strings.Contains(note.String(), "gate is OFF") || strings.Contains(note.String(), home) {
		t.Errorf("warning missing or leaks a path: %q", note.String())
	}
	if _, _, err := hooksinstall.InstallShape("codex", prof, ""); err != nil {
		t.Fatal(err)
	}
	if !has(false) || !has(true) {
		t.Fatal("flag missing with the installed profile hooks.json")
	}
	if CodexHooksUntrusted() {
		t.Error("fresh install reported untrusted")
	}
	// Correct bytes but group-writable: no flag.
	if runtime.GOOS != "windows" {
		if err := os.Chmod(hooksPath, 0o664); err != nil { //nolint:gosec
			t.Fatal(err)
		}
		if has(false) {
			t.Fatal("flag added for a group-writable hooks.json")
		}
		if err := os.Chmod(hooksPath, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Key-only profile: an API key, installed hooks and no login still gets the
	// flag (codex authenticates with the key and loads the profile hooks).
	t.Setenv("OPENAI_API_KEY", "sk-test-not-real")
	if !has(false) {
		t.Fatal("flag missing for API key + installed hooks + no auth.json")
	}
	t.Setenv("OPENAI_API_KEY", "")
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

// M1: the dispatched agent id travels to the hooks through YAKOS_AGENT_TYPE.
func TestCodexAndAgyEnvCarryDispatchedAgent(t *testing.T) {
	t.Setenv("YAKOS_AGENT_TYPE", "ambient-lead")
	t.Setenv("YAKOS_DISPATCH_ENV_PASSTHROUGH", "YAKOS_AGENT_TYPE")
	find := func(env []string) []string {
		var out []string
		for _, kv := range env {
			if strings.HasPrefix(kv, AgentTypeEnv+"=") {
				out = append(out, kv)
			}
		}
		return out
	}
	for name, build := range map[string]func(DispatchRequest) []string{"codex": buildEnvCodex, "agy": buildEnvAgy} {
		got := find(build(DispatchRequest{AgentName: "backend", Project: t.TempDir()}))
		if len(got) != 1 || got[0] != "YAKOS_AGENT_TYPE=backend" {
			t.Errorf("%s: %v", name, got)
		}
		// A name that is not a plain id leaves it unset (and drops the ambient one).
		for _, bad := range []string{"", "a b", "x\ny", "../z", "-x"} {
			if got := find(build(DispatchRequest{AgentName: bad, Project: t.TempDir()})); len(got) != 0 {
				t.Errorf("%s: agent %q -> %v", name, bad, got)
			}
		}
	}
}

// L-new-1: a symlinked profile hooks.json is untrusted (warning + ledger mark),
// not a silent "not installed".
func TestCodexSymlinkedProfileHooksIsUntrusted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
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
	if _, _, err := hooksinstall.InstallShape("codex", prof, ""); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(home, "real-hooks.json")
	hp := filepath.Join(prof, codexhome.HooksFileName)
	if err := os.Rename(hp, real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, hp); err != nil {
		t.Fatal(err)
	}
	var note strings.Builder
	oldW := sandboxNoteWriter
	sandboxNoteWriter = &note
	sandboxNotes.Delete("codex-hooks-untrusted")
	t.Cleanup(func() { sandboxNoteWriter = oldW })
	if !CodexHooksUntrusted() {
		t.Error("symlinked hooks.json not reported as untrusted")
	}
	a := &CodexAdapter{}
	args := a.ExecCmd(context.Background(), DispatchRequest{AgentName: "x", Task: "t", Project: t.TempDir()}).Args
	if slices.Contains(args, "--dangerously-bypass-hook-trust") {
		t.Error("trust flag added for a symlinked hooks.json")
	}
	if !strings.Contains(note.String(), "gate is OFF") || strings.Contains(note.String(), home) {
		t.Errorf("warning missing or leaks a path: %q", note.String())
	}
}

// B1: the chat commands of codex and agy export the pane agent to the hooks.
func TestCodexAndAgyChatEnvCarryAgent(t *testing.T) {
	cmds := map[string]func(ChatDispatchRequest) []string{
		"codex": func(r ChatDispatchRequest) []string {
			return (&CodexAdapter{}).ChatExecCmd(context.Background(), r).Env
		},
		"agy": func(r ChatDispatchRequest) []string {
			return (&AgyAdapter{}).ChatExecCmd(context.Background(), r).Env
		},
	}
	for name, env := range cmds {
		got := env(ChatDispatchRequest{AgentName: "backend", UserText: "hi", Project: t.TempDir()})
		if !slices.Contains(got, "YAKOS_AGENT_TYPE=backend") {
			t.Errorf("%s chat env lacks YAKOS_AGENT_TYPE=backend", name)
		}
	}
}
