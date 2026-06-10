// Package initialize is the Go port of cli/lib/init.sh.
//
// It implements `yakos init <name> --project <path> [flags]`:
//
//  1. Validate <name> (alphanumeric + hyphen + underscore).
//  2. Validate --project path exists and is a git repo.
//  3. Create ~/agent-control/<name>/work/current/{logs,artifacts,reports}/ and
//     work/archive/.
//  4. Write ~/agent-control/<name>/work/current/.session-started-history.ndjson
//     (empty) if missing.
//  5. Write ~/agent-control/<name>/.gitignore from embedded template if missing.
//  6. Write ~/agent-control/<name>/settings.local.json from embedded template
//     if missing.
//  7. Write ~/agent-control/<name>/work/current/hook-bypass.md from embedded
//     template if missing.
//  8. Write ~/agent-control/<name>/work/sessions.ndjson if missing.
//  9. Write ~/agent-control/<name>/.project-path.
// 10. Write <project>/.claude/path-allowlist.json from embedded template if
//     missing.
// 11. Write <project>/.claude/settings.json from embedded template if missing.
// 12. Ensure ~/.claude/projects/<encoded>/MEMORY.md exists.
// 13. Write <project>/AGENTS.md from embedded template if missing.
// 14. Write <project>/.yakos.yml from embedded template if missing.
// 15. Append yakOS runtime-agent gitignore patterns to <project>/.gitignore.
// 16. Apply --template <kind> overlay (rails/go/python/node/rust/static-site).
//
// Template files ship inside the binary via //go:embed (Decision D from
// docs/go-port-decisions-2026-06-02.md). No external file lookup at runtime.
//
// Hook script installation (bash's step 8) is NOT performed: the Go binary
// cannot self-copy the bash hook scripts from lib/hooks/ because those are
// bash source that lives outside the embed boundary. The binary instead prints
// an advisory reminding the operator to run `yakos refresh --project <path>`
// which handles hook installation idempotently.
//
// --with-gate and --multi-dev are accepted as no-ops with advisory messages;
// those features depend on bash utilities (sha256, git hooks, /var/lib/yakos)
// beyond the Phase 1 Go scope.
package initialize

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// templates holds every file under the templates/ directory, including
// hidden files (those starting with ".") via the "all:" prefix.
//
//go:embed all:templates
var templates embed.FS

// validNameRe matches names consisting only of alphanumeric, hyphen, underscore.
// Mirrors the case-pattern check in init.sh: *[!A-Za-z0-9_-]*)
var validNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// validKinds lists the accepted --template values in the order shown in help.
var validKinds = []string{"base", "rails", "go", "python", "node", "rust", "static-site"}

// Config carries everything Run needs.
type Config struct {
	// Name is the short project identifier (required).
	// Must match [A-Za-z0-9_-]+.
	Name string

	// ProjectPath is the absolute path to the project's git repo (required).
	ProjectPath string

	// Template is the kind overlay to apply on top of base.
	// Empty string or "base" means base only.
	// Accepted values: base, rails, go, python, node, rust, static-site.
	Template string

	// Force, when true, allows overwriting existing files.
	// In v0.1 only the project's .yakos.yml and .claude/ files honour this;
	// agent-control files are always preserved (the operator owns them).
	Force bool

	// WithGate and MultiDev are accepted for CLI parity but are no-ops
	// in the Go port; advisory messages are printed instead.
	WithGate bool
	MultiDev bool

	// DryRun lists files that would be created/written without writing them.
	DryRun bool

	// HomeDir overrides $HOME.  Defaults to os.Getenv("HOME") when empty.
	HomeDir string

	// Now is injected for tests; zero value uses time.Now().
	Now time.Time

	// Writer receives progress and summary output (default: os.Stdout).
	Writer io.Writer

	// ErrWriter receives advisory/warning messages (default: os.Stderr).
	ErrWriter io.Writer
}

// Result is the outcome of a successful Run call.
type Result struct {
	// Name is the project name that was initialised.
	Name string

	// ControlDir is ~/agent-control/<name>.
	ControlDir string

	// ProjectPath is the resolved (realpath'd) project directory.
	ProjectPath string

	// MemoryFile is the MEMORY.md path that was seeded.
	MemoryFile string

	// FilesWritten lists every file that was created or overwritten.
	FilesWritten []string

	// FilesSkipped lists every file that already existed and was not overwritten.
	FilesSkipped []string

	// DryRunFiles lists files that would have been written in a real run.
	// Populated only when Config.DryRun is true.
	DryRunFiles []string
}

