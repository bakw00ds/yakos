// Package doctor implements the Go port of `yakos doctor`.
//
// Doctor verifies the YakOS install and environment health. It is read-only:
// it never writes state. (The --fix mode is explicitly excluded from Phase 1
// per the port plan; it is tracked as ideas wishlist rank 5.)
//
// Section order mirrors doctor.sh exactly:
//
//  1. Required commands
//  2. Optional commands
//  3. Install pointer
//  4. YakOS symlinks
//  5. settings.json
//  6. Auto-memory
//  7. Project hook drift (only when ProjectPath is set)
//  8. Pre-push version gate (only when ProjectPath is set)
//  9. Multi-dev coord
//  10. Optional local/cross-model tooling
//  11. External provider API keys
//  12. Runtime feature probe (only when ProbeRuntime is true)
//  13. Production checklist (only when Production is true)
//  14. Summary
package doctor

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Severity describes the importance of a finding.
type Severity int

const (
	SeverityInfo Severity = iota
	SeverityOK
	SeverityWarn
	SeverityErr
)

// Section identifies which check produced a finding.
type Section int

const (
	SectionRequiredCommands Section = iota
	SectionOptionalCommands
	SectionInstallPointer
	SectionSymlinks
	SectionSettingsJSON
	SectionAutoMemory
	SectionHookDrift
	SectionPrePushGate
	SectionMultiDevCoord
	SectionLocalTooling
	SectionAPIKeys
	SectionRuntimeProbe
	SectionProduction
)

// Finding is one reported item: a severity level plus a human-readable message.
type Finding struct {
	Section  Section
	Severity Severity
	Message  string
}

// Report is the structured result of a doctor run.
type Report struct {
	Findings []Finding
	Errors   int
	Warnings int
}

// Config controls a doctor run.
type Config struct {
	// YakosRoot is the framework repo root (set from $YAKOS_ROOT via the entry-point).
	YakosRoot string

	// YakosLib is the framework lib/ directory (set from $YAKOS_LIB).
	YakosLib string

	// ProjectPath is the optional project path positional argument.
	// When empty, project-specific checks are skipped.
	ProjectPath string

	// ProbeRuntime enables the --probe-runtime section.
	ProbeRuntime bool

	// Production enables the --production section.
	Production bool

	// HomeDir overrides $HOME. Used in tests. If empty, os.Getenv("HOME") is used.
	HomeDir string

	// Environ is an optional function for reading environment variables.
	// If nil, os.Getenv is used. Tests inject a controlled map.
	Environ func(string) string

	// LookPath is an optional function for looking up commands on PATH.
	// If nil, exec.LookPath from os/exec is used. Tests inject a controlled version.
	LookPath func(string) (string, error)

	// Writer is the output destination. Defaults to os.Stdout.
	Writer io.Writer

	// ErrWriter is the error destination. Defaults to os.Stderr.
	ErrWriter io.Writer
}

// Run performs the full doctor check and returns a structured Report.
// All output is written to cfg.Writer as the check proceeds (streaming).
// The caller should check Report.Errors to determine the exit code.
func Run(cfg Config) (*Report, error) {
	if cfg.Writer == nil {
		cfg.Writer = os.Stdout
	}
	if cfg.ErrWriter == nil {
		cfg.ErrWriter = os.Stderr
	}

	env := cfg.Environ
	if env == nil {
		env = os.Getenv
	}

	home := cfg.HomeDir
	if home == "" {
		home = env("HOME")
	}
	if home == "" {
		home = "/tmp"
	}

	yakosRoot := cfg.YakosRoot
	if r := env("YAKOS_ROOT"); r != "" {
		yakosRoot = r
	}

	r := &runner{
		cfg:       cfg,
		w:         cfg.Writer,
		home:      home,
		yakosRoot: yakosRoot,
		env:       env,
		report:    &Report{},
	}

	_, _ = fmt.Fprintln(r.w, "yakos doctor")
	_, _ = fmt.Fprintln(r.w, "")

	r.checkRequiredCommands()
	r.checkOptionalCommands()
	r.checkInstallPointer()
	r.checkSymlinks()
	r.checkSettingsJSON()
	r.checkAutoMemory()

	if cfg.ProjectPath != "" {
		r.checkHookDrift()
		r.checkPrePushGate()
	}

	r.checkMultiDevCoord()
	r.checkLocalTooling()
	r.checkAPIKeys()

	if cfg.ProbeRuntime {
		r.checkRuntimeProbe()
	}

	if cfg.Production {
		r.checkProduction()
	}

	_, _ = fmt.Fprintf(r.w, "Summary: %d error(s), %d warning(s)\n", r.report.Errors, r.report.Warnings)

	return r.report, nil
}

// runner holds per-run state.
type runner struct {
	cfg       Config
	w         io.Writer
	home      string
	yakosRoot string
	env       func(string) string
	report    *Report
}

// ---- output helpers ---------------------------------------------------------

