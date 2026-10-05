package dispatch

import (
	"fmt"
	"io"
	"os"

	"github.com/bakw00ds/yakos/internal/agentscompose"
)

// materializeNoteWriter receives the one-line notes about agent files. Replaced
// in tests.
var materializeNoteWriter io.Writer = os.Stderr

// materializeAgentFiles writes the file-based agent registration the codex and
// agy framed prompts depend on (K-134):
//
//	codex: <workdir>/.codex/agents/yakos-<id>.toml   ("Delegate to subagent named <id>")
//	agy:   <workdir>/.agents/skills/yakos-<id>/SKILL.md   ("@yakos-<id> <task>")
//
// It runs in the dispatch layer, not in the adapters: agentscompose imports the
// runtime package, so an adapter cannot call it, and keeping the adapters free
// of file writes leaves ExecCmd a pure argv builder. The chat path never calls
// it: chat carries the persona inline (codex developer_instructions, agy prompt
// prefix) and must not litter the project.
//
// The file is a function of the agent definition alone. The per-dispatch model
// rides -m / --model, so Model is cleared here: it would otherwise let one
// dispatch's override rewrite the file under a concurrent dispatch of the same
// agent, and the composed Model is a Claude tier that codex and agy cannot use.
//
// Failures never stop the dispatch. The file only adds the agent's persona, and
// refusing to run a task because a project directory is read-only would turn a
// missing nicety into an outage; the note says what was skipped.
func materializeAgentFiles(runtimeName, project, workDirOverride string, agent agentscompose.ComposedAgent) {
	if runtimeName != "codex" && runtimeName != "agy" {
		return
	}
	workDir := workDirOverride
	if workDir == "" {
		workDir = project
	}
	agent.Model = ""
	res, err := agentscompose.MaterializeRuntimeAgent(runtimeName, workDir, agent)
	switch {
	case err != nil:
		_, _ = fmt.Fprintf(materializeNoteWriter,
			"yakos: %s agent file for %q not written (%v); dispatching without the agent definition\n",
			runtimeName, agent.ID, err)
	case res.Skipped == agentscompose.SkipNotYakosManaged:
		_, _ = fmt.Fprintf(materializeNoteWriter,
			"yakos: leaving %s alone (no yakos-generated marker); the %s dispatch will use your file\n",
			res.Path, runtimeName)
	}
}