// Run executes the init operation and returns a structured Result.
// Output is streamed to cfg.Writer as work proceeds.
//
// Errors are returned (not printed); the caller prints them and exits.
func Run(cfg Config) (*Result, error) {
	if cfg.Writer == nil {
		cfg.Writer = os.Stdout
	}
	if cfg.ErrWriter == nil {
		cfg.ErrWriter = os.Stderr
	}

	// ---- validate name -------------------------------------------------------
	if cfg.Name == "" {
		return nil, fmt.Errorf("init: missing <name>")
	}
	if !validNameRe.MatchString(cfg.Name) {
		return nil, fmt.Errorf("init: <name> must be alphanumeric (with - or _ allowed): got %q", cfg.Name)
	}

	// ---- validate project path -----------------------------------------------
	if cfg.ProjectPath == "" {
		return nil, fmt.Errorf("init: --project <path> is required")
	}
	projAbs, err := filepath.Abs(cfg.ProjectPath)
	if err != nil {
		return nil, fmt.Errorf("init: resolving project path %q: %w", cfg.ProjectPath, err)
	}
	if _, err := os.Stat(projAbs); err != nil {
		return nil, fmt.Errorf("init: project path not found: %s", projAbs)
	}
	if !isGitRepo(projAbs) {
		return nil, fmt.Errorf("init: %s is not a git repository", projAbs)
	}

	// ---- validate template kind ----------------------------------------------
	kind := cfg.Template
	if kind == "" {
		kind = "base"
	}
	if !isValidKind(kind) {
		return nil, fmt.Errorf("init: unknown --template %q; did you mean one of: %s",
			kind, strings.Join(validKinds, ", "))
	}

	// ---- resolve home --------------------------------------------------------
	home := cfg.HomeDir
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		home = "/tmp"
	}

	now := cfg.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	res := &Result{
		Name:        cfg.Name,
		ProjectPath: projAbs,
	}

	controlDir := filepath.Join(home, "agent-control", cfg.Name)
	res.ControlDir = controlDir
	workDir := filepath.Join(controlDir, "work")
	currentDir := filepath.Join(workDir, "current")

	_, _ = fmt.Fprintf(cfg.Writer, "yakos init %q --project %s\n", cfg.Name, projAbs)
	if cfg.DryRun {
		_, _ = fmt.Fprintln(cfg.Writer, "(dry-run: no files will be written)")
	}

	// ---- step 2-7: agent-control tree ----------------------------------------

	// Create directory structure.
	for _, sub := range []string{
		filepath.Join(currentDir, "logs"),
		filepath.Join(currentDir, "artifacts"),
		filepath.Join(currentDir, "reports"),
		filepath.Join(workDir, "archive"),
	} {
		if err := safeMkdirAll(sub, cfg.DryRun, res); err != nil {
			return nil, err
		}
	}

	// .session-started-history.ndjson — empty, created only if absent.
	sessHistory := filepath.Join(currentDir, ".session-started-history.ndjson")
	if err := writeIfAbsent(sessHistory, nil, cfg.DryRun, res); err != nil {
		return nil, err
	}

	// .gitignore for agent-control.
	gitignoreDst := filepath.Join(controlDir, ".gitignore")
	if err := writeTemplateIfAbsent("base", ".gitignore", gitignoreDst, cfg.DryRun, res); err != nil {
		return nil, err
	}

	// settings.local.json.
	localSettings := filepath.Join(controlDir, "settings.local.json")
	if err := writeTemplateIfAbsent("base", "settings.local.json", localSettings, cfg.DryRun, res); err != nil {
		return nil, err
	}

	// hook-bypass.md in work/current/.
	bypassDst := filepath.Join(currentDir, "hook-bypass.md")
	if err := writeTemplateIfAbsent("base", "hook-bypass.md", bypassDst, cfg.DryRun, res); err != nil {
		return nil, err
	}

	// sessions.ndjson — empty ledger.
	sessLog := filepath.Join(workDir, "sessions.ndjson")
	if err := writeIfAbsent(sessLog, nil, cfg.DryRun, res); err != nil {
		return nil, err
	}

	// .project-path — always written (idempotent pointer).
	projectPathFile := filepath.Join(controlDir, ".project-path")
	content := projAbs + "\n"
	if cfg.DryRun {
		res.DryRunFiles = append(res.DryRunFiles, projectPathFile)
	} else {
		if err := atomicWrite(projectPathFile, []byte(content), 0644); err != nil {
			return nil, fmt.Errorf("init: write .project-path: %w", err)
		}
		res.FilesWritten = append(res.FilesWritten, projectPathFile)
	}

	// ---- step 9-10: project .claude templates ---------------------------------

	claudeDir := filepath.Join(projAbs, ".claude")
	if !cfg.DryRun {
		if err := os.MkdirAll(claudeDir, 0755); err != nil { //nolint:gosec
			return nil, fmt.Errorf("init: mkdir %s: %w", claudeDir, err)
		}
	}

	pathAllowlist := filepath.Join(claudeDir, "path-allowlist.json")
	if err := writeTemplateConditional("base", ".claude/path-allowlist.json", pathAllowlist, cfg.Force, cfg.DryRun, res); err != nil {
		return nil, err
	}

	projectSettings := filepath.Join(claudeDir, "settings.json")
	if err := writeTemplateConditional("base", ".claude/settings.json", projectSettings, cfg.Force, cfg.DryRun, res); err != nil {
		return nil, err
	}

	// ---- step 11: auto-memory MEMORY.md --------------------------------------

	encoded := encodeProjectPath(projAbs)
	memDir := filepath.Join(home, ".claude", "projects", encoded)
	memFile := filepath.Join(memDir, "MEMORY.md")
	res.MemoryFile = memFile

	if cfg.DryRun {
		res.DryRunFiles = append(res.DryRunFiles, memFile)
	} else {
		if err := os.MkdirAll(memDir, 0755); err != nil { //nolint:gosec
			return nil, fmt.Errorf("init: mkdir %s: %w", memDir, err)
		}
		memContent := buildMemoryMD(cfg.Name, projAbs, now)
		if err := writeIfAbsent(memFile, []byte(memContent), cfg.DryRun, res); err != nil {
			return nil, err
		}
	}

	// ---- AGENTS.md -----------------------------------------------------------

	agentsMD := filepath.Join(projAbs, "AGENTS.md")
	if kind != "base" {
		// Kind overlay provides its own AGENTS.md; use it instead of base.
		if err := writeTemplateConditional(kind, "AGENTS.md", agentsMD, cfg.Force, cfg.DryRun, res); err != nil {
			return nil, err
		}
	} else {
		if err := writeTemplateConditional("base", "AGENTS.md", agentsMD, cfg.Force, cfg.DryRun, res); err != nil {
			return nil, err
		}
	}

	// ---- .yakos.yml ----------------------------------------------------------

	yakosYML := filepath.Join(projAbs, ".yakos.yml")
	// Apply kind overlay if not base; otherwise use base template.
	yakosYMLSrc := "yakos.yml"
	if kind != "base" {
		// Kind-specific overlay file (always exists in the embedded templates).
		if err := writeKindYAML(kind, yakosYMLSrc, yakosYML, cfg.Force, cfg.DryRun, res); err != nil {
			return nil, err
		}
	} else {
		if err := writeTemplateConditional("base", yakosYMLSrc, yakosYML, cfg.Force, cfg.DryRun, res); err != nil {
			return nil, err
		}
	}

	// ---- .gitignore yakOS runtime-agent patterns ---------------------------

	projGitignore := filepath.Join(projAbs, ".gitignore")
	const giMarker = "# yakOS — runtime-emitted agent files (v0.4.0+)"
	if cfg.DryRun {
		res.DryRunFiles = append(res.DryRunFiles, projGitignore+" (append yakOS patterns)")
	} else {
		if err := appendGitignorePatterns(projGitignore, giMarker); err != nil {
			_, _ = fmt.Fprintf(cfg.ErrWriter, "init: warning: could not update project .gitignore: %v\n", err)
		}
	}

	// ---- kind-specific .gitignore additions --------------------------------

	if kind != "base" {
		kindGiMarker := "# yakOS " + kind + " template additions"
		if cfg.DryRun {
			res.DryRunFiles = append(res.DryRunFiles, projGitignore+" (append "+kind+" patterns)")
		} else {
			if data, err := readTemplate(kind, ".gitignore"); err == nil {
				if appendErr := appendKindGitignore(projGitignore, kindGiMarker, data); appendErr != nil {
					_, _ = fmt.Fprintf(cfg.ErrWriter, "init: warning: could not append %s .gitignore additions: %v\n", kind, appendErr)
				}
			}
			// If the kind has no .gitignore template, skip silently.
		}
	}

	// ---- kind-specific .claude/agents/ files --------------------------------

	if kind != "base" {
		if err := writeKindAgents(kind, projAbs, cfg.Force, cfg.DryRun, res); err != nil {
			return nil, err
		}
	}

	// ---- advisories for no-op flags ------------------------------------------

	if cfg.WithGate {
		_, _ = fmt.Fprintln(cfg.ErrWriter, "init: --with-gate: git hook installation is handled by the bash yakos in Phase 1.")
		_, _ = fmt.Fprintln(cfg.ErrWriter, "      Run: YAKOS_IMPL=bash yakos git-hooks install")
	}
	if cfg.MultiDev {
		_, _ = fmt.Fprintln(cfg.ErrWriter, "init: --multi-dev: coord provisioning is handled by the bash yakos in Phase 1.")
		_, _ = fmt.Fprintln(cfg.ErrWriter, "      Run: YAKOS_IMPL=bash yakos init --multi-dev "+cfg.Name+" --project "+projAbs)
	}

	// ---- hook scripts advisory -----------------------------------------------

	_, _ = fmt.Fprintf(cfg.ErrWriter,
		"init: hook scripts not copied (Go port Phase 1 scope).\n"+
			"      Run: yakos refresh --project %s  (to install/update hooks)\n", projAbs)

	// ---- summary -------------------------------------------------------------

	if cfg.DryRun {
		_, _ = fmt.Fprintf(cfg.Writer, "\n[dry-run] files that would be created (%d):\n", len(res.DryRunFiles))
		for _, f := range res.DryRunFiles {
			_, _ = fmt.Fprintf(cfg.Writer, "  %s\n", f)
		}
	} else {
		_, _ = fmt.Fprintf(cfg.Writer, "\nYakOS init complete for %q.\n\n", cfg.Name)
		_, _ = fmt.Fprintf(cfg.Writer, "  Control dir:  %s\n", controlDir)
		_, _ = fmt.Fprintf(cfg.Writer, "  Project:      %s\n", projAbs)
		_, _ = fmt.Fprintf(cfg.Writer, "  Auto-memory:  %s\n", memFile)
		_, _ = fmt.Fprintf(cfg.Writer, "  Template:     %s\n", kind)
		_, _ = fmt.Fprintf(cfg.Writer, "  Written:      %d file(s)\n", len(res.FilesWritten))
		_, _ = fmt.Fprintf(cfg.Writer, "  Skipped:      %d file(s) (already exist)\n", len(res.FilesSkipped))
		_, _ = fmt.Fprintf(cfg.Writer, "\nTo start a session:\n  yakos start %s\n", cfg.Name)
	}

	return res, nil
}

