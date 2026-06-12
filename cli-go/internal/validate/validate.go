// Package validate is the Go implementation of `yakos validate`.
//
// Design decisions (mirrors validate.sh exactly):
//
//   - Output lines use the same [ok] / [warn] / [err] / [info] prefixes and
//     the same two-space indent: "  [ok]   <text>".
//   - In strict mode (--strict) warnings are promoted to errors with the suffix
//     " (strict mode)".
//   - Exit 0 on clean/warn-only; exit 1 when any error exists.
//   - No [info] on python3 capability: Go always does full YAML parse.
//   - File ordering is determined by filepath.WalkDir (lexicographic), which
//     differs from bash's `find` order.  The parity tests use CompareRegex /
//     line-set comparison rather than CompareExact for the per-file lines
//     precisely because bash `find` order is platform-dependent.
//   - Standards checks (line budgets, playbook refs, eval dirs, shebang,
//     header-purpose, etc.) are implemented to match the bash source.
package validate

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Level is the severity of a finding.
type Level int

const (
	LevelOK   Level = iota
	LevelInfo       // purely informational, not counted
	LevelWarn       // counted; in --strict becomes an error
	LevelErr        // always an error
)

// Finding is one validation result for a single check.
type Finding struct {
	Level   Level
	Message string
}

// Result holds the complete output of a validate run.
type Result struct {
	Findings []Finding
	Errors   int
	Warnings int
}

// Config controls validate behaviour.
type Config struct {
	// YakosRoot is $YAKOS_ROOT — the repo root.
	YakosRoot string
	// Strict, when true, treats warnings as errors.
	Strict bool
	// Writer is where the formatted output is written.  Defaults to os.Stdout.
	Writer io.Writer
	// ErrWriter is where error output is written.  Defaults to os.Stderr.
	ErrWriter io.Writer
}

// RunFramework validates $YAKOS_ROOT/lib/ (framework mode).
func RunFramework(cfg Config) *Result {
	w := cfg.writer()
	r := &Result{}

	libDir := filepath.Join(cfg.YakosRoot, "lib")
	validateTree(cfg, r, w, "framework", libDir)
	runStandardsChecks(cfg, r, w)
	return r
}

// RunProject validates <projectPath>/.claude/ (project mode).
func RunProject(cfg Config, projectPath string) *Result {
	w := cfg.writer()
	r := &Result{}

	// Accept either <path> or <path>/.claude directly.
	var base string
	if strings.HasSuffix(projectPath, "/.claude") || strings.HasSuffix(projectPath, "/.claude/") {
		base = strings.TrimSuffix(projectPath, "/")
	} else {
		base = filepath.Join(projectPath, ".claude")
	}

	validateTree(cfg, r, w, "project", base)
	return r
}

// RunAll validates both framework and project (--all mode).
func RunAll(cfg Config, projectPaths []string) *Result {
	w := cfg.writer()
	r := &Result{}

	libDir := filepath.Join(cfg.YakosRoot, "lib")
	validateTree(cfg, r, w, "framework", libDir)
	runStandardsChecks(cfg, r, w)

	if len(projectPaths) == 0 {
		r.addWarn(cfg, w, "--all without a project path; only framework lib/ was validated")
	} else {
		for _, p := range projectPaths {
			var base string
			if strings.HasSuffix(p, "/.claude") || strings.HasSuffix(p, "/.claude/") {
				base = strings.TrimSuffix(p, "/")
			} else {
				base = filepath.Join(p, ".claude")
			}
			validateTree(cfg, r, w, "project", base)
		}
	}
	return r
}

// PrintSummary writes the final summary line and returns the exit code.
// Write errors to w are treated as fatal because stdout failure is unrecoverable.
func PrintSummary(w io.Writer, r *Result) int {
	if _, err := fmt.Fprintf(w, "\nSummary: %d error(s), %d warning(s)\n", r.Errors, r.Warnings); err != nil {
		fmt.Fprintf(os.Stderr, "yakos: write error: %v\n", err)
		os.Exit(1)
	}
	if r.Errors > 0 {
		return 1
	}
	return 0
}

