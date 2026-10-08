package validate

// agentfiles.go — the agent-file findings of `yakos validate` that mirror what
// the Go dispatcher does (sec-324). Compose leaves an agent file out, with a
// warning, when it has a line over the cap, is a symlink that resolves outside
// the framework's lib/agents and the project's .claude/agents or to anything but
// a regular file, is not a regular file, is over the size cap, or has an extends: that is
// not a bare agent id. The persona that file
// would have supplied is then silently the framework agent's, or nothing, so
// validate turns each of those into an error, and CI sees it.
//
// The rules are agentscompose's, called directly. The bash validator
// (check_agent_enums in cli/lib/validate.sh) mirrors them by hand and prints the
// same text; tests/run-agent-enums-test.sh keeps the two byte-identical.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bakw00ds/yakos/internal/agentscompose"
)

// agentFileFinding returns why Compose would not read the agent file at path, as
// the text of a validate error, or "" when it would. It reads nothing from a file
// that is not a safe regular file. roots are the directories a symlink may
// resolve into (agentscompose.AgentFileRoots).
func agentFileFinding(path string, roots []string) string {
	problem, err := agentscompose.InspectAgentFile(path, roots)
	if err != nil {
		return "" // not examinable: the frontmatter pass reports a file it cannot read
	}
	switch problem {
	case agentscompose.ProblemUnresolved:
		return "symlink does not resolve to a regular file; the Go dispatcher skips it"
	case agentscompose.ProblemOutside:
		return agentscompose.AgentOutsideReason + "; the Go dispatcher skips it"
	case agentscompose.ProblemNotRegular:
		return "not a regular file; the Go dispatcher skips it"
	case agentscompose.ProblemTooLarge:
		return fmt.Sprintf("file is larger than %d bytes; the Go dispatcher skips it", agentscompose.MaxAgentFileBytes)
	}
	// Read through the hardened reader, not os.ReadFile: the inspection above and an
	// open here are two steps, and the entry can change between them.
	data, err := agentscompose.ReadAgentFileIn(path, roots)
	if err != nil {
		return ""
	}
	content := string(data)
	if n := agentscompose.LongLine(content); n > 0 {
		return fmt.Sprintf("line %d is longer than %d bytes; the Go dispatcher skips it; split the line", n, agentscompose.MaxLineBytes)
	}
	// extends: is a bare agent id and nothing else. The value is read as Compose
	// reads it, raw, so a quoted value or one with a trailing comment is not one.
	if v := agentscompose.ExtendsValue(content); v != "" && !agentscompose.BareAgentID(v) {
		return fmt.Sprintf("extends value %s is not a bare agent id (%s); the Go dispatcher skips it", agentscompose.DisplayValue(v), agentscompose.BareIDRule)
	}
	return ""
}

// agentsDirOf and skillsDirOf are the directories a pass reads agent and skill
// files from: base/agents and base/skills, or a path that never exists when
// validateTree refused the project's directory, so the pass finds nothing in it.
func agentsDirOf(cfg Config, base string) string {
	if cfg.skipAgentsDir {
		return filepath.Join(base, ".rejected-agents")
	}
	return filepath.Join(base, "agents")
}

func skillsDirOf(cfg Config, base string) string {
	if cfg.skipSkillsDir {
		return filepath.Join(base, ".rejected-skills")
	}
	return filepath.Join(base, "skills")
}

// agentRootsFor returns the directories a symlinked agent file under base/agents
// may resolve into: lib/agents, and in project mode (base is a project's .claude)
// the project's .claude/agents too. It is what Compose uses for the same tree.
func agentRootsFor(cfg Config, base string) []string {
	if filepath.Base(filepath.Clean(base)) == ".claude" {
		return agentscompose.AgentFileRoots(cfg.YakosRoot, filepath.Dir(filepath.Clean(base)))
	}
	return agentscompose.AgentFileRoots(cfg.YakosRoot, "")
}

// skillRootsFor is agentRootsFor for a SKILL.md: lib/skills, and in project mode
// the project's .claude/skills too. It is what ComposeSkills uses for the same tree.
func skillRootsFor(cfg Config, base string) []string {
	if filepath.Base(filepath.Clean(base)) == ".claude" {
		return agentscompose.SkillFileRoots(cfg.YakosRoot, filepath.Dir(filepath.Clean(base)))
	}
	return agentscompose.SkillFileRoots(cfg.YakosRoot, "")
}

// refusedLink reports a symlink at path that the dispatcher would not follow,
// given the roots a link may resolve into. It is false for anything that is not a
// symlink: such a file is read, or not, by the checks of readableAgentFile.
func refusedLink(path string, roots []string) bool {
	fi, err := os.Lstat(path)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return false
	}
	problem, err := agentscompose.InspectAgentFile(path, roots)
	return err != nil || problem != agentscompose.ProblemNone
}

// readableAgentEntry is readableAgentFile for an entry of an agents directory: a
// symlink must also be one Compose would follow. A link it refuses is not read
// through, so a pass over agent files does not print about, or look inside, a
// file the dispatcher never sees (the project's .env, say). The link is reported
// once, by checkAgentEnums.
func readableAgentEntry(path string, roots []string) bool {
	return readableAgentFile(path) && !refusedLink(path, roots)
}

// readableAgentFile reports whether a validate pass can read the markdown file
// without risk: once symlinks are followed it is a regular file within the size
// cap. A FIFO would block the read for good and a device such as /dev/zero would
// never end. The readers every pass shares (parseFrontmatter, countLines) and the
// passes that read agent files themselves ask first; the file is reported, once,
// by checkAgentEnums.
func readableAgentFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular() && fi.Size() <= agentscompose.MaxAgentFileBytes
}
