package restapi

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadOrGenerateTokens_GeneratesOnMissing(t *testing.T) {
	dir := t.TempDir()
	toks, err := LoadOrGenerateTokens(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isValidToken(toks.Read) {
		t.Errorf("read token invalid: %q", toks.Read)
	}
	if !isValidToken(toks.Write) {
		t.Errorf("write token invalid: %q", toks.Write)
	}
	if toks.Read == toks.Write {
		t.Error("read and write tokens must differ")
	}
}

func TestLoadOrGenerateTokens_PersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	toks1, err := LoadOrGenerateTokens(dir)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	toks2, err := LoadOrGenerateTokens(dir)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if toks1.Read != toks2.Read {
		t.Errorf("read token changed between loads: %q vs %q", toks1.Read, toks2.Read)
	}
	if toks1.Write != toks2.Write {
		t.Errorf("write token changed between loads: %q vs %q", toks1.Write, toks2.Write)
	}
}

func TestLoadOrGenerateTokens_FilesAreMode0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		// NTFS does not enforce POSIX mode bits; ACL-based hardening is a
		// Phase 1.5 followup. Skip rather than false-fail on Windows CI.
		t.Skip("POSIX mode bits not enforced on Windows; ACL hardening is a Phase 1.5 followup")
	}
	dir := t.TempDir()
	_, err := LoadOrGenerateTokens(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, name := range []string{readTokenFile, writeTokenFile} {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if perm := info.Mode().Perm(); perm != 0600 {
			t.Errorf("%s: perm=%o; want 0600", name, perm)
		}
	}
}

func TestLoadOrGenerateTokens_RegeneratesCorrupt(t *testing.T) {
	dir := t.TempDir()
	// Write a corrupt (too short) token file.
	path := filepath.Join(dir, readTokenFile)
	if err := os.WriteFile(path, []byte("tooshort\n"), 0600); err != nil { //nolint:gosec
		t.Fatalf("write corrupt: %v", err)
	}
	toks, err := LoadOrGenerateTokens(dir)
	if err != nil {
		t.Fatalf("load after corrupt: %v", err)
	}
	if !isValidToken(toks.Read) {
		t.Errorf("regenerated read token invalid: %q", toks.Read)
	}
}

func TestRotateTokens_ProducesNewTokens(t *testing.T) {
	dir := t.TempDir()
	toks1, err := LoadOrGenerateTokens(dir)
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	toks2, err := RotateTokens(dir)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if toks1.Read == toks2.Read {
		t.Error("rotate did not change read token")
	}
	if toks1.Write == toks2.Write {
		t.Error("rotate did not change write token")
	}
}

func TestIsValidToken(t *testing.T) {
	tests := []struct {
		name  string
		tok   string
		valid bool
	}{
		{"64 hex lower", strings.Repeat("a", 64), true},
		{"64 hex mixed digit", "0123456789abcdef" + strings.Repeat("a", 48), true},
		{"too short", "abc123", false},
		{"too long", strings.Repeat("a", 65), false},
		{"uppercase", strings.Repeat("A", 64), false},
		{"non-hex", strings.Repeat("g", 64), false},
		{"empty", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isValidToken(tc.tok); got != tc.valid {
				t.Errorf("isValidToken(%q)=%v; want %v", tc.tok, got, tc.valid)
			}
		})
	}
}