// ---- private helpers --------------------------------------------------------

func (cfg Config) writer() io.Writer {
	if cfg.Writer != nil {
		return cfg.Writer
	}
	return os.Stdout
}

func (r *Result) addOK(w io.Writer, msg string) {
	r.Findings = append(r.Findings, Finding{LevelOK, msg})
	_, _ = fmt.Fprintf(w, "  [ok]   %s\n", msg)
}

func (r *Result) addInfo(w io.Writer, msg string) {
	r.Findings = append(r.Findings, Finding{LevelInfo, msg})
	_, _ = fmt.Fprintf(w, "  [info] %s\n", msg)
}

func (r *Result) addWarn(cfg Config, w io.Writer, msg string) {
	if cfg.Strict {
		r.Findings = append(r.Findings, Finding{LevelErr, msg + " (strict mode)"})
		_, _ = fmt.Fprintf(w, "  [err]  %s (strict mode)\n", msg)
		r.Errors++
	} else {
		r.Findings = append(r.Findings, Finding{LevelWarn, msg})
		_, _ = fmt.Fprintf(w, "  [warn] %s\n", msg)
		r.Warnings++
	}
}

func (r *Result) addErr(w io.Writer, msg string) {
	r.Findings = append(r.Findings, Finding{LevelErr, msg})
	_, _ = fmt.Fprintf(w, "  [err]  %s\n", msg)
	r.Errors++
}

// ---- validate_tree ----------------------------------------------------------

func validateTree(cfg Config, r *Result, w io.Writer, label, base string) {
	_, _ = fmt.Fprintf(w, "\n%s: %s\n", label, base)

	if _, err := os.Stat(base); os.IsNotExist(err) {
		r.addInfo(w, "directory does not exist; nothing to validate")
		return
	}

	nAgents := countDirFiles(filepath.Join(base, "agents"), "*.md")
	nSkills := countDirFiles(filepath.Join(base, "skills"), "SKILL.md")
	nRules := countDirFiles(filepath.Join(base, "rules"), "*.md")
	r.addInfo(w, fmt.Sprintf("agents: %d | skills: %d | rules: %d", nAgents, nSkills, nRules))

	if nAgents == 0 && nSkills == 0 && nRules == 0 {
		r.addInfo(w, "no agents/skills/rules to validate (this is expected for the empty lib/ in v0.1)")
		return
	}

	// Validate frontmatter for agents, skills, and rules (excluding README/INDEX).
	files := collectMDFiles(base)
	for _, f := range files {
		if _, err := parseFrontmatter(f); err != nil {
			r.addErr(w, fmt.Sprintf("%s: bad YAML frontmatter", f))
		} else {
			r.addOK(w, f)
		}
	}

	// settings.json (project-only or framework)
	settingsPath := filepath.Join(base, "settings.json")
	if fileExists(settingsPath) {
		if isValidJSON(settingsPath) {
			r.addOK(w, settingsPath+": valid JSON")
		} else {
			r.addErr(w, settingsPath+": not valid JSON")
		}
	}

	allowlistPath := filepath.Join(base, "path-allowlist.json")
	if fileExists(allowlistPath) {
		if isValidJSON(allowlistPath) {
			r.addOK(w, allowlistPath+": valid JSON")
		} else {
			r.addErr(w, allowlistPath+": not valid JSON")
		}
	}

	// Line budget warnings
	checkLineBudgets(cfg, r, w, base)

	// Playbook reference resolution
	checkPlaybookReferences(cfg, r, w, base)

	// Golden-case eval validation
	checkEvalDirs(cfg, r, w, base)
}

