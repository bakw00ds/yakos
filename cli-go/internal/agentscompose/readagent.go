package agentscompose

// readagent.go: the one exported way to read an agent file for anything that is
// not Compose. `yakos agent`, `yakos teach` and the model router each used to open
// a project agent with os.ReadFile, which follows a symlink anywhere, opens a FIFO
// and reads a file of any size, the three things readAgentFile exists to refuse. A
// cloned repository controls .claude/agents, so a link there to ~/.aws/credentials
// became the agent's text in a lint, a lesson backup or a promote. They call this
// instead, so there is no second reader to drift from the first.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrRefused marks a file the roster reader's rules do not allow to be read: a
// symlink out of the agent directories, a file that is not regular or is over
// MaxAgentFileBytes, a linked project directory, or an extends value that is not a
// bare agent id. The wrapped message says which, and never carries a path.
var ErrRefused = errors.New("agent file refused")

// ReadAgentFile reads the agent file at path under the rules Compose reads under
// (see agentfile.go). A file that may not be read returns an error wrapping
// ErrRefused; a failure to read one that may be (including a missing file, which
// satisfies errors.Is(err, fs.ErrNotExist)) is returned as it is.
func ReadAgentFile(yakosRoot, project, path string) ([]byte, error) {
	if project != "" {
		dir := filepath.Join(project, ".claude", "agents")
		if sameDir(filepath.Dir(filepath.Clean(path)), dir) {
			if p := InspectProjectDir(project, dir); p != DirOK {
				return nil, fmt.Errorf("%w: %s", ErrRefused, p.Reason())
			}
		}
	}
	data, skip, err := readAgentFile(path, agentRules(yakosRoot, project))
	if err != nil {
		return nil, err
	}
	if skip != "" {
		return nil, fmt.Errorf("%w: %s", ErrRefused, skip)
	}
	return data, nil
}

// sameDir reports whether a and b name one directory: the same cleaned string, or
// directories os.SameFile calls the same (a case-variant or symlinked spelling of
// the project root). A string compare alone let a caller that spelled the root
// differently skip the linked-directory check.
func sameDir(a, b string) bool {
	if a == b {
		return true
	}
	ia, err := os.Stat(a)
	if err != nil {
		return false
	}
	ib, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ia, ib)
}

// ReadExtendsTemplate reads the template an `extends:` value names: lib/agents/<id>.md
// under yakosRoot, where id must be a bare agent id, and nowhere else (not the
// project's own agents, which Compose does not extend from either).
func ReadExtendsTemplate(yakosRoot, project, id string) ([]byte, error) {
	if !BareAgentID(id) {
		return nil, fmt.Errorf("%w: extends value %s is not a bare agent id (%s)", ErrRefused, DisplayValue(id), BareIDRule)
	}
	return ReadAgentFile(yakosRoot, project, filepath.Join(yakosRoot, "lib", "agents", id+".md"))
}

// ReadAgentFileIn is ReadAgentFile for a caller that holds the symlink roots and
// not the framework and project paths: `yakos validate` walks a tree it was
// given, and reads each file under the roots agentscompose.AgentFileRoots names
// for that tree. The rules are the same ones, the open is the same race-free
// open, and the read is bounded by MaxAgentFileBytes.
func ReadAgentFileIn(path string, roots []string) ([]byte, error) {
	data, skip, err := readAgentFile(path, fileRules{roots: roots, outside: AgentOutsideReason})
	if err != nil {
		return nil, err
	}
	if skip != "" {
		return nil, fmt.Errorf("%w: %s", ErrRefused, skip)
	}
	return data, nil
}

// ReadRegularFile reads a markdown file whose directory the caller has already
// chosen: a symlink may lead anywhere, but the target must be a regular file within
// MaxAgentFileBytes, it is opened without blocking and without following a link
// swapped in after the check, it must be the file that was checked, and the read is
// bounded. It is what a pass that cannot say which roots apply (the frontmatter
// and line-count passes shared by agents, skills and rules) reads with, instead of
// a stat followed by os.ReadFile.
func ReadRegularFile(path string) ([]byte, error) {
	data, skip, err := readAgentFile(path, fileRules{anywhere: true})
	if err != nil {
		return nil, err
	}
	if skip != "" {
		return nil, fmt.Errorf("%w: %s", ErrRefused, skip)
	}
	return data, nil
}
