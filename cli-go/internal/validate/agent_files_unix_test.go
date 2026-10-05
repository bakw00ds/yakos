//go:build !windows

package validate

// agent_files_unix_test.go — a FIFO among the agent files. Every pass that reads
// an agent file would block on it for good, so each of them skips what it cannot
// read safely, and the one place that reports it is checkAgentEnums. The runs are
// under a timeout, and a reader stuck in open(2) is released afterwards.

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func releaseFIFO(path string) {
	if w, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
		_ = w.Close()
	}
}

// within runs fn and fails the test if it has not returned in d.
func within(t *testing.T, d time.Duration, fifo string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { fn(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		stacks := make([]byte, 1<<16)
		stacks = stacks[:runtime.Stack(stacks, true)]
		releaseFIFO(fifo)
		t.Fatalf("validate did not return within %s: a pass is blocked on the FIFO\n%s", d, stacks)
	}
}

func TestAgentFiles_ASymlinkToAFIFOIsRejectedWithoutBlocking(t *testing.T) {
	root, proj, agents := agentFilesProject(t)
	fifo := filepath.Join(proj, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("cannot create a FIFO here: %v", err)
	}
	link := filepath.Join(agents, "pipe.md")
	symlinkOrSkip(t, fifo, link)
	var out string
	var errs []string
	within(t, 10*time.Second, fifo, func() { out, errs = validateProject(t, root, proj) })
	wantOneError(t, out, errs, link, msgUnresolved)
}

func TestAgentFiles_AFIFOEntryIsRejectedWithoutBlocking(t *testing.T) {
	root, proj, agents := agentFilesProject(t)
	entry := filepath.Join(agents, "pipe.md")
	if err := syscall.Mkfifo(entry, 0o600); err != nil {
		t.Skipf("cannot create a FIFO here: %v", err)
	}
	var out string
	var errs []string
	within(t, 10*time.Second, entry, func() { out, errs = validateProject(t, root, proj) })
	wantOneError(t, out, errs, entry, "not a regular file; the Go dispatcher skips it")
}

// Framework mode reads the agents in more passes, the section check among them.
// The standards checks that walk the whole framework tree are not in play here:
// that tree is the framework's own, and the exposure is a cloned project's.
func TestAgentFiles_FrameworkAgentPassesDoNotBlockOnAFIFO(t *testing.T) {
	root := t.TempDir()
	lib := filepath.Join(root, "lib")
	agents := filepath.Join(lib, "agents")
	writeFile(t, filepath.Join(agents, "real.md"), agentBody("real"))
	fifo := filepath.Join(root, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("cannot create a FIFO here: %v", err)
	}
	link := filepath.Join(agents, "pipe.md")
	symlinkOrSkip(t, fifo, link)

	t.Setenv("HOME", t.TempDir())
	var buf bytes.Buffer
	cfg := Config{YakosRoot: root, Writer: &buf, ErrWriter: &buf}
	r := &Result{}
	within(t, 10*time.Second, fifo, func() {
		validateTree(cfg, r, &buf, "framework", lib)
		checkAgentMDSections(cfg, r, &buf)
	})
	var got []string
	for _, f := range r.Findings {
		if f.Level == LevelErr && strings.HasPrefix(f.Message, link+": ") {
			got = append(got, f.Message)
		}
	}
	if len(got) != 1 || got[0] != link+": "+msgUnresolved {
		t.Errorf("errors for the FIFO link = %q, want exactly %q\n%s", got, link+": "+msgUnresolved, buf.String())
	}
}