// collectMDFiles returns agent .md, skills SKILL.md, and rules .md files under
// base, excluding README.md and INDEX.md.  Files are sorted for stable output.
func collectMDFiles(base string) []string {
	var files []string

	// agents/*.md (not README.md)
	agentsDir := filepath.Join(base, "agents")
	if fi, err := os.Stat(agentsDir); err == nil && fi.IsDir() {
		_ = filepath.WalkDir(agentsDir, func(p string, de fs.DirEntry, err error) error {
			if err != nil || de.IsDir() {
				return nil
			}
			name := de.Name()
			if strings.HasSuffix(name, ".md") && name != "README.md" {
				files = append(files, p)
			}
			return nil
		})
	}

	// skills/*/SKILL.md
	skillsDir := filepath.Join(base, "skills")
	if fi, err := os.Stat(skillsDir); err == nil && fi.IsDir() {
		_ = filepath.WalkDir(skillsDir, func(p string, de fs.DirEntry, err error) error {
			if err != nil || de.IsDir() {
				return nil
			}
			if de.Name() == "SKILL.md" {
				files = append(files, p)
			}
			return nil
		})
	}

	// rules/*.md (not README.md, not INDEX.md)
	rulesDir := filepath.Join(base, "rules")
	if fi, err := os.Stat(rulesDir); err == nil && fi.IsDir() {
		_ = filepath.WalkDir(rulesDir, func(p string, de fs.DirEntry, err error) error {
			if err != nil || de.IsDir() {
				return nil
			}
			name := de.Name()
			if strings.HasSuffix(name, ".md") && name != "README.md" && name != "INDEX.md" {
				files = append(files, p)
			}
			return nil
		})
	}

	sort.Strings(files)
	return files
}

// countDirFiles counts files matching a glob pattern under dir, excluding
// hidden files and README.md.
func countDirFiles(dir, pattern string) int {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return 0
	}
	n := 0
	_ = filepath.WalkDir(dir, func(p string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			return nil
		}
		name := de.Name()
		if strings.HasPrefix(name, ".") || name == "README.md" {
			return nil
		}
		matched, _ := filepath.Match(pattern, name)
		if matched {
			n++
		}
		return nil
	})
	return n
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

func isValidJSON(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return json.Valid(data)
}

// ---- line budget checks -----------------------------------------------------

func checkLineBudgets(cfg Config, r *Result, w io.Writer, base string) {
	agentsDir := filepath.Join(base, "agents")
	_ = filepath.WalkDir(agentsDir, func(p string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			return nil
		}
		if !strings.HasSuffix(de.Name(), ".md") || de.Name() == "README.md" {
			return nil
		}
		n := countLines(p)
		if n < 80 || n > 140 {
			r.addWarn(cfg, w, fmt.Sprintf("%s: agent file is %d lines (budget 80-140)", p, n))
		}
		return nil
	})

	skillsDir := filepath.Join(base, "skills")
	_ = filepath.WalkDir(skillsDir, func(p string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			return nil
		}
		if de.Name() != "SKILL.md" {
			return nil
		}
		n := countLines(p)
		if n < 80 || n > 350 {
			r.addWarn(cfg, w, fmt.Sprintf("%s: skill is %d lines (budget 80-350)", p, n))
		}
		return nil
	})

	rulesDir := filepath.Join(base, "rules")
	_ = filepath.WalkDir(rulesDir, func(p string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			return nil
		}
		name := de.Name()
		if !strings.HasSuffix(name, ".md") || name == "INDEX.md" || name == "README.md" {
			return nil
		}
		n := countLines(p)
		if n < 60 || n > 150 {
			r.addWarn(cfg, w, fmt.Sprintf("%s: rule is %d lines (budget 60-150)", p, n))
		}
		return nil
	})
}

func countLines(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return strings.Count(string(data), "\n")
}

// ---- playbook reference checks ----------------------------------------------

// playbook: reference pattern — matches "  - playbook:foo/bar" style lines.
var playbookRefRE = regexp.MustCompile(`(?m)^\s*-\s*playbook:([A-Za-z0-9._/-]+)`)

