package agentscompose

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// writeAgents writes "<id>.md" files with the given frontmatter lines into dir.
func writeAgentDir(t *testing.T, dir string, agents map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for id, fm := range agents {
		body := "---\nid: " + id + "\n" + fm + "---\n\n## Purpose\n\nFixture " + id + ".\n"
		if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func captureWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	orig := WarnWriter
	WarnWriter = &buf
	warnedPaths = sync.Map{}
	t.Cleanup(func() { WarnWriter = orig; warnedPaths = sync.Map{} })
	return &buf
}

// sec-324 F4: an agent named after a runtime would shadow the generic agent of
// that name. Compose skips it, from the project and from the framework, so a
// cloned repository cannot send `yakos dispatch claude` or the console's default
// pane to another vendor with a frontmatter pin.
func TestCompose_SkipsAgentsNamedAfterARuntime(t *testing.T) {
	warnings := captureWarnings(t)
	root, project := t.TempDir(), t.TempDir()
	writeAgentDir(t, filepath.Join(root, "lib", "agents"), map[string]string{
		"backend": "model: sonnet\n",
		"codex":   "runtime: agy\n", // a framework file named after a runtime
	})
	writeAgentDir(t, filepath.Join(project, ".claude", "agents"), map[string]string{
		"claude":  "runtime: codex\n", // the sec-324 probe: a clone's pin for the default pane
		"agy":     "runtime: codex\n",
		"helper":  "model: haiku\n",
		"backend": "model: opus\n", // an ordinary override still works
	})

	roster, err := Compose(root, project)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, a := range roster {
		ids = append(ids, a.ID)
		if IsKnownRuntime(a.ID) {
			t.Errorf("roster contains an agent named after a runtime: %q", a.ID)
		}
	}
	if got := strings.Join(ids, ","); got != "backend,helper" {
		t.Errorf("roster = %s, want backend,helper (the runtime-named files skipped, the override kept)", got)
	}
	for _, a := range roster {
		if a.ID == "backend" && a.Model != "opus" {
			t.Errorf("project override of backend lost: model %q", a.Model)
		}
	}
	for _, name := range []string{"claude.md", "agy.md", "codex.md"} {
		if !strings.Contains(warnings.String(), name) {
			t.Errorf("no warning about skipping %s: %q", name, warnings.String())
		}
	}
}

// A daemon composes the roster on every request; the warning is once per file.
func TestCompose_WarnsOncePerSkippedFile(t *testing.T) {
	warnings := captureWarnings(t)
	root := t.TempDir()
	writeAgentDir(t, filepath.Join(root, "lib", "agents"), map[string]string{"claude": "runtime: codex\n"})
	for i := 0; i < 5; i++ {
		if _, err := Compose(root, ""); err != nil {
			t.Fatal(err)
		}
	}
	if n := strings.Count(warnings.String(), "claude.md"); n != 1 {
		t.Errorf("warned %d times for one file across 5 composes, want 1:\n%s", n, warnings.String())
	}
}

// Agents that merely contain a runtime's name are fine.
func TestCompose_KeepsAgentsWhoseNameOnlyContainsARuntime(t *testing.T) {
	captureWarnings(t)
	root := t.TempDir()
	writeAgentDir(t, filepath.Join(root, "lib", "agents"), map[string]string{
		"general-codex": "runtime: codex\n",
		"claude-helper": "model: haiku\n",
		"agyx":          "model: haiku\n",
	})
	roster, err := Compose(root, "")
	if err != nil || len(roster) != 3 {
		t.Fatalf("roster = %v, err %v, want all three kept", roster, err)
	}
}

// The line endings of an agent file do not change what it composes to: the
// claude relay's --agents JSON is part of the cached prefix, so a Windows
// checkout (CRLF) of the same file must not bust the cache for every dispatch.
func TestAgentToJSON_IsTheSameForCRLFAndLFCheckouts(t *testing.T) {
	const def = "---\nid: crlf-agent\ndescription: x\ntools: Read, Edit\nmodel: sonnet # a tier\nruntime: claude\n---\n\n" +
		"# crlf-agent\n\n## Purpose\n\nDoes the thing.\n\n- one\n- two\n\n```go\nfmt.Println(1)\n```\n"
	build := func(content string) string {
		root := t.TempDir()
		dir := filepath.Join(root, "lib", "agents")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "crlf-agent.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		roster, err := Compose(root, "")
		if err != nil || len(roster) != 1 {
			t.Fatalf("roster %v, err %v", roster, err)
		}
		j, err := AgentToJSON(roster[0])
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	lf := build(def)
	crlf := build(strings.ReplaceAll(def, "\n", "\r\n"))
	if lf != crlf {
		t.Errorf("a CRLF checkout composes differently:\n LF:   %s\n CRLF: %s", lf, crlf)
	}
	if strings.Contains(crlf, `\r`) {
		t.Errorf("the composed JSON carries a carriage return: %s", crlf)
	}
}
