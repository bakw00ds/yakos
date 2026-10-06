package agentscompose

// compose_files_test.go — Compose reads an agent file only when that is safe
// (sec-324). A cloned repository controls the project's .claude/agents, and the
// daemon composes the roster from it on every request:
//
//   - a symlink is followed only to a regular file inside the framework's
//     lib/agents or the project's .claude/agents, and not to the project's own
//     .env or .git/config, or any other file in the tree around them;
//   - a symlink that goes anywhere else, or does not resolve to a regular file,
//     is skipped with the same once-per-file warning as the other skips, and the
//     other agents still compose;
//   - nothing over MaxAgentFileBytes is read.
//
// The FIFO, device and unreadable-file cases are in compose_files_unix_test.go.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const secretText = "OPENAI_API_KEY=sk-TOPSECRET-1234"

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
}

// filesFixture is a framework root with a backend agent and a project with a
// helper agent. It returns the project's agents directory too.
func filesFixture(t *testing.T) (root, project, agents string) {
	t.Helper()
	root, project = t.TempDir(), t.TempDir()
	writeAgentDir(t, filepath.Join(root, "lib", "agents"), map[string]string{"backend": "model: sonnet\n"})
	agents = filepath.Join(project, ".claude", "agents")
	writeAgentDir(t, agents, map[string]string{"helper": "model: haiku\n"})
	return root, project, agents
}

// composeIDs composes the roster and requires that it succeeds.
func composeIDs(t *testing.T, root, project string) (string, []ComposedAgent) {
	t.Helper()
	roster, err := Compose(root, project)
	if err != nil {
		t.Fatalf("Compose = %v; one bad file must not fail the whole roster", err)
	}
	return strings.Join(rosterIDs(roster), ","), roster
}

func requireWarning(t *testing.T, warnings *bytes.Buffer, file, reason string) {
	t.Helper()
	got := warnings.String()
	for _, want := range []string{"WARN", file, reason} {
		if !strings.Contains(got, want) {
			t.Errorf("warning %q does not mention %q", got, want)
		}
	}
	if n := strings.Count(got, "WARN"); n != 1 {
		t.Errorf("%d warnings, want one: %q", n, got)
	}
}

// A symlink with nothing at the other end used to fail the whole roster.
func TestCompose_SkipsADanglingSymlink(t *testing.T) {
	warnings := captureWarnings(t)
	root, project, agents := filesFixture(t)
	link := filepath.Join(agents, "ghost.md")
	symlinkOrSkip(t, filepath.Join(project, "does-not-exist.md"), link)

	if got, _ := composeIDs(t, root, project); got != "backend,helper" {
		t.Errorf("roster = %q, want backend,helper", got)
	}
	requireWarning(t, warnings, link, "symlink does not resolve to a regular file")
}

func TestCompose_SkipsASymlinkToADirectory(t *testing.T) {
	warnings := captureWarnings(t)
	root, project, agents := filesFixture(t)
	dir := filepath.Join(project, "somedir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(agents, "dir.md")
	symlinkOrSkip(t, dir, link)

	if got, _ := composeIDs(t, root, project); got != "backend,helper" {
		t.Errorf("roster = %q, want backend,helper", got)
	}
	requireWarning(t, warnings, link, "symlink does not resolve to a regular file")
}

// The probe: a link to a file outside both roots became the agent's prompt, its
// first line the description /api/skills returns, and the daemon then sent the
// file to the vendor as the persona.
func TestCompose_SkipsASymlinkToAnOutsideFile(t *testing.T) {
	warnings := captureWarnings(t)
	root, project, agents := filesFixture(t)
	outside := filepath.Join(t.TempDir(), "credentials")
	writeFileT(t, outside, secretText+"\nsecond line\n")
	link := filepath.Join(agents, "leak.md")
	symlinkOrSkip(t, outside, link)

	got, roster := composeIDs(t, root, project)
	if got != "backend,helper" {
		t.Errorf("roster = %q, want backend,helper", got)
	}
	encoded, err := json.Marshal(roster)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "TOPSECRET") {
		t.Errorf("the outside file's content reached the roster: %s", encoded)
	}
	requireWarning(t, warnings, link, AgentOutsideReason)
	if strings.Contains(warnings.String(), "TOPSECRET") {
		t.Errorf("the warning echoes the file's content: %q", warnings.String())
	}
}

