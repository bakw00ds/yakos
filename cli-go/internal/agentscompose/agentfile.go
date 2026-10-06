package agentscompose

// agentfile.go — which files Compose reads as an agent, and how much of one.
//
// A cloned repository controls the project's .claude/agents, and a daemon
// composes the roster from it on every request, so the roster reader does not
// read a directory entry blindly:
//
//   - A symlink is followed only to a regular file inside the framework's
//     lib/agents or the project's .claude/agents (lib/skills and .claude/skills
//     for a SKILL.md). A link to ~/.aws/credentials, or to the project's own
//     .env or .git/config, would otherwise become an agent's persona, its first
//     line the description /api/skills hands to every reader, and the whole file
//     text what the model vendor is sent as the system prompt. The roots are
//     those directories, not the project or lib/ around them, because the
//     project holds files that are not agents. Installed layouts keep working:
//     the per-file links in a project's .claude/agents that point into
//     lib/agents resolve inside it.
//   - An entry that is not a regular file is skipped without being opened. A
//     FIFO blocks open(2) for good, and a device such as /dev/zero never ends.
//   - A file over MaxAgentFileBytes is skipped, and the read itself is bounded,
//     so a file whose size is not known up front cannot exhaust the daemon.
//   - The project's agent and skill directories are checked themselves. A file
//     seen through a linked directory is a regular file, so it never reaches the
//     symlink rule above, and a root that is itself a link resolves outside by
//     identity. So a project's .claude/agents or .claude/skills that is a symlink,
//     or has a symlinked .claude above it, must resolve to a directory inside the
//     project, or the whole directory is skipped, once, with a warning.
//   - What is inspected is what is read. Inspecting a path and then opening it
//     again lets the entry change in between: a symlink retargeted to an outside
//     file, or a regular file swapped for a FIFO. So the file is opened by the
//     path the inspection resolved, without following a link and without
//     blocking, and the opened descriptor must be a regular file and the same
//     file (os.SameFile) as the one inspected. Anything else is a skip.
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
	"strings"
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
	// ProblemOutside: a symlink to a regular file outside the agent directories
	// (AgentFileRoots), or outside the skill directories for a SKILL.md.
	ProblemOutside
	// ProblemNotRegular: the entry is neither a regular file nor a symlink, for
	// example a FIFO, a socket or a device.
	ProblemNotRegular
	// ProblemTooLarge: a regular file over MaxAgentFileBytes.
	ProblemTooLarge
	// ProblemChanged: the file opened is not the one inspected, or is not a
	// regular file. Only a read returns it, never InspectAgentFile.
	ProblemChanged
)

// AgentOutsideReason and SkillOutsideReason are what is said of a symlink that
// leaves the directories it may lead into. The bash twin prints the agent one,
// byte for byte, and the tests compare them.
const (
	AgentOutsideReason = "symlink resolves outside the framework lib/agents and the project's .claude/agents"
	SkillOutsideReason = "symlink resolves outside the framework lib/skills and the project's .claude/skills"
)

// fileRules say where a symlink may lead, and what is said when it leads
// elsewhere. There is one set for agent files and extends templates and one for
// SKILL.md.
type fileRules struct {
	roots   []string
	outside string
}

func agentRules(yakosRoot, project string) fileRules {
	return fileRules{roots: AgentFileRoots(yakosRoot, project), outside: AgentOutsideReason}
}

func skillRules(yakosRoot, project string) fileRules {
	return fileRules{roots: SkillFileRoots(yakosRoot, project), outside: SkillOutsideReason}
}

// warning is the reason Compose gives when it skips the file.
func (p Problem) warning(outside string) string {
	switch p {
	case ProblemUnresolved:
		return "symlink does not resolve to a regular file"
	case ProblemOutside:
		return outside
	case ProblemNotRegular:
		return "not a regular file"
	case ProblemTooLarge:
		return fmt.Sprintf("larger than %d bytes", MaxAgentFileBytes)
	case ProblemChanged:
		return "changed while it was being read"
	}
	return ""
}

