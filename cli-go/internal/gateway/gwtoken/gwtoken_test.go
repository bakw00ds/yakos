package gwtoken

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var testStore = Store{File: "t-token", Label: "test"}

func TestMatchFailsClosedOnAnUnusableConfiguredToken(t *testing.T) {
	good := strings.Repeat("ab", 32)
	if !Match(good, good) {
		t.Fatal("the right token did not match")
	}
	for _, want := range []string{"", "short", strings.Repeat("a", MinLen-1)} {
		for _, got := range []string{"", want, " ", good} {
			if Match(want, got) {
				t.Errorf("Match(%q, %q) = true; an unusable configured token must admit nobody", want, got)
			}
		}
	}
	if Match(good, "") || Match(good, good+"x") || Match(good, good[:len(good)-1]) {
		t.Error("a wrong offered token matched")
	}
}

func TestStoreMintsPrivateFileAndRefusesUntrustedOnes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if _, err := testStore.Read(dir); err == nil {
		t.Fatal("Read invented a token")
	}
	tok, err := testStore.LoadOrCreate(dir)
	if err != nil || !Valid(tok) {
		t.Fatalf("LoadOrCreate = %q, %v", tok, err)
	}
	if again, err := testStore.LoadOrCreate(dir); err != nil || again != tok {
		t.Fatalf("second load = %q, %v; want the same token", again, err)
	}
	rotated, err := testStore.Rotate(dir)
	if err != nil || rotated == tok {
		t.Fatalf("Rotate = %q, %v; want a different token", rotated, err)
	}
	if got, _ := testStore.Read(dir); got != rotated {
		t.Fatalf("Read = %q, want the rotated token", got)
	}
	if runtime.GOOS == "windows" {
		return
	}
	p := testStore.Path(dir)
	if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, %v; want 0600", fi.Mode(), err)
	}
	for _, mode := range []os.FileMode{0o640, 0o604, 0o644} {
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := testStore.Read(dir); err == nil {
			t.Errorf("Read trusted a token file with mode %o", mode)
		}
	}
	fresh, err := testStore.LoadOrCreate(dir)
	if err != nil || fresh == rotated {
		t.Errorf("an exposed token was kept: %q, %v", fresh, err)
	}
	// A symlink is refused, not followed, even to a private file.
	other := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(other, []byte(fresh+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(p)
	if err := os.Symlink(other, p); err != nil {
		t.Fatal(err)
	}
	if _, err := testStore.Read(dir); err == nil {
		t.Error("Read followed a symlink")
	}
	// A malformed body is not a token.
	_ = os.Remove(p)
	if err := os.WriteFile(p, []byte("not-hex\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := testStore.Read(dir); err == nil {
		t.Error("Read accepted a malformed token")
	}
}