// A link to a regular file inside the project's agent directory is fine,
// absolute or relative, here in a subdirectory of it. The temp directory sits
// behind /var on macOS, so this also proves the containment test does not
// compare path strings.
func TestCompose_FollowsASymlinkInsideTheProjectsAgentDirectory(t *testing.T) {
	warnings := captureWarnings(t)
	root, project, agents := filesFixture(t)
	shared := filepath.Join(agents, "shared", "shared.md")
	writeFileT(t, shared, "---\nid: shared\nmodel: opus\n---\n\n## Purpose\n\nShared persona.\n")
	symlinkOrSkip(t, shared, filepath.Join(agents, "abs.md"))
	symlinkOrSkip(t, filepath.Join("shared", "shared.md"), filepath.Join(agents, "rel.md"))

	// The framework's agents come first, then the project's in file name order.
	got, roster := composeIDs(t, root, project)
	if got != "backend,abs,helper,rel" {
		t.Fatalf("roster = %q, want backend,abs,helper,rel", got)
	}
	for _, a := range roster {
		if a.ID != "abs" && a.ID != "rel" {
			continue
		}
		if !strings.Contains(a.Prompt, "Shared persona.") || a.Model != "opus" {
			t.Errorf("%s was not composed from the linked file: model %q, prompt %.40q", a.ID, a.Model, a.Prompt)
		}
	}
	if warnings.Len() != 0 {
		t.Errorf("unexpected warnings: %q", warnings.String())
	}
}

// The project holds files that are not agents, and a link to one of them became
// the agent's persona and was sent to the vendor: rev-324 got the project's .env
// and .git/config that way. The roots are the agent directories, not the project
// around them, so these links are refused, and so is one to another file in the
// project or in the framework's lib/.
func TestCompose_SkipsALinkToAFileOutsideTheAgentDirectories(t *testing.T) {
	warnings := captureWarnings(t)
	root, project, agents := filesFixture(t)
	writeFileT(t, filepath.Join(project, ".env"), secretText+"\n")
	writeFileT(t, filepath.Join(project, ".git", "config"), "[remote \"origin\"]\n\turl = https://user:TOKEN-9999@example.com/x.git\n")
	writeFileT(t, filepath.Join(project, "docs", "notes.md"), "NOTES-MARKER\n")
	writeFileT(t, filepath.Join(root, "lib", "rules", "rule.md"), "RULE-MARKER\n")
	writeFileT(t, filepath.Join(root, "lib", "skills", "fw", "SKILL.md"), skillBody("fw", "SKILL-MARKER"))
	links := map[string]string{
		"dotenv.md": filepath.Join("..", "..", ".env"),
		"gitcfg.md": filepath.Join("..", "..", ".git", "config"),
		"notes.md":  filepath.Join("..", "..", "docs", "notes.md"),
		"rule.md":   filepath.Join(root, "lib", "rules", "rule.md"),
		"skill.md":  filepath.Join(root, "lib", "skills", "fw", "SKILL.md"),
	}
	for name, target := range links {
		symlinkOrSkip(t, target, filepath.Join(agents, name))
	}

	got, roster := composeIDs(t, root, project)
	if got != "backend,helper" {
		t.Errorf("roster = %q, want backend,helper", got)
	}
	encoded, err := json.Marshal(roster)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"TOPSECRET", "TOKEN-9999", "NOTES-MARKER", "RULE-MARKER", "SKILL-MARKER"} {
		if strings.Contains(string(encoded), marker) || strings.Contains(warnings.String(), marker) {
			t.Errorf("%s reached the roster or a warning: %s", marker, encoded)
		}
	}
	for name := range links {
		requireLine(t, warnings, filepath.Join(agents, name), AgentOutsideReason)
	}
}

