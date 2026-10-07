package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/agentscompose"
)

// The agent commands read project agents through agentscompose's reader: a link
// out of the agent directories, an oversize file and an extends outside
// lib/agents are refused, not read.

const okAgent = "---\nid: %s\nrole: specialist\ndomain: api\n---\n\n## Purpose\n\nx.\n"

func plantEscape(t *testing.T, proj string) (secret string) {
	t.Helper()
	secret = filepath.Join(t.TempDir(), "creds.md")
	if err := os.WriteFile(secret, []byte("---\nid: creds\nrole: r\ndomain: d\n---\n\n## Purpose\n\nTOPSECRET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(proj, ".claude", "agents", "linked.md")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	return secret
}

func TestLint_RefusesOutOfRootSymlinkAndOversize(t *testing.T) {
	root := buildFixture(t, nil)
	proj := buildProjectDir(t, nil)
	secret := plantEscape(t, proj)
	if err := os.WriteFile(filepath.Join(proj, ".claude", "agents", "huge.md"), make([]byte, agentscompose.MaxAgentFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	out, r, err := runCapture(t, Config{YakosRoot: root, Subcommand: "lint", Project: proj})
	if err != nil {
		t.Fatal(err)
	}
	if r.Errors != 2 {
		t.Errorf("errors = %d, want 2 (the link and the oversize file)\n%s", r.Errors, out)
	}
	for _, name := range []string{"linked.md", "huge.md"} {
		if !strings.Contains(out, name+": cannot read file") {
			t.Errorf("%s not refused:\n%s", name, out)
		}
	}
	if strings.Contains(out, secret) || strings.Contains(out, "TOPSECRET") {
		t.Errorf("the output leaks the target:\n%s", out)
	}
}

func TestLint_ExtendsMustBeABareIDInLibAgents(t *testing.T) {
	root := buildFixture(t, map[string]string{"base.md": "---\nid: base\n---\n\n## Purpose\n\nb.\n"})
	proj := buildProjectDir(t, map[string]string{
		"viaproj.md": "---\nid: viaproj\nextends: mine\nrole: r\ndomain: d\n---\n\n## Purpose\n\nx.\n",
		"mine.md":    "---\nid: mine\nrole: r\ndomain: d\n---\n\n## Purpose\n\nx.\n",
		"escape.md":  "---\nid: escape\nextends: ../../../etc/passwd\nrole: r\ndomain: d\n---\n\n## Purpose\n\nx.\n",
		"good.md":    "---\nid: good\nextends: base\nextends-version: 1\nrole: r\ndomain: d\n---\n\n## Purpose\n\nx.\n",
	})
	out, r, err := runCapture(t, Config{YakosRoot: root, Subcommand: "lint", Project: proj})
	if err != nil {
		t.Fatal(err)
	}
	if r.Errors != 2 {
		t.Errorf("errors = %d, want 2 (a project template, a path)\n%s", r.Errors, out)
	}
	if !strings.Contains(out, `viaproj.md: extends "mine" not found in framework agents`) {
		t.Errorf("a project agent is not an extends template:\n%s", out)
	}
	if !strings.Contains(out, "escape.md: extends: agent file refused") {
		t.Errorf("a path is not a bare id:\n%s", out)
	}
}

func TestDiffAndNew_RefuseExtendsOutsideLibAgents(t *testing.T) {
	root := buildFixture(t, nil)
	proj := buildProjectDir(t, map[string]string{
		"escape.md": "---\nid: escape\nextends: ../../x\nrole: r\ndomain: d\n---\n\n## Purpose\n\nx.\n",
	})
	if _, _, err := runCapture(t, Config{YakosRoot: root, Subcommand: "diff", Name: "escape", Project: proj}); !errors.Is(err, agentscompose.ErrRefused) {
		t.Errorf("diff: err = %v, want ErrRefused", err)
	}
	if _, _, err := runCapture(t, Config{YakosRoot: root, Subcommand: "new", Name: "child", Project: proj, Extends: "../escape"}); !errors.Is(err, agentscompose.ErrRefused) {
		t.Errorf("new: err = %v, want ErrRefused", err)
	}
	if _, err := os.Stat(filepath.Join(proj, ".claude", "agents", "child.md")); err == nil {
		t.Error("new wrote the agent despite the refused extends")
	}
}

func TestDiff_RefusesALinkedProjectAgent(t *testing.T) {
	root := buildFixture(t, nil)
	proj := buildProjectDir(t, nil)
	plantEscape(t, proj)
	_, _, err := runCapture(t, Config{YakosRoot: root, Subcommand: "diff", Name: "linked", Project: proj})
	if !errors.Is(err, agentscompose.ErrRefused) {
		t.Errorf("err = %v, want ErrRefused", err)
	}
}
