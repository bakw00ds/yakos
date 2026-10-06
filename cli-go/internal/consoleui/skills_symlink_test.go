package consoleui_test

// skills_symlink_test.go — GET /api/skills lists the project's agents with a
// description taken from each file. A symlink in the project's agent directory
// that points at a file outside the project must not be listed, and its content
// must not reach the response (sec-324: the probe leaked a line of a credentials
// file to every reader of the endpoint).

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
