package validate

// agent_files_test.go — `yakos validate` rejects the agent files the Go
// dispatcher would skip (sec-324): a line over the cap, a symlink that does not
// resolve to a regular file or resolves outside the framework's lib/ and the
// project directory, a file over the size cap. Without it a skipped override
// silently falls back to the framework's agent, and nothing tells the operator.
//
// The bash validator prints the same text; tests/run-agent-enums-test.sh runs
// both on the same fixtures and compares them byte for byte.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/agentscompose"
)

// agentBody is a valid agent file that stays inside the line budget.
func agentBody(id string) string {
	return "---\nid: " + id + "\nrole: specialist\n---\n\n# " + id + "\n" + strings.Repeat("filler\n", 90)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
}

// agentFilesProject is a project with one good agent. root is a framework root
// whose lib/agents holds one good agent too.
func agentFilesProject(t *testing.T) (root, proj, agents string) {
	t.Helper()
	root, proj = t.TempDir(), t.TempDir()
	agents = filepath.Join(proj, ".claude", "agents")
	writeFile(t, filepath.Join(agents, "good.md"), agentBody("good"))
	writeFile(t, filepath.Join(root, "lib", "agents", "framework.md"), agentBody("framework"))
	return root, proj, agents
}

func validateProject(t *testing.T, root, proj string) (out string, errs []string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir()) // no plugins
	var buf bytes.Buffer
	res := RunProject(Config{YakosRoot: root, Writer: &buf, ErrWriter: &buf}, proj)
	for _, f := range res.Findings {
		if f.Level == LevelErr {
			errs = append(errs, f.Message)
		}
	}
	return buf.String(), errs
}

// wantOneError requires exactly one error, which is the given text for file, and
// no warning: the file is not also reported as having bad frontmatter, or as
// being a file of 0 lines, or anything else. The bash twin prints neither.
func wantOneError(t *testing.T, out string, errs []string, file, text string) {
	t.Helper()
	if len(errs) != 1 || errs[0] != file+": "+text {
		t.Fatalf("errors = %q\nwant exactly one: %q\nfull output:\n%s", errs, file+": "+text, out)
	}
	if strings.Contains(out, "[warn]") {
		t.Fatalf("the rejected file also produced a warning:\n%s", out)
	}
}

const msgUnresolved = "symlink does not resolve to a regular file; the Go dispatcher skips it"

var msgOutside = agentscompose.AgentOutsideReason + "; the Go dispatcher skips it"

func TestAgentFiles_ALongLineIsRejected(t *testing.T) {
	root, proj, agents := agentFilesProject(t)
	bad := filepath.Join(agents, "wide.md")
	writeFile(t, bad, "---\nid: wide\nrole: specialist\n---\n\n"+strings.Repeat("y", 2<<20)+"\n"+strings.Repeat("filler\n", 90))
	out, errs := validateProject(t, root, proj)
	wantOneError(t, out, errs, bad, "line 6 is longer than 1048576 bytes; the Go dispatcher skips it; split the line")
}

// The edge is Compose's: a line of the bound is refused, one byte less is not.
func TestAgentFiles_TheLineBoundIsComposes(t *testing.T) {
	root, proj, agents := agentFilesProject(t)
	head := "---\nid: edge\nrole: specialist\n---\n\n"
	filler := strings.Repeat("filler\n", 90)
	writeFile(t, filepath.Join(agents, "under.md"), head+strings.Repeat("a", agentscompose.MaxLineBytes-1)+"\n"+filler)
	atBound := filepath.Join(agents, "bound.md")
	writeFile(t, atBound, head+strings.Repeat("a", agentscompose.MaxLineBytes)+"\n"+filler)
	crlf := filepath.Join(agents, "crlf.md")
	writeFile(t, crlf, head+strings.Repeat("a", agentscompose.MaxLineBytes-1)+"\r\n"+filler)
	out, errs := validateProject(t, root, proj)
	if len(errs) != 2 ||
		errs[0] != atBound+": line 6 is longer than 1048576 bytes; the Go dispatcher skips it; split the line" ||
		errs[1] != crlf+": line 6 is longer than 1048576 bytes; the Go dispatcher skips it; split the line" {
		t.Fatalf("errors = %q\n%s", errs, out)
	}
}

