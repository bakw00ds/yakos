package agentscompose

// agentfile.go — which files Compose reads as an agent, and how much of one.
//
// A cloned repository controls the project's .claude/agents, and a daemon
// composes the roster from it on every request, so the roster reader does not
// read a directory entry blindly:
//
//   - A symlink is followed only to a regular file inside the framework's lib/
//     or the project directory. A link to ~/.aws/credentials would otherwise
//     become an agent's persona, its first line the description /api/skills
//     hands to every reader, and the whole file text what the model vendor is
//     sent as the system prompt. Installed layouts keep working: the per-file
//     links in a project's .claude/agents that point into lib/agents resolve
//     inside lib/.
//   - An entry that is not a regular file is skipped without being opened. A
//     FIFO blocks open(2) for good, and a device such as /dev/zero never ends.
//   - A file over MaxAgentFileBytes is skipped, and the read itself is bounded,
//     so a file whose size is not known up front cannot exhaust the daemon.
//
// `yakos validate` (Go) calls the same functions, so what it rejects is exactly
// what Compose leaves out. The bash validator mirrors the rules by hand and the
// parity test in tests/run-agent-enums-test.sh keeps the two in step.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// MaxAgentFileBytes bounds one agent file. A real one is a few KiB, and the chat
// paths cap the persona they pass on the command line at 64 KiB, so 4 MiB is far
// beyond sane and still small enough that no file can exhaust the daemon.
const MaxAgentFileBytes = 4 << 20

// MaxLineBytes is the longest an agent file's line may be. A line of this many
// bytes or more, counting a carriage return before the newline, is refused.
const MaxLineBytes = maxLineBytes

// Problem names why Compose does not read an agent file.
type Problem int

const (
	// ProblemNone: Compose reads the file.
	ProblemNone Problem = iota
	// ProblemUnresolved: a symlink that does not end at a regular file. It is
	// dangling, loops, or points at a directory or a special file.
	ProblemUnresolved
	// ProblemOutside: a symlink to a regular file outside the framework's lib/
	// and the project directory.
	ProblemOutside
	// ProblemNotRegular: the entry is neither a regular file nor a symlink, for
	// example a FIFO, a socket or a device.
	ProblemNotRegular
	// ProblemTooLarge: a regular file over MaxAgentFileBytes.
	ProblemTooLarge
)

// warning is the reason Compose gives when it skips the file.
func (p Problem) warning() string {
	switch p {
	case ProblemUnresolved:
		return "symlink does not resolve to a regular file"
	case ProblemOutside:
		return "symlink resolves outside the framework lib/ and the project directory"
	case ProblemNotRegular:
		return "not a regular file"
	case ProblemTooLarge:
		return fmt.Sprintf("larger than %d bytes", MaxAgentFileBytes)
	}
	return ""
}

// AgentFileRoots returns the directories a symlinked agent file may resolve into:
// the framework's lib/ and, when there is one, the project directory.
func AgentFileRoots(yakosRoot, project string) []string {
	var roots []string
	if yakosRoot != "" {
		roots = append(roots, filepath.Join(yakosRoot, "lib"))
	}
	if project != "" {
		roots = append(roots, project)
	}
	return roots
}

// InspectAgentFile says whether Compose would read the agent file at path,
// without opening it, so a FIFO cannot block it. The error is for a file that
// cannot even be examined, which is not the same as a file that is refused.
func InspectAgentFile(path string, roots []string) (Problem, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return ProblemNone, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return ProblemUnresolved, nil
		}
		target, err := os.Stat(resolved)
		if err != nil || !target.Mode().IsRegular() {
			return ProblemUnresolved, nil
		}
		if !insideRoots(resolved, roots) {
			return ProblemOutside, nil
		}
		fi = target
	} else if !fi.Mode().IsRegular() {
		return ProblemNotRegular, nil
	}
	if fi.Size() > MaxAgentFileBytes {
		return ProblemTooLarge, nil
	}
	return ProblemNone, nil
}

// insideRoots reports whether the file at resolved, a path with no symlinks left
// in it, lies under one of the root directories. It compares the identity of
// each ancestor directory with the roots, not path strings, so a root that is
// itself a symlink, a temp directory behind /var, and a differently cased
// spelling on a case-insensitive filesystem all count as the same place.
func insideRoots(resolved string, roots []string) bool {
	var rootInfos []os.FileInfo
	for _, r := range roots {
		if fi, err := os.Stat(r); err == nil && fi.IsDir() {
			rootInfos = append(rootInfos, fi)
		}
	}
	if len(rootInfos) == 0 {
		return false
	}
	for dir := filepath.Dir(resolved); ; {
		if fi, err := os.Stat(dir); err == nil {
			for _, ri := range rootInfos {
				if os.SameFile(fi, ri) {
					return true
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// readAgentFile reads the agent file at path when InspectAgentFile allows it. A
// non-empty skip is the reason to leave the file out, and err is an I/O failure
// on a file that was allowed.
func readAgentFile(path string, roots []string) (data []byte, skip string, err error) {
	problem, err := InspectAgentFile(path, roots)
	if err != nil {
		return nil, "", err
	}
	if problem != ProblemNone {
		return nil, problem.warning(), nil
	}
	f, err := os.Open(path) //nolint:gosec // inspected just above
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = f.Close() }()
	if fi, err := f.Stat(); err != nil {
		return nil, "", err
	} else if !fi.Mode().IsRegular() {
		return nil, ProblemNotRegular.warning(), nil // swapped between the two looks
	}
	// One byte past the cap, to tell "exactly the cap" from "more".
	data, err = io.ReadAll(io.LimitReader(f, MaxAgentFileBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > MaxAgentFileBytes {
		return nil, ProblemTooLarge.warning(), nil
	}
	return data, "", nil
}

// LongLine returns the 1-based number of the first line of content that the
// roster reader refuses for being too long, or 0 when it accepts every line. It
// runs the reader Compose uses, so the answer is Compose's, edge cases included.
func LongLine(content string) int {
	_, _, err := splitFrontmatter(content)
	var tooLong *lineTooLongError
	if errors.As(err, &tooLong) {
		return tooLong.Line
	}
	return 0
}