func (r *runner) ok(section Section, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprintf(r.w, "  [ok]   %s\n", msg)
	r.report.Findings = append(r.report.Findings, Finding{Section: section, Severity: SeverityOK, Message: msg})
}

func (r *runner) info(section Section, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprintf(r.w, "  [info] %s\n", msg)
	r.report.Findings = append(r.report.Findings, Finding{Section: section, Severity: SeverityInfo, Message: msg})
}

func (r *runner) warn(section Section, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprintf(r.w, "  [warn] %s\n", msg)
	r.report.Findings = append(r.report.Findings, Finding{Section: section, Severity: SeverityWarn, Message: msg})
	r.report.Warnings++
}

func (r *runner) err(section Section, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprintf(r.w, "  [err]  %s\n", msg)
	r.report.Findings = append(r.report.Findings, Finding{Section: section, Severity: SeverityErr, Message: msg})
	r.report.Errors++
}

// lookPath wraps the configured LookPath function, falling back to the stdlib.
func (r *runner) lookPath(cmd string) (string, error) {
	if r.cfg.LookPath != nil {
		return r.cfg.LookPath(cmd)
	}
	// Import is deferred to avoid importing os/exec at package level for tests.
	return defaultLookPath(cmd)
}

// ---- section 1: required commands -------------------------------------------

func (r *runner) checkRequiredCommands() {
	_, _ = fmt.Fprintln(r.w, "Required commands")
	for _, c := range []string{"bash", "git", "jq"} {
		if path, err := r.lookPath(c); err == nil {
			r.ok(SectionRequiredCommands, "%s: %s", c, path)
		} else {
			r.err(SectionRequiredCommands, "%s: not found in PATH", c)
		}
	}
	_, _ = fmt.Fprintln(r.w, "")
}

// ---- section 2: optional commands -------------------------------------------

func (r *runner) checkOptionalCommands() {
	_, _ = fmt.Fprintln(r.w, "Optional commands")
	for _, c := range []string{"gtimeout", "gsed", "shellcheck", "python3", "realpath"} {
		if path, err := r.lookPath(c); err == nil {
			r.ok(SectionOptionalCommands, "%s: %s", c, path)
		} else {
			r.info(SectionOptionalCommands, "%s: not present (compat handles macOS BSD fallback)", c)
		}
	}
	_, _ = fmt.Fprintln(r.w, "")
}

// ---- section 3: install pointer ---------------------------------------------

func (r *runner) checkInstallPointer() {
	_, _ = fmt.Fprintln(r.w, "Install pointer")
	pointer := filepath.Join(r.home, ".yakos")
	if _, err := os.Stat(pointer); os.IsNotExist(err) {
		r.err(SectionInstallPointer, "%s does not exist; run 'yakos install'", pointer)
	} else {
		data, rerr := os.ReadFile(pointer) //nolint:gosec
		if rerr != nil {
			r.err(SectionInstallPointer, "%s: could not read: %v", pointer, rerr)
		} else {
			target := strings.TrimSpace(string(data))
			if target == "" {
				r.err(SectionInstallPointer, "%s is empty", pointer)
			} else if fi, err := os.Stat(target); err != nil || !fi.IsDir() {
				r.err(SectionInstallPointer, "%s points to '%s' which does not exist", pointer, target)
			} else if _, err := os.Stat(filepath.Join(target, "lib")); err != nil {
				r.err(SectionInstallPointer, "%s points to '%s' but no lib/ directory there", pointer, target)
			} else {
				r.ok(SectionInstallPointer, "%s → %s", pointer, target)
			}
		}
	}
	_, _ = fmt.Fprintln(r.w, "")
}

// ---- section 4: symlinks ----------------------------------------------------

func (r *runner) checkSymlinks() {
	claudeDir := filepath.Join(r.home, ".claude")
	_, _ = fmt.Fprintf(r.w, "YakOS symlinks under %s\n", claudeDir)

	if _, err := os.Stat(claudeDir); os.IsNotExist(err) {
		r.info(SectionSymlinks, "%s does not exist (run 'yakos install')", claudeDir)
		_, _ = fmt.Fprintln(r.w, "")
		return
	}

	// Read the pointer to determine which symlinks are yakos-owned.
	pointerTarget := ""
	pointerFile := filepath.Join(r.home, ".yakos")
	if data, err := os.ReadFile(pointerFile); err == nil { //nolint:gosec
		pointerTarget = strings.TrimSpace(string(data))
	}

	totalYakos := 0
	totalBroken := 0
	totalForeign := 0

	for _, sub := range []string{"agents", "skills", "rules", "playbooks"} {
		d := filepath.Join(claudeDir, sub)
		if _, err := os.Stat(d); err != nil {
			continue
		}
		_ = filepath.Walk(d, func(path string, info os.FileInfo, err error) error {
			if err != nil || path == d {
				return nil
			}
			// Only examine symlinks.
			linfo, lerr := os.Lstat(path)
			if lerr != nil || linfo.Mode()&os.ModeSymlink == 0 {
				return nil
			}
			tgt, rerr := filepath.EvalSymlinks(path)
			if rerr != nil || tgt == "" {
				// Broken symlink.
				r.err(SectionSymlinks, "broken symlink: %s → %s", path, tgt)
				totalBroken++
				return nil
			}
			if pointerTarget != "" && strings.HasPrefix(tgt, pointerTarget+string(filepath.Separator)) {
				totalYakos++
			} else {
				totalForeign++
			}
			return nil
		})
	}

	if totalYakos == 0 && totalBroken == 0 && totalForeign == 0 {
		r.info(SectionSymlinks, "no symlinks present (lib/ is empty in v0.1; agents/skills come in Batch 3)")
	} else {
		r.ok(SectionSymlinks, "%d YakOS-owned symlink(s) resolved", totalYakos)
		if totalForeign > 0 {
			r.info(SectionSymlinks, "%d symlink(s) under those dirs point outside YakOS (left alone)", totalForeign)
		}
	}
	_, _ = fmt.Fprintln(r.w, "")
}

