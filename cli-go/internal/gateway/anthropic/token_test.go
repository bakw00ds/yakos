package anthropic

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestTokenFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if _, err := ReadToken(dir); err == nil {
		t.Fatal("ReadToken invented a token")
	}
	tok, err := LoadOrCreateToken(dir)
	if err != nil || !validToken(tok) {
		t.Fatalf("LoadOrCreateToken = %q, %v", tok, err)
	}
	again, err := LoadOrCreateToken(dir)
	if err != nil || again != tok {
		t.Fatalf("second load = %q, %v; want the same token", again, err)
	}
	if got, err := ReadToken(dir); err != nil || got != tok {
		t.Fatalf("ReadToken = %q, %v", got, err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	fi, err := os.Stat(TokenPath(dir))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode %v, %v; want 0600", fi.Mode(), err)
	}
	di, _ := os.Stat(dir)
	if di.Mode().Perm() != 0o700 {
		t.Errorf("state dir mode %v, want 0700", di.Mode().Perm())
	}
	// A token file other users can read is exposed: never trusted, replaced.
	if err := os.Chmod(TokenPath(dir), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadToken(dir); err == nil {
		t.Error("ReadToken trusted a world-readable token file")
	}
	fresh, err := LoadOrCreateToken(dir)
	if err != nil || fresh == tok {
		t.Errorf("exposed token kept: %q, %v", fresh, err)
	}
	// A symlinked token file is refused, not followed.
	other := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(other, []byte(tok+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(TokenPath(dir))
	if err := os.Symlink(other, TokenPath(dir)); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadToken(dir); err == nil {
		t.Error("ReadToken followed a symlink")
	}
}
