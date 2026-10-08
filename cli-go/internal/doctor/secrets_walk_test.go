package doctor

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// K-164: the production secret scan walks the project and opens every file. A link
// to /dev/zero is small by Lstat and reads without end; it must be skipped.
func TestHasObviousSecretsSkipsLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	proj := t.TempDir()
	if err := os.Symlink("/dev/zero", filepath.Join(proj, "zero")); err != nil {
		t.Fatal(err)
	}
	done := make(chan bool, 1)
	go func() { done <- hasObviousSecrets(proj) }()
	select {
	case found := <-done:
		if found {
			t.Fatal("found a secret in an empty project")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the secret scan read a link to /dev/zero")
	}
}