func checkPlaybookReferences(cfg Config, r *Result, w io.Writer, base string) {
	pbDir := filepath.Join(cfg.YakosRoot, "lib", "playbooks")

	// Collect all referenced playbook names across agents, rules, skills.
	refSet := map[string]struct{}{}
	roots := []string{
		filepath.Join(base, "agents"),
		filepath.Join(base, "rules"),
		filepath.Join(base, "skills"),
	}
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(p string, de fs.DirEntry, err error) error {
			if err != nil || de.IsDir() {
				return nil
			}
			if !strings.HasSuffix(de.Name(), ".md") {
				return nil
			}
			data, readErr := os.ReadFile(p)
			if readErr != nil {
				return nil
			}
			for _, m := range playbookRefRE.FindAllSubmatch(data, -1) {
				refSet[string(m[1])] = struct{}{}
			}
			return nil
		})
	}

	// Sort refs for stable output.
	refs := make([]string, 0, len(refSet))
	for k := range refSet {
		refs = append(refs, k)
	}
	sort.Strings(refs)

	for _, ref := range refs {
		target := filepath.Join(pbDir, ref+".md")
		if !fileExists(target) {
			r.addErr(w, fmt.Sprintf("broken playbook reference: 'playbook:%s' — no file at %s", ref, target))
		}
	}
}

// ---- standards checks -------------------------------------------------------

func runStandardsChecks(cfg Config, r *Result, w io.Writer) {
	_, _ = fmt.Fprintln(w, "\nStandards checks (WARN-only in v0.1; see STYLE.md):")
	checkShebangStrictMode(cfg, r, w)
	checkHeaderPurpose(cfg, r, w)
	checkExecutableBits(cfg, r, w)
	checkTODOOnlyFiles(cfg, r, w)
	checkDarkCode(cfg, r, w)
	checkSkillMDSections(cfg, r, w)
	checkAgentMDSections(cfg, r, w)
	// Golden-case eval validation for framework agents
	checkEvalDirs(cfg, r, w, filepath.Join(cfg.YakosRoot, "lib"))
}

// checkShebangStrictMode warns on shell scripts under cli/ or lib/hooks/
// that lack a bash shebang or set -e[u].
func checkShebangStrictMode(cfg Config, r *Result, w io.Writer) {
	roots := []string{
		filepath.Join(cfg.YakosRoot, "cli"),
		filepath.Join(cfg.YakosRoot, "lib", "hooks"),
	}
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(p string, de fs.DirEntry, err error) error {
			if err != nil || de.IsDir() {
				return nil
			}
			if !strings.HasSuffix(de.Name(), ".sh") || strings.HasSuffix(de.Name(), ".framework-hash") {
				return nil
			}
			data, readErr := os.ReadFile(p)
			if readErr != nil {
				return nil
			}
			lines := strings.SplitN(string(data), "\n", 51)
			if len(lines) == 0 {
				return nil
			}
			first := strings.TrimRight(lines[0], "\r")
			if first != "#!/usr/bin/env bash" && first != "#!/bin/bash" {
				r.addWarn(cfg, w, fmt.Sprintf("%s: missing or non-bash shebang", p))
				return nil
			}
			// Skip sourced libraries (they have a source-guard return).
			if strings.Contains(string(data), "return 0 2>/dev/null || exit 0") {
				return nil
			}
			// Check for set -e[u] in first 50 lines.
			setEPat := regexp.MustCompile(`(?m)^set -e[a-z]*u[a-z]*`)
			top := strings.Join(lines[:min(50, len(lines))], "\n")
			if !setEPat.MatchString(top) {
				r.addWarn(cfg, w, fmt.Sprintf("%s: missing 'set -e[u][o pipefail]' near top", p))
			}
			return nil
		})
	}
	// Also check the entry point.
	entryPoint := filepath.Join(cfg.YakosRoot, "cli", "yakos")
	if fileExists(entryPoint) {
		data, err := os.ReadFile(entryPoint)
		if err == nil {
			top := strings.Join(strings.SplitN(string(data), "\n", 51)[:50], "\n")
			setEPat := regexp.MustCompile(`(?m)^set -e`)
			if !setEPat.MatchString(top) {
				r.addWarn(cfg, w, fmt.Sprintf("%s: missing 'set -e' near top", entryPoint))
			}
		}
	}
}

