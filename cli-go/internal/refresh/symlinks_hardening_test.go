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
