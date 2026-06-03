//go:build windows

package install

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSymlinkFile_DevModeEnabled verifies that when the DevMode probe succeeds,
// symlinkFile uses os.Symlink directly and creates a real symlink.
func TestSymlinkFile_DevModeEnabled(t *testing.T) {
	// Inject a probe that always reports DevMode available.
	ResetDevModeProbe(func() bool { return true })
	t.Cleanup(func() { ResetDevModeProbe(nil) })

	tmp := t.TempDir()
	target := filepath.Join(tmp, "target.txt")
	link := filepath.Join(tmp, "link.txt")

	if err := os.WriteFile(target, []byte("devmode content"), 0644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	if err := symlinkFile(target, link, nil); err != nil {
		t.Fatalf("symlinkFile (DevMode=true): %v", err)
	}

	// The result must be a real symlink, not a copy.
	linfo, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("Lstat link: %v", err)
	}
	if linfo.Mode()&os.ModeSymlink == 0 {
		t.Errorf("expected real symlink when DevMode is enabled; got mode %v", linfo.Mode())
	}

	// Reading the link should yield the target content.
	got, err := os.ReadFile(link)
	if err != nil {
		t.Fatalf("ReadFile via link: %v", err)
	}
	if string(got) != "devmode content" {
		t.Errorf("link content: got %q, want %q", string(got), "devmode content")
	}
}

// TestSymlinkFile_DevModeDisabled verifies that when the DevMode probe fails,
// symlinkFile falls back to copy (for file targets) rather than producing a
// symlink.  The copy fallback is the safe path for stock Windows without
// Developer Mode.
func TestSymlinkFile_DevModeDisabled(t *testing.T) {
	// Inject a probe that always reports DevMode unavailable.
	ResetDevModeProbe(func() bool { return false })
	t.Cleanup(func() { ResetDevModeProbe(nil) })

	tmp := t.TempDir()
	target := filepath.Join(tmp, "target.txt")
	link := filepath.Join(tmp, "copy.txt")

	if err := os.WriteFile(target, []byte("fallback content"), 0644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	// On stock Windows (no elevated process), os.Symlink will fail in the
	// fallback branch and the copy path will be taken.  We inject a nil
	// junctionFn because target is a file, not a directory.
	//
	// Note: if the test runner happens to be elevated (e.g. an administrator
	// CI agent), os.Symlink may succeed in the fallback branch even though our
	// probe returned false.  In that case the result is still a valid artifact
	// at link — we accept either a symlink or a copy, but the file must be
	// readable with the right content.
	if err := symlinkFile(target, link, nil); err != nil {
		t.Fatalf("symlinkFile (DevMode=false): %v", err)
	}

	// The link/copy must exist and be readable.
	got, err := os.ReadFile(link)
	if err != nil {
		t.Fatalf("ReadFile result: %v", err)
	}
	if string(got) != "fallback content" {
		t.Errorf("result content: got %q, want %q", string(got), "fallback content")
	}
}
