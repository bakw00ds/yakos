package agentscompose

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bakw00ds/yakos/internal/runtime"
)

// Shared plumbing for the codex and agy agent-file materializers (K-134).
//
// Both write yakOS-owned files into the project the agent will run in:
//
//	codex: <workdir>/.codex/agents/yakos-<id>.toml
//	agy:   <workdir>/.agents/skills/yakos-<id>/SKILL.md
//
// The emitters are ports of the bash emitters (cli/lib/runtimes/codex.sh
// yk_rt_codex_emit_toml and agy.sh yk_rt_agy_emit_md) and write the same bytes
// for the same agent JSON; TestMaterializeParity_* and
// tests/run-runtime-fixtures.sh prove it, over a corpus and over every framework
// agent (the two composers feed the emitters different JSON, which the emitters
// absorb: see promptBody and writesModelLine).
//
// Safety rules, all enforced here:
//   - A file is rewritten only if it carries the yakos-generated marker. A file
//     without it (an operator's own agent that happens to use the yakos- prefix)
//     is left untouched and reported as skipped. Deleting the marker line is
//     how an operator takes ownership of a generated file.
//   - A file whose bytes would not change is not rewritten, so its mtime stays
//     put and concurrent dispatches of one agent never race on content.
//   - Files are 0644, written to a temp file and renamed into place.
//   - No path component below the working directory may be a symlink, and the
//     target itself must not be one: a cloned repository can ship a symlinked
//     .codex/agents pointing outside the project.
//   - The agent id becomes part of a file name, so it must be a plain token.

// generatedMarkerToken is how a generated file is recognised. Each emitter
// wraps it in the comment syntax of its format.
const generatedMarkerToken = "yakos-generated:"

const (
	codexMarkerLine = "# " + generatedMarkerToken + " rewritten on every dispatch. Delete this line to keep your edits."
	agyMarkerLine   = "<!-- " + generatedMarkerToken + " rewritten on every dispatch. Delete this line to keep your edits. -->"
)

// markerScanLines bounds how much of an existing file is searched for the
// marker: its first lines. The bash emitters scan the same window (head -n 12),
// so both implementations decide identically which files are theirs.
const markerScanLines = 12

// maxManagedFileBytes bounds an existing file read for comparison; anything
// larger is not a file this package wrote.
const maxManagedFileBytes = 1 << 20

var agentIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

// ErrUnsupportedRuntime is returned for a runtime with no file-based agent form.
var ErrUnsupportedRuntime = errors.New("agentscompose: runtime has no agent file layout")

// MaterializeResult reports what a materialize call did.
type MaterializeResult struct {
	// Path is the agent definition file (the TOML or the SKILL.md).
	Path string
	// Written is true when the file's bytes were created or changed.
	Written bool
	// Skipped is empty when the file is in place and current. Otherwise it
	// says why nothing was written: "unchanged" or "not yakos-generated".
	Skipped string
}

// Skip reasons.
const (
	SkipUnchanged       = "unchanged"
	SkipNotYakosManaged = "not yakos-generated"
)

// validAgentID reports whether id may become part of a file name.
func validAgentID(id string) bool { return agentIDPattern.MatchString(id) }

// hasMarker reports whether the first markerScanLines lines of content carry
// the generated marker.
func hasMarker(content []byte) bool {
	lines := bytes.SplitN(content, []byte("\n"), markerScanLines+1)
	if len(lines) > markerScanLines {
		lines = lines[:markerScanLines]
	}
	return bytes.Contains(bytes.Join(lines, []byte("\n")), []byte(generatedMarkerToken))
}

// oneLine collapses line breaks to single spaces. TOML basic strings and YAML
// double-quoted scalars are single-line here; a description with a raw newline
// would corrupt the file.
var lineBreaks = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ")

func oneLine(s string) string { return lineBreaks.Replace(s) }

