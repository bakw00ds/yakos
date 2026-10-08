package budget

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writeProject(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".yakos.yml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// within fails the test if fn does not return by the deadline, so a regression
// that blocks on a FIFO reports instead of hanging the run.
func within(t *testing.T, d time.Duration, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); fn() }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("read did not return within %s", d)
	}
}

func TestReadProjectConfigRegularFile(t *testing.T) {
	dir := writeProject(t, "agent_budgets:\n  backend: 5\nsupervisor:\n  agent: chief\n")
	c := readProjectConfig(dir)
	if c.warn != "" {
		t.Fatalf("warn = %q", c.warn)
	}
	if v := c.projectUSD("backend"); v == nil || *v != 5 {
		t.Fatalf("backend = %v", v)
	}
	if c.aliasFor("chief") != supervisorAgent {
		t.Fatalf("supervisor alias lost: %v", c.supervisor)
	}
}

func TestReadProjectConfigMissingIsQuiet(t *testing.T) {
	c := readProjectConfig(t.TempDir())
	if c.warn != "" || len(c.limits) != 0 {
		t.Fatalf("missing file: %+v", c)
	}
}

func TestReadProjectConfigOversized(t *testing.T) {
	// A valid prefix with a limit in it, padded past the cap by a comment: the
	// whole file is refused, its limits are not used.
	body := "agent_budgets:\n  backend: 5\n#" + strings.Repeat("x", MaxProjectFileBytes)
	c := readProjectConfig(writeProject(t, body))
	if !strings.Contains(c.warn, "larger than") || len(c.limits) != 0 {
		t.Fatalf("oversized file accepted: %+v", c)
	}
	// Exactly at the cap is still read.
	pad := MaxProjectFileBytes - len("agent_budgets:\n  backend: 5\n#")
	c = readProjectConfig(writeProject(t, "agent_budgets:\n  backend: 5\n#"+strings.Repeat("x", pad)))
	if c.warn != "" || c.projectUSD("backend") == nil {
		t.Fatalf("file at the cap refused: %+v", c)
	}
}

func TestReadProjectConfigSymlinkRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	real := writeProject(t, "agent_budgets:\n  backend: 5\n")
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(real, ".yakos.yml"), filepath.Join(dir, ".yakos.yml")); err != nil {
		t.Fatal(err)
	}
	c := readProjectConfig(dir)
	if !strings.Contains(c.warn, "symlink") || len(c.limits) != 0 {
		t.Fatalf("symlink followed: %+v", c)
	}
}

func TestReadProjectConfigDeviceSymlinkRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /dev/zero")
	}
	dir := t.TempDir()
	if err := os.Symlink("/dev/zero", filepath.Join(dir, ".yakos.yml")); err != nil {
		t.Fatal(err)
	}
	var c projectConfig
	within(t, 5*time.Second, func() { c = readProjectConfig(dir) })
	if !strings.Contains(c.warn, "symlink") {
		t.Fatalf("warn = %q", c.warn)
	}
}

func TestReadProjectConfigDirectoryRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".yakos.yml"), 0o700); err != nil {
		t.Fatal(err)
	}
	if c := readProjectConfig(dir); !strings.Contains(c.warn, "not a regular file") {
		t.Fatalf("warn = %q", c.warn)
	}
}

func TestReadProjectFileSwappedAfterLstat(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs symlinks and Unix open flags")
	}
	dir := writeProject(t, "agent_budgets:\n  backend: 5\n")
	target := writeProject(t, "agent_budgets:\n  backend: 999\n")
	afterProjectLstat = func(path string) {
		_ = os.Remove(path)
		_ = os.Symlink(filepath.Join(target, ".yakos.yml"), path)
	}
	defer func() { afterProjectLstat = func(string) {} }()
	if _, err := readProjectFile(dir); err == nil {
		t.Fatal("file swapped for a symlink after the Lstat was read")
	}
}

// A different regular file takes the place of the inspected one between the
// Lstat and the open: only the identity check can tell.
func TestReadProjectFileReplacedAfterLstat(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("rename over an open path differs on Windows")
	}
	dir := writeProject(t, "agent_budgets:\n  backend: 5\n")
	other := filepath.Join(dir, "other")
	if err := os.WriteFile(other, []byte("agent_budgets:\n  backend: 999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	afterProjectLstat = func(path string) { _ = os.Rename(other, path) }
	defer func() { afterProjectLstat = func(string) {} }()
	if _, err := readProjectFile(dir); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("replaced file accepted: %v", err)
	}
}
