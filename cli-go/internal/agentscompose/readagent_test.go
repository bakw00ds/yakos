package agentscompose

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readerFixture(t *testing.T) (root, project, outside string) {
	t.Helper()
	root, project = t.TempDir(), t.TempDir()
	for _, d := range []string{filepath.Join(root, "lib", "agents"), filepath.Join(project, ".claude", "agents")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	outside = filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outside, []byte("---\nid: x\n---\nsecret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, project, outside
}

func TestReadAgentFile_RefusesWhatComposeRefuses(t *testing.T) {
	root, project, outside := readerFixture(t)
	dir := filepath.Join(project, ".claude", "agents")
	if err := os.Symlink(outside, filepath.Join(dir, "linked.md")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "huge.md"), make([]byte, MaxAgentFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ok.md"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"linked.md", "huge.md"} {
		_, err := ReadAgentFile(root, project, filepath.Join(dir, name))
		if !errors.Is(err, ErrRefused) {
			t.Errorf("%s: err = %v, want ErrRefused", name, err)
		}
		if err != nil && strings.Contains(err.Error(), outside) {
			t.Errorf("%s: the refusal names a path: %v", name, err)
		}
	}
	if b, err := ReadAgentFile(root, project, filepath.Join(dir, "ok.md")); err != nil || string(b) != "hi" {
		t.Errorf("plain file: %q, %v", b, err)
	}
	if _, err := ReadAgentFile(root, project, filepath.Join(dir, "none.md")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing file: %v, want ErrNotExist", err)
	}
	// A link into lib/agents is the installed layout and stays readable.
	if err := os.WriteFile(filepath.Join(root, "lib", "agents", "fw.md"), []byte("fw"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "lib", "agents", "fw.md"), filepath.Join(dir, "fw.md")); err != nil {
		t.Fatal(err)
	}
	if b, err := ReadAgentFile(root, project, filepath.Join(dir, "fw.md")); err != nil || string(b) != "fw" {
		t.Errorf("link into lib/agents: %q, %v", b, err)
	}
}

func TestReadAgentFile_RefusesALinkedProjectAgentsDir(t *testing.T) {
	root, project, _ := readerFixture(t)
	real := filepath.Join(t.TempDir(), "agents")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "a.md"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(project, ".claude", "agents")
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, dir); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if _, err := ReadAgentFile(root, project, filepath.Join(dir, "a.md")); !errors.Is(err, ErrRefused) {
		t.Errorf("err = %v, want ErrRefused", err)
	}
}

func TestReadExtendsTemplate_BareIDInLibAgentsOnly(t *testing.T) {
	root, project, outside := readerFixture(t)
	if err := os.WriteFile(filepath.Join(root, "lib", "agents", "base.md"), []byte("base"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A project agent of the same name is not a template.
	if err := os.WriteFile(filepath.Join(project, ".claude", "agents", "mine.md"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, err := ReadExtendsTemplate(root, project, "base"); err != nil || string(b) != "base" {
		t.Fatalf("base: %q, %v", b, err)
	}
	if _, err := ReadExtendsTemplate(root, project, "mine"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a project agent as template: %v, want ErrNotExist", err)
	}
	for _, id := range []string{"../../" + strings.TrimSuffix(filepath.Base(outside), ".md"), "a/b", "/etc/passwd", "..", ".hidden", ""} {
		if _, err := ReadExtendsTemplate(root, project, id); !errors.Is(err, ErrRefused) {
			t.Errorf("extends %q: err = %v, want ErrRefused", id, err)
		}
	}
	// A link in lib/agents that leaves lib/agents is refused too.
	if err := os.Symlink(outside, filepath.Join(root, "lib", "agents", "evil.md")); err == nil {
		if _, err := ReadExtendsTemplate(root, project, "evil"); !errors.Is(err, ErrRefused) {
			t.Errorf("template link out of lib/agents: %v", err)
		}
	}
}

// The linked-directory check compares directories, not strings: a project root
// spelled another way (here a symlink to it) than the agent path still gets it.
func TestReadAgentFile_LinkedDirCheckSurvivesAnotherSpellingOfTheRoot(t *testing.T) {
	root, project, _ := readerFixture(t)
	real := filepath.Join(t.TempDir(), "agents")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "a.md"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(project, ".claude", "agents")
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, dir); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(project, alias); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	// The root is the alias; the path is spelled through the real project.
	if _, err := ReadAgentFile(root, alias, filepath.Join(dir, "a.md")); !errors.Is(err, ErrRefused) {
		t.Errorf("err = %v, want ErrRefused", err)
	}
}