// AgentFileRoots returns the directories a symlinked agent file, or an extends
// template, may resolve into: the framework's lib/agents and, when there is a
// project, its .claude/agents. A file anywhere else in either tree is not an agent
// file, so a link to it is not followed.
func AgentFileRoots(yakosRoot, project string) []string {
	var roots []string
	if yakosRoot != "" {
		roots = append(roots, filepath.Join(yakosRoot, "lib", "agents"))
	}
	if project != "" {
		roots = append(roots, filepath.Join(project, ".claude", "agents"))
	}
	return roots
}

// SkillFileRoots is AgentFileRoots for a SKILL.md: lib/skills and the project's
// .claude/skills.
func SkillFileRoots(yakosRoot, project string) []string {
	var roots []string
	if yakosRoot != "" {
		roots = append(roots, filepath.Join(yakosRoot, "lib", "skills"))
	}
	if project != "" {
		roots = append(roots, filepath.Join(project, ".claude", "skills"))
	}
	return roots
}

// InspectAgentFile says whether Compose would read the agent file at path,
// without opening it, so a FIFO cannot block it. The error is for a file that
// cannot even be examined, which is not the same as a file that is refused.
func InspectAgentFile(path string, roots []string) (Problem, error) {
	_, _, problem, err := inspect(path, roots)
	return problem, err
}

// inspect is InspectAgentFile that also says what it looked at: the identity of
// the file that would be read, and the path to open it by, which for a symlink is
// the path it resolves to and not the link.
func inspect(path string, roots []string) (target os.FileInfo, openPath string, problem Problem, err error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, "", ProblemNone, err
	}
	openPath = path
	if fi.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, "", ProblemUnresolved, nil
		}
		resolvedInfo, err := os.Stat(resolved)
		if err != nil || !resolvedInfo.Mode().IsRegular() {
			return nil, "", ProblemUnresolved, nil
		}
		if !insideRoots(resolved, roots) {
			return nil, "", ProblemOutside, nil
		}
		fi, openPath = resolvedInfo, resolved
	} else if !fi.Mode().IsRegular() {
		return nil, "", ProblemNotRegular, nil
	}
	if fi.Size() > MaxAgentFileBytes {
		return nil, "", ProblemTooLarge, nil
	}
	return fi, openPath, ProblemNone, nil
}

// DirProblem names why a project's agent or skill directory may not be read.
type DirProblem int

const (
	// DirOK: the directory may be read.
	DirOK DirProblem = iota
	// DirUnresolved: a symlink (the directory, or the .claude above it) that does
	// not end at a directory: dangling, a loop, or a file.
	DirUnresolved
	// DirOutside: a symlink that ends at a directory outside the project.
	DirOutside
)

// DirOutsideReason and DirUnresolvedReason are what is said of such a directory.
// The bash twin prints the same text, and the tests compare them.
const (
	DirOutsideReason    = "symlink resolves outside the project directory"
	DirUnresolvedReason = "symlink does not resolve to a directory"
)

// Reason is the text for the problem, "" for DirOK.
func (p DirProblem) Reason() string {
	switch p {
	case DirUnresolved:
		return DirUnresolvedReason
	case DirOutside:
		return DirOutsideReason
	}
	return ""
}

// InspectProjectDir says whether the project's agent or skill directory dir may be
// read: a plain directory may, and one reached through a symlink (dir itself, or
// the .claude above it) must resolve to a directory inside the project, compared
// by directory identity like the files are. Without a link there is nothing to
// resolve and nothing is looked up, so the common case costs two Lstat calls.
// The framework's directories are not the project's and are not checked.
func InspectProjectDir(project, dir string) DirProblem {
	if project == "" {
		return DirOK
	}
	linked := false
	for _, p := range []string{filepath.Join(project, ".claude"), dir} {
		if fi, err := os.Lstat(p); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			linked = true
		}
	}
	if !linked {
		return DirOK
	}
	if _, err := os.Lstat(dir); err != nil {
		return DirOK // nothing there, so nothing is read through the link
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return DirUnresolved
	}
	if fi, err := os.Stat(resolved); err != nil || !fi.IsDir() {
		return DirUnresolved
	}
	if !insideRoots(resolved, []string{project}) {
		return DirOutside
	}
	return DirOK
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

