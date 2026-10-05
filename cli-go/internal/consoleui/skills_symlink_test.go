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