// PrintHelp writes the --help text for `yakos init`, matching init.sh usage().
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos init <name> --project <path> [flags]

Bootstrap a project for use with YakOS:
  - Creates ~/agent-control/<name>/ with work/current/{logs,artifacts,reports}/
  - Writes project .claude/settings.json and .claude/path-allowlist.json
    from embedded templates if those files don't already exist.
  - Writes project .yakos.yml and AGENTS.md from embedded templates.
  - Ensures ~/.claude/projects/<encoded>/MEMORY.md exists for auto-memory.
  - Appends yakOS runtime-agent patterns to the project's .gitignore.

Hook scripts: run 'yakos refresh --project <path>' after init to install
hook scripts from lib/hooks/ (the Go port delegates hook installation to
the bash refresh command in Phase 1).

Arguments:
  <name>              Short identifier for this project (alphanumeric,
                      hyphen, underscore).
  --project <path>    Absolute path to the project's git repo (required).

Options:
  --template <kind>   Apply a project-type overlay on top of the base
                      template. Accepted kinds:
                        base (default), rails, go, python, node, rust,
                        static-site
  --force             Overwrite existing project-side files (.yakos.yml,
                      .claude/). Agent-control files are always preserved.
  --dry-run           List files that would be created without writing them.
  --with-gate         (advisory) Git pre-push gate — use bash yakos in Phase 1.
  --multi-dev         (advisory) Co-pilot mode — use bash yakos in Phase 1.
  --help, -h          Print this help.