// ---- section 5: settings.json -----------------------------------------------

func (r *runner) checkSettingsJSON() {
	_, _ = fmt.Fprintln(r.w, "settings.json")
	settingsFile := filepath.Join(r.home, ".claude", "settings.json")
	if _, err := os.Stat(settingsFile); os.IsNotExist(err) {
		r.info(SectionSettingsJSON, "%s does not exist (run 'yakos install' to create)", settingsFile)
		_, _ = fmt.Fprintln(r.w, "")
		return
	}

	data, err := os.ReadFile(settingsFile) //nolint:gosec
	if err != nil {
		r.err(SectionSettingsJSON, "%s is not valid JSON", settingsFile)
		_, _ = fmt.Fprintln(r.w, "")
		return
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		r.err(SectionSettingsJSON, "%s is not valid JSON", settingsFile)
		_, _ = fmt.Fprintln(r.w, "")
		return
	}

	r.ok(SectionSettingsJSON, "%s is valid JSON", settingsFile)

	// Check env.CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS.
	agentTeams := ""
	if env, ok := parsed["env"].(map[string]any); ok {
		if v, ok := env["CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"].(string); ok {
			agentTeams = v
		}
	}
	if agentTeams == "1" {
		r.ok(SectionSettingsJSON, "env.CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS = 1")
	} else {
		r.warn(SectionSettingsJSON, "env.CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS not set to '1' (agent teams disabled)")
	}
	_, _ = fmt.Fprintln(r.w, "")
}

// ---- section 6: auto-memory -------------------------------------------------

func (r *runner) checkAutoMemory() {
	_, _ = fmt.Fprintln(r.w, "Auto-memory")
	projectsDir := filepath.Join(r.home, ".claude", "projects")
	if _, err := os.Stat(projectsDir); os.IsNotExist(err) {
		r.info(SectionAutoMemory, "%s does not exist (no auto-memory yet)", projectsDir)
	} else {
		entries, err := os.ReadDir(projectsDir)
		count := 0
		if err == nil {
			for _, e := range entries {
				if e.IsDir() {
					count++
				}
			}
		}
		r.info(SectionAutoMemory, "%s/ exists (%d project dir(s)). YakOS never modifies this.", projectsDir, count)
	}
	_, _ = fmt.Fprintln(r.w, "")
}

// ---- section 7: project hook drift ------------------------------------------

func (r *runner) checkHookDrift() {
	projectPath := r.cfg.ProjectPath
	hooksDir := filepath.Join(projectPath, "scripts", "hooks")
	_, _ = fmt.Fprintf(r.w, "Project hook drift: %s/scripts/hooks/\n", projectPath)

	if _, err := os.Stat(projectPath); os.IsNotExist(err) {
		r.err(SectionHookDrift, "project path not found: %s", projectPath)
		_, _ = fmt.Fprintln(r.w, "")
		return
	}
	if _, err := os.Stat(hooksDir); os.IsNotExist(err) {
		r.info(SectionHookDrift, "%s does not exist (run 'yakos init <name> --project %s')",
			hooksDir, projectPath)
		_, _ = fmt.Fprintln(r.w, "")
		return
	}

	report, err := checkHookDriftDir(hooksDir, projectPath)
	if err != nil {
		r.err(SectionHookDrift, "drift check failed: %v", err)
		_, _ = fmt.Fprintln(r.w, "")
		return
	}

	for _, result := range report.Results {
		if result.State == 1 { // drifted
			r.info(SectionHookDrift, "DRIFT: %s  (expected %s, got %s; not an error — projects are expected to customize)",
				result.RelPath, result.ExpectedHash, result.ActualHash)
		}
	}
	r.ok(SectionHookDrift, "%d clean, %d drifted, %d unhashed (no .framework-hash sibling)",
		report.Clean, report.Drifted, report.Unhashed)
	_, _ = fmt.Fprintln(r.w, "")
}

// ---- section 8: pre-push version gate ---------------------------------------