// checkHeaderPurpose warns on scripts in cli/ or lib/hooks/ without a
// "Purpose:" header line in the first 30 lines.
func checkHeaderPurpose(cfg Config, r *Result, w io.Writer) {
	roots := []string{
		filepath.Join(cfg.YakosRoot, "cli"),
		filepath.Join(cfg.YakosRoot, "lib", "hooks"),
	}
	purposePat := regexp.MustCompile(`(?m)^# (Purpose|purpose):`)
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(p string, de fs.DirEntry, err error) error {
			if err != nil || de.IsDir() {
				return nil
			}
			if !strings.HasSuffix(de.Name(), ".sh") {
				return nil
			}
			if strings.HasSuffix(de.Name(), ".framework-hash") {
				return nil
			}
			data, readErr := os.ReadFile(p)
			if readErr != nil {
				return nil
			}
			top30 := strings.Join(strings.SplitN(string(data), "\n", 31)[:30], "\n")
			if !purposePat.MatchString(top30) {
				r.addWarn(cfg, w, fmt.Sprintf("%s: header lacks 'Purpose:' line (see STYLE.md §2)", p))
			}
			return nil
		})
	}
}

// checkExecutableBits errors on hook .sh files that are not executable.
//
// The bash source uses:
//
//	find "$YAKOS_ROOT/lib/hooks" -maxdepth 2 -type f -name '*.sh' ! -path '*/lib/*'
//
// The glob '*/lib/*' matches any path containing "/lib/" as a directory
// component.  Since $YAKOS_ROOT/lib/hooks is itself inside a "lib" directory,
// every file found contains "/lib/" and is excluded.  The result is that on
// the framework repo the check produces no output (all hook paths contain
// "/lib/").  We replicate this behavior: emit errors only for .sh files whose
// absolute path does NOT contain the "/lib/" component anywhere.
func checkExecutableBits(cfg Config, r *Result, w io.Writer) {
	hooksDir := filepath.Join(cfg.YakosRoot, "lib", "hooks")
	depth := 0
	_ = filepath.WalkDir(hooksDir, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel := strings.TrimPrefix(p, hooksDir)
		// Compute depth: count path separators in the relative part.
		depth = strings.Count(strings.TrimPrefix(rel, string(filepath.Separator)), string(filepath.Separator))
		if de.IsDir() {
			if depth > 2 {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(de.Name(), ".sh") {
			return nil
		}
		// Replicate bash '! -path '*/lib/*'' — exclude files whose path
		// contains "/lib/" as a path component.  On this repo all hooks are
		// under <yakosRoot>/lib/hooks/ so they ALL contain "/lib/" and are
		// excluded, matching bash's zero-output behavior.
		if strings.Contains(p, "/lib/") {
			return nil
		}
		fi, statErr := de.Info()
		if statErr != nil {
			return nil
		}
		if fi.Mode()&0111 == 0 {
			r.addErr(w, fmt.Sprintf("%s: hook script is not executable (chmod +x to fix)", p))
		}
		return nil
	})
	_ = depth // suppress unused warning
}

// checkTODOOnlyFiles warns on files where >50% of non-empty lines are TODOs.
func checkTODOOnlyFiles(cfg Config, r *Result, w io.Writer) {
	roots := []string{
		filepath.Join(cfg.YakosRoot, "cli"),
		filepath.Join(cfg.YakosRoot, "lib"),
	}
	todoLinePat := regexp.MustCompile(`(?m)^\s*(#|//)\s*TODO`)
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(p string, de fs.DirEntry, err error) error {
			if err != nil || de.IsDir() {
				return nil
			}
			name := de.Name()
			if !strings.HasSuffix(name, ".sh") && !strings.HasSuffix(name, ".md") {
				return nil
			}
			data, readErr := os.ReadFile(p)
			if readErr != nil {
				return nil
			}
			allLines := strings.Split(string(data), "\n")
			if len(allLines) < 5 {
				return nil
			}
			nonEmpty := 0
			for _, l := range allLines {
				if strings.TrimSpace(l) != "" {
					nonEmpty++
				}
			}
			if nonEmpty < 5 {
				return nil
			}
			todoCount := len(todoLinePat.FindAllStringIndex(string(data), -1))
			if todoCount > 0 && todoCount*2 > nonEmpty {
				r.addWarn(cfg, w, fmt.Sprintf("%s: appears to be TODO-only (%d TODOs of %d non-empty lines)", p, todoCount, nonEmpty))
			}
			return nil
		})
	}
}

