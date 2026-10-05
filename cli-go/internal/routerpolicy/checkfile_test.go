package routerpolicy

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckFile_AppliesTheSameTrustRulesAsLoad pins K-137's reuse: `yakos
// doctor --policy` vets the other owner-only state file that steers dispatch
// (default-runtime) with exactly the rules Load applies to the policy file.
func TestCheckFile_AppliesTheSameTrustRulesAsLoad(t *testing.T) {
	skipIfNoPosixModes(t)
	dir := t.TempDir()

	ok := filepath.Join(dir, "ok")
	if err := os.WriteFile(ok, []byte("codex\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckFile(ok); err != nil {
		t.Errorf("a regular 0600 file you own is trusted, got %v", err)
	}

	for name, mode := range map[string]os.FileMode{"group-writable": 0o620, "world-writable": 0o602} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		err := CheckFile(p)
		if !errors.Is(err, ErrUntrusted) || !strings.Contains(err.Error(), "writable") {
			t.Errorf("%s: err = %v, want ErrUntrusted naming the writable bit", name, err)
		}
	}

	link := filepath.Join(dir, "link")
	if err := os.Symlink(ok, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := CheckFile(link); !errors.Is(err, ErrUntrusted) || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("a symlink is untrusted, got %v", err)
	}

	if err := CheckFile(dir); !errors.Is(err, ErrUntrusted) || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("a directory is untrusted, got %v", err)
	}
}

func TestCheckFile_MissingFileIsNotAnUntrustedFile(t *testing.T) {
	err := CheckFile(filepath.Join(t.TempDir(), "absent"))
	if err == nil || !os.IsNotExist(err) || errors.Is(err, ErrUntrusted) {
		t.Fatalf("an absent file must report not-exist and must not count as refused, got %v", err)
	}
}
