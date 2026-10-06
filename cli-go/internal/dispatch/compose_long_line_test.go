package dispatch

// compose_long_line_test.go — an agent file with a line over the roster reader's
// bound is skipped with one warning, and every other agent still dispatches
// (rev-324). Before, Compose returned an error and routeDispatch turned it into
// "dispatch: compose agents: ..." for every agent in the project, although a
// cloned repository controls that file.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/agentscompose"
)

// bigLineProject writes a framework root with a plain backend agent and a project
// whose agent directory holds one file with a 2 MiB line. It returns the root,
// the project, the path of the huge file and the buffer that collects what
// Compose warns.
func bigLineProject(t *testing.T) (root, project, huge string, warnings *bytes.Buffer) {
	t.Helper()
	root, project = t.TempDir(), t.TempDir()
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "lib", "agents", "backend.md"),
		"---\nid: backend\nmodel: sonnet\n---\n\n## Purpose\n\nBackend fixture.\n")
	huge = filepath.Join(project, ".claude", "agents", "huge.md")
	write(huge, "---\nid: huge\n---\n\n## Purpose\n\n"+strings.Repeat("z", 2<<20)+"\n")

	warnings = &bytes.Buffer{}
	orig := agentscompose.WarnWriter
	agentscompose.WarnWriter = warnings
	t.Cleanup(func() { agentscompose.WarnWriter = orig })
	return root, project, huge, warnings
}

// The agent lookup behind every route still finds backend. With the old error it
// found nothing, and the dispatch went on as if there were no roster.
func TestAgentForQuery_AnOversizedFileDoesNotHideOtherAgents(t *testing.T) {
	root, project, huge, warnings := bigLineProject(t)
	a := agentForQuery(RouteQuery{Agent: "backend", YakosRoot: root, Project: project})
	if a == nil || a.ID != "backend" || !strings.Contains(a.Prompt, "Backend fixture") {
		t.Fatalf("backend did not resolve from the roster: %+v", a)
	}
	if got := warnings.String(); !strings.Contains(got, huge) || !strings.Contains(got, "longer than") {
		t.Errorf("the skipped file was not named: %q", got)
	}
}

// End to end: `yakos dispatch backend` runs, claude is the only runtime started,
// and the warning names the huge file once even though the roster is composed
// more than once on the way.
func TestRun_AnOversizedAgentFileDoesNotBlockOtherAgents(t *testing.T) {
	root, project, huge, warnings := bigLineProject(t)
	rec := fakeCLIs(t)
	isolatedLogDir(t)

	if _, _, err := Run(context.Background(), Request{AgentName: "backend", Task: "hi", Project: project, YakosRoot: root}); err != nil {
		t.Fatalf("dispatch backend: %v", err)
	}
	if got := invoked(t, rec); strings.Join(got, ",") != "claude" {
		t.Errorf("executed %v, want only claude", got)
	}
	if n := strings.Count(warnings.String(), huge); n != 1 {
		t.Errorf("the huge file was named %d times, want once: %q", n, warnings.String())
	}
}