// checkDarkCode warns on hook/cli scripts not referenced anywhere in the
// broader codebase.
func checkDarkCode(cfg Config, r *Result, w io.Writer) {
	// Build a concatenated reference blob.
	var refsBuilder strings.Builder

	appendFileIfExists := func(path string) {
		d, err := os.ReadFile(path)
		if err == nil {
			refsBuilder.Write(d)
		}
	}
	appendFileIfExists(filepath.Join(cfg.YakosRoot, "cli", "yakos"))

	_ = filepath.WalkDir(filepath.Join(cfg.YakosRoot, "lib", "settings"), func(p string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			return nil
		}
		n := de.Name()
		if strings.HasSuffix(n, ".json") || strings.Contains(n, ".template.") {
			appendFileIfExists(p)
		}
		return nil
	})
	_ = filepath.WalkDir(filepath.Join(cfg.YakosRoot, "lib", "skills"), func(p string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			return nil
		}
		if de.Name() == "SKILL.md" {
			appendFileIfExists(p)
		}
		return nil
	})
	_ = filepath.WalkDir(filepath.Join(cfg.YakosRoot, "docs"), func(p string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			return nil
		}
		if strings.HasSuffix(de.Name(), ".md") {
			appendFileIfExists(p)
		}
		return nil
	})
	// top-level .md files
	entries, _ := os.ReadDir(cfg.YakosRoot)
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			appendFileIfExists(filepath.Join(cfg.YakosRoot, e.Name()))
		}
	}
	appendFileIfExists(filepath.Join(cfg.YakosRoot, "lib", "hooks", "README.md"))

	refs := refsBuilder.String()

	// Always-exempt basenames — framework helpers, dispatched CLI scripts,
	// and runtime adapters (mirrors the bash case statements).
	alwaysExempt := map[string]bool{
		"hook-input.sh":      true,
		"hook-output.sh":     true,
		"paths.sh":           true,
		"compat.sh":          true,
		"agents-compose.sh":  true,
		"runtime-resolve.sh": true,
		"hooks-install.sh":   true,
		"README.md":          true,
		"agent.sh":           true,
		"cost.sh":            true,
		"memory.sh":          true,
		"session.sh":         true,
		"project-config.sh":  true,
		"migrate.sh":         true,
		"plugin.sh":          true,
		"teach.sh":           true,
		"soul.sh":            true,
		"retro.sh":           true,
		"skill.sh":           true,
		"compact.sh":         true,
		"checkpoint.sh":      true,
		"kanban.sh":          true,
		"env.sh":             true,
		"standards.sh":       true,
		"peer.sh":            true,
		"quickstart.sh":      true,
		"mcp.sh":             true,
		"completion.sh":      true,
		"supervise.sh":       true,
	}

	searchRoots := []string{
		filepath.Join(cfg.YakosRoot, "lib", "hooks"),
		filepath.Join(cfg.YakosRoot, "cli", "lib"),
	}

	for _, root := range searchRoots {
		_ = filepath.WalkDir(root, func(p string, de fs.DirEntry, err error) error {
			if err != nil || de.IsDir() {
				return nil
			}
			if !strings.HasSuffix(de.Name(), ".sh") {
				return nil
			}
			base := de.Name()
			if alwaysExempt[base] {
				return nil
			}
			// Skip runtime adapters.
			if strings.Contains(p, "/cli/lib/runtimes/") {
				return nil
			}
			if !strings.Contains(refs, base) {
				r.addWarn(cfg, w, fmt.Sprintf("%s: potential dark code — '%s' is not referenced anywhere (settings.template.json, SKILL.md, docs/, or CLI)", p, base))
			}
			return nil
		})
	}
}

