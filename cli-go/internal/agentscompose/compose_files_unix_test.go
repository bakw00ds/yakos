//go:build !windows

package agentscompose

// compose_files_unix_test.go — the cases that need a FIFO, a device or file
// permissions: a symlink to a FIFO used to block Compose in open(2) for good, and
// a project file that cannot be read used to fail the whole roster (sec-324).

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// composeWithin runs Compose and fails the test if it has not returned in d. A
// reader stuck in open(2) on a FIFO is released first, so a failing run does not
// leave its goroutine behind.
func composeWithin(t *testing.T, d time.Duration, root, project string, fifos ...string) ([]ComposedAgent, error) {
	t.Helper()
	type result struct {
		roster []ComposedAgent
		err    error
	}
	done := make(chan result, 1)
	go func() {
		roster, err := Compose(root, project)
		done <- result{roster, err}
	}()
	select {
	case res := <-done:
		return res.roster, res.err
	case <-time.After(d):
		for _, f := range fifos {
			if w, err := os.OpenFile(f, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
				_ = w.Close()
			}
		}
		t.Fatalf("Compose did not return within %s: it is blocked on a FIFO", d)
		return nil, nil
	}
}

func mkfifoOrSkip(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("cannot create a FIFO here: %v", err)
	}
}

func rosterString(roster []ComposedAgent) string { return strings.Join(rosterIDs(roster), ",") }

// A symlink to a FIFO: opening it for reading waits for a writer that never
// comes. Compose must not open it.
func TestCompose_SkipsASymlinkToAFIFO(t *testing.T) {
	warnings := captureWarnings(t)
	root, project, agents := filesFixture(t)
	fifo := filepath.Join(project, "pipe")
	mkfifoOrSkip(t, fifo)
	link := filepath.Join(agents, "pipe.md")
	symlinkOrSkip(t, fifo, link)

	roster, err := composeWithin(t, 5*time.Second, root, project, fifo)
	if err != nil {
		t.Fatalf("Compose = %v", err)
	}
	if got := rosterString(roster); got != "backend,helper" {
		t.Errorf("roster = %q, want backend,helper", got)
	}
	requireWarning(t, warnings, link, "symlink does not resolve to a regular file")
}

// A FIFO that is itself the directory entry, no link involved.
func TestCompose_SkipsAFIFOEntry(t *testing.T) {
	warnings := captureWarnings(t)
	root, project, agents := filesFixture(t)
	entry := filepath.Join(agents, "pipe.md")
	mkfifoOrSkip(t, entry)

	roster, err := composeWithin(t, 5*time.Second, root, project, entry)
	if err != nil {
		t.Fatalf("Compose = %v", err)
	}
	if got := rosterString(roster); got != "backend,helper" {
		t.Errorf("roster = %q, want backend,helper", got)
	}
	requireWarning(t, warnings, entry, "not a regular file")
}

// A device is not a regular file either. /dev/zero is the one that would never
// end; /dev/null stands in for it here because a regression would read it to
// EOF at once and fail the test, where /dev/zero would eat memory until the
// timeout. The rule is the same for both.
func TestCompose_SkipsASymlinkToADevice(t *testing.T) {
	warnings := captureWarnings(t)
	root, project, agents := filesFixture(t)
	link := filepath.Join(agents, "null.md")
	symlinkOrSkip(t, "/dev/null", link)

	roster, err := composeWithin(t, 5*time.Second, root, project)
	if err != nil {
		t.Fatalf("Compose = %v", err)
	}
	if got := rosterString(roster); got != "backend,helper" {
		t.Errorf("roster = %q, want backend,helper (a device became an agent)", got)
	}
	requireWarning(t, warnings, link, "symlink does not resolve to a regular file")
}

// A failure to read a file in the project directory is a skip, never a failure
// of the whole roster. A framework file that cannot be read stays an error: that
// is a broken install, not something a clone can cause.
func TestCompose_AnUnreadableFileIsASkipInTheProjectAndAnErrorInTheFramework(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads files whatever their mode")
	}
	warnings := captureWarnings(t)
	root, project, agents := filesFixture(t)
	locked := filepath.Join(agents, "locked.md")
	writeFileT(t, locked, "---\nid: locked\n---\n\n## Purpose\n\nLocked.\n")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })

	if got, _ := composeIDs(t, root, project); got != "backend,helper" {
		t.Errorf("roster = %q, want backend,helper", got)
	}
	requireWarning(t, warnings, locked, "cannot be read")

	fwLocked := filepath.Join(root, "lib", "agents", "sealed.md")
	writeFileT(t, fwLocked, "---\nid: sealed\n---\n\n## Purpose\n\nSealed.\n")
	if err := os.Chmod(fwLocked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(fwLocked, 0o644) })
	if _, err := Compose(root, project); err == nil || !strings.Contains(err.Error(), "sealed.md") {
		t.Errorf("Compose = %v, want an error naming the unreadable framework file", err)
	}
}