func (r *runner) checkPrePushGate() {
	projectPath := r.cfg.ProjectPath
	gateDst := filepath.Join(projectPath, ".git", "hooks", "pre-push")
	gateHashSibling := gateDst + ".framework-hash"
	gateSrc := filepath.Join(r.yakosRoot, "lib", "hooks", "git", "pre-push-version-gate.sh")

	_, _ = fmt.Fprintf(r.w, "Pre-push version gate: %s/.git/hooks/pre-push\n", projectPath)

	if _, err := os.Stat(gateDst); os.IsNotExist(err) {
		r.info(SectionPrePushGate, "not installed (install: 'yakos git-hooks install' from inside the project, or 'yakos init <name> --project %s --with-gate')", projectPath)
		_, _ = fmt.Fprintln(r.w, "")
		return
	}
	if _, err := os.Stat(gateHashSibling); os.IsNotExist(err) {
		r.info(SectionPrePushGate, "present but not YakOS-owned (no .framework-hash sibling)")
		_, _ = fmt.Fprintln(r.w, "")
		return
	}
	if _, err := os.Stat(gateSrc); os.IsNotExist(err) {
		r.info(SectionPrePushGate, "installed but framework source missing at %s", gateSrc)
		_, _ = fmt.Fprintln(r.w, "")
		return
	}

	expectedData, rerr := os.ReadFile(gateHashSibling) //nolint:gosec
	if rerr != nil {
		r.info(SectionPrePushGate, "could not read .framework-hash: %v", rerr)
		_, _ = fmt.Fprintln(r.w, "")
		return
	}
	expectedHash := strings.TrimSpace(string(expectedData))
	actualSrcHash, herr := sha256File(gateSrc)
	if herr != nil {
		r.info(SectionPrePushGate, "could not hash framework source: %v", herr)
		_, _ = fmt.Fprintln(r.w, "")
		return
	}

	if expectedHash == actualSrcHash {
		r.ok(SectionPrePushGate, "installed (current version, no drift)")
	} else {
		r.info(SectionPrePushGate, "DRIFT: installed gate differs from framework (refresh: 'yakos git-hooks install --force')")
	}
	_, _ = fmt.Fprintln(r.w, "")
}

// ---- section 9: multi-dev coord ---------------------------------------------

func (r *runner) checkMultiDevCoord() {
	_, _ = fmt.Fprintln(r.w, "Multi-dev coord (co-pilot mode; optional)")
	coordRoot := r.env("YAKOS_COORD_ROOT")
	if coordRoot == "" {
		coordRoot = "/var/lib/yakos"
	}

	if _, err := os.Stat(coordRoot); os.IsNotExist(err) {
		r.info(SectionMultiDevCoord, "%s does not exist (no multi-dev coord; see docs/co-pilot-mode.md to provision)", coordRoot)
		_, _ = fmt.Fprintln(r.w, "")
		return
	}

	// Check writability.
	if !isWritable(coordRoot) {
		r.info(SectionMultiDevCoord, "%s exists but is not writable by you — group membership / umask?", coordRoot)
	} else {
		r.ok(SectionMultiDevCoord, "%s exists and is writable", coordRoot)
	}

	nProjects := countDirs(coordRoot)
	r.info(SectionMultiDevCoord, "%d project(s) provisioned under %s", nProjects, coordRoot)

	if r.cfg.ProjectPath != "" {
		projName := filepath.Base(r.cfg.ProjectPath)
		projCoord := filepath.Join(coordRoot, projName, "coord")
		if _, err := os.Stat(projCoord); os.IsNotExist(err) {
			r.info(SectionMultiDevCoord, "  %s: coord not provisioned for this project (run 'yakos init --multi-dev %s --project %s')",
				projName, projName, r.cfg.ProjectPath)
		} else {
			r.ok(SectionMultiDevCoord, "  %s: coord dir present at %s", projName, projCoord)
			nSessions := countGlob(filepath.Join(projCoord, "sessions"), "*.ndjson")
			r.info(SectionMultiDevCoord, "  %s: %d session ledger(s) on file", projName, nSessions)

			// Stale claims check.
			r.checkStaleClaims(projCoord, projName)

			// Memory symlink.
			r.checkMemorySymlink(coordRoot, projName)
		}
	}
	_, _ = fmt.Fprintln(r.w, "")
}