// checkSkillMDSections warns on SKILL.md files missing required sections.
func checkSkillMDSections(cfg Config, r *Result, w io.Writer) {
	required := []string{"Purpose", "Scope", "Automated pass", "Manual pass", "Known gotchas"}
	skillsDir := filepath.Join(cfg.YakosRoot, "lib", "skills")
	_ = filepath.WalkDir(skillsDir, func(p string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			return nil
		}
		if de.Name() != "SKILL.md" {
			return nil
		}
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return nil
		}
		content := string(data)
		for _, sec := range required {
			pat := regexp.MustCompile(`(?m)^##\s+` + regexp.QuoteMeta(sec) + `\b`)
			if !pat.MatchString(content) {
				r.addWarn(cfg, w, fmt.Sprintf("%s: SKILL.md missing section '## %s'", p, sec))
			}
		}
		return nil
	})
}

// checkAgentMDSections warns on agent .md files missing required sections.
func checkAgentMDSections(cfg Config, r *Result, w io.Writer) {
	required := []string{"Purpose", "Execution", "Special rules", "Handling peer messages", "Personality"}
	agentsDir := filepath.Join(cfg.YakosRoot, "lib", "agents")
	_ = filepath.WalkDir(agentsDir, func(p string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			return nil
		}
		name := de.Name()
		if !strings.HasSuffix(name, ".md") || name == "README.md" || name == "INDEX.md" {
			return nil
		}
		// Skip lead-template until Batch 3 ships content.
		if name == "lead-template.md" {
			return nil
		}
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return nil
		}
		content := string(data)
		for _, sec := range required {
			pat := regexp.MustCompile(`(?m)^##\s+` + regexp.QuoteMeta(sec) + `\b`)
			if !pat.MatchString(content) {
				r.addWarn(cfg, w, fmt.Sprintf("%s: agent file missing section '## %s'", p, sec))
			}
		}
		return nil
	})
}

// ---- golden-case eval validation -------------------------------------------

// goldenCaseSchema describes the required fields in a case-*.json eval file.
// This mirrors the jq validation in validate.sh:_validate_eval_case_file.
type goldenCase struct {
	CaseID           *string       `json:"case_id"`
	Task             *string       `json:"task"`
	Rubric           *goldenRubric `json:"rubric"`
	ExpectedOutcomes []any         `json:"expected_outcomes"`
	ContextFiles     []any         `json:"context_files"`
	MaxDurationS     *float64      `json:"max_duration_s"`
	MaxCostUSD       *float64      `json:"max_cost_usd"`
	Tags             []any         `json:"tags"`
}

type goldenRubric struct {
	Criteria []goldenCriterion `json:"criteria"`
}

type goldenCriterion struct {
	Name   *string  `json:"name"`
	Weight *float64 `json:"weight"`
	Type   *string  `json:"type"`
}

func validateEvalCaseFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read error: %w", err)
	}
	var gc goldenCase
	if err := json.Unmarshal(data, &gc); err != nil {
		return fmt.Errorf("JSON parse error: %w", err)
	}
	if gc.CaseID == nil {
		return fmt.Errorf("case_id must be a string")
	}
	if gc.Task == nil {
		return fmt.Errorf("task must be a string")
	}
	if gc.Rubric == nil {
		return fmt.Errorf("rubric must be an object")
	}
	if len(gc.Rubric.Criteria) == 0 {
		return fmt.Errorf("rubric.criteria must be non-empty")
	}
	for i, c := range gc.Rubric.Criteria {
		if c.Name == nil {
			return fmt.Errorf("criteria[%d].name must be a string", i)
		}
		if c.Weight == nil {
			return fmt.Errorf("criteria[%d].weight must be a number", i)
		}
		if *c.Weight <= 0 || *c.Weight > 1 {
			return fmt.Errorf("criteria[%d].weight must be in (0,1]", i)
		}
		if c.Type == nil || (*c.Type != "binary" && *c.Type != "scalar_0_1") {
			return fmt.Errorf("criteria[%d].type must be binary or scalar_0_1", i)
		}
	}
	if gc.ExpectedOutcomes == nil {
		return fmt.Errorf("expected_outcomes must be an array")
	}
	if len(gc.ExpectedOutcomes) == 0 {
		return fmt.Errorf("expected_outcomes must be non-empty")
	}
	if gc.ContextFiles == nil {
		return fmt.Errorf("context_files must be an array")
	}
	if gc.MaxDurationS == nil {
		return fmt.Errorf("max_duration_s must be a number")
	}
	if *gc.MaxDurationS <= 0 {
		return fmt.Errorf("max_duration_s must be > 0")
	}
	if gc.MaxCostUSD == nil {
		return fmt.Errorf("max_cost_usd must be a number")
	}
	if *gc.MaxCostUSD <= 0 {
		return fmt.Errorf("max_cost_usd must be > 0")
	}
	if gc.Tags == nil {
		return fmt.Errorf("tags must be an array")
	}
	return nil
}

// checkEvalDirs validates golden-case eval directories for each agent .md.
// Mirrors check_eval_dirs in validate.sh.
func checkEvalDirs(cfg Config, r *Result, w io.Writer, root string) {
	agentsDir := filepath.Join(root, "agents")
	if _, err := os.Stat(agentsDir); os.IsNotExist(err) {
		return
	}

	// Walk only maxdepth 1 (direct children of agents/).
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		agentFile := filepath.Join(agentsDir, e.Name())
		name := e.Name()
		switch name {
		case "README.md", "INDEX.md", "lead-template.md":
			continue
		}

		agentBase := strings.TrimSuffix(name, ".md")

		// Locate eval/ sibling.
		var evalDir string
		candidate1 := filepath.Join(agentsDir, agentBase, "eval")
		if fi, err := os.Stat(candidate1); err == nil && fi.IsDir() {
			evalDir = candidate1
		} else {
			candidate2 := filepath.Join(agentsDir, "eval")
			if fi, err := os.Stat(candidate2); err == nil && fi.IsDir() {
				// Only if agentsDir's basename == agentBase.
				if filepath.Base(agentsDir) == agentBase {
					evalDir = candidate2
				}
			}
		}

		// Check model-policy: frontmatter field.
		fm, _ := parseFrontmatter(agentFile)
		var modelPolicy string
		if fm != nil {
			if v, ok := fm["model-policy"]; ok {
				modelPolicy = fmt.Sprintf("%v", v)
			}
		}
		if modelPolicy != "" && evalDir == "" {
			r.addWarn(cfg, w, fmt.Sprintf("%s: has model-policy:%s but no eval/ directory — create cases before routing can recommend", agentFile, modelPolicy))
		}

		if evalDir == "" {
			continue
		}

		// Validate each case-*.json in the eval dir.
		caseEntries, err := os.ReadDir(evalDir)
		if err != nil {
			continue
		}
		var caseFiles []string
		for _, ce := range caseEntries {
			if !ce.IsDir() && strings.HasPrefix(ce.Name(), "case-") && strings.HasSuffix(ce.Name(), ".json") {
				caseFiles = append(caseFiles, filepath.Join(evalDir, ce.Name()))
			}
		}
		sort.Strings(caseFiles)

		if len(caseFiles) == 0 {
			r.addWarn(cfg, w, fmt.Sprintf("%s: eval/ directory exists but contains no case-*.json files", evalDir))
			continue
		}
		for _, cf := range caseFiles {
			if err := validateEvalCaseFile(cf); err != nil {
				r.addWarn(cfg, w, fmt.Sprintf("%s: golden-case schema violation: %v", cf, err))
			} else {
				r.addOK(w, fmt.Sprintf("eval case %s", cf))
			}
		}
	}
}

// min returns the smaller of a and b.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
