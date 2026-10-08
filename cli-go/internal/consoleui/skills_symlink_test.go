package consoleui_test

// skills_symlink_test.go — GET /api/skills lists the project's agents with a
// description taken from each file. A symlink in the project's agent directory
// that points at a file outside the project must not be listed, and its content
// must not reach the response (sec-324: the probe leaked a line of a credentials
// file to every reader of the endpoint).

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/agentscompose"
	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// newSkillsTestServerFor is newSkillsTestServer with a chosen workspace root,
// which is the project whose .claude/agents the endpoint composes.
func newSkillsTestServerFor(t *testing.T, yakosRoot, workspace string) (*httptest.Server, string) {
	t.Helper()
	tok, err := consoleui.LoadOrCreateToken(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)
	srv := consoleui.MustNew(t, consoleui.Config{
		Token:             tok,
		KanbanBoardPath:   filepath.Join(t.TempDir(), "kanban.md"),
		KanbanProject:     "test",
		MetricsProjectDir: t.TempDir(),
		PerfWorkDir:       t.TempDir(),
		Bus:               bus,
		WorkDir:           t.TempDir(),
		WorkspaceRoot:     workspace,
		YakosRoot:         yakosRoot,
	})
	ts := httptest.NewServer(srv.HandlerForTest())
	t.Cleanup(ts.Close)
	return ts, tok
}