// checkStaleClaims inspects active-claims.json for expired claims.
func (r *runner) checkStaleClaims(projCoord, projName string) {
	claimsFile := filepath.Join(projCoord, "active-claims.json")
	if _, err := os.Stat(claimsFile); err != nil {
		return
	}
	data, err := os.ReadFile(claimsFile) //nolint:gosec
	if err != nil {
		return
	}

	type owner struct {
		User      string `json:"user"`
		Host      string `json:"host"`
		ExpiresAt string `json:"expires_at"`
	}
	type claim struct {
		Path   string  `json:"path"`
		Owners []owner `json:"owners"`
	}
	type claimsDoc struct {
		Claims []claim `json:"claims"`
	}

	var doc claimsDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return
	}

	// Simple string comparison for ISO-8601 timestamps is correct when both
	// sides are UTC (the format sorts lexicographically).
	nowStr := nowUTCString()
	var staleLines []string
	for _, c := range doc.Claims {
		if len(c.Owners) == 0 {
			continue
		}
		o := c.Owners[0]
		if o.ExpiresAt != "" && o.ExpiresAt <= nowStr {
			staleLines = append(staleLines, fmt.Sprintf("  %s — %s@%s (expired %s)", c.Path, o.User, o.Host, o.ExpiresAt))
		}
	}

	if len(staleLines) > 0 {
		r.info(SectionMultiDevCoord, "  %s: STALE CLAIMS detected (run any 'yakos peer claims' to trigger rebuild):", projName)
		for i, line := range staleLines {
			if i >= 5 {
				break
			}
			_, _ = fmt.Fprintf(r.w, "  %s\n", line)
		}
	} else {
		r.ok(SectionMultiDevCoord, "  %s: no stale claims", projName)
	}
}

// checkMemorySymlink verifies the user's memory symlink for the project.
func (r *runner) checkMemorySymlink(coordRoot, projName string) {
	userMem := filepath.Join(r.home, ".yakos-state", "memory", projName)
	linfo, err := os.Lstat(userMem)
	if err != nil {
		r.info(SectionMultiDevCoord, "  %s: %s not present", projName, userMem)
		return
	}
	if linfo.Mode()&os.ModeSymlink != 0 {
		target, rerr := os.Readlink(userMem)
		if rerr != nil {
			target = "<unreadable>"
		}
		expected := filepath.Join(coordRoot, projName, "memory")
		if target == expected {
			r.ok(SectionMultiDevCoord, "  %s: memory symlink correct (%s → %s)", projName, userMem, expected)
		} else {
			r.info(SectionMultiDevCoord, "  %s: memory symlink points to '%s'; expected '%s'", projName, target, expected)
		}
	} else if linfo.IsDir() {
		r.info(SectionMultiDevCoord, "  %s: %s is a directory, not a symlink (run 'yakos init --multi-dev')", projName, userMem)
	}
}

// ---- section 10: local/cross-model tooling ----------------------------------

func (r *runner) checkLocalTooling() {
	_, _ = fmt.Fprintln(r.w, "Optional local/cross-model tooling")
	for _, tool := range []string{"ollama", "lms", "llama-server"} {
		if _, err := r.lookPath(tool); err == nil {
			// Best-effort version probe.
			version := toolVersion(tool)
			if version != "" {
				r.ok(SectionLocalTooling, "%s: found (%s)", tool, version)
			} else {
				r.ok(SectionLocalTooling, "%s: found", tool)
			}
		} else {
			r.info(SectionLocalTooling, "%s: not found", tool)
		}
	}
	_, _ = fmt.Fprintln(r.w, "")
}

// ---- section 11: API keys ---------------------------------------------------

func (r *runner) checkAPIKeys() {
	_, _ = fmt.Fprintln(r.w, "External provider API keys (NOT used by YakOS or Claude Code core;")
	_, _ = fmt.Fprintln(r.w, "relevant only for custom MCP servers or future provider routing)")
	for _, varName := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY"} {
		if r.env(varName) != "" {
			r.ok(SectionAPIKeys, "%s: set", varName)
		} else {
			r.info(SectionAPIKeys, "%s: not set", varName)
		}
	}
	_, _ = fmt.Fprintln(r.w, "")
	r.info(SectionAPIKeys, "(All integrations above are optional. See COMPATIBILITY.md and COOKBOOK.md for usage.)")
	_, _ = fmt.Fprintln(r.w, "")
}

// ---- section 12: runtime probe ----------------------------------------------