func TestAgentFiles_AFileOverTheSizeCapIsRejected(t *testing.T) {
	root, proj, agents := agentFilesProject(t)
	big := filepath.Join(agents, "big.md")
	writeFile(t, big, agentBody("big")+strings.Repeat("x", agentscompose.MaxAgentFileBytes)+"\n")
	out, errs := validateProject(t, root, proj)
	wantOneError(t, out, errs, big, "file is larger than 4194304 bytes; the Go dispatcher skips it")
}

func TestAgentFiles_ADanglingSymlinkIsRejected(t *testing.T) {
	root, proj, agents := agentFilesProject(t)
	link := filepath.Join(agents, "ghost.md")
	symlinkOrSkip(t, filepath.Join(proj, "does-not-exist.md"), link)
	out, errs := validateProject(t, root, proj)
	wantOneError(t, out, errs, link, msgUnresolved)
}

func TestAgentFiles_ASymlinkToADirectoryIsRejected(t *testing.T) {
	root, proj, agents := agentFilesProject(t)
	dir := filepath.Join(proj, "somedir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(agents, "dir.md")
	symlinkOrSkip(t, dir, link)
	out, errs := validateProject(t, root, proj)
	wantOneError(t, out, errs, link, msgUnresolved)
}

func TestAgentFiles_ASymlinkOutsideTheRootsIsRejected(t *testing.T) {
	root, proj, agents := agentFilesProject(t)
	outside := filepath.Join(t.TempDir(), "outside.md")
	writeFile(t, outside, agentBody("outside")) // a valid agent: only where it lives is wrong
	link := filepath.Join(agents, "leak.md")
	symlinkOrSkip(t, outside, link)
	out, errs := validateProject(t, root, proj)
	wantOneError(t, out, errs, link, msgOutside)
}

// Links the dispatcher follows are not findings: into the project's agent
// directory, and into the framework's lib/agents (the layout an install makes).
func TestAgentFiles_SymlinksInsideTheRootsAreAccepted(t *testing.T) {
	root, proj, agents := agentFilesProject(t)
	writeFile(t, filepath.Join(agents, "shared", "shared.md"), agentBody("shared"))
	symlinkOrSkip(t, filepath.Join(agents, "shared", "shared.md"), filepath.Join(agents, "abs.md"))
	symlinkOrSkip(t, filepath.Join("shared", "shared.md"), filepath.Join(agents, "rel.md"))
	symlinkOrSkip(t, filepath.Join(root, "lib", "agents", "framework.md"), filepath.Join(agents, "framework.md"))
	out, errs := validateProject(t, root, proj)
	if len(errs) != 0 || strings.Contains(out, "[warn]") {
		t.Fatalf("errors = %q\n%s", errs, out)
	}
}

// In framework mode only lib/agents is a root: not lib/ around it, and not the
// project.
func TestAgentFiles_FrameworkModeAcceptsOnlyLibAgentsAsARoot(t *testing.T) {
	root := t.TempDir()
	agents := filepath.Join(root, "lib", "agents")
	writeFile(t, filepath.Join(agents, "real.md"), agentBody("real"))
	writeFile(t, filepath.Join(agents, "sub", "inner.md"), agentBody("inner"))
	symlinkOrSkip(t, filepath.Join("sub", "inner.md"), filepath.Join(agents, "inlib.md")) // inside lib/agents: fine
	writeFile(t, filepath.Join(root, "lib", "agents-extra", "extra.md"), agentBody("extra"))
	notAgents := filepath.Join(agents, "notagents.md")
	symlinkOrSkip(t, filepath.Join("..", "agents-extra", "extra.md"), notAgents) // in lib/, not in lib/agents
	elsewhere := filepath.Join(root, "docs", "notes.md")
	writeFile(t, elsewhere, agentBody("notes"))
	notLib := filepath.Join(agents, "notlib.md")
	symlinkOrSkip(t, elsewhere, notLib)

	t.Setenv("HOME", t.TempDir())
	var buf bytes.Buffer
	res := RunFramework(Config{YakosRoot: root, Writer: &buf, ErrWriter: &buf})
	var got []string
	for _, f := range res.Findings {
		if f.Level == LevelErr && strings.Contains(f.Message, "symlink") {
			got = append(got, f.Message)
		}
	}
	want := []string{notAgents + ": " + msgOutside, notLib + ": " + msgOutside}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("symlink errors =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The project holds files that are not agents (rev-324): a link to its .env or
// .git/config, or to any other file of the project or of the framework, is
// rejected. Only the agent directories are roots.
func TestAgentFiles_ALinkToAFileOutsideTheAgentDirectoriesIsRejected(t *testing.T) {
	root, proj, agents := agentFilesProject(t)
	writeFile(t, filepath.Join(proj, ".env"), "OPENAI_API_KEY=sk-TOPSECRET-1234\n")
	writeFile(t, filepath.Join(proj, ".git", "config"), "[remote \"origin\"]\n")
	writeFile(t, filepath.Join(proj, "docs", "notes.md"), agentBody("notes"))
	writeFile(t, filepath.Join(root, "lib", "rules", "rule.md"), "rule\n")
	files := map[string]string{
		"dotenv.md": filepath.Join(proj, ".env"),
		"gitcfg.md": filepath.Join(proj, ".git", "config"),
		"notes.md":  filepath.Join(proj, "docs", "notes.md"),
		"rule.md":   filepath.Join(root, "lib", "rules", "rule.md"),
	}
	var want []string
	for _, name := range []string{"dotenv.md", "gitcfg.md", "notes.md", "rule.md"} {
		symlinkOrSkip(t, files[name], filepath.Join(agents, name))
		want = append(want, filepath.Join(agents, name)+": "+msgOutside)
	}
	out, errs := validateProject(t, root, proj)
	if strings.Join(errs, "\n") != strings.Join(want, "\n") {
		t.Fatalf("errors =\n%s\nwant\n%s\nfull output:\n%s", strings.Join(errs, "\n"), strings.Join(want, "\n"), out)
	}
	if strings.Contains(out, "[warn]") {
		t.Errorf("unexpected warning:\n%s", out)
	}
}

// extends: is a bare agent id (sec-324). The value is printed the way the
// dispatcher's warning prints it, which the bash validator reproduces byte for
// byte: bytes outside printable ASCII become "?", 64 bytes at most.
func TestAgentFiles_ANonBareExtendsIsRejected(t *testing.T) {
	root, proj, agents := agentFilesProject(t)
	rule := agentscompose.BareIDRule
	cases := []struct{ file, value, shown string }{
		{"up.md", "../outside", `"../outside"`},
		{"abs.md", "/etc/passwd", `"/etc/passwd"`},
		{"sub.md", "sub/dir", `"sub/dir"`},
		{"dot.md", ".hidden", `".hidden"`},
		{"tab.md", "a\tb", `"a?b"`},
		{"utf.md", "caf\u00e9", `"caf??"`},
		{"long.md", strings.Repeat("a", 80) + "/x", `"` + strings.Repeat("a", 64) + `..."`},
	}
	var want []string
	for _, c := range cases {
		writeFile(t, filepath.Join(agents, c.file), "---\nid: x\nrole: specialist\nextends: "+c.value+"\n---\n\n# x\n"+strings.Repeat("filler\n", 90))
	}
	// Bare ids are fine, whatever they name: a missing template is not a finding.
	for _, ok := range []string{"backend", "a_b.c-d", "Upper1"} {
		writeFile(t, filepath.Join(agents, "ok-"+ok+".md"), "---\nid: x\nrole: specialist\nextends: "+ok+"\n---\n\n# x\n"+strings.Repeat("filler\n", 90))
	}
	sorted := append([]struct{ file, value, shown string }(nil), cases...)
	for i := range sorted {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].file < sorted[i].file {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	for _, c := range sorted {
		want = append(want, filepath.Join(agents, c.file)+": extends value "+c.shown+" is not a bare agent id ("+rule+"); the Go dispatcher skips it")
	}
	out, errs := validateProject(t, root, proj)
	if strings.Join(errs, "\n") != strings.Join(want, "\n") {
		t.Fatalf("errors differ\n got:\n%s\nwant:\n%s\nfull output:\n%s", strings.Join(errs, "\n"), strings.Join(want, "\n"), out)
	}
	if strings.Contains(out, "[warn]") {
		t.Errorf("unexpected warning:\n%s", out)
	}
}