func TestSkillsHandler_ASymlinkOutOfTheProjectIsNotListed(t *testing.T) {
	root := buildFakeYakosRoot(t)
	project := t.TempDir()
	agents := filepath.Join(project, ".claude", "agents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	mine := "---\nid: mine\nmodel: haiku\n---\n\n## Purpose\n\nThe project's own agent.\n"
	if err := os.WriteFile(filepath.Join(agents, "mine.md"), []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "credentials")
	if err := os.WriteFile(outside, []byte("OPENAI_API_KEY=sk-TOPSECRET-1234\nsecond line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(agents, "leak.md")); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}

	ts, tok := newSkillsTestServerFor(t, root, project)
	resp := doSkillsGET(t, ts, tok)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), "TOPSECRET") {
		t.Fatalf("the outside file's content is in the response: %s", body)
	}
	var got struct {
		Agents []struct {
			Name string `json:"name"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var names []string
	for _, a := range got.Agents {
		names = append(names, a.Name)
	}
	listed := strings.Join(names, ",")
	if !strings.Contains(listed, "testworker") || !strings.Contains(listed, "mine") {
		t.Errorf("the other agents are missing from the roster: %q", listed)
	}
	if strings.Contains(listed, "leak") {
		t.Errorf("the symlinked file was listed as an agent: %q", listed)
	}
}

// The listing survives: a symlinked SKILL.md that leads outside the project, one
// that points at a directory, and one that points at nothing are each left out,
// the other skills are still served, and the outside file's text is nowhere. A
// link to a directory used to fail the whole listing, which this endpoint served
// as an empty one.
func TestSkillsHandler_ASymlinkedSkillIsLeftOutAndTheListingSurvives(t *testing.T) {
	root := buildFakeYakosRoot(t)
	project := t.TempDir()
	skills := filepath.Join(project, ".claude", "skills")
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(skills, "good", "SKILL.md"), "---\nname: good-skill\ndescription: a good one\n---\n\n## Purpose\n\nBody.\n")
	outside := filepath.Join(t.TempDir(), "credentials")
	write(outside, "---\nname: SECRET-NAME\ndescription: OPENAI_API_KEY=sk-TOPSECRET-1234\n---\n")
	for slug, target := range map[string]string{
		"leak":  outside,
		"dir":   t.TempDir(),
		"ghost": filepath.Join(project, "nothing"),
	} {
		if err := os.MkdirAll(filepath.Join(skills, slug), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(skills, slug, "SKILL.md")); err != nil {
			t.Skipf("cannot create symlinks here: %v", err)
		}
	}

	ts, tok := newSkillsTestServerFor(t, root, project)
	resp := doSkillsGET(t, ts, tok)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), "TOPSECRET") || strings.Contains(string(body), "SECRET-NAME") {
		t.Fatalf("the outside file's content is in the response: %s", body)
	}
	var got struct {
		Skills []struct {
			Name string `json:"name"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Skills) != 1 || got.Skills[0].Name != "good-skill" {
		t.Errorf("skills = %+v, want only good-skill (the listing must survive the bad entries)", got.Skills)
	}
}

// rev-324: the project's own .env and .git/config are not agents or skills, and a
// link to one from .claude/agents or .claude/skills must not put its first line
// into the response of the endpoint every reader can call.
func TestSkillsHandler_ALinkToTheProjectsDotEnvOrGitConfigIsNotListed(t *testing.T) {
	root := buildFakeYakosRoot(t)
	project := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	envFile := filepath.Join(project, ".env")
	gitConfig := filepath.Join(project, ".git", "config")
	write(envFile, "OPENAI_API_KEY=sk-TOPSECRET-1234\n")
	write(gitConfig, "[remote \"origin\"]\n\turl = https://user:TOKEN-9999@example.com/x.git\n")
	agents := filepath.Join(project, ".claude", "agents")
	skills := filepath.Join(project, ".claude", "skills")
	write(filepath.Join(agents, "mine.md"), "---\nid: mine\n---\n\n## Purpose\n\nThe project's own agent.\n")
	write(filepath.Join(skills, "good", "SKILL.md"), "---\nname: good-skill\ndescription: a good one\n---\n\n## Purpose\n\nBody.\n")
	links := map[string]string{
		filepath.Join(agents, "dotenv.md"):          envFile,
		filepath.Join(agents, "gitcfg.md"):          gitConfig,
		filepath.Join(skills, "env", "SKILL.md"):    envFile,
		filepath.Join(skills, "gitcfg", "SKILL.md"): gitConfig,
	}
	for link, target := range links {
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("cannot create symlinks here: %v", err)
		}
	}

	ts, tok := newSkillsTestServerFor(t, root, project)
	resp := doSkillsGET(t, ts, tok)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}
	for _, marker := range []string{"TOPSECRET", "TOKEN-9999", "OPENAI_API_KEY", "[remote"} {
		if strings.Contains(string(body), marker) {
			t.Fatalf("%q from the project's own files is in the response: %s", marker, body)
		}
	}
	var got struct {
		Agents []struct {
			Name string `json:"name"`
		} `json:"agents"`
		Skills []struct {
			Name string `json:"name"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var agentNames []string
	for _, a := range got.Agents {
		agentNames = append(agentNames, a.Name)
	}
	listed := strings.Join(agentNames, ",")
	if !strings.Contains(listed, "testworker") || !strings.Contains(listed, "mine") || strings.Contains(listed, "dotenv") || strings.Contains(listed, "gitcfg") {
		t.Errorf("agents = %q, want testworker and mine and neither link", listed)
	}
	if len(got.Skills) != 1 || got.Skills[0].Name != "good-skill" {
		t.Errorf("skills = %+v, want only good-skill", got.Skills)
	}
}

// ---- directories that are links (rev-324) ------------------------------------
//
// A file seen through a linked directory is a regular file and never reaches the
// rule for files, so the project's .claude/agents and .claude/skills are refused
// when they are links, or sit under a linked .claude, wherever the link leads. The
// endpoint every reader can call serves none of what is in them, and the daemon
// says so once, in the one text every reader of the roster uses. The framework's
// own directories may be links and are listed.

// writeSkillsTestFile makes the file with its directories.
func writeSkillsTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// markedTree is a .claude-shaped directory whose agent and skill carry a marker, so
// that anything read from it is seen in the response.
func markedTree(t *testing.T, dir, where string) {
	t.Helper()
	writeSkillsTestFile(t, filepath.Join(dir, "agents", "evil.md"),
		"---\nid: evil\n---\n\n## Purpose\n\n"+where+"-AGENT-MARKER\n")
	writeSkillsTestFile(t, filepath.Join(dir, "skills", "evil", "SKILL.md"),
		"---\nname: evil-skill\ndescription: "+where+"-SKILL-MARKER\n---\n\n## Purpose\n\nBody.\n")
}

// skillsGET returns the decoded agent and skill names and the raw body.
func skillsGET(t *testing.T, ts *httptest.Server, tok string) (agents, skills []string, body string) {
	t.Helper()
	resp := doSkillsGET(t, ts, tok)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, raw)
	}
	var got struct {
		Agents []struct {
			Name string `json:"name"`
		} `json:"agents"`
		Skills []struct {
			Name string `json:"name"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, a := range got.Agents {
		agents = append(agents, a.Name)
	}
	for _, sk := range got.Skills {
		skills = append(skills, sk.Name)
	}
	return agents, skills, string(raw)
}

// dirLinkText is the one text said of a project directory that is a link, written
// out here so that a change of it, in any place that prints it, is a failure.
const dirLinkText = "a symlinked directory is not followed (this directory or .claude is a symlink)"

// captureDaemonWarnings points the composers' notices at a buffer for the test.
func captureDaemonWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := agentscompose.WarnWriter
	agentscompose.WarnWriter = &buf
	t.Cleanup(func() { agentscompose.WarnWriter = old })
	return &buf
}

func TestSkillsHandler_AProjectDirectoryThatIsALinkIsNotListed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, project string)
	}{
		{"agents and skills linked outside the project", func(t *testing.T, project string) {
			outside := t.TempDir()
			markedTree(t, outside, "OUTSIDE")
			symlinkOrSkip(t, filepath.Join(outside, "agents"), filepath.Join(project, ".claude", "agents"))
			symlinkOrSkip(t, filepath.Join(outside, "skills"), filepath.Join(project, ".claude", "skills"))
		}},
		{"agents and skills linked to a directory of the project", func(t *testing.T, project string) {
			markedTree(t, filepath.Join(project, "shared"), "INSIDE")
			symlinkOrSkip(t, filepath.Join("..", "shared", "agents"), filepath.Join(project, ".claude", "agents"))
			symlinkOrSkip(t, filepath.Join("..", "shared", "skills"), filepath.Join(project, ".claude", "skills"))
		}},
		{".claude linked outside the project", func(t *testing.T, project string) {
			outside := t.TempDir()
			markedTree(t, outside, "OUTSIDE")
			symlinkOrSkip(t, outside, filepath.Join(project, ".claude"))
		}},
		{".claude linked to a directory of the project", func(t *testing.T, project string) {
			markedTree(t, filepath.Join(project, "dotclaude"), "INSIDE")
			symlinkOrSkip(t, "dotclaude", filepath.Join(project, ".claude"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			warnings := captureDaemonWarnings(t)
			root := buildFakeYakosRoot(t)
			project := t.TempDir()
			tc.setup(t, project)

			ts, tok := newSkillsTestServerFor(t, root, project)
			agents, skills, body := skillsGET(t, ts, tok)
			if strings.Contains(body, "MARKER") || strings.Contains(strings.Join(agents, ","), "evil") {
				t.Fatalf("something from a refused directory is in the response: %s", body)
			}
			if strings.Join(agents, ",") != "testworker" || len(skills) != 0 {
				t.Errorf("agents = %v, skills = %v; want only the framework's testworker and no skills", agents, skills)
			}
			// the daemon says so, once per directory, in the one text
			for _, kind := range []string{"agent", "skill"} {
				line := "yakos: WARN: ignoring " + kind + " directory " + filepath.Join(project, ".claude", kind+"s") + ": " + dirLinkText + "\n"
				if strings.Count(warnings.String(), line) != 1 {
					t.Errorf("want exactly one %q in the daemon's warnings:\n%s", line, warnings.String())
				}
			}
		})
	}
}

// The framework is not the project: a root reached through a link, and lib/agents
// and lib/skills that are links themselves (a bare install or a re-pointed upgrade
// leaves them so), are listed, with no warning.
func TestSkillsHandler_TheFrameworkRootAndItsDirectoriesMayBeLinks(t *testing.T) {
	fillFramework := func(t *testing.T, lib string) {
		t.Helper()
		writeSkillsTestFile(t, filepath.Join(lib, "agents", "testworker.md"),
			"---\nid: testworker\nmodel: sonnet\n---\n\n## Purpose\n\nTest specialist.\n")
		writeSkillsTestFile(t, filepath.Join(lib, "skills", "fwskill", "SKILL.md"),
			"---\nname: fw-skill\ndescription: a framework skill\n---\n\n## Purpose\n\nBody.\n")
	}
	check := func(t *testing.T, root string, warnings *bytes.Buffer) {
		t.Helper()
		ts, tok := newSkillsTestServerFor(t, root, t.TempDir())
		agents, skills, body := skillsGET(t, ts, tok)
		if strings.Join(agents, ",") != "testworker" || strings.Join(skills, ",") != "fw-skill" {
			t.Errorf("agents = %v, skills = %v; want testworker and fw-skill\n%s", agents, skills, body)
		}
		if warnings.Len() != 0 {
			t.Errorf("unexpected warnings: %q", warnings.String())
		}
	}

	t.Run("the root is a link", func(t *testing.T) {
		warnings := captureDaemonWarnings(t)
		real := t.TempDir()
		fillFramework(t, filepath.Join(real, "lib"))
		linked := filepath.Join(t.TempDir(), "root")
		symlinkOrSkip(t, real, linked)
		check(t, linked, warnings)
	})
	t.Run("lib/agents and lib/skills are links", func(t *testing.T) {
		warnings := captureDaemonWarnings(t)
		shared := t.TempDir()
		fillFramework(t, shared)
		root := t.TempDir()
		symlinkOrSkip(t, filepath.Join(shared, "agents"), filepath.Join(root, "lib", "agents"))
		symlinkOrSkip(t, filepath.Join(shared, "skills"), filepath.Join(root, "lib", "skills"))
		check(t, root, warnings)
	})
}

// symlinkOrSkip makes the link, or skips the test where links cannot be made.
func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
}

