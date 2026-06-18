package claudeauth

import (
	"os"
	"path/filepath"
	"testing"
)

// helper: write file at path with given content, creating parent dirs.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func TestIsAuthed_APIKey(t *testing.T) {
	home := t.TempDir()
	if !IsAuthed(home, "sk-ant-test-XXX") {
		t.Error("IsAuthed = false; want true when ANTHROPIC_API_KEY is set")
	}
}

func TestIsAuthed_LegacyAuthJSON(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".claude", "auth.json"), `{"token":"abc"}`)
	if !IsAuthed(home, "") {
		t.Error("IsAuthed = false; want true when ~/.claude/auth.json exists")
	}
}

func TestIsAuthed_OAuthAccount_Present(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".claude.json"), `{
		"oauthAccount": {"email": "user@example.com", "id": "acc_123"}
	}`)
	if !IsAuthed(home, "") {
		t.Error("IsAuthed = false; want true when oauthAccount is a non-empty object")
	}
}

func TestIsAuthed_OAuthAccount_Null(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".claude.json"), `{"oauthAccount": null}`)
	if IsAuthed(home, "") {
		t.Error("IsAuthed = true; want false when oauthAccount is null")
	}
}

func TestIsAuthed_OAuthAccount_Absent(t *testing.T) {
	home := t.TempDir()
	// ~/.claude.json exists but has no oauthAccount key (onboarding-only state).
	writeFile(t, filepath.Join(home, ".claude.json"), `{"onboardingComplete": true}`)
	if IsAuthed(home, "") {
		t.Error("IsAuthed = true; want false when oauthAccount key is absent")
	}
}

func TestIsAuthed_OAuthAccount_EmptyObject(t *testing.T) {
	home := t.TempDir()
	// oauthAccount is present but is an empty object {} → not authed.
	writeFile(t, filepath.Join(home, ".claude.json"), `{"oauthAccount": {}}`)
	if IsAuthed(home, "") {
		t.Error("IsAuthed = true; want false when oauthAccount is empty object {}")
	}
}

func TestIsAuthed_ClaudeJSONNotExist(t *testing.T) {
	home := t.TempDir()
	// No ~/.claude.json at all.
	if IsAuthed(home, "") {
		t.Error("IsAuthed = true; want false when no auth files exist")
	}
}

func TestIsAuthed_CredentialsJSON(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".claude", "credentials.json"), `{"token":"xyz"}`)
	if !IsAuthed(home, "") {
		t.Error("IsAuthed = false; want true when ~/.claude/credentials.json exists")
	}
}

func TestIsAuthed_NoneConfigured(t *testing.T) {
	home := t.TempDir()
	if IsAuthed(home, "") {
		t.Error("IsAuthed = true; want false when nothing is configured")
	}
}

func TestIsAuthed_EmptyHome(t *testing.T) {
	// Empty homeDir with no API key → false (no panic).
	if IsAuthed("", "") {
		t.Error("IsAuthed = true; want false when homeDir is empty and no API key")
	}
}

func TestIsAuthed_ClaudeJSONMalformed(t *testing.T) {
	home := t.TempDir()
	// Malformed JSON → should not panic, returns false.
	writeFile(t, filepath.Join(home, ".claude.json"), `not-json`)
	if IsAuthed(home, "") {
		t.Error("IsAuthed = true; want false when ~/.claude.json is malformed JSON")
	}
}
