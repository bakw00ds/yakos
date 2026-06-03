package wsbus

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadOrCreateToken_CreatesOnMissing(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "ws-token")

	tok, err := LoadOrCreateToken(path)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	if len(tok) != 64 {
		t.Errorf("token length=%d; want 64", len(tok))
	}

	// The caller must be able to read the file regardless of platform.
	// On Windows this verifies winsec.SecureFile did not lock out the current
	// user; on Unix it also implicitly checks existence.
	if _, err := os.ReadFile(path); err != nil { //nolint:gosec
		t.Fatalf("current user cannot read token file after generation: %v", err)
	}

	// On non-Windows also assert POSIX 0600 mode bits.
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat token file: %v", err)
		}
		if fi.Mode() != 0600 {
			t.Errorf("token file mode=%o; want 0600", fi.Mode())
		}
	}
}

func TestLoadOrCreateToken_ReturnsExisting(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "ws-token")

	tok1, err := LoadOrCreateToken(path)
	if err != nil {
		t.Fatalf("first LoadOrCreateToken: %v", err)
	}

	tok2, err := LoadOrCreateToken(path)
	if err != nil {
		t.Fatalf("second LoadOrCreateToken: %v", err)
	}

	if tok1 != tok2 {
		t.Errorf("token changed between loads: %q != %q", tok1, tok2)
	}
}

func TestLoadOrCreateToken_RegeneratesOnMalformed(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "ws-token")

	// Write a malformed (short) token.
	if err := os.WriteFile(path, []byte("short\n"), 0600); err != nil {
		t.Fatalf("write bad token: %v", err)
	}

	tok, err := LoadOrCreateToken(path)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	if len(tok) != 64 {
		t.Errorf("regenerated token length=%d; want 64", len(tok))
	}
}

func TestRotateToken_ProducesNewToken(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "ws-token")

	tok1, err := LoadOrCreateToken(path)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}

	tok2, err := RotateToken(path)
	if err != nil {
		t.Fatalf("RotateToken: %v", err)
	}

	if tok1 == tok2 {
		t.Error("rotated token should differ from original (statistically guaranteed for 256-bit random)")
	}
	if len(tok2) != 64 {
		t.Errorf("rotated token length=%d; want 64", len(tok2))
	}
}

func TestRotateToken_NewTokenPersisted(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "ws-token")

	rotated, err := RotateToken(path)
	if err != nil {
		t.Fatalf("RotateToken: %v", err)
	}

	loaded, err := LoadOrCreateToken(path)
	if err != nil {
		t.Fatalf("LoadOrCreateToken after rotate: %v", err)
	}

	if rotated != loaded {
		t.Errorf("rotated token %q != loaded %q", rotated, loaded)
	}
}

func TestLoadOrCreateToken_IsHexString(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "ws-token")

	tok, err := LoadOrCreateToken(path)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}

	tok = strings.TrimSpace(tok)
	for _, c := range tok {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Errorf("token contains non-hex char %q in %q", c, tok)
			break
		}
	}
}

func TestLoadOrCreateToken_CreatesParentDir(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "nested", "dir", "ws-token")

	_, err := LoadOrCreateToken(path)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}

	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Errorf("parent dir not created: %v", err)
	}
}

func TestTokenFilePath_NotEmpty(t *testing.T) {
	if TokenFilePath() == "" {
		t.Error("TokenFilePath() should return a non-empty path")
	}
}
