package validate

// agentfiles.go — the agent-file findings of `yakos validate` that mirror what
// the Go dispatcher does (sec-324). Compose leaves an agent file out, with a
// warning, when it has a line over the cap, is a symlink that resolves outside
// the framework's lib/ and the project directory or to anything but a regular
// file, is not a regular file, is over the size cap, or has an extends: that is
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
		return "symlink resolves outside the framework lib/ and the project directory; the Go dispatcher skips it"
	case agentscompose.ProblemNotRegular:
		return "not a regular file; the Go dispatcher skips it"
	case agentscompose.ProblemTooLarge:
		return fmt.Sprintf("file is larger than %d bytes; the Go dispatcher skips it", agentscompose.MaxAgentFileBytes)
	}
	data, err := os.ReadFile(path) //nolint:gosec // a regular file within the size cap, checked above
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