`)
}

// ---- template helpers -------------------------------------------------------

// writeTemplateIfAbsent writes a base template file to dst only if dst does
// not exist.
func writeTemplateIfAbsent(kind, src, dst string, dryRun bool, res *Result) error {
	return writeTemplateConditional(kind, src, dst, false, dryRun, res)
}

// writeTemplateConditional writes a template file to dst.
// If force is false and dst already exists, it records dst in FilesSkipped.
// If force is true, it overwrites.
func writeTemplateConditional(kind, src, dst string, force, dryRun bool, res *Result) error {
	if !force {
		if _, err := os.Stat(dst); err == nil {
			res.FilesSkipped = append(res.FilesSkipped, dst)
			return nil
		}
	}
	data, err := readTemplate(kind, src)
	if err != nil {
		return fmt.Errorf("init: read template %s/%s: %w", kind, src, err)
	}
	if dryRun {
		res.DryRunFiles = append(res.DryRunFiles, dst)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil { //nolint:gosec
		return fmt.Errorf("init: mkdir %s: %w", filepath.Dir(dst), err)
	}
	if err := atomicWrite(dst, data, 0644); err != nil {
		return fmt.Errorf("init: write %s: %w", dst, err)
	}
	res.FilesWritten = append(res.FilesWritten, dst)
	return nil
}

// writeKindYAML writes the kind-specific yakos.yml overlay to dst.
func writeKindYAML(kind, src, dst string, force, dryRun bool, res *Result) error {
	return writeTemplateConditional(kind, src, dst, force, dryRun, res)
}

// readTemplate reads the embedded template file at templates/<kind>/<src>.
func readTemplate(kind, src string) ([]byte, error) {
	path := "templates/" + kind + "/" + src
	return templates.ReadFile(path)
}

// writeIfAbsent creates dst with content only if dst does not already exist.
// An empty/nil content creates an empty file.
func writeIfAbsent(dst string, content []byte, dryRun bool, res *Result) error {
	if _, err := os.Stat(dst); err == nil {
		res.FilesSkipped = append(res.FilesSkipped, dst)
		return nil
	}
	if dryRun {
		res.DryRunFiles = append(res.DryRunFiles, dst)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil { //nolint:gosec
		return fmt.Errorf("init: mkdir %s: %w", filepath.Dir(dst), err)
	}
	if err := atomicWrite(dst, content, 0644); err != nil {
		return fmt.Errorf("init: write %s: %w", dst, err)
	}
	res.FilesWritten = append(res.FilesWritten, dst)
	return nil
}

// safeMkdirAll creates dir (and parents). In dry-run mode it just records.
func safeMkdirAll(dir string, dryRun bool, res *Result) error {
	if dryRun {
		res.DryRunFiles = append(res.DryRunFiles, dir+"/")
		return nil
	}
	if err := os.MkdirAll(dir, 0755); err != nil { //nolint:gosec
		return fmt.Errorf("init: mkdir %s: %w", dir, err)
	}
	return nil
}

// atomicWrite writes content to path via a temp-rename, ensuring no torn writes.
func atomicWrite(path string, content []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, content, mode); err != nil { //nolint:gosec
		return fmt.Errorf("write tmp %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s → %s: %w", tmp, path, err)
	}
	return nil
}

// appendKindGitignore appends kind-specific .gitignore content to path if the
// marker is not already present.
func appendKindGitignore(path, marker string, content []byte) error {
	if data, err := os.ReadFile(path); err == nil { //nolint:gosec
		if strings.Contains(string(data), marker) {
			return nil
		}
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644) //nolint:gosec
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	_, err = fmt.Fprintf(f, "\n%s\n%s", marker, string(content))
	return err
}

// writeKindAgents copies all .md files from templates/<kind>/.claude/agents/
// into <projAbs>/.claude/agents/. Files are written conditionally (skip if
// already present, unless force is set).
func writeKindAgents(kind, projAbs string, force, dryRun bool, res *Result) error {
	prefix := "templates/" + kind + "/.claude/agents"
	entries, err := fs.ReadDir(templates, prefix)
	if err != nil {
		// No agents directory for this kind — skip silently.
		return nil
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		src := ".claude/agents/" + e.Name()
		dst := filepath.Join(projAbs, ".claude", "agents", e.Name())
		if err := writeTemplateConditional(kind, src, dst, force, dryRun, res); err != nil {
			return err
		}
	}
	return nil
}

// appendGitignorePatterns appends yakOS runtime-agent gitignore patterns to
// path if the marker line is not already present. Creates the file if absent.
func appendGitignorePatterns(path, marker string) error {
	// Check if already present.
	if data, err := os.ReadFile(path); err == nil { //nolint:gosec
		if strings.Contains(string(data), marker) {
			return nil
		}
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644) //nolint:gosec
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	_, err = fmt.Fprintf(f, "\n%s\n.codex/agents/yakos-*.toml\n.gemini/agents/yakos-*.md\n.gemini/settings.json.yakos-bak-*\n.agents/skills/yakos-*.md\n.agents/mcp_config.json\n.agents/hooks.json\n", marker)
	return err
}

// ---- git + encoding helpers -------------------------------------------------

// isGitRepo returns true if path is inside a git repository.
func isGitRepo(path string) bool {
	// Check for .git directory.
	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		return true
	}
	// Fallback: run git rev-parse.
	cmd := exec.Command("git", "-C", path, "rev-parse", "--git-dir") //nolint:gosec
	return cmd.Run() == nil
}

// encodeProjectPath encodes a project path to the format used by Claude Code
// for ~/.claude/projects/. It mirrors ct_encode_project_path from compat.sh
// and extends it to handle Windows drive letters safely.
//
// Algorithm:
//  1. Strip a Windows drive-letter prefix (e.g. "C:" or "c:") so that the
//     colon never appears in a path component (colons are illegal in Windows
//     directory names, even inside a longer path).
//  2. Replace every path separator ('/' and '\') and every other character
//     that is illegal in a Windows path component (':', '<', '>', '"', '|',
//     '?', '*') with '-'.
//  3. Trim leading and trailing '-' characters that result from the above
//     replacements (e.g. an absolute Unix path "/foo" becomes "foo" not
//     "-foo").
//  4. Collapse consecutive '-' runs to a single '-' so that a Windows root
//     like "C:\" doesn't produce "---".
//  5. Return "root" for the degenerate case where only separators were
//     present (e.g. "/" or "C:\").
//
// This produces identical output for the same logical project path on both
// Unix and Windows.  Unix parity with bash ct_encode_project_path is
// preserved: "/Users/bob/proj" → "Users-bob-proj".
func encodeProjectPath(absPath string) string {
	s := absPath

	// Step 1: strip Windows drive-letter prefix (e.g. "C:" or "c:").
	// A drive prefix is exactly one ASCII letter followed by ':' at position 0.
	if len(s) >= 2 && s[1] == ':' && ((s[0] >= 'A' && s[0] <= 'Z') || (s[0] >= 'a' && s[0] <= 'z')) {
		s = s[2:]
	}

	// Step 2: replace separators and Windows-illegal chars with '-'.
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		switch r {
		case '/', '\\', ':', '<', '>', '"', '|', '?', '*':
			sb.WriteByte('-')
		default:
			sb.WriteRune(r)
		}
	}
	encoded := sb.String()

	// Step 3: trim leading and trailing '-'.
	encoded = strings.Trim(encoded, "-")

	// Step 4: collapse consecutive '-' into a single '-'.
	for strings.Contains(encoded, "--") {
		encoded = strings.ReplaceAll(encoded, "--", "-")
	}

	// Step 5: guard against empty result (e.g. input was "/" or `C:\`).
	if encoded == "" {
		return "root"
	}
	return encoded
}

// buildMemoryMD builds the content for a new MEMORY.md file.
func buildMemoryMD(name, projectPath string, now time.Time) string {
	return fmt.Sprintf("# Memory index for %s\n\nProject repo: %s\nInitialized:  %s\n\nThis file is the auto-memory index for the project at the path above.\nAdd entries below as one-line links to memory files in this directory.\nLines past 200 may be truncated, so keep entries concise.\n\n",
		name, projectPath, now.UTC().Format(time.RFC3339))
}

// isValidKind returns true if kind is in validKinds.
func isValidKind(kind string) bool {
	for _, k := range validKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// TemplateKinds returns the list of accepted --template values.
func TemplateKinds() []string {
	return append([]string(nil), validKinds...)
}

// TemplateFiles returns all embedded file paths for the given kind (for
// testing and dry-run preview). Returns an error if the kind is invalid.
func TemplateFiles(kind string) ([]string, error) {
	if !isValidKind(kind) {
		return nil, fmt.Errorf("initialize: unknown kind %q", kind)
	}
	var paths []string
	prefix := "templates/" + kind
	err := fs.WalkDir(templates, prefix, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	return paths, err
}
