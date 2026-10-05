package statepath

import (
	"path/filepath"
	"testing"
)

// TestTrustedDir_IgnoresDispatchLogRelocation: Dir honors YAKOS_DISPATCH_LOG
// (an operator may relocate the ledger), but TrustedDir must not, because a
// project can set environment variables for the processes it spawns (K-129).
func TestTrustedDir_IgnoresDispatchLogRelocation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	reloc := filepath.Join(t.TempDir(), "relocated")
	t.Setenv("YAKOS_DISPATCH_LOG", reloc)

	if got := Dir(); got != reloc {
		t.Fatalf("Dir() = %q, want the relocated %q", got, reloc)
	}
	want := filepath.Join(home, ".yakos-state")
	if got := TrustedDir(); got != want {
		t.Fatalf("TrustedDir() = %q, want %q", got, want)
	}
}
