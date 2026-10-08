//go:build !windows

package install

// agents_gate_unix_test.go: ~/.claude/agents is global, so install links only the
// lib/agents entries Compose would read (K-167 fix round 1). Everything else is
// skipped with a warning and never linked.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/bakw00ds/yakos/internal/agentscompose"
)

func TestInstall_DoesNotLinkAgentsComposeRefuses(t *testing.T) {
	root := newFakeYakosRoot(t)
	agents := filepath.Join(root, "lib", "agents")
	outside := filepath.Join(t.TempDir(), "credentials.md")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, outside, filepath.Join(agents, "leak.md"))
	symlinkOrSkip(t, "loop-b.md", filepath.Join(agents, "loop-a.md"))
	symlinkOrSkip(t, "loop-a.md", filepath.Join(agents, "loop-b.md"))
	symlinkOrSkip(t, "/dev/zero", filepath.Join(agents, "zero.md"))
	symlinkOrSkip(t, "example.md", filepath.Join(agents, "alias.md"))
	if err := syscall.Mkfifo(filepath.Join(agents, "pipe.md"), 0o600); err != nil {
		t.Skipf("cannot create a FIFO here: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agents, "huge.md"), make([]byte, agentscompose.MaxAgentFileBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := baseConfig(t, root)
	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	dst := filepath.Join(cfg.HomeDir, ".claude", "agents")
	for _, name := range []string{"example.md", "alias.md"} {
		if _, err := os.Lstat(filepath.Join(dst, name)); err != nil {
			t.Errorf("%s was not linked: %v", name, err)
		}
	}
	stderr := cfg.ErrWriter.(*bytes.Buffer).String()
	for _, name := range []string{"leak.md", "loop-a.md", "loop-b.md", "zero.md", "pipe.md", "huge.md"} {
		if _, err := os.Lstat(filepath.Join(dst, name)); err == nil {
			t.Errorf("%s was linked into the global agents directory", name)
		}
		if !strings.Contains(stderr, "agents/"+name+" not linked") {
			t.Errorf("no warning for %s in %q", name, stderr)
		}
	}
}

func TestInstall_SkipLineCannotCarryANewline(t *testing.T) {
	root := newFakeYakosRoot(t)
	name := "a" + string(rune(10)) + "forged: line.md"
	if err := os.WriteFile(filepath.Join(root, "lib", "agents", name), make([]byte, agentscompose.MaxAgentFileBytes+1), 0o644); err != nil {
		t.Skipf("no newline in file names here: %v", err)
	}
	cfg := baseConfig(t, root)
	if _, err := Run(cfg); err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(cfg.ErrWriter.(*bytes.Buffer).String(), "\n") {
		if strings.HasPrefix(l, "forged:") {
			t.Errorf("a file name forged a log line: %q", l)
		}
	}
}
