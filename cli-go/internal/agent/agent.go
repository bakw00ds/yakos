// Package agent is the Go port of cli/lib/agent.sh.
//
// It implements:
//
//	yakos agent new <name> [--runtime <id>] [--project <path>] [--extends <id>]
//	                       [--role specialist|reviewer|maintainer]
//	                       [--domain <text>] [--model <alias>]
//	                       [--tools "<list>"] [--force]
//	yakos agent lint [<project>]     — audit every agent file in the project
//	yakos agent diff <name> [--project <path>] — show body diff vs parent
//	yakos agent list [--project <path>] [--json] — list resolved agents
//	yakos agents lint [<project>]    — plural alias routes here too
//
// Reuses agentscompose.splitFrontmatter / parseFrontmatter internals via the
// exported Frontmatter / SplitFrontmatter helpers, and runtime.Known for
// runtime validation.
package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/bakw00ds/yakos/internal/agentscompose"
	"github.com/bakw00ds/yakos/internal/runtime"
)

// Config drives Run. The Subcommand field determines which sub-action to take.
type Config struct {
	// YakosRoot is $YAKOS_ROOT — the framework repo root.
	YakosRoot string

	// Subcommand is "new", "lint", "diff", or "list".
	Subcommand string

	// Name is the agent identifier for new/diff.
	Name string

	// Project is the project directory (may be empty → infer from cwd).
	Project string

	// --- flags for `new` ---
	Runtime string // claude | codex | gemini | agy
	Extends string // framework template to extend
	Role    string // specialist | reviewer | maintainer
	Domain  string
	Model   string
	Tools   string
	Force   bool

	// --- flags for `diff` ---
	// (uses Name + Project)

	// --- flags for `list` ---
	JSON bool // emit JSON instead of table

	// IO
	Writer    io.Writer
	ErrWriter io.Writer

	// HomeDir is used for project inference fallback.
	HomeDir string
}

// Result is returned from Run; callers should check Errors for lint exit code.
type Result struct {
	// Errors is the count of error-severity findings (lint subcommand).
	Errors int
	// Warnings is the count of warn-severity findings (lint subcommand).
	Warnings int
}

// Run dispatches to the appropriate sub-action.
func Run(cfg Config) (*Result, error) {
	w := cfg.writer()
	ew := cfg.errWriter()
	_ = ew

	switch cfg.Subcommand {
	case "new", "create":
		return runNew(cfg, w)
	case "lint":
		return runLint(cfg, w)
	case "diff":
		return runDiff(cfg, w)
	case "list":
		return runList(cfg, w)
	default:
		return nil, fmt.Errorf("agent: unknown subcommand %q (try --help)", cfg.Subcommand)
	}
}

// PrintHelp writes the usage message mirroring cli/lib/agent.sh usage().
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos agent <subcommand> [args...]
yakos agents <subcommand> [args...]    (alias for the plural)

Subcommands:
  new <name>      Scaffold a new project agent file at
                  <project>/.claude/agents/<name>.md.
                  Flags:
                    --runtime <id>    claude (default) | codex | gemini | agy
                    --project <path>  Project repo. Defaults to inferring
                                      from cwd.
                    --extends <id>    Framework template to extend.
                    --role <r>        specialist | reviewer | maintainer
                                      (default: specialist).
                    --domain <text>   Free-form domain tag.
                    --model <alias>   opus | sonnet | haiku | <full>.
                    --tools "<list>"  Comma-separated tool names.
                    --force           Overwrite if exists.

  diff <name> [--project <path>]
                  Show the diff between a project agent's body and
                  its extends: parent's body. Falls back to a message
                  if the agent doesn't inherit.

  lint [<project>]    Audit every agent file:
                      - frontmatter has required fields (id, role, domain)
                      - runtime: (if set) is in known list
                      - runtime-fallback: (if set) all entries valid
                      - extends: target exists
                      - body has '## Purpose' section
                      Exit 0 clean; 1 if any error.

  list [--project <path>] [--json]
                  List the composed agent roster (framework + project).

