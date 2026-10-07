package codexhome

import (
	"os"
	"path/filepath"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func seedProfileAuth(t *testing.T, home string) string {
	t.Helper()
	dir := ProfileDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"stub":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestEffective_NoProfileNoEnvUsesCodexDefault(t *testing.T) {
	home := t.TempDir()
	dir, isolated := Effective(home, env(nil))
	if dir != "" || isolated {
		t.Fatalf("got (%q, %v), want (\"\", false): codex keeps its own default home", dir, isolated)
	}
	if got, want := AuthDir(home, env(nil)), filepath.Join(home, ".codex"); got != want {
		t.Errorf("AuthDir = %q, want %q", got, want)
	}
}

func TestEffective_EnvCodexHomeIsKeptWithoutProfile(t *testing.T) {
	home := t.TempDir()
	dir, isolated := Effective(home, env(map[string]string{"CODEX_HOME": "/opt/codex"}))
	if dir != "/opt/codex" || isolated {
		t.Fatalf("got (%q, %v), want (/opt/codex, false)", dir, isolated)
	}
	if got := AuthDir(home, env(map[string]string{"CODEX_HOME": "/opt/codex"})); got != "/opt/codex" {
		t.Errorf("AuthDir = %q, want /opt/codex", got)
	}
}

func TestEffective_ProfileWithAuthWinsOverEnv(t *testing.T) {
	home := t.TempDir()
	profile := seedProfileAuth(t, home)
	dir, isolated := Effective(home, env(map[string]string{"CODEX_HOME": "/somewhere/else"}))
	if dir != profile || !isolated {
		t.Fatalf("got (%q, %v), want (%q, true): the yakOS profile must win over an ambient CODEX_HOME", dir, isolated, profile)
	}
	if got := AuthDir(home, env(nil)); got != profile {
		t.Errorf("AuthDir = %q, want %q", got, profile)
	}
}

func TestEffective_ProfileDirWithoutAuthIsIgnored(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(ProfileDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	dir, isolated := Effective(home, env(nil))
	if dir != "" || isolated {
		t.Fatalf("got (%q, %v): an empty profile (login not completed) must not be selected", dir, isolated)
	}
}

func TestProfileHasAuth_DirectoryNamedAuthJSONIsNotAuth(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ProfileDir(home), "auth.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if ProfileHasAuth(home) {
		t.Fatal("a directory named auth.json is not a login")
	}
}

func TestProfileDir_EmptyHome(t *testing.T) {
	if ProfileDir("") != "" || ProfileHasAuth("") {
		t.Fatal("empty home must yield no profile")
	}
}

func TestEffectiveUsesProfileForHooksWithAPIKey(t *testing.T) {
	home := t.TempDir()
	dir := ProfileDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	key := func(k string) string {
		if k == "OPENAI_API_KEY" {
			return "k"
		}
		return ""
	}
	if _, iso := Effective(home, key); iso {
		t.Fatal("profile without hooks or login must not be used")
	}
	if err := os.WriteFile(filepath.Join(dir, HooksFileName), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if d, iso := Effective(home, key); !iso || d != dir {
		t.Fatalf("hooks + API key: got %q %v", d, iso)
	}
	if _, iso := Effective(home, func(string) string { return "" }); iso {
		t.Fatal("hooks without a login or key must not isolate")
	}
}