// escapeBackslashQuote escapes the two characters that end or alter a
// double-quoted string, in the order the bash emitters apply them.
func escapeBackslashQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}

// escapeControls rewrites the characters a TOML basic string or a YAML
// double-quoted scalar may not hold raw as \u00XX, in the upper-case hex the
// chat path's tomlString (runtime/codex.go) uses: the C0 controls and DEL. TAB
// is legal in both and stays. A multi-line TOML string (multiline = true) also
// keeps LF and a CR that starts a CRLF pair, which TOML reads as newlines; a
// lone CR is not a newline there and is escaped. Everything else, including
// all non-ASCII, passes through. The result is only valid input to those two
// quoting forms; apply it after the backslash and quote escaping.
//
// The bash emitters (python, and the jq fallback) apply the same rule, and
// TestMaterializeParity_* holds the three in step.
func escapeControls(s string, multiline bool) string {
	clean := true
	for i := 0; i < len(s); i++ {
		if needsControlEscape(s, i, multiline) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	for i := 0; i < len(s); i++ {
		if needsControlEscape(s, i, multiline) {
			fmt.Fprintf(&b, `\u%04X`, s[i])
		} else {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// needsControlEscape reports whether the byte at s[i] must be written as an
// escape. Bytes below 0x80 are whole characters in UTF-8 and every byte of a
// multi-byte character is >= 0x80, so scanning bytes cannot split a rune.
func needsControlEscape(s string, i int, multiline bool) bool {
	c := s[i]
	switch {
	case c == '\t':
		return false
	case c == '\n':
		return !multiline
	case c == '\r':
		return !multiline || i+1 >= len(s) || s[i+1] != '\n'
	}
	return c < 0x20 || c == 0x7f
}

// quoteLine is the body (without the surrounding quotes) of a one-line TOML
// basic string or YAML double-quoted scalar holding s.
func quoteLine(s string) string {
	return escapeControls(escapeBackslashQuote(oneLine(s)), false)
}

// promptBody normalises an agent prompt for the generated file: leading line
// breaks and trailing newlines are dropped. The bash composer keeps the blank
// line that follows the frontmatter in a prompt and the Go composer trims it, so
// without this the two would write different files for the same agent. Both
// emitters apply the same rule (TestMaterializeParity_RealFrameworkAgents).
func promptBody(prompt string) string {
	return strings.TrimRight(strings.TrimLeft(prompt, "\r\n"), "\n")
}

// writesModelLine reports whether the generated file gets a model line for
// model. A Claude tier (haiku, sonnet, opus, fable) never does: it is what the
// composers produce for an agent pinned to a semantic alias such as balanced
// (the bash composer resolves aliases to tiers itself), and neither codex nor
// agy has a model by that name. codex fails such a subagent ("its fixed `sonnet`
// model is not supported with this Codex ChatGPT account", checked live with
// codex-cli 0.154.0). With no line the harness default applies, which is what
// an empty alias for the runtime means.
func writesModelLine(model string) bool {
	return model != "" && !runtime.ValidateTier(model)
}

// checkAgentText refuses an agent whose text holds a NUL byte. There is no
// legitimate persona with one, and neither TOML files nor argv carry it. The
// tools are checked only when the file lists them (the agy skill does, the
// codex agent does not).
func checkAgentText(agent ComposedAgent, withTools bool) error {
	fields := []struct{ name, text string }{
		{"description", agent.Description},
		{"prompt", agent.Prompt},
		{"model", agent.Model},
	}
	if withTools {
		for _, t := range agent.Tools {
			fields = append(fields, struct{ name, text string }{"tool", t})
		}
	}
	for _, f := range fields {
		if strings.IndexByte(f.text, 0) >= 0 {
			return fmt.Errorf("agentscompose: agent %q: %s contains a NUL byte", agent.ID, f.name)
		}
	}
	return nil
}

// ensureDirNoSymlinks creates root/rel... one component at a time, refusing a
// component that exists as a symlink or as a non-directory. root itself is
// trusted (it is the project the operator pointed yakOS at, and may legitimately
// be reached through a symlink such as /tmp on macOS).
func ensureDirNoSymlinks(root string, rel ...string) (string, error) {
	cur := root
	for _, part := range rel {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		switch {
		case err == nil:
			if fi.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("agentscompose: refusing %s: it is a symlink", cur)
			}
			if !fi.IsDir() {
				return "", fmt.Errorf("agentscompose: refusing %s: not a directory", cur)
			}
		case os.IsNotExist(err):
			if err := os.Mkdir(cur, 0o755); err != nil && !os.IsExist(err) { //nolint:gosec // project-visible agent dir
				return "", fmt.Errorf("agentscompose: mkdir %s: %w", cur, err)
			}
		default:
			return "", fmt.Errorf("agentscompose: stat %s: %w", cur, err)
		}
	}
	return cur, nil
}

// readManaged reads an existing target for comparison. exists is false when
// the file is absent. A symlink, a non-regular file or an oversized file is an
// error: none of them is something this package wrote.
func readManaged(path string) (content []byte, exists bool, err error) {
	fi, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil, true, fmt.Errorf("agentscompose: refusing %s: it is a symlink", path)
	}
	if !fi.Mode().IsRegular() {
		return nil, true, fmt.Errorf("agentscompose: refusing %s: not a regular file", path)
	}
	f, err := os.Open(path) //nolint:gosec // path built from a validated id under the project
	if err != nil {
		return nil, true, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxManagedFileBytes+1))
	if err != nil {
		return nil, true, err
	}
	if len(data) > maxManagedFileBytes {
		return nil, true, fmt.Errorf("agentscompose: refusing %s: larger than %d bytes, not a file yakOS wrote", path, maxManagedFileBytes)
	}
	return data, true, nil
}

// writeFileAtomic writes data to path via a temp file in the same directory,
// with mode 0644, and renames it into place.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".yakos-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil { //nolint:gosec // agent files are project-visible, not secret
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// syncManagedFile brings path to want under the marker rule. isOurs decides
// whether an existing file may be replaced (it carries the marker, or is a
// legacy yakOS-generated file).
func syncManagedFile(path string, want []byte, isOurs func(existing []byte) bool) (MaterializeResult, error) {
	res := MaterializeResult{Path: path}
	existing, exists, err := readManaged(path)
	if err != nil {
		return res, err
	}
	if exists {
		if !isOurs(existing) {
			res.Skipped = SkipNotYakosManaged
			return res, nil
		}
		if bytes.Equal(existing, want) {
			res.Skipped = SkipUnchanged
			return res, nil
		}
	}
	if err := writeFileAtomic(path, want); err != nil {
		return res, fmt.Errorf("agentscompose: write %s: %w", path, err)
	}
	res.Written = true
	return res, nil
}

// MaterializeRuntimeAgent writes the file-based registration of agent for
// runtimeName ("codex" or "agy") under workDir, the directory the runtime will
// be started in. Any other runtime yields ErrUnsupportedRuntime.
func MaterializeRuntimeAgent(runtimeName, workDir string, agent ComposedAgent) (MaterializeResult, error) {
	switch runtimeName {
	case "codex":
		return MaterializeCodexAgent(workDir, agent)
	case "agy":
		return MaterializeAgyAgent(workDir, agent)
	}
	return MaterializeResult{}, fmt.Errorf("%w: %q", ErrUnsupportedRuntime, runtimeName)
}

func checkMaterializeArgs(workDir string, agent ComposedAgent) error {
	if workDir == "" || !filepath.IsAbs(workDir) {
		return fmt.Errorf("agentscompose: working directory must be an absolute path, got %q", workDir)
	}
	if !validAgentID(agent.ID) {
		return fmt.Errorf("agentscompose: agent id %q cannot be used in a file name", agent.ID)
	}
	return nil
}
