//go:build unix

package budget

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestReadProjectConfigFIFORefused(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, ".yakos.yml"), 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	var c projectConfig
	within(t, 5*time.Second, func() { c = readProjectConfig(dir) })
	if !strings.Contains(c.warn, "not a regular file") || !c.refused {
		t.Fatalf("warn = %q refused = %v", c.warn, c.refused)
	}
}
