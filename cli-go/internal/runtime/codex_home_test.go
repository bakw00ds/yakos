package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CODEX_HOME selection (K-133): once `yakos auth login codex` has created a
// login in ~/.yakos-state/codex-home, dispatch runs codex under that profile so
// it never shares an auth.json with the operator's interactive codex.

func envValues(env []string, key string) []string {
	var out []string
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, key) {
			out = append(out, v)
		}
	}
	return out
}

func seedCodexProfile(t *testing.T, home string) string {
	t.Helper()
	dir := filepath.Join(home, ".yakos-state", "codex-home")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"stub":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCodexHome_NoProfileLeavesAmbientCodexHomeAlone(t *testing.T) {
	useEmptyHome(t)
	t.Setenv("CODEX_HOME", "/opt/my-codex")
	got := envValues(buildEnvCodex(DispatchRequest{}), "CODEX_HOME")
	if len(got) != 1 || got[0] != "/opt/my-codex" {
		t.Fatalf("CODEX_HOME = %v, want the operator's own /opt/my-codex (nothing breaks before the login command is run)", got)
	}
}

func TestCodexHome_NoProfileNoAmbientSetsNothing(t *testing.T) {
	useEmptyHome(t)
	if got := envValues(buildEnvCodex(DispatchRequest{}), "CODEX_HOME"); len(got) != 0 {
		t.Fatalf("CODEX_HOME = %v, want unset so codex uses its default ~/.codex", got)
	}
}

func TestCodexHome_ProfileWithLoginIsSetExplicitly(t *testing.T) {
	home := useEmptyHome(t)
	profile := seedCodexProfile(t, home)
	got := envValues(buildEnvCodex(DispatchRequest{}), "CODEX_HOME")
	if len(got) != 1 || got[0] != profile {
		t.Fatalf("CODEX_HOME = %v, want exactly the yakOS profile %q", got, profile)
	}
}

func TestCodexHome_ProfileReplacesAmbientCodexHomeAndSaysSo(t *testing.T) {
	home := useEmptyHome(t)
	notes := captureSandboxNotes(t)
	profile := seedCodexProfile(t, home)
	t.Setenv("CODEX_HOME", "/project/supplied/home")

	env := buildEnvCodex(DispatchRequest{})
	got := envValues(env, "CODEX_HOME")
	if len(got) != 1 || got[0] != profile {
		t.Fatalf("CODEX_HOME = %v, want only the yakOS profile %q (an ambient value must not survive)", got, profile)
	}
	if !strings.Contains(notes.String(), "ignoring CODEX_HOME=/project/supplied/home") {
		t.Errorf("replacing an operator-set CODEX_HOME must be explained, got %q", notes.String())
	}
}

// TestCodexHome_ProfileKeepsTheRestOfTheEnvironment: swapping CODEX_HOME must not
// drop anything else the codex subprocess needs. useEmptyHome clears
// OPENAI_API_KEY, so set it (and another allowlisted codex variable) here, then
// require them, PATH and HOME to come through next to the profile.
func TestCodexHome_ProfileKeepsTheRestOfTheEnvironment(t *testing.T) {
	home := useEmptyHome(t)
	profile := seedCodexProfile(t, home)
	t.Setenv("OPENAI_API_KEY", "sk-test-not-a-real-key")
	t.Setenv("CODEX_SOMETHING_ELSE", "kept")
	t.Setenv("PATH", "/usr/bin:/bin:/opt/test-bin")
	t.Setenv("ANTHROPIC_API_KEY", "must-not-leak")

	env := buildEnvCodex(DispatchRequest{})
	if got := envValues(env, "CODEX_HOME"); len(got) != 1 || got[0] != profile {
		t.Fatalf("CODEX_HOME = %v, want %q", got, profile)
	}
	for key, want := range map[string]string{
		"OPENAI_API_KEY":       "sk-test-not-a-real-key",
		"CODEX_SOMETHING_ELSE": "kept",
		"PATH":                 "/usr/bin:/bin:/opt/test-bin",
		"HOME":                 home,
	} {
		if got := envValues(env, key); len(got) != 1 || got[0] != want {
			t.Errorf("%s = %v, want the inherited %q to be forwarded next to CODEX_HOME", key, got, want)
		}
	}
	if hasEnvKey(env, "ANTHROPIC_API_KEY") {
		t.Error("another provider's credential must stay out of the codex environment")
	}
}

func TestCodexHome_ProfileWithoutLoginIsIgnored(t *testing.T) {
	home := useEmptyHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".yakos-state", "codex-home"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := envValues(buildEnvCodex(DispatchRequest{}), "CODEX_HOME"); len(got) != 0 {
		t.Fatalf("CODEX_HOME = %v: a profile whose login never completed must not be selected", got)
	}
}

// TestCodexHome_DispatchLogRelocationCannotPlantAProfile: a project that sets
// YAKOS_DISPATCH_LOG (K-129) must not be able to point codex at a CODEX_HOME of
// its own, because a CODEX_HOME carries a config.toml (notify commands, MCP
// servers, approval policy).
func TestCodexHome_DispatchLogRelocationCannotPlantAProfile(t *testing.T) {
	useEmptyHome(t)
	planted := t.TempDir()
	if err := os.MkdirAll(filepath.Join(planted, "codex-home"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planted, "codex-home", "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YAKOS_DISPATCH_LOG", planted)
	if got := envValues(buildEnvCodex(DispatchRequest{}), "CODEX_HOME"); len(got) != 0 {
		t.Fatalf("CODEX_HOME = %v: YAKOS_DISPATCH_LOG must not relocate the profile", got)
	}
}

func TestCodexHome_ChatPathUsesTheSameProfile(t *testing.T) {
	skipOnWindows(t)
	home := useEmptyHome(t)
	profile := seedCodexProfile(t, home)
	cmd := (&CodexAdapter{}).ChatExecCmd(context.Background(), ChatDispatchRequest{Project: t.TempDir(), UserText: "hi"})
	if got := envValues(cmd.Env, "CODEX_HOME"); len(got) != 1 || got[0] != profile {
		t.Fatalf("chat CODEX_HOME = %v, want %q", got, profile)
	}
}

func TestCodexAvailable_FollowsTheEffectiveCodexHome(t *testing.T) {
	skipOnWindows(t)
	home := useEmptyHome(t)
	mockDir, _ := writeMockRuntime(t, "codex", 0, "")
	t.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))
	a := &CodexAdapter{}

	if a.Available(context.Background()) {
		t.Fatal("no login anywhere: Available must be false")
	}
	// The operator's own ~/.codex login counts (nothing breaks today).
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !a.Available(context.Background()) {
		t.Fatal("~/.codex/auth.json present: Available must be true")
	}
	// Remove it; the yakOS profile alone must be enough.
	if err := os.Remove(filepath.Join(home, ".codex", "auth.json")); err != nil {
		t.Fatal(err)
	}
	seedCodexProfile(t, home)
	if !a.Available(context.Background()) {
		t.Fatal("yakOS profile login present: Available must be true")
	}
}