// K-167: the endpoint composes through the roster reader, so the other two refusals
// hold here as well. A project agent over the size cap, and one whose extends: leaves
// lib/agents, are left out of the response.
func TestSkillsHandler_HugeAndEscapingExtendsAgentsAreNotListed(t *testing.T) {
	root := buildFakeYakosRoot(t)
	project := t.TempDir()
	agents := filepath.Join(project, ".claude", "agents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	huge := append([]byte("---\nid: hugeone\nmodel: haiku\n---\n\n## Purpose\n\n"), bytes.Repeat([]byte("x\n"), agentscompose.MaxAgentFileBytes/2+1)...)
	if err := os.WriteFile(filepath.Join(agents, "hugeone.md"), huge, 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "evil.md")
	if err := os.WriteFile(outside, []byte("---\nid: evil\n---\nOUTSIDE-TEMPLATE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	esc := "---\nid: escaper\nextends: " + strings.TrimSuffix(outside, ".md") + "\nmodel: haiku\n---\n\n## Purpose\n\nescapes\n"
	if err := os.WriteFile(filepath.Join(agents, "escaper.md"), []byte(esc), 0o644); err != nil {
		t.Fatal(err)
	}
	dots := "---\nid: dotter\nextends: ../../../../etc/hosts\nmodel: haiku\n---\n\n## Purpose\n\ndots\n"
	if err := os.WriteFile(filepath.Join(agents, "dotter.md"), []byte(dots), 0o644); err != nil {
		t.Fatal(err)
	}

	ts, tok := newSkillsTestServerFor(t, root, project)
	resp := doSkillsGET(t, ts, tok)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}
	var got struct {
		Agents []struct {
			Name string `json:"name"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, a := range got.Agents {
		names[a.Name] = true
	}
	for _, bad := range []string{"hugeone", "escaper", "dotter"} {
		if names[bad] {
			t.Errorf("agent %q was listed", bad)
		}
	}
	if !names["testworker"] {
		t.Error("the framework agent vanished from the response")
	}
	if strings.Contains(string(body), "OUTSIDE-TEMPLATE") {
		t.Error("an extends template from outside lib/agents reached the response")
	}
}