func (r *runner) checkRuntimeProbe() {
	_, _ = fmt.Fprintln(r.w, "Claude Code runtime feature probe")

	teamsDir := filepath.Join(r.home, ".claude", "teams")
	tasksDir := filepath.Join(r.home, ".claude", "tasks")

	if _, err := os.Stat(teamsDir); os.IsNotExist(err) {
		r.info(SectionRuntimeProbe, "%s/ does not exist (no teams created yet)", teamsDir)
	} else {
		nTeams := countDirs(teamsDir)
		r.ok(SectionRuntimeProbe, "%s/ exists (%d team(s))", teamsDir, nTeams)
	}

	if _, err := os.Stat(tasksDir); os.IsNotExist(err) {
		r.info(SectionRuntimeProbe, "%s/ does not exist (no teams created yet)", tasksDir)
	} else {
		nTasks := countDirs(tasksDir)
		nTaskFiles := countFilesExcluding(tasksDir, ".lock")
		if nTaskFiles == 0 && nTasks > 0 {
			r.info(SectionRuntimeProbe, "%s/ has %d team dir(s); 0 task files (consistent with TaskCreate not exposed; see incident:v0.2.1-task-tools-not-exposed)",
				tasksDir, nTasks)
		} else {
			r.info(SectionRuntimeProbe, "%s/ has %d team dir(s), %d task file(s)", tasksDir, nTasks, nTaskFiles)
		}
	}

	nInboxes := countGlob(teamsDir, "*/inboxes/*.json")
	r.info(SectionRuntimeProbe, "mailbox files (inboxes/*.json): %d across all teams", nInboxes)
	_, _ = fmt.Fprintln(r.w, "")

	// In-session tool availability from last probe artifact.
	probeFile := filepath.Join(r.home, ".yakos-state", "runtime-probe.json")
	_, _ = fmt.Fprintln(r.w, "In-session tool availability (last known state):")
	if _, err := os.Stat(probeFile); os.IsNotExist(err) {
		r.info(SectionRuntimeProbe, "no probe file found at %s", probeFile)
		r.info(SectionRuntimeProbe, "  per the most recent in-session probe (2026-04-29):")
		r.info(SectionRuntimeProbe, "  TaskCreate / TaskList / TaskUpdate: NOT AVAILABLE in this Claude Code build")
		r.info(SectionRuntimeProbe, "  see docs/architecture/phase-0.5-results.md and incident:v0.2.1-task-tools-not-exposed")
	} else {
		r.parseProbeFile(probeFile)
	}
	_, _ = fmt.Fprintln(r.w, "")

	// Refresh prompt.
	_, _ = fmt.Fprintln(r.w, "To refresh the last-known state, run this prompt in a Claude Code session:")
	_, _ = fmt.Fprintln(r.w, "")
	_, _ = fmt.Fprintln(r.w, "  Run this in your next Claude Code session to refresh the runtime probe:")
	_, _ = fmt.Fprintln(r.w, "")
	_, _ = fmt.Fprintln(r.w, `      Run ToolSearch with query "select:TaskCreate,TaskList,TaskUpdate".`)
	_, _ = fmt.Fprintln(r.w, `      Report each tool as "available" if a schema returned, or "unavailable"`)
	_, _ = fmt.Fprintln(r.w, `      if "No matching deferred tools found". Then write the result to`)
	_, _ = fmt.Fprintln(r.w, `      ~/.yakos-state/runtime-probe.json with shape:`)
	_, _ = fmt.Fprintln(r.w, `      {"timestamp":"<iso>","claude_code_version":"<env>","tools":{"TaskCreate":"available|unavailable",...}}`)
	_, _ = fmt.Fprintln(r.w, "")
	_, _ = fmt.Fprintln(r.w, "")

	// yakos start projection.
	_, _ = fmt.Fprintln(r.w, "yakos start projection (--agents JSON injection):")
	if r.cfg.ProjectPath != "" {
		if _, err := os.Stat(r.cfg.ProjectPath); err == nil {
			r.checkAgentProjection()
		} else {
			r.info(SectionRuntimeProbe, "pass <project-path> to see the agents that would be injected")
		}
	} else {
		r.info(SectionRuntimeProbe, "pass <project-path> to see the agents that would be injected")
	}
	_, _ = fmt.Fprintln(r.w, "")

	// Launch log.
	launchLog := filepath.Join(r.home, ".yakos-state", "launch-log.ndjson")
	if _, err := os.Stat(launchLog); os.IsNotExist(err) {
		r.info(SectionRuntimeProbe, "launch-log.ndjson: none yet (run 'yakos start <name>' to populate)")
	} else {
		nLaunches := countFileLines(launchLog)
		lastLaunch := lastJSONLField(launchLog, "ts")
		if lastLaunch == "" {
			lastLaunch = "unknown"
		}
		r.info(SectionRuntimeProbe, "launch-log.ndjson: %d recorded session launch(es); last at %s", nLaunches, lastLaunch)
	}
	_, _ = fmt.Fprintln(r.w, "")

	// Runtime version drift.
	probeDir := filepath.Join(r.home, ".yakos-state", "runtime-probes")
	_, _ = fmt.Fprintln(r.w, "Runtime version drift (last 2 probes per runtime):")
	if _, err := os.Stat(probeDir); os.IsNotExist(err) {
		r.info(SectionRuntimeProbe, "no runtime-probe history yet (run 'yakos start' against each runtime)")
	} else {
		r.checkRuntimeDrift(probeDir)
	}
	_, _ = fmt.Fprintln(r.w, "")
}

