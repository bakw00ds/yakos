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
// and must stay byte-identical to it for the same agent JSON:
//
//	# yakos-generated: ...
//	name = "<id>"
//	description = "<one line, \ and " escaped>"
//	model = "<model>"            (only for a model that is not a Claude tier)
//	developer_instructions = """
//	<prompt: leading line breaks and trailing newlines dropped, \ escaped,
//	 """ broken up, control characters and a lone CR written as \u00XX>
//	"""
//
// An agent text that holds a NUL byte is refused with an error: the file would
// not be valid TOML and no persona has one.
//
// The model line is omitted for the Claude tiers the composers produce (see
// writesModelLine): a file must never name a model the runtime does not have.
//
// codex-cli 0.154.0 discovers the file by this layout and delegates to the
// agent by its name = "<id>" (verified live: a delegated subagent answered with
// the token its developer_instructions demanded). Tools are not emitted;
// codex agents have no tool list in this format.
func EmitCodexTOML(agent ComposedAgent) ([]byte, error) {
	if err := checkAgentText(agent, false); err != nil {
		return nil, err
	}
	desc := agent.Description
	if desc == "" {
		desc = "Agent: " + agent.ID
	}
	lines := []string{
		codexMarkerLine,
		`name = "` + agent.ID + `"`,
		`description = "` + quoteLine(desc) + `"`,
	}
	if writesModelLine(agent.Model) {
		lines = append(lines, `model = "`+quoteLine(agent.Model)+`"`)
	}
	body := strings.ReplaceAll(promptBody(agent.Prompt), `\`, `\\`)
	body = strings.ReplaceAll(body, `"""`, `\"\"\"`)
	lines = append(lines,
		`developer_instructions = """`,
		escapeControls(body, true),
		`"""`,
	)
	return []byte(strings.Join(lines, "\n") + "\n"), nil
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
	want, err := EmitCodexTOML(agent)
	if err != nil {
		return MaterializeResult{Path: CodexAgentPath(workDir, agent.ID)}, err
	}
	if _, err := ensureDirNoSymlinks(workDir, codexAgentRelDir...); err != nil {
		return MaterializeResult{Path: CodexAgentPath(workDir, agent.ID)}, err
	}
	return syncManagedFile(CodexAgentPath(workDir, agent.ID), want, isCodexGenerated(agent.ID))
}
