package refresh

// symlinks_hardening_test.go: ~/.claude/agents is global, so syncAgents links only
// what the roster reader would read from lib/agents (K-167).

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/agentscompose"
)

func TestSyncAgents_DoesNotLinkWhatComposeRefuses(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	src := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(home, ".claude", "agents")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "good.md"), []byte("# good\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "huge.md"), make([]byte, agentscompose.MaxAgentFileBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "credentials.md")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(src, "leak.md")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if err := os.Symlink("good.md", filepath.Join(src, "alias.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "nowhere"), filepath.Join(src, "dangling.md")); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	rpt, err := syncAgents(root, home, false, &out)
	if err != nil {
		t.Fatal(err)
	}
	if rpt.New != 2 || rpt.Warns != 3 {
		t.Errorf("rpt = %+v; want new=2 (good, alias) warns=3 (huge, leak, dangling)", rpt)
	}
	for _, name := range []string{"good.md", "alias.md"} {
		if _, err := os.Lstat(filepath.Join(dst, name)); err != nil {
			t.Errorf("%s not linked: %v", name, err)
		}
	}
	for _, name := range []string{"huge.md", "leak.md", "dangling.md"} {
		if _, err := os.Lstat(filepath.Join(dst, name)); err == nil {
			t.Errorf("%s was linked into the global agents directory", name)
		}
		if !strings.Contains(out.String(), name+" not linked") {
			t.Errorf("no warning for %s in %q", name, out.String())
		}
	}
}

// A link an earlier refresh made to a file that is refused now stays in the
// global roster unless refresh removes it. Only a link to that very source goes:
// one that points elsewhere, and a real file, are not refresh's to touch.
func TestSyncAgents_RemovesAStaleLinkToARefusedSourceOnly(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	src := filepath.Join(root, "lib", "agents")
	dst := filepath.Join(home, ".claude", "agents")
	for _, d := range []string{src, dst} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "credentials.md")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(src, "leak.md")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "huge.md"), make([]byte, agentscompose.MaxAgentFileBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "real.md"), make([]byte, agentscompose.MaxAgentFileBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(t.TempDir(), "mine.md")
	if err := os.WriteFile(elsewhere, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// leak.md: stale link to the refused source. huge.md: the operator's own link
	// elsewhere. real.md: a real file.
	if err := os.Symlink(filepath.Join(src, "leak.md"), filepath.Join(dst, "leak.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(dst, "huge.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "real.md"), []byte("operator\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var dry bytes.Buffer
	if _, err := syncAgents(root, home, true, &dry); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dst, "leak.md")); err != nil {
		t.Fatalf("dry-run removed the stale link: %v", err)
	}
	if !strings.Contains(dry.String(), "would remove stale symlink leak.md") {
		t.Errorf("dry-run did not say it would remove the link: %q", dry.String())
	}

	var out bytes.Buffer
	if _, err := syncAgents(root, home, false, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dst, "leak.md")); err == nil {
		t.Errorf("the stale link to a refused source was kept")
	}
	if !strings.Contains(out.String(), "removed stale symlink leak.md") {
		t.Errorf("no warning for the removed link: %q", out.String())
	}
	if got, err := os.Readlink(filepath.Join(dst, "huge.md")); err != nil || got != elsewhere {
		t.Errorf("a link that points elsewhere was touched: %q, %v", got, err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "real.md")); err != nil || string(b) != "operator\n" {
		t.Errorf("a real file was touched: %q, %v", b, err)
	}
}

// A link whose target name ends in a newline is judged by the real target, as the
// bash twin now does (it once judged it by a decoy named without the newline).
func TestSyncAgents_LinkToANameEndingInANewlineIsJudgedByTheRealTarget(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	src := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "big"), []byte("decoy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "big"+string(rune(10))), make([]byte, agentscompose.MaxAgentFileBytes+1), 0o644); err != nil {
		t.Skipf("no newline in file names here: %v", err)
	}
	if err := os.Symlink("big"+string(rune(10)), filepath.Join(src, "nl.md")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	var out bytes.Buffer
	if _, err := syncAgents(root, home, false, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "agents", "nl.md")); err == nil {
		t.Errorf("a link to an oversize file was linked")
	}
	if !strings.Contains(out.String(), "nl.md not linked") {
		t.Errorf("no warning: %q", out.String())
	}
}