// parseProbeFile reads ~/.yakos-state/runtime-probe.json and reports tool state.
func (r *runner) parseProbeFile(probeFile string) {
	data, err := os.ReadFile(probeFile) //nolint:gosec
	if err != nil {
		r.info(SectionRuntimeProbe, "  raw probe file at: %s", probeFile)
		return
	}

	type probeDoc struct {
		Timestamp          string            `json:"timestamp"`
		ClaudeCodeVersion  string            `json:"claude_code_version"`
		Tools              map[string]string `json:"tools"`
	}
	var probe probeDoc
	if err := json.Unmarshal(data, &probe); err != nil {
		r.info(SectionRuntimeProbe, "  raw probe file at: %s", probeFile)
		return
	}

	ts := probe.Timestamp
	if ts == "" {
		ts = "unknown"
	}
	ccVer := probe.ClaudeCodeVersion
	if ccVer == "" {
		ccVer = "unknown"
	}
	r.info(SectionRuntimeProbe, "Last probe: %s (Claude Code build: %s)", ts, ccVer)

	for _, tool := range []string{"TaskCreate", "TaskList", "TaskUpdate"} {
		state := probe.Tools[tool]
		if state == "" {
			state = "unknown"
		}
		switch state {
		case "available":
			r.ok(SectionRuntimeProbe, "%s: AVAILABLE", tool)
		case "unavailable":
			r.info(SectionRuntimeProbe, "%s: NOT AVAILABLE", tool)
		default:
			r.info(SectionRuntimeProbe, "%s: %s", tool, state)
		}
	}
}

// checkAgentProjection reports what yakos start would inject.
// Since this requires agents-compose.sh (bash), we report the framework +
// project agent counts without invoking the bash compose function.
func (r *runner) checkAgentProjection() {
	fwAgentsDir := filepath.Join(r.yakosRoot, "lib", "agents")
	fwCount := countMarkdownFiles(fwAgentsDir, "README.md", "lead-template.md")

	projCount := 0
	projAgentsDir := filepath.Join(r.cfg.ProjectPath, ".claude", "agents")
	if _, err := os.Stat(projAgentsDir); err == nil {
		projCount = countMarkdownFiles(projAgentsDir, "README.md")
	}

	total := fwCount + projCount
	r.ok(SectionRuntimeProbe, "would inject %d agent(s) (%d framework + %d project; project overrides on id collision)",
		total, fwCount, projCount)
	if total > 0 {
		r.info(SectionRuntimeProbe, "addressable subagent_types after launch: (run yakos doctor --probe-runtime with bash for full list)")
	}
}

// checkRuntimeDrift compares last 2 probes per runtime ndjson file.
func (r *runner) checkRuntimeDrift(probeDir string) {
	entries, err := os.ReadDir(probeDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".ndjson") {
			continue
		}
		rt := strings.TrimSuffix(e.Name(), ".ndjson")
		path := filepath.Join(probeDir, e.Name())
		lines := readLastNJSONLLines(path, 2)
		if len(lines) == 0 {
			continue
		}
		cur := jsonField(lines[len(lines)-1], "version")
		if len(lines) == 1 || cur == jsonField(lines[0], "version") {
			r.ok(SectionRuntimeProbe, "%s: %s (%d probe(s) on file)", rt, cur, countFileLines(path))
		} else {
			prev := jsonField(lines[0], "version")
			r.info(SectionRuntimeProbe, "%s: %s (DRIFT — was '%s'; review adapter for schema changes)", rt, cur, prev)
		}
	}
}

// ---- section 13: production checklist ---------------------------------------