// raceHook, when a test sets it, runs between inspecting a file and opening it,
// where the entry can change under a real reader. It is nil otherwise.
var raceHook func(path string)

// readAgentFile reads the agent file at path when InspectAgentFile allows it. A
// non-empty skip is the reason to leave the file out, and err is an I/O failure
// on a file that was allowed.
func readAgentFile(path string, rules fileRules) (data []byte, skip string, err error) {
	inspected, openPath, problem, err := inspect(path, rules.roots)
	if err != nil {
		return nil, "", err
	}
	if problem != ProblemNone {
		return nil, problem.warning(rules.outside), nil
	}
	if raceHook != nil {
		raceHook(path)
	}
	f, err := os.OpenFile(openPath, readFlags, 0) //nolint:gosec // the path the inspection resolved
	if err != nil {
		if linkRefused(err) {
			return nil, ProblemChanged.warning(""), nil // a link swapped in for the file
		}
		return nil, "", err
	}
	defer func() { _ = f.Close() }()
	// What was opened must be what was inspected: a regular file, and that one.
	// Without this a directory swapped for a link, or a file replaced by another,
	// is read as though nothing happened. Both halves are needed: a file system may
	// hand a deleted file's inode number to the FIFO that replaces it, and then the
	// identity matches and only the type tells.
	opened, err := f.Stat()
	if err != nil {
		return nil, "", err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(opened, inspected) {
		return nil, ProblemChanged.warning(""), nil
	}
	return readBounded(f)
}

// readBounded reads r, which holds at most MaxAgentFileBytes if it is to be used.
// It stops one byte past that, to tell "exactly the cap" from "more", and an
// input of more is a skip. The size seen by the inspection does not bound this: a
// file can grow after it, and some regular files (under /proc) report no size at
// all. So the read itself is bounded, and no input can exhaust the daemon.
func readBounded(r io.Reader) (data []byte, skip string, err error) {
	data, err = io.ReadAll(io.LimitReader(r, MaxAgentFileBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > MaxAgentFileBytes {
		return nil, ProblemTooLarge.warning(""), nil
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

// BareIDRule says what BareAgentID accepts, for messages. cli/lib/agent-files.sh
// keeps the same text and the tests compare the two.
const BareIDRule = `1 to 128 of A-Z a-z 0-9 . _ -, starting with a letter or digit, no ".."`

// BareAgentID reports whether v is a bare agent id, the only form `extends:` may
// take: a file name stem and nothing that can reach another directory. It has
// no "/" or "\", does not start with a dot, and has no "..". Without this an
// agent could extend any .md file the daemon can read, such as one outside
// lib/agents, or aim the extends step at a huge file to fail every dispatch.
func BareAgentID(v string) bool {
	if v == "" || len(v) > 128 || strings.Contains(v, "..") {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case i > 0 && (c == '.' || c == '_' || c == '-'):
		default:
			return false
		}
	}
	return true
}

// DisplayValue renders a frontmatter value for a message. The value comes from a
// file the operator may not have written, and the bash twin has to print the same
// bytes, so the rule is simple: every byte outside printable ASCII becomes "?",
// at most 64 bytes are shown and "..." says there were more, and the whole is
// in double quotes.
func DisplayValue(v string) string {
	const shown = 64
	more := len(v) > shown
	if more {
		v = v[:shown]
	}
	b := []byte(v)
	for i, c := range b {
		if c < 0x20 || c > 0x7e {
			b[i] = '?'
		}
	}
	if more {
		return `"` + string(b) + `..."`
	}
	return `"` + string(b) + `"`
}

// ExtendsValue returns the `extends:` value of an agent file as Compose reads it,
// raw: the text after the colon, trimmed, quotes and a trailing comment kept. It
// is "" when there is none, and so when the frontmatter is unreadable.
func ExtendsValue(content string) string {
	fm, _, err := splitFrontmatter(content)
	if err != nil {
		return ""
	}
	return parseFrontmatter(fm)["extends"]
}

// skipAgentError says why one agent is left out of the roster although its own
// file was read: an `extends:` that is not a bare agent id, or a template that
// may not be read. Compose skips the agent with a warning and goes on.
type skipAgentError struct{ reason string }

func (e *skipAgentError) Error() string { return e.reason }
