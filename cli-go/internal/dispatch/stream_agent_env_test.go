package dispatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// B1: the console chat path hands codex and agy the pane agent as
// YAKOS_AGENT_TYPE, so their hooks apply that agent's path policy.
func TestRunStream_CodexAgyChatExportsAgentType(t *testing.T) {
	root := routingRoot(t)
	fakeCLIs(t)
	isolatedLogDir(t)
	out := t.TempDir()
	stubs := t.TempDir()
	for _, n := range []string{"codex", "agy"} {
		body := "#!/bin/sh\nprintf '%s' \"$YAKOS_AGENT_TYPE\" > '" + filepath.Join(out, n) + "'\nprintf 'ok\\n'\n"
		if err := os.WriteFile(filepath.Join(stubs, n), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", stubs+string(os.PathListSeparator)+os.Getenv("PATH"))
	svc := newResolutionSvc(t, root)
	for agent, rt := range map[string]string{"general-codex": "codex", "general-agy": "agy"} {
		collectChunks(t, svc, Params{Agent: agent, Task: "hi", Project: t.TempDir()})
		b, err := os.ReadFile(filepath.Join(out, rt))
		if err != nil {
			t.Fatalf("%s stub not run: %v", rt, err)
		}
		if got := strings.TrimSpace(string(b)); got != agent {
			t.Errorf("%s chat YAKOS_AGENT_TYPE = %q, want %q", rt, got, agent)
		}
	}
}