Examples:
  yakos agent new codex-helper --runtime codex --domain cross-cutting
  yakos agent new myapp-backend --extends backend --runtime claude
  yakos agents lint
`)
}

// ---- helpers ------------------------------------------------------------------

func (cfg Config) writer() io.Writer {
	if cfg.Writer != nil {
		return cfg.Writer
	}
	return os.Stdout
}

func (cfg Config) errWriter() io.Writer {
	if cfg.ErrWriter != nil {
		return cfg.ErrWriter
	}
	return os.Stderr
}

// isKnownRuntime returns true when rt is in runtime.Known.
func isKnownRuntime(rt string) bool {
	for _, k := range runtime.Known {
		if k == rt {
			return true
		}
	}
	return false
}

// knownRuntimesStr returns a comma-separated string of known runtimes.
func knownRuntimesStr() string {
	return strings.Join(runtime.Known, ", ")
}

// titleCaseName converts a kebab-case agent name to Title Case words.
// "my-backend" → "My Backend".
func titleCaseName(name string) string {
	words := strings.Split(strings.ReplaceAll(name, "-", " "), " ")
	for i, w := range words {
		if len(w) == 0 {
			continue
		}
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		for j := 1; j < len(r); j++ {
			r[j] = unicode.ToLower(r[j])
		}
		words[i] = string(r)
	}
	return strings.Join(words, " ")
}

// validateAgentName returns an error when name contains characters outside
// [A-Za-z0-9_-].
func validateAgentName(name string) error {
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' {
			return fmt.Errorf("agent new: <name> must be alphanumeric (- and _ allowed), got %q", name)
		}
	}
	return nil
}

// inferProjectFromCWD attempts to resolve a project path from cwd using the
// ~/agent-control/<name>/.project-path convention.
func inferProjectFromCWD(cwd, home string) string {
	if home == "" {
		return ""
	}
	acRoot := filepath.Join(home, "agent-control")
	// If cwd is inside agent-control/<name>/, read .project-path.
	if strings.HasPrefix(cwd, acRoot+string(os.PathSeparator)) {
		rest := cwd[len(acRoot)+1:]
		name := rest
		for i, c := range name {
			if c == '/' || c == os.PathSeparator {
				name = name[:i]
				break
			}
		}
		ppFile := filepath.Join(acRoot, name, ".project-path")
		if data, err := os.ReadFile(ppFile); err == nil { //nolint:gosec
			return strings.TrimSpace(string(data))
		}
	}
	// Scan all agent-control entries.
	entries, err := os.ReadDir(acRoot)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ppFile := filepath.Join(acRoot, e.Name(), ".project-path")
		data, err := os.ReadFile(ppFile) //nolint:gosec
		if err != nil {
			continue
		}
		p := strings.TrimSpace(string(data))
		if cwd == p || strings.HasPrefix(cwd, p+string(os.PathSeparator)) {
			return p
		}
	}
	return ""
}

// resolveProject returns a non-empty project path or an error.
// Uses cfg.Project first, then env YAKOS_PROJECT_PATH, then cwd inference.
func resolveProject(cfg Config) (string, error) {
	if cfg.Project != "" {
		return cfg.Project, nil
	}
	if v := os.Getenv("YAKOS_PROJECT_PATH"); v != "" {
		return v, nil
	}
	home := cfg.HomeDir
	if home == "" {
		home = os.Getenv("HOME")
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	p := inferProjectFromCWD(cwd, home)
	if p != "" {
		return p, nil
	}
	return "", fmt.Errorf("cannot infer project from cwd; use --project <path>")
}

// ---- new ----------------------------------------------------------------------

func runNew(cfg Config, w io.Writer) (*Result, error) {
	name := cfg.Name
	if name == "" {
		return nil, fmt.Errorf("agent new: <name> required")
	}
	if err := validateAgentName(name); err != nil {
		return nil, err
	}

	project, err := resolveProject(cfg)
	if err != nil {
		return nil, fmt.Errorf("agent new: %w", err)
	}
	if _, err := os.Stat(project); err != nil {
		return nil, fmt.Errorf("agent new: project not found: %s", project)
	}

	rt := cfg.Runtime
	if rt != "" && !isKnownRuntime(rt) {
		return nil, fmt.Errorf("agent new: unknown runtime %q (known: %s)", rt, knownRuntimesStr())
	}

	extends := cfg.Extends
	if extends != "" {
		fwPath := filepath.Join(cfg.YakosRoot, "lib", "agents", extends+".md")
		projPath := filepath.Join(project, ".claude", "agents", extends+".md")
		if _, err := os.Stat(fwPath); err != nil {
			if _, err2 := os.Stat(projPath); err2 != nil {
				return nil, fmt.Errorf("agent new: --extends %q not found in framework or project agents", extends)
			}
		}
	}

	role := cfg.Role
	if role == "" {
		role = "specialist"
	}
	domain := cfg.Domain
	if domain == "" {
		domain = "cross-cutting"
	}
	model := cfg.Model
	if model == "" {
		model = "sonnet"
	}
	tools := cfg.Tools
	if tools == "" {
		tools = "Read, Edit, Bash, Grep, SendMessage"
	}

	outDir := filepath.Join(project, ".claude", "agents")
	if err := os.MkdirAll(outDir, 0755); err != nil { //nolint:gosec
		return nil, fmt.Errorf("agent new: mkdir %s: %w", outDir, err)
	}
	outFile := filepath.Join(outDir, name+".md")

	if !cfg.Force {
		if _, err := os.Stat(outFile); err == nil {
			return nil, fmt.Errorf("agent new: %s already exists (use --force to overwrite)", outFile)
		}
	}

	content := buildAgentTemplate(name, role, domain, tools, model, extends, rt)

	// Atomic write via temp-rename per Q8.
	tmpFile := outFile + ".tmp"
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil { //nolint:gosec
		return nil, fmt.Errorf("agent new: write temp: %w", err)
	}
	if err := os.Rename(tmpFile, outFile); err != nil {
		_ = os.Remove(tmpFile)
		return nil, fmt.Errorf("agent new: rename: %w", err)
	}

	_, _ = fmt.Fprintf(w, "wrote %s\n", outFile)
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Frontmatter:")
	_, _ = fmt.Fprintf(w, "  id:       %s\n", name)
	_, _ = fmt.Fprintf(w, "  role:     %s\n", role)
	_, _ = fmt.Fprintf(w, "  domain:   %s\n", domain)
	_, _ = fmt.Fprintf(w, "  model:    %s\n", model)
	_, _ = fmt.Fprintf(w, "  tools:    [%s]\n", tools)
	if extends != "" {
		_, _ = fmt.Fprintf(w, "  extends:  %s\n", extends)
	}
	if rt != "" && rt != "claude" {
		_, _ = fmt.Fprintf(w, "  runtime:  %s\n", rt)
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Next steps:")
	_, _ = fmt.Fprintf(w, "  1. Edit %s — fill in the TODO sections.\n", outFile)
	_, _ = fmt.Fprintln(w, "  2. yakos agents lint    # verify the file parses")
	if rt != "" && rt != "claude" {
		_, _ = fmt.Fprintf(w, "  3. yakos auth status %s   # ensure the chosen runtime is configured\n", rt)
	}

	return &Result{}, nil
}

// buildAgentTemplate builds the content of a newly scaffolded agent file.
func buildAgentTemplate(name, role, domain, tools, model, extends, rt string) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "id: %s\n", name)
	fmt.Fprintf(&sb, "role: %s\n", role)
	fmt.Fprintf(&sb, "domain: %s\n", domain)
	sb.WriteString("mode: [feature]\n")
	fmt.Fprintf(&sb, "tools: [%s]\n", tools)
	fmt.Fprintf(&sb, "model: %s\n", model)
	if extends != "" {
		fmt.Fprintf(&sb, "extends: %s\n", extends)
	}
	if rt != "" && rt != "claude" {
		fmt.Fprintf(&sb, "runtime: %s\n", rt)
	}
	sb.WriteString("references: []\n")
	sb.WriteString("---\n\n")
	fmt.Fprintf(&sb, "# %s\n\n", titleCaseName(name))
	sb.WriteString("## Purpose\n\n")
	sb.WriteString("TODO — what does this agent do? Why does it exist?\n\n")
	sb.WriteString("## Execution\n\n")
	sb.WriteString("1. TODO — first step\n\n")
	sb.WriteString("## Special rules\n\n")
	sb.WriteString("- TODO — what should this agent never do?\n\n")
	sb.WriteString("## When to push back / escalate\n\n")
	sb.WriteString("1. TODO — when to push back\n")
	sb.WriteString("2. TODO — when to escalate\n")
	sb.WriteString("3. **Done means:** TODO\n\n")
	sb.WriteString("## Personality\n\n")
	sb.WriteString("TODO — short paragraph on disposition.\n")
	return sb.String()
}

// ---- lint ---------------------------------------------------------------------

// LintFinding is one lint result for a single file.
type LintFinding struct {
	Level   string // "ok", "warn", "err"
	File    string
	Message string
}

func runLint(cfg Config, w io.Writer) (*Result, error) {
	project, err := resolveProject(cfg)
	if err != nil {
		return nil, fmt.Errorf("agents lint: %w", err)
	}

	projDir := filepath.Join(project, ".claude", "agents")

	_, _ = fmt.Fprintf(w, "yakos agents lint — %s\n\n", project)

	if _, err := os.Stat(projDir); os.IsNotExist(err) {
		_, _ = fmt.Fprintf(w, "no project agents at %s (nothing to lint)\n", projDir)
		return &Result{}, nil
	}

	entries, err := os.ReadDir(projDir)
	if err != nil {
		return nil, fmt.Errorf("agents lint: read dir %s: %w", projDir, err)
	}

	r := &Result{}
	total := 0

	errFn := func(msg string) {
		_, _ = fmt.Fprintf(w, "  [ERR]  %s\n", msg)
		r.Errors++
	}
	warnFn := func(msg string) {
		_, _ = fmt.Fprintf(w, "  [warn] %s\n", msg)
		r.Warnings++
	}
	okFn := func(msg string) {
		_, _ = fmt.Fprintf(w, "  [ok]   %s\n", msg)
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		if e.Name() == "README.md" {
			continue
		}

		total++
		fPath := filepath.Join(projDir, e.Name())
		base := e.Name()
		localErrs := 0

		fm, body, parseErr := readAgentFile(fPath)
		if parseErr != nil {
			errFn(fmt.Sprintf("%s: cannot read file: %v", base, parseErr))
			continue
		}

		// Required fields: id, role, domain.
		for _, req := range []string{"id", "role", "domain"} {
			if fm[req] == "" {
				errFn(fmt.Sprintf("%s: missing required frontmatter field %q", base, req))
				localErrs++
			}
		}

		// runtime: must be a known runtime if set.
		if rt := fm["runtime"]; rt != "" {
			if !isKnownRuntime(rt) {
				errFn(fmt.Sprintf("%s: runtime %q is not in known list (%s)", base, rt, knownRuntimesStr()))
				localErrs++
			}
		}

		// runtime-fallback: all entries valid.
		if fb := fm["runtime-fallback"]; fb != "" {
			for _, entry := range splitList(fb) {
				if !isKnownRuntime(entry) {
					errFn(fmt.Sprintf("%s: runtime-fallback entry %q is not a known runtime", base, entry))
					localErrs++
				}
			}
		}

		// extends: target exists in framework or project.
		if ext := fm["extends"]; ext != "" {
			extFwFile := filepath.Join(cfg.YakosRoot, "lib", "agents", ext+".md")
			extProjFile := filepath.Join(projDir, ext+".md")
			extFound := false
			if _, err := os.Stat(extFwFile); err == nil {
				extFound = true
				// Check extends-version drift.
				if extFm, _, err := readAgentFile(extFwFile); err == nil {
					fwVer := extFm["version"]
					projExtVer := fm["extends-version"]
					if fwVer != "" && projExtVer != "" && fwVer != projExtVer {
						warnFn(fmt.Sprintf("%s: extends-version %s but framework %s is at version %s — review with 'yakos agent diff %s'",
							base, projExtVer, ext, fwVer, strings.TrimSuffix(base, ".md")))
					} else if fwVer != "" && projExtVer == "" {
						warnFn(fmt.Sprintf("%s: extends %q (v%s) but no 'extends-version:' recorded — add to track future drift", base, ext, fwVer))
					}
				}
			} else if _, err := os.Stat(extProjFile); err == nil {
				extFound = true
			}
			if !extFound {
				errFn(fmt.Sprintf("%s: extends %q not found in framework or project agents", base, ext))
				localErrs++
			}
		}

		// Body must have '## Purpose' section.
		if !hasPurposeSection(body) {
			warnFn(fmt.Sprintf("%s: body missing '## Purpose' section", base))
		}

		if localErrs == 0 {
			okFn(base)
		}
	}

	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "lint summary: %d agent(s), %d error(s), %d warning(s)\n",
		total, r.Errors, r.Warnings)

	return r, nil
}

// hasPurposeSection returns true when the body contains a "## Purpose" heading.
func hasPurposeSection(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimRight(line, " \t\r")
		if trimmed == "## Purpose" {
			return true
		}
	}
	return false
}

// splitList splits a YAML inline list or whitespace-separated values.
// Handles "[a, b, c]" and "a b c".
func splitList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	// YAML inline list.
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		inner := raw[1 : len(raw)-1]
		parts := strings.Split(inner, ",")
		var result []string
		for _, p := range parts {
			t := strings.TrimSpace(p)
			if t != "" {
				result = append(result, t)
			}
		}
		return result
	}
	// Whitespace/newline-separated.
	var result []string
	for _, p := range strings.Fields(raw) {
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// readAgentFile reads an agent .md file and returns (frontmatter map, body, error).
// The frontmatter map has string values only (simple key: value parsing, not full YAML).
func readAgentFile(path string) (map[string]string, string, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, "", err
	}
	content := string(data)

	fm, body := splitFrontmatter(content)
	fields := parseFrontmatterSimple(fm)
	return fields, body, nil
}

// splitFrontmatter splits content into (frontmatter, body) on --- fences.
// Returns ("", content) when no frontmatter is present.
func splitFrontmatter(content string) (string, string) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", content
	}
	closeIdx := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			closeIdx = i
			break
		}
	}
	if closeIdx < 0 {
		return "", content
	}
	fm := strings.Join(lines[1:closeIdx], "\n")
	bd := strings.Join(lines[closeIdx+1:], "\n")
	return fm, bd
}

// parseFrontmatterSimple parses simple key: value YAML lines.
// Returns a map of key → raw string value.
func parseFrontmatterSimple(fm string) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(fm, "\n") {
		idx := strings.IndexByte(line, ':')
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		if key != "" {
			result[key] = val
		}
	}
	return result
}

// ---- diff ---------------------------------------------------------------------

func runDiff(cfg Config, w io.Writer) (*Result, error) {
	if cfg.Name == "" {
		return nil, fmt.Errorf("agent diff: <name> required")
	}
	project, err := resolveProject(cfg)
	if err != nil {
		return nil, fmt.Errorf("agent diff: %w", err)
	}

	projFile := filepath.Join(project, ".claude", "agents", cfg.Name+".md")
	if _, err := os.Stat(projFile); err != nil {
		return nil, fmt.Errorf("agent diff: %s not found", projFile)
	}

	fm, projBody, err := readAgentFile(projFile)
	if err != nil {
		return nil, fmt.Errorf("agent diff: read %s: %w", projFile, err)
	}

	parent := fm["extends"]
	if parent == "" {
		_, _ = fmt.Fprintf(w, "agent diff: %s does not extend a framework template (no 'extends:' field).\n", cfg.Name)
		_, _ = fmt.Fprintln(w, "The project agent is the source of truth for its discipline.")
		return &Result{}, nil
	}

	parentFile := filepath.Join(cfg.YakosRoot, "lib", "agents", parent+".md")
	if _, err := os.Stat(parentFile); err != nil {
		// Try project-local parent.
		parentFile = filepath.Join(project, ".claude", "agents", parent+".md")
		if _, err2 := os.Stat(parentFile); err2 != nil {
			return nil, fmt.Errorf("agent diff: parent %q not found", parent)
		}
	}

	_, parentBody, err := readAgentFile(parentFile)
	if err != nil {
		return nil, fmt.Errorf("agent diff: read parent %s: %w", parentFile, err)
	}

	_, _ = fmt.Fprintf(w, "agent diff: %s (extends: %s)\n", cfg.Name, parent)
	_, _ = fmt.Fprintf(w, "--- %s\n", parentFile)
	_, _ = fmt.Fprintf(w, "+++ %s\n", projFile)
	diffOutput := unifiedDiff(parentBody, projBody)
	_, _ = fmt.Fprint(w, diffOutput)

	return &Result{}, nil
}

// unifiedDiff produces a simple unified-style diff of two strings.
// It is a Go-native implementation that avoids the external 'diff' binary.
// editLine is one line in a diff edit sequence, used by lcsLineDiff.
type editLine struct {
	kind rune // ' ', '+', '-'
	text string
}

// lcsLineDiff produces a sequence of editLine describing the edit operations
// between aLines and bLines using a simple LCS dynamic-programming approach.
func lcsLineDiff(aLines, bLines []string) []editLine {
	n, m := len(aLines), len(bLines)
	// dp[i][j] = length of LCS of aLines[:i], bLines[:j].
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if aLines[i-1] == bLines[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] > dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	// Backtrack.
	var result []editLine
	i, j := n, m
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && aLines[i-1] == bLines[j-1]:
			result = append(result, editLine{' ', aLines[i-1]})
			i--
			j--
		case j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]):
			result = append(result, editLine{'+', bLines[j-1]})
			j--
		default:
			result = append(result, editLine{'-', aLines[i-1]})
			i--
		}
	}
	// Reverse.
	for l, r := 0, len(result)-1; l < r; l, r = l+1, r-1 {
		result[l], result[r] = result[r], result[l]
	}
	return result
}

// unifiedDiff produces a simple unified-style diff of two strings.
// It is a Go-native implementation that avoids the external 'diff' binary.
func unifiedDiff(aStr, bStr string) string {
	aLines := strings.Split(aStr, "\n")
	bLines := strings.Split(bStr, "\n")

	if aStr == bStr {
		return ""
	}

	edits := lcsLineDiff(aLines, bLines)

	var sb strings.Builder
	ctx := 3 // context lines

	// Group into hunks.
	type hunk struct {
		aStart, aLen int
		bStart, bLen int
		lines        []editLine
	}

	var hunks []hunk
	var curHunk *hunk
	aIdx, bIdx := 0, 0
	pendingCtx := make([]editLine, 0, ctx)

	flushPending := func() {
		if curHunk != nil && len(pendingCtx) > 0 {
			curHunk.lines = append(curHunk.lines, pendingCtx...)
			curHunk.aLen += len(pendingCtx)
			curHunk.bLen += len(pendingCtx)
			pendingCtx = pendingCtx[:0]
		}
	}
	_ = flushPending

	for _, e := range edits {
		switch e.kind {
		case '-':
			if curHunk == nil {
				// Include up to ctx context lines before the change.
				ctxStart := len(pendingCtx)
				if ctxStart > ctx {
					ctxStart = ctx
				}
				startPending := len(pendingCtx) - ctxStart
				curHunk = &hunk{
					aStart: aIdx - ctxStart + 1,
					bStart: bIdx - ctxStart + 1,
				}
				curHunk.lines = append(curHunk.lines, pendingCtx[startPending:]...)
				curHunk.aLen += ctxStart
				curHunk.bLen += ctxStart
				pendingCtx = pendingCtx[:0]
			} else {
				flushPending()
			}
			curHunk.lines = append(curHunk.lines, e)
			curHunk.aLen++
			aIdx++
		case '+':
			if curHunk == nil {
				ctxStart := len(pendingCtx)
				if ctxStart > ctx {
					ctxStart = ctx
				}
				startPending := len(pendingCtx) - ctxStart
				curHunk = &hunk{
					aStart: aIdx - ctxStart + 1,
					bStart: bIdx - ctxStart + 1,
				}
				curHunk.lines = append(curHunk.lines, pendingCtx[startPending:]...)
				curHunk.aLen += ctxStart
				curHunk.bLen += ctxStart
				pendingCtx = pendingCtx[:0]
			} else {
				flushPending()
			}
			curHunk.lines = append(curHunk.lines, e)
			curHunk.bLen++
			bIdx++
		case ' ':
			if curHunk != nil {
				pendingCtx = append(pendingCtx, e)
				// If we have more than 2*ctx pending context, close the hunk.
				if len(pendingCtx) >= 2*ctx {
					// Append ctx trailing lines and close.
					curHunk.lines = append(curHunk.lines, pendingCtx[:ctx]...)
					curHunk.aLen += ctx
					curHunk.bLen += ctx
					hunks = append(hunks, *curHunk)
					curHunk = nil
					pendingCtx = pendingCtx[ctx:]
					// Keep remaining ctx as leading context for next hunk.
				}
			} else {
				pendingCtx = append(pendingCtx, e)
				if len(pendingCtx) > ctx {
					pendingCtx = pendingCtx[1:]
				}
			}
			aIdx++
			bIdx++
		}
	}
	if curHunk != nil {
		// Flush remaining pending context (up to ctx lines).
		tail := pendingCtx
		if len(tail) > ctx {
			tail = tail[:ctx]
		}
		curHunk.lines = append(curHunk.lines, tail...)
		curHunk.aLen += len(tail)
		curHunk.bLen += len(tail)
		hunks = append(hunks, *curHunk)
	}

	for _, h := range hunks {
		fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", h.aStart, h.aLen, h.bStart, h.bLen)
		for _, l := range h.lines {
			fmt.Fprintf(&sb, "%c%s\n", l.kind, l.text)
		}
	}
	return sb.String()
}

// ---- list ---------------------------------------------------------------------

// AgentEntry is one row in the list output.
type AgentEntry struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Model       string   `json:"model,omitempty"`
	Tools       []string `json:"tools,omitempty"`
}

func runList(cfg Config, w io.Writer) (*Result, error) {
	project := cfg.Project
	if project == "" {
		if v := os.Getenv("YAKOS_PROJECT_PATH"); v != "" {
			project = v
		} else {
			home := cfg.HomeDir
			if home == "" {
				home = os.Getenv("HOME")
			}
			cwd, _ := os.Getwd()
			project = inferProjectFromCWD(cwd, home)
			// list is OK with no project — shows only framework agents.
		}
	}

	roster, err := agentscompose.Compose(cfg.YakosRoot, project)
	if err != nil {
		return nil, fmt.Errorf("agent list: %w", err)
	}

	entries := make([]AgentEntry, 0, len(roster))
	for _, a := range roster {
		entries = append(entries, AgentEntry{
			ID:          a.ID,
			Description: a.Description,
			Model:       a.Model,
			Tools:       a.Tools,
		})
	}

	if cfg.JSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(entries); err != nil {
			return nil, fmt.Errorf("agent list: json encode: %w", err)
		}
		return &Result{}, nil
	}

	// Text table.
	_, _ = fmt.Fprintf(w, "%-30s  %-10s  %s\n", "ID", "MODEL", "DESCRIPTION")
	_, _ = fmt.Fprintln(w, strings.Repeat("-", 80))
	for _, e := range entries {
		model := e.Model
		if model == "" {
			model = "(default)"
		}
		desc := e.Description
		maxDesc := 80 - 30 - 10 - 4
		if len(desc) > maxDesc {
			desc = desc[:maxDesc-3] + "..."
		}
		_, _ = fmt.Fprintf(w, "%-30s  %-10s  %s\n", e.ID, model, desc)
	}
	_, _ = fmt.Fprintf(w, "\n%d agent(s)\n", len(entries))

	return &Result{}, nil
}

// ---- docs (bonus: idea rank 9 renders auto-generated reference page) ----------

// DocsFormat is the output format for the docs subcommand.
type DocsFormat string

const (
	DocsFormatMD   DocsFormat = "md"
	DocsFormatHTML DocsFormat = "html"
)

// DocsConfig drives RenderDocs.
type DocsConfig struct {
	YakosRoot string
	Project   string
	Format    DocsFormat
	Writer    io.Writer
}

// RenderDocs generates an auto-generated reference page from each agent's
// frontmatter + Purpose section. Implements idea rank 9.
func RenderDocs(cfg DocsConfig) error {
	w := cfg.Writer
	if w == nil {
		w = os.Stdout
	}

	roster, err := agentscompose.Compose(cfg.YakosRoot, cfg.Project)
	if err != nil {
		return fmt.Errorf("agent docs: compose: %w", err)
	}

	switch cfg.Format {
	case DocsFormatHTML:
		return renderDocsHTML(w, roster)
	default:
		return renderDocsMD(w, roster)
	}
}

func renderDocsMD(w io.Writer, roster []agentscompose.ComposedAgent) error {
	_, err := fmt.Fprintf(w, "# yakOS Agent Reference\n\n")
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(w, "_Auto-generated from agent frontmatter. Do not edit manually._\n\n")

	for _, a := range roster {
		_, _ = fmt.Fprintf(w, "## `%s`\n\n", a.ID)
		_, _ = fmt.Fprintf(w, "%s\n\n", a.Description)
		if a.Model != "" {
			_, _ = fmt.Fprintf(w, "**Model:** %s  \n", a.Model)
		}
		if len(a.Tools) > 0 {
			_, _ = fmt.Fprintf(w, "**Tools:** %s  \n", strings.Join(a.Tools, ", "))
		}
		_, _ = fmt.Fprintln(w)
	}
	return nil
}

func renderDocsHTML(w io.Writer, roster []agentscompose.ComposedAgent) error {
	var buf bytes.Buffer
	buf.WriteString("<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n")
	buf.WriteString("  <meta charset=\"UTF-8\">\n")
	buf.WriteString("  <title>yakOS Agent Reference</title>\n")
	buf.WriteString("  <style>body{font-family:sans-serif;max-width:900px;margin:auto;padding:2rem;}")
	buf.WriteString("h2{border-bottom:1px solid #ccc;}code{background:#f4f4f4;padding:0.1em 0.3em;}</style>\n")
	buf.WriteString("</head>\n<body>\n")
	buf.WriteString("<h1>yakOS Agent Reference</h1>\n")
	buf.WriteString("<p><em>Auto-generated from agent frontmatter. Do not edit manually.</em></p>\n")

	for _, a := range roster {
		fmt.Fprintf(&buf, "<h2><code>%s</code></h2>\n", htmlEscape(a.ID))
		fmt.Fprintf(&buf, "<p>%s</p>\n", htmlEscape(a.Description))
		if a.Model != "" {
			fmt.Fprintf(&buf, "<p><strong>Model:</strong> %s</p>\n", htmlEscape(a.Model))
		}
		if len(a.Tools) > 0 {
			fmt.Fprintf(&buf, "<p><strong>Tools:</strong> %s</p>\n", htmlEscape(strings.Join(a.Tools, ", ")))
		}
	}
	buf.WriteString("</body>\n</html>\n")
	_, err := w.Write(buf.Bytes())
	return err
}

// htmlEscape escapes < > & " for HTML output.
func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}
