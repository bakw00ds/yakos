//go:build unix

package doctor

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// `doctor --production` read .yakos.yml with os.ReadFile: a FIFO there hung it for good.
func TestProductionDoesNotHangOnAFIFOProjectFile(t *testing.T) {
	proj := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(proj, ".yakos.yml"), 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan string, 1)
	go func() {
		done <- fullDoctor(t, makeTmpHome(t), Config{ProjectPath: proj, Production: true})
	}()
	select {
	case out := <-done:
		if !strings.Contains(out, ".yakos.yml ignored: is not a regular file") {
			t.Fatalf("production output:\n%s", out)
		}
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, ".yakos.yml ignored") && strings.Contains(l, proj) {
				t.Fatalf("the notice names the project path: %q", l)
			}
		}
	case <-time.After(20 * time.Second):
		t.Fatal("doctor --production did not return on a FIFO .yakos.yml")
	}
}