func (r *runner) checkProduction() {
	if r.cfg.ProjectPath == "" {
		r.err(SectionProduction, "doctor --production requires <project-path>")
		return
	}
	projectPath := r.cfg.ProjectPath

	_, _ = fmt.Fprintln(r.w, "")
	_, _ = fmt.Fprintln(r.w, "=== Production-readiness checklist (--production) ===")
	_, _ = fmt.Fprintln(r.w, "    See lib/settings/harness-checklist.template.md for the full list.")
	_, _ = fmt.Fprintln(r.w, "")

	prodErrs := 0
	prodWarns := 0

	pok := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		_, _ = fmt.Fprintf(r.w, "  [PASS] %s\n", msg)
		r.report.Findings = append(r.report.Findings, Finding{Section: SectionProduction, Severity: SeverityOK, Message: msg})
	}
	pwarn := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		_, _ = fmt.Fprintf(r.w, "  [WARN] %s\n", msg)
		r.report.Findings = append(r.report.Findings, Finding{Section: SectionProduction, Severity: SeverityWarn, Message: msg})
		prodWarns++
	}
	pfail := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		_, _ = fmt.Fprintf(r.w, "  [FAIL] %s\n", msg)
		r.report.Findings = append(r.report.Findings, Finding{Section: SectionProduction, Severity: SeverityErr, Message: msg})
		prodErrs++
	}

	// Security posture.
	_, _ = fmt.Fprintln(r.w, "Security posture:")
	securityMD := filepath.Join(r.yakosRoot, "SECURITY.md")
	if _, err := os.Stat(securityMD); err == nil {
		pok("framework SECURITY.md present")
	} else {
		pfail("SECURITY.md missing")
	}

	// Obvious-secret scan (grep-based; no ripgrep dependency).
	if hasObviousSecrets(projectPath) {
		pwarn("potential secrets found in project tree (check manually)")
	} else {
		pok("no obvious-secret patterns in tree")
	}

	// path-allowlist.json.
	allowlist := filepath.Join(projectPath, ".claude", "path-allowlist.json")
	if _, err := os.Stat(allowlist); err != nil {
		pfail("path-allowlist.json missing at %s", allowlist)
	} else {
		pok("path-allowlist.json present")
		data, _ := os.ReadFile(allowlist) //nolint:gosec
		var m map[string]any
		if err := json.Unmarshal(data, &m); err == nil && len(m) > 0 {
			pok("path-allowlist.json has %d agent entr(ies)", len(m))
		} else {
			pwarn("path-allowlist.json is empty {} — no agent has restrictions")
		}
	}

	// Security hooks.
	for _, hook := range []string{"secret-scan.sh", "output-injection-scan.sh", "budget-guard.sh", "supervisor-gate.sh"} {
		hookPath := filepath.Join(projectPath, "scripts", "hooks", hook)
		fi, err := os.Stat(hookPath)
		if err == nil && fi.Mode()&0111 != 0 {
			pok("%s installed + executable", hook)
		} else {
			pwarn("%s not installed at %s", hook, hookPath)
		}
	}

	// Hook discipline.
	_, _ = fmt.Fprintln(r.w, "")
	_, _ = fmt.Fprintln(r.w, "Hook discipline:")
	projName := filepath.Base(projectPath)
	bypassFile := filepath.Join(r.home, "agent-control", projName, "work", "current", "hook-bypass.md")
	if _, err := os.Stat(bypassFile); os.IsNotExist(err) {
		pwarn("hook-bypass.md missing (run yakos init first)")
	} else {
		if hasActiveBypass(bypassFile) {
			pwarn("hook-bypass.md has active entries — production-ready state should be empty")
		} else {
			pok("hook-bypass.md has no active entries")
		}
	}

	// Pre-push gate.
	prePush := filepath.Join(projectPath, ".git", "hooks", "pre-push")
	prePushHash := prePush + ".framework-hash"
	if _, err1 := os.Stat(prePush); err1 == nil {
		if _, err2 := os.Stat(prePushHash); err2 == nil {
			pok("pre-push version gate installed (yakOS-owned)")
		} else {
			pwarn("pre-push version gate not installed (run yakos git-hooks install)")
		}
	} else {
		pwarn("pre-push version gate not installed (run yakos git-hooks install)")
	}

	// Agent discipline.
	_, _ = fmt.Fprintln(r.w, "")
	_, _ = fmt.Fprintln(r.w, "Agent discipline:")
	agentsDir := filepath.Join(projectPath, ".claude", "agents")
	if _, err := os.Stat(agentsDir); os.IsNotExist(err) {
		pwarn("no .claude/agents/ in project (framework agents only)")
	} else {
		agentCount, missingTools, emptyTools := auditAgents(agentsDir)
		if agentCount == 0 {
			pwarn("no project agents in .claude/agents/ (using framework only)")
		} else {
			if missingTools == 0 {
				pok("%d project agent(s); all declare tools:", agentCount)
			} else {
				pwarn("%d/%d agents missing tools: declaration", missingTools, agentCount)
			}
			if emptyTools == 0 {
				pok("no agents with empty tools: []")
			} else {
				pfail("%d agent(s) have tools: [] (= unrestricted)", emptyTools)
			}
		}
	}

	// Budget + supervisor.
	_, _ = fmt.Fprintln(r.w, "")
	_, _ = fmt.Fprintln(r.w, "Budget + supervisor:")
	yakosYML := filepath.Join(projectPath, ".yakos.yml")
	if _, err := os.Stat(yakosYML); os.IsNotExist(err) {
		pfail(".yakos.yml missing (run yakos init)")
	} else {
		data, rerr := os.ReadFile(yakosYML) //nolint:gosec
		if rerr == nil {
			content := string(data)
			if hasBudgetBlock(content) {
				pok("budget block present in .yakos.yml")
			} else {
				pwarn("no budget block in .yakos.yml (no per-session caps)")
			}
			supEnabled, supBlockCritical := parseSupervisorBlock(content)
			if supEnabled {
				pok("supervisor.enabled: true")
				if supBlockCritical {
					pok("supervisor.block_on_critical: true (active mode)")
				} else {
					pwarn("supervisor in passive mode (block_on_critical: false) — fine for low-stakes, surface-only for production")
				}
			} else {
				pwarn("supervisor not enabled — production-ready posture should have it on")
			}
		}
	}

	_, _ = fmt.Fprintln(r.w, "")
	_, _ = fmt.Fprintf(r.w, "Production checklist: %d error(s), %d warning(s)\n", prodErrs, prodWarns)

	// Roll production errors/warnings into the top-level counts.
	r.report.Errors += prodErrs
	r.report.Warnings += prodWarns

	_, _ = fmt.Fprintln(r.w, "")
	_, _ = fmt.Fprintln(r.w, "Non-automated items (operator responsibility) — see")
	_, _ = fmt.Fprintln(r.w, "  lib/settings/harness-checklist.template.md sections under")
	_, _ = fmt.Fprintln(r.w, "  'Non-automated checks' for the full list.")
}