// The installed layout: per-file links in a project's .claude/agents that point
// into the framework's lib/agents must keep working, also when the framework
// root is reached through a symlink.
func TestCompose_FollowsASymlinkIntoTheFrameworkLib(t *testing.T) {
	warnings := captureWarnings(t)
	root, project, agents := filesFixture(t)
	alias := filepath.Join(t.TempDir(), "framework-alias")
	symlinkOrSkip(t, root, alias)
	symlinkOrSkip(t, filepath.Join(alias, "lib", "agents", "backend.md"), filepath.Join(agents, "backend.md"))

	for _, yakosRoot := range []string{root, alias} {
		got, roster := composeIDs(t, yakosRoot, project)
		if got != "backend,helper" {
			t.Errorf("root %s: roster = %q, want backend,helper", yakosRoot, got)
		}
		if !strings.Contains(roster[0].Prompt, "Fixture backend") {
			t.Errorf("root %s: backend was not composed through the link: %.40q", yakosRoot, roster[0].Prompt)
		}
	}
	if warnings.Len() != 0 {
		t.Errorf("unexpected warnings: %q", warnings.String())
	}
}

// The size cap is on the file: exactly the cap composes, one byte more is
// skipped. Every line is short, so only the cap can be what stops it.
func TestCompose_SkipsAFileOverTheSizeCap(t *testing.T) {
	warnings := captureWarnings(t)
	root, project, agents := filesFixture(t)
	head := "---\nid: big\n---\n\n## Purpose\n\nBig.\n\n"
	line := strings.Repeat("x", 99) + "\n"
	body := head + strings.Repeat(line, (MaxAgentFileBytes-len(head))/len(line))
	exact := body + strings.Repeat("z", MaxAgentFileBytes-len(body))
	if len(exact) != MaxAgentFileBytes {
		t.Fatalf("fixture is %d bytes, want %d", len(exact), MaxAgentFileBytes)
	}
	writeFileT(t, filepath.Join(agents, "exact.md"), exact)
	over := filepath.Join(agents, "over.md")
	writeFileT(t, over, exact+"z")

	if got, _ := composeIDs(t, root, project); got != "backend,exact,helper" {
		t.Errorf("roster = %q, want backend,exact,helper", got)
	}
	requireWarning(t, warnings, over, "larger than 4194304 bytes")
}

// The line bound Compose applies, edge by edge. tests/run-agent-enums-test.sh
// runs the same table through the bash validator, which cannot call this code
// and measures with awk: a line is refused when it is MaxLineBytes or longer,
// counting a carriage return before the newline.
func TestLongLine_TheBoundTheBashValidatorMirrors(t *testing.T) {
	a := func(n int) string { return strings.Repeat("a", n) }
	cases := []struct {
		name, content string
		want          int
	}{
		{"LF line one under the bound", a(MaxLineBytes-1) + "\n", 0},
		{"LF line at the bound", a(MaxLineBytes) + "\n", 1},
		{"CRLF line one under the bound", a(MaxLineBytes-2) + "\r\n", 0},
		{"CRLF line at the bound", a(MaxLineBytes-1) + "\r\n", 1},
		{"last line without a newline, one under", "x\n" + a(MaxLineBytes-1), 0},
		{"last line without a newline, at the bound", "x\n" + a(MaxLineBytes), 2},
		{"second line over", "x\n" + a(MaxLineBytes) + "\n", 2},
		{"no long line", "---\nid: x\n---\nbody\n", 0},
	}
	for _, c := range cases {
		if got := LongLine(c.content); got != c.want {
			t.Errorf("%s: LongLine = %d, want %d", c.name, got, c.want)
		}
	}
}
