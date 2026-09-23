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
	dir := t.TempDir()
	_, err := LoadOrGenerateTokens(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, name := range []string{readTokenFile, writeTokenFile} {
		path := filepath.Join(dir, name)
		// Verify the file is readable by the current process. On Windows
		// this confirms winsec.SecureFile did not lock out the current user;
		// on Unix it also verifies the file exists.
		if _, err := os.ReadFile(path); err != nil { //nolint:gosec
			t.Fatalf("%s: current user cannot read token file after generation: %v", name, err)
		}
		// On non-Windows platforms also assert the POSIX mode bits are 0600.
		if runtime.GOOS != "windows" {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat %s: %v", name, err)
			}
			if perm := info.Mode().Perm(); perm != 0600 {
				t.Errorf("%s: perm=%o; want 0600", name, perm)
			}
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

// ---- round-2 review R4: secureStateDir hardening --------------------------

// TestLoadOrGenerateTokens_RejectsSymlinkStateDir is the R4 regression: an
// attacker-plantable symlink at the state dir path must be refused, not
// followed and "tightened" (which would just chmod whatever the symlink
// points at).
func TestLoadOrGenerateTokens_RejectsSymlinkStateDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows; posix-focused regression")
	}
	parent := t.TempDir()
	realDir := filepath.Join(parent, "real")
	if err := os.Mkdir(realDir, 0700); err != nil {
		t.Fatalf("mkdir real: %v", err)
	}
	linkPath := filepath.Join(parent, "state-symlink")
	if err := os.Symlink(realDir, linkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	_, err := LoadOrGenerateTokens(linkPath)
	if err == nil {
		t.Fatal("LoadOrGenerateTokens on a symlinked state dir: want error, got nil (R4 regression: planted-symlink attack not rejected)")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error should mention symlink; got: %v", err)
	}
}

// TestLoadOrGenerateTokens_TightensPermissiveExistingDir is the R4
// regression for the world-writable-directory attack: MkdirAll is a no-op
// on an existing directory and does not tighten its mode, so an
// attacker-created `mkdir -m 0777 <stateDir>` ahead of the daemon starting
// previously stayed world-writable forever. secureStateDir must now detect
// and tighten it.
func TestLoadOrGenerateTokens_TightensPermissiveExistingDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix permission bits; posix-focused regression")
	}
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "world-writable-state")
	if err := os.Mkdir(stateDir, 0777); err != nil {
		t.Fatalf("mkdir 0777: %v", err)
	}
	// Force the mode past umask, since Mkdir's mode argument is masked by
	// the process umask.
	if err := os.Chmod(stateDir, 0777); err != nil {
		t.Fatalf("chmod 0777: %v", err)
	}

	if _, err := LoadOrGenerateTokens(stateDir); err != nil {
		t.Fatalf("LoadOrGenerateTokens: %v", err)
	}

	fi, err := os.Stat(stateDir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm&0077 != 0 {
		t.Errorf("state dir mode after LoadOrGenerateTokens = %o; want group/other bits cleared (0700) — R4 regression: pre-existing 0777 dir left world-writable", perm)
	}
}
