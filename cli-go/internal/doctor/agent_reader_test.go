package doctor

// agent_reader_test.go: the doctor reads a project's .claude/agents through the
// roster reader (K-167), so a cloned repository cannot make it follow a link out
// of the agent directories, read a file past the size cap, or read through a
// linked agents directory.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bakw00ds/yakos/internal/agentscompose"
)

const goodAgent = "---\nid: good\ntools: [Read]\n---\nbody\n"

func agentFixture(t *testing.T) (root, project, agents string) {
	t.Helper()
	root, project = t.TempDir(), t.TempDir()
	agents = filepath.Join(project, ".claude", "agents")
	for _, d := range []string{filepath.Join(root, "lib", "agents"), agents} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root, project, agents
}

func TestAuditAgents_RefusesLinkOutOfRootAndHugeFile(t *testing.T) {
	root, project, agents := agentFixture(t)
	writeFile(t, filepath.Join(agents, "good.md"), goodAgent)
	// An out-of-root link to a file that, if read, would count as "tools: []".
	outside := filepath.Join(t.TempDir(), "elsewhere.md")
	writeFile(t, outside, "---\ntools: []\n---\n")
	if err := os.Symlink(outside, filepath.Join(agents, "linked.md")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	huge := append([]byte("---\ntools: []\n---\n"), make([]byte, agentscompose.MaxAgentFileBytes)...)
	if err := os.WriteFile(filepath.Join(agents, "huge.md"), huge, 0o600); err != nil {
		t.Fatal(err)
	}

	count, missing, empty, refused := auditAgents(root, project)
	if count != 1 || missing != 0 || empty != 0 || refused != 2 {
		t.Errorf("count=%d missing=%d empty=%d refused=%d; want 1 0 0 2", count, missing, empty, refused)
	}
}

func TestAuditAgents_LinkedAgentsDirectoryIsNotRead(t *testing.T) {
	root, project := t.TempDir(), t.TempDir()
	real := filepath.Join(t.TempDir(), "agents")
	writeFile(t, filepath.Join(real, "good.md"), goodAgent)
	if err := os.MkdirAll(filepath.Join(project, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(project, ".claude", "agents")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	count, _, _, refused := auditAgents(root, project)
	if count != 0 || refused != 1 {
		t.Errorf("count=%d refused=%d; want 0 and 1 for a linked directory", count, refused)
	}
}

func TestCountAgentFiles_CountsOnlyWhatComposeReads(t *testing.T) {
	root, project, agents := agentFixture(t)
	fw := filepath.Join(root, "lib", "agents")
	writeFile(t, filepath.Join(fw, "a.md"), goodAgent)
	writeFile(t, filepath.Join(fw, "README.md"), "x")
	writeFile(t, filepath.Join(agents, "b.md"), goodAgent)
	outside := filepath.Join(t.TempDir(), "elsewhere.md")
	writeFile(t, outside, goodAgent)
	if err := os.Symlink(outside, filepath.Join(agents, "linked.md")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(fw, "linked.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agents, "huge.md"), make([]byte, agentscompose.MaxAgentFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if n := countAgentFiles(root, "", fw, "README.md"); n != 1 {
		t.Errorf("framework count = %d, want 1", n)
	}
	if n := countAgentFiles(root, project, agents, "README.md"); n != 1 {
		t.Errorf("project count = %d, want 1", n)
	}
}

func TestCountAgentFiles_LinkedProjectDirectoryCountsZero(t *testing.T) {
	root, project := t.TempDir(), t.TempDir()
	real := filepath.Join(t.TempDir(), "agents")
	writeFile(t, filepath.Join(real, "good.md"), goodAgent)
	if err := os.MkdirAll(filepath.Join(project, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(project, ".claude", "agents")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if n := countAgentFiles(root, project, link, "README.md"); n != 0 {
		t.Errorf("count = %d, want 0 through a linked directory", n)
	}
}
