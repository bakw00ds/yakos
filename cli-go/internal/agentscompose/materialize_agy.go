package agentscompose

import (
	"path/filepath"
	"strings"
)

// agySkillGitignore keeps the generated skill directory out of the project's
// git status without touching the project's own .gitignore: a .gitignore that
// ignores "*" also ignores itself.
const agySkillGitignore = "*\n"

// AgySkillDir returns the directory MaterializeAgyAgent owns for id under
// workDir: <workDir>/.agents/skills/yakos-<id>.
func AgySkillDir(workDir, id string) string {
	return filepath.Join(workDir, ".agents", "skills", "yakos-"+id)
}

// AgySkillPath returns the SKILL.md path for id under workDir.
func AgySkillPath(workDir, id string) string {
	return filepath.Join(AgySkillDir(workDir, id), "SKILL.md")
}

// yamlQuote renders s as a YAML double-quoted scalar on one line, applying the
// escapes the bash emitter's yq() applies (backslash, quote, and \u00XX for
// control characters, which a YAML double-quoted scalar may not hold raw).
func yamlQuote(s string) string { return `"` + quoteLine(s) + `"` }

// EmitAgySkill returns the bytes of the agy workspace skill for agent. It is a
// port of yk_rt_agy_emit_md (cli/lib/runtimes/agy.sh, python path) and must
// stay byte-identical to it for the same agent JSON:
//
//	---
//	name: yakos-<id>
//	description: "<one line>"
//	model: "<model>"             (only for a model that is not a Claude tier)
//	tools: ["Read", "Edit"]      (only when agent.Tools is non-empty)
//	---
//	<!-- yakos-generated: ... -->
//
//	<prompt: leading line breaks and trailing newlines dropped>
//
// An agent text that holds a NUL byte is refused with an error. The model line
// is omitted for a Claude tier, as in the codex file (see writesModelLine).
//
// The skill name carries the yakos- prefix, equal to the directory name as the
// Agent Skills layout requires, so that the framed prompt's @yakos-<id> mention
// resolves by either name. Note this is NOT what the bash emitter wrote before
// K-134: it wrote a flat yakos-<id>.md with name: <id>, a layout agy 1.2.x does
// not discover (it loads <dir>/<skill>/SKILL.md).
func EmitAgySkill(agent ComposedAgent) ([]byte, error) {
	if err := checkAgentText(agent, true); err != nil {
		return nil, err
	}
	desc := agent.Description
	if desc == "" {
		desc = "Agent: " + agent.ID
	}
	lines := []string{
		"---",
		"name: yakos-" + agent.ID,
		"description: " + yamlQuote(desc),
	}
	if writesModelLine(agent.Model) {
		lines = append(lines, "model: "+yamlQuote(agent.Model))
	}
	if len(agent.Tools) > 0 {
		quoted := make([]string, len(agent.Tools))
		for i, t := range agent.Tools {
			quoted[i] = yamlQuote(t)
		}
		lines = append(lines, "tools: ["+strings.Join(quoted, ", ")+"]")
	}
	lines = append(lines,
		"---",
		agyMarkerLine,
		"",
		promptBody(agent.Prompt),
	)
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}

// MaterializeAgyAgent writes <workDir>/.agents/skills/yakos-<id>/SKILL.md (and
// a .gitignore beside it) for agent. See materialize_files.go for the marker,
// skip-write, permission and symlink rules. If SKILL.md exists without the
// marker the whole directory is treated as the operator's and left alone.
func MaterializeAgyAgent(workDir string, agent ComposedAgent) (MaterializeResult, error) {
	if err := checkMaterializeArgs(workDir, agent); err != nil {
		return MaterializeResult{}, err
	}
	skillPath := AgySkillPath(workDir, agent.ID)
	want, err := EmitAgySkill(agent)
	if err != nil {
		return MaterializeResult{Path: skillPath}, err
	}
	if _, err := ensureDirNoSymlinks(workDir, ".agents", "skills", "yakos-"+agent.ID); err != nil {
		return MaterializeResult{Path: skillPath}, err
	}
	res, err := syncManagedFile(skillPath, want, hasMarker)
	if err != nil || res.Skipped == SkipNotYakosManaged {
		return res, err
	}
	// The directory is ours now: keep its generated files out of git status.
	giPath := filepath.Join(AgySkillDir(workDir, agent.ID), ".gitignore")
	if _, err := syncManagedFile(giPath, []byte(agySkillGitignore), func([]byte) bool {
		return true // any .gitignore inside our own skill directory is ours to normalise
	}); err != nil {
		return res, err
	}
	return res, nil
}
