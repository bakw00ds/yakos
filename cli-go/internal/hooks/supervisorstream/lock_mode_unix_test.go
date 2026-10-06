//go:build !windows

package supervisorstream

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// The lock file is owner-only whatever the caller's umask (K-128, S5): the bash
// hook and its wrapper create theirs the same way, so a mixed fleet leaves one
// kind of file. No test in this package runs in parallel, so changing the
// process umask for the length of one is safe.
func TestLockFileIsOwnerOnlyWhateverTheUmask(t *testing.T) {
	for _, mask := range []int{0, 0o022, 0o077} {
		old := syscall.Umask(mask)
		lock := filepath.Join(t.TempDir(), "a.lock")
		ok := tryLock(lock)
		syscall.Umask(old)
		if !ok {
			t.Fatalf("umask %04o: could not create a free lock", mask)
		}
		fi, err := os.Stat(lock)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("umask %04o: lock file mode = %v, want 0600", mask, fi.Mode().Perm())
		}
	}
}
