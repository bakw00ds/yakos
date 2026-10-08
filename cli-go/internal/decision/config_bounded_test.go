package decision

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// K-164: `yakos decide` reads ./.yakos.yml by default; a link to /dev/zero there
// must not be read. LoadConfig reports it as an error (callers treat that as
// "provider unavailable", never as enabled).
func TestLoadConfigRefusesALinkWithoutReadingIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	path := filepath.Join(t.TempDir(), ".yakos.yml")
	if err := os.Symlink("/dev/zero", path); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := LoadConfig(path); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("LoadConfig accepted a link to /dev/zero")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("LoadConfig read a link to /dev/zero")
	}
}
