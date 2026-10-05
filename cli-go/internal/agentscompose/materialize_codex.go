package agentscompose

import (
	"bytes"
	"path/filepath"
	"strings"
)

// codexAgentRelDir is where codex looks for project-scoped custom agents.
var codexAgentRelDir = []string{".codex", "agents"}

// CodexAgentPath returns the path MaterializeCodexAgent writes for id under
// workDir: <workDir>/.codex/agents/yakos-<id>.toml.
func CodexAgentPath(workDir, id string) string {
	return filepath.Join(workDir, ".codex", "agents", "yakos-"+id+".toml")
}

// EmitCodexTOML returns the bytes of the codex agent definition for agent. It
// is a port of yk_rt_codex_emit_toml (cli/lib/runtimes/codex.sh, python path)
// and must stay byte-identical to it:
//
//	# yakos-generated: ...
//	name = "<id>"
//	description = "<one line, \ and " escaped>"
//	model = "<model>"            (only when agent.Model is set)
//	developer_instructions = """
//	<prompt, \ escaped and """ broken up, trailing newlines trimmed>
//	"""
//
// codex-cli 0.154.0 discovers the file by this layout and delegates to the
// agent by its name = "<id>" (verified live: a delegated subagent answered with
// the token its developer_instructions demanded). Tools are not emitted;
// codex agents have no tool list in this format.
func EmitCodexTOML(agent ComposedAgent) []byte {
	desc := agent.Description
	if desc == "" {
		desc = "Agent: " + agent.ID
	}
	lines := []string{
		codexMarkerLine,
		`name = "` + agent.ID + `"`,
		`description = "` + escapeBackslashQuote(oneLine(desc)) + `"`,
	}
	if agent.Model != "" {
		lines = append(lines, `model = "`+escapeBackslashQuote(oneLine(agent.Model))+`"`)
	}
	body := strings.ReplaceAll(agent.Prompt, `\`, `\\`)
	body = strings.ReplaceAll(body, `"""`, `\"\"\"`)
	lines = append(lines,
		`developer_instructions = """`,
		strings.TrimRight(body, "\n"),
		`"""`,
	)
	return []byte(strings.Join(lines, "\n") + "\n")
}

// isCodexGenerated reports whether an existing file at the codex agent path may
// be replaced: it carries the marker, or it is a legacy file written by an
// earlier yakOS (no marker, name = "<id>" on the first line and a description on
// the second). Anything else is an operator's own file.
func isCodexGenerated(id string) func([]byte) bool {
	return func(existing []byte) bool {
		if hasMarker(existing) {
			return true
		}
		first, rest, _ := bytes.Cut(existing, []byte("\n"))
		second, _, _ := bytes.Cut(rest, []byte("\n"))
		return string(bytes.TrimRight(first, "\r")) == `name = "`+id+`"` &&
			bytes.HasPrefix(second, []byte(`description = "`))
	}
}

// MaterializeCodexAgent writes <workDir>/.codex/agents/yakos-<id>.toml for
// agent. See the package notes in materialize_files.go for the marker,
// skip-write, permission and symlink rules.
func MaterializeCodexAgent(workDir string, agent ComposedAgent) (MaterializeResult, error) {
	if err := checkMaterializeArgs(workDir, agent); err != nil {
		return MaterializeResult{}, err
	}
	if _, err := ensureDirNoSymlinks(workDir, codexAgentRelDir...); err != nil {
		return MaterializeResult{Path: CodexAgentPath(workDir, agent.ID)}, err
	}
	return syncManagedFile(CodexAgentPath(workDir, agent.ID), EmitCodexTOML(agent), isCodexGenerated(agent.ID))
}
