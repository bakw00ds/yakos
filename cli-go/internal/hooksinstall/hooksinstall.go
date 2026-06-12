// Package hooksinstall implements `yakos hooks` — install/status for
// translatable yakOS hook scripts into runtime-native hook configurations.
//
// Decision Q9: hook BODIES remain bash (Phase 3 if ever). This port only
// manages SYMLINK/config deployment that tells runtimes where to find the
// existing bash hook scripts that live under <project>/scripts/hooks/.
//
// Supported runtimes: codex, gemini, agy.
// "claude" is yakOS's source-of-truth runtime and needs no translation.
//
// Hook translation table (matches bash HOOK_PORTS):
//
//	path-allowlist  PreToolUse  PreToolUse  BeforeTool  Edit Write
//	secret-scan     PreToolUse  PreToolUse  BeforeTool  Edit Write apply_patch
//
// Output files:
//
//	codex:  <project>/.codex/hooks.json          (new on install)
//	gemini: <project>/.gemini/settings.json      (merged .hooks block)
//	agy:    <project>/.agents/hooks.json         (new on install)
package hooksinstall

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// hookPort describes a translatable yakOS hook.
type hookPort struct {
	scriptName string   // e.g. "path-allowlist" (without .sh)
	claudeEvt  string   // e.g. "PreToolUse"
	codexEvt   string   // e.g. "PreToolUse"
	geminiEvt  string   // e.g. "BeforeTool"
	matchers   []string // tool names to match, e.g. ["Edit","Write"]
}

// hookPorts is the canonical translation table (matches bash HOOK_PORTS).
var hookPorts = []hookPort{
	{
		scriptName: "path-allowlist",
		claudeEvt:  "PreToolUse",
		codexEvt:   "PreToolUse",
		geminiEvt:  "BeforeTool",
		matchers:   []string{"Edit", "Write"},
	},
	{
		scriptName: "secret-scan",
		claudeEvt:  "PreToolUse",
		codexEvt:   "PreToolUse",
		geminiEvt:  "BeforeTool",
		matchers:   []string{"Edit", "Write", "apply_patch"},
	},
}

// KnownRuntimes is the set of runtime IDs accepted by Run.
var KnownRuntimes = []string{"claude", "codex", "gemini", "agy"}

// Config holds the options for a hooks install/status invocation.
type Config struct {
	// Subcommand is "install" or "status".
	Subcommand string

	// Runtime is the target runtime: "claude", "codex", "gemini", or "agy".
	// Required for "install"; ignored for "status".
	Runtime string

	// ProjectDir is the absolute path to the project root.
	// Required for "install"; optional for "status" (see StatusAllProjects).
	ProjectDir string

	// Force, when true, skips the backup of existing config files on install.
	Force bool

	// Writer receives informational log output.
	Writer io.Writer

	// ErrWriter receives error/warning output.
	ErrWriter io.Writer

	// NowFn returns the current UTC time string (ISO 8601) for backup filenames.
	// Injected for tests. When nil, uses time.Now().UTC().
	NowFn func() string
}

// Result summarises what install/status did.
type Result struct {
	// HooksWritten is the number of hook entries written into the config file.
	HooksWritten int

	// SkippedMissing is the number of hook scripts not found under scripts/hooks/.
	SkippedMissing int

	// Runtime is the runtime that was targeted.
	Runtime string

	// ConfigFile is the path to the generated/updated config file.
	ConfigFile string
}

// Run executes the hooks subcommand according to cfg.
func Run(cfg Config) (Result, error) {
	if cfg.Writer == nil {
		cfg.Writer = io.Discard
	}
	if cfg.ErrWriter == nil {
		cfg.ErrWriter = io.Discard
	}
	if cfg.NowFn == nil {
		cfg.NowFn = func() string { return time.Now().UTC().Format("20060102T150405Z") }
	}

	switch cfg.Subcommand {
	case "install":
		return runInstall(cfg)
	case "status":
		return runStatus(cfg)
	default:
		return Result{}, fmt.Errorf("hooks: unknown subcommand %q (install | status)", cfg.Subcommand)
	}
}

// IsKnownRuntime returns true if rt is in KnownRuntimes.
func IsKnownRuntime(rt string) bool {
	for _, r := range KnownRuntimes {
		if r == rt {
			return true
		}
	}
	return false
}

// PrintHelp writes the hooks usage text to w.
func PrintHelp(w io.Writer) {
	fmt.Fprintln(w, `yakos hooks <subcommand> [args...]

Subcommands:
  install <runtime> --project <path>   Translate yakOS hooks to the
                                       runtime's native config.
                                       Supported: codex, gemini, agy.
  status [<project>]                   Report which hooks are installed
                                       per-runtime for the project.
  lint [--hooks-dir <path>]            Lint .star Starlark hook files
                                       in the given directory (Phase 3).

Hooks translated for codex / gemini / agy in v0.5:
  - path-allowlist (PreToolUse / BeforeTool)
  - secret-scan    (PreToolUse / BeforeTool)

NOT translated (yakOS-specific events or claude-only primitives):
  - mailbox-mirror, team-lifecycle, task-complete-dispatch,
    task-dependency-gate, session-end-check, path-log

The project's <repo>/scripts/hooks/ (copied at 'yakos init' time)
remains the source-of-truth for hook content. This subcommand only
writes the runtime config that points at those scripts.

Decision Q9: hook bodies remain bash (Phase 3). This port manages
script deployment only.

Examples:
  yakos hooks install codex --project ~/code/myapp
  yakos hooks status ~/code/myapp
  yakos hooks lint --hooks-dir lib/hooks/`)
}

// ---- install ----------------------------------------------------------------

func runInstall(cfg Config) (Result, error) {
	if cfg.Runtime == "" {
		return Result{}, fmt.Errorf("hooks install: runtime required")
	}
	if !IsKnownRuntime(cfg.Runtime) {
		return Result{}, fmt.Errorf("hooks install: unknown runtime %q", cfg.Runtime)
	}
	if cfg.ProjectDir == "" {
		return Result{}, fmt.Errorf("hooks install: --project required")
	}

	// claude is the source-of-truth runtime; no translation needed.
	if cfg.Runtime == "claude" {
		fmt.Fprintln(cfg.Writer,
			"hooks install claude: claude is yakOS's source-of-truth runtime.\n"+
				"Hooks live at <project>/scripts/hooks/ and are wired via\n"+
				"<project>/.claude/settings.json on init. Nothing to translate.")
		return Result{Runtime: "claude"}, nil
	}

	switch cfg.Runtime {
	case "codex":
		return installCodex(cfg)
	case "gemini":
		return installGemini(cfg)
	case "agy":
		return installAgy(cfg)
	default:
		return Result{}, fmt.Errorf("hooks install: unhandled runtime %q", cfg.Runtime)
	}
}

// matcherRegex builds "^(Edit|Write|apply_patch)$" from the matcher list.
func matcherRegex(matchers []string) string {
	return "^(" + strings.Join(matchers, "|") + ")$"
}

// hookScriptPath returns the absolute path to a hook script.
// Returns ("", false) if the script doesn't exist or isn't considered executable.
// The executable check is platform-aware: see executable_unix.go / executable_windows.go.
func hookScriptPath(hooksDir, scriptName string) (string, bool) {
	p := filepath.Join(hooksDir, scriptName+".sh")
	fi, err := os.Stat(p)
	if err != nil {
		return "", false
	}
	return p, isExecutable(fi)
}

// ---- codex ------------------------------------------------------------------

// codexHooksEntry is one entry in the codex hooks config event array.
type codexHooksEntry struct {
	Matcher string `json:"matcher"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

// codexHooksFile is the shape of .codex/hooks.json.
type codexHooksFile struct {
	Hooks map[string][]codexHooksEntry `json:"hooks"`
}

func installCodex(cfg Config) (Result, error) {
	hooksDir := filepath.Join(cfg.ProjectDir, "scripts", "hooks")
	if _, err := os.Stat(hooksDir); os.IsNotExist(err) {
		return Result{}, fmt.Errorf("hooks install codex: %s missing — run 'yakos init' first", hooksDir)
	}

	target := filepath.Join(cfg.ProjectDir, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil { //nolint:gosec
		return Result{}, fmt.Errorf("hooks install codex: mkdir .codex: %w", err)
	}

	// Backup existing file unless --force.
	if !cfg.Force {
		if err := backupIfExists(target, cfg.NowFn()); err != nil {
			fmt.Fprintf(cfg.ErrWriter, "hooks install codex: backup warning: %v\n", err)
		}
	}

	// Also translate path-allowlist.json into codex permissions (belt-and-suspenders).
	if err := installCodexPermissions(cfg.ProjectDir, cfg.Writer, cfg.ErrWriter); err != nil {
		fmt.Fprintf(cfg.ErrWriter, "hooks install codex: permissions note: %v\n", err)
	}

	// Build entries.
	hooksMap := map[string][]codexHooksEntry{}
	written := 0
	skipped := 0
	for _, hp := range hookPorts {
		scriptPath, ok := hookScriptPath(hooksDir, hp.scriptName)
		if !ok {
			fmt.Fprintf(cfg.ErrWriter,
				"hooks install codex: %s.sh not found or not executable; skipping\n", hp.scriptName)
			skipped++
			continue
		}
		entry := codexHooksEntry{
			Matcher: matcherRegex(hp.matchers),
			Command: scriptPath,
			Timeout: 30,
		}
		hooksMap[hp.codexEvt] = append(hooksMap[hp.codexEvt], entry)
		written++
	}

	data, err := json.MarshalIndent(codexHooksFile{Hooks: hooksMap}, "", "  ")
	if err != nil {
		return Result{}, fmt.Errorf("hooks install codex: marshal: %w", err)
	}
	data = append(data, '\n')
	if err := atomicWriteFile(target, data, 0644); err != nil {
		return Result{}, fmt.Errorf("hooks install codex: write %s: %w", target, err)
	}

	fmt.Fprintf(cfg.Writer, "wrote codex hooks config: %s (%d entries)\n", target, written)
	return Result{
		HooksWritten:   written,
		SkippedMissing: skipped,
		Runtime:        "codex",
		ConfigFile:     target,
	}, nil
}

// installCodexPermissions translates .claude/path-allowlist.json allow/deny into
// a [permissions.yakos-paths] block in .codex/config.toml.
func installCodexPermissions(projectDir string, w, ew io.Writer) error {
	src := filepath.Join(projectDir, ".claude", "path-allowlist.json")
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return nil // nothing to translate
	}

	type allowlist struct {
		Allow []string `json:"allow"`
		Deny  []string `json:"deny"`
	}
	raw, err := os.ReadFile(src) //nolint:gosec
	if err != nil {
		return fmt.Errorf("read path-allowlist.json: %w", err)
	}
	var al allowlist
	if err := json.Unmarshal(raw, &al); err != nil {
		return fmt.Errorf("parse path-allowlist.json: %w", err)
	}

	cfg := filepath.Join(projectDir, ".codex", "config.toml")
	if _, err := os.Stat(cfg); os.IsNotExist(err) {
		if err := os.WriteFile(cfg, []byte("# yakOS managed: codex configuration\n"), 0644); err != nil { //nolint:gosec
			return fmt.Errorf("create config.toml: %w", err)
		}
	}

	// Read current config, strip prior yakos-permissions block.
	existing, _ := os.ReadFile(cfg) //nolint:gosec
	stripped := stripYakosPermissionsBlock(string(existing))

	var block strings.Builder
	block.WriteString(stripped)
	block.WriteString("\n# yakos-permissions-start\n")
	block.WriteString("[permissions.yakos-paths]\n")
	if len(al.Allow) > 0 {
		block.WriteString("filesystem.allow_glob = [")
		for i, a := range al.Allow {
			if i > 0 {
				block.WriteString(", ")
			}
			fmt.Fprintf(&block, "%q", a)
		}
		block.WriteString("]\n")
	}
	if len(al.Deny) > 0 {
		block.WriteString("filesystem.deny_glob = [")
		for i, d := range al.Deny {
			if i > 0 {
				block.WriteString(", ")
			}
			fmt.Fprintf(&block, "%q", d)
		}
		block.WriteString("]\n")
	}
	block.WriteString("# yakos-permissions-end\n")

	if err := atomicWriteFile(cfg, []byte(block.String()), 0644); err != nil {
		return fmt.Errorf("write config.toml: %w", err)
	}
	fmt.Fprintf(w, "translated path-allowlist.json into %s [permissions.yakos-paths]\n", cfg)
	return nil
}

// stripYakosPermissionsBlock removes the # yakos-permissions-start … end block.
func stripYakosPermissionsBlock(content string) string {
	var out strings.Builder
	skip := false
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == "# yakos-permissions-start" {
			skip = true
			continue
		}
		if strings.TrimSpace(line) == "# yakos-permissions-end" {
			skip = false
			continue
		}
		if !skip {
			out.WriteString(line)
			out.WriteByte('\n')
		}
	}
	// Remove trailing extra newlines that accumulate across idempotent runs.
	return strings.TrimRight(out.String(), "\n") + "\n"
}

// ---- gemini -----------------------------------------------------------------

// geminiHookEntry is one matcher entry in the gemini hooks block.
type geminiHookEntry struct {
	Matcher string            `json:"matcher"`
	Hooks   []geminiInnerHook `json:"hooks"`
}

type geminiInnerHook struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

func installGemini(cfg Config) (Result, error) {
	hooksDir := filepath.Join(cfg.ProjectDir, "scripts", "hooks")
	if _, err := os.Stat(hooksDir); os.IsNotExist(err) {
		return Result{}, fmt.Errorf("hooks install gemini: %s missing — run 'yakos init' first", hooksDir)
	}

	target := filepath.Join(cfg.ProjectDir, ".gemini", "settings.json")
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil { //nolint:gosec
		return Result{}, fmt.Errorf("hooks install gemini: mkdir .gemini: %w", err)
	}

	// Load or seed the existing settings.json.
	existing := map[string]interface{}{}
	if raw, err := os.ReadFile(target); err == nil { //nolint:gosec
		_ = json.Unmarshal(raw, &existing)
	}

	if !cfg.Force {
		if err := backupIfExists(target, cfg.NowFn()); err != nil {
			fmt.Fprintf(cfg.ErrWriter, "hooks install gemini: backup warning: %v\n", err)
		}
	}

	hooksBlock := map[string][]geminiHookEntry{}
	written := 0
	skipped := 0

	for _, hp := range hookPorts {
		scriptPath, ok := hookScriptPath(hooksDir, hp.scriptName)
		if !ok {
			fmt.Fprintf(cfg.ErrWriter,
				"hooks install gemini: %s.sh not found; skipping\n", hp.scriptName)
			skipped++
			continue
		}
		entry := geminiHookEntry{
			Matcher: matcherRegex(hp.matchers),
			Hooks: []geminiInnerHook{
				{
					Name:    "yakos-" + hp.scriptName,
					Type:    "command",
					Command: scriptPath,
					Timeout: 30000,
				},
			},
		}
		hooksBlock[hp.geminiEvt] = append(hooksBlock[hp.geminiEvt], entry)
		written++
	}

	// Merge into existing settings (preserving unrelated keys).
	existing["hooks"] = mergeGeminiHooks(getExistingGeminiHooks(existing), hooksBlock)

	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return Result{}, fmt.Errorf("hooks install gemini: marshal: %w", err)
	}
	data = append(data, '\n')
	if err := atomicWriteFile(target, data, 0644); err != nil {
		return Result{}, fmt.Errorf("hooks install gemini: write %s: %w", target, err)
	}

	fmt.Fprintf(cfg.Writer, "wrote gemini hooks into: %s (%d entries)\n", target, written)
	return Result{
		HooksWritten:   written,
		SkippedMissing: skipped,
		Runtime:        "gemini",
		ConfigFile:     target,
	}, nil
}

func getExistingGeminiHooks(settings map[string]interface{}) map[string][]geminiHookEntry {
	existing := map[string][]geminiHookEntry{}
	hooksRaw, _ := settings["hooks"].(map[string]interface{})
	for evt, entries := range hooksRaw {
		arr, _ := entries.([]interface{})
		for _, item := range arr {
			m, _ := item.(map[string]interface{})
			if m == nil {
				continue
			}
			// Re-encode item to geminiHookEntry.
			b, _ := json.Marshal(m)
			var e geminiHookEntry
			if json.Unmarshal(b, &e) == nil {
				existing[evt] = append(existing[evt], e)
			}
		}
	}
	return existing
}

func mergeGeminiHooks(existing, incoming map[string][]geminiHookEntry) map[string][]geminiHookEntry {
	out := map[string][]geminiHookEntry{}
	for k, v := range existing {
		out[k] = append(out[k], v...)
	}
	for k, v := range incoming {
		out[k] = append(out[k], v...)
	}
	return out
}

// ---- agy --------------------------------------------------------------------
// Per Antigravity migration guide, agy hooks share Gemini's JSON shape but
// go into <project>/.agents/hooks.json (top-level object) instead of
// settings.json .hooks block.

type agyHooksFile struct {
	Hooks map[string][]geminiHookEntry `json:"hooks"`
}

func installAgy(cfg Config) (Result, error) {
	hooksDir := filepath.Join(cfg.ProjectDir, "scripts", "hooks")
	if _, err := os.Stat(hooksDir); os.IsNotExist(err) {
		return Result{}, fmt.Errorf("hooks install agy: %s missing — run 'yakos init' first", hooksDir)
	}

	target := filepath.Join(cfg.ProjectDir, ".agents", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil { //nolint:gosec
		return Result{}, fmt.Errorf("hooks install agy: mkdir .agents: %w", err)
	}

	// Load or seed.
	existing := agyHooksFile{Hooks: map[string][]geminiHookEntry{}}
	if raw, err := os.ReadFile(target); err == nil { //nolint:gosec
		_ = json.Unmarshal(raw, &existing)
	}

	if !cfg.Force {
		if err := backupIfExists(target, cfg.NowFn()); err != nil {
			fmt.Fprintf(cfg.ErrWriter, "hooks install agy: backup warning: %v\n", err)
		}
	}

	written := 0
	skipped := 0
	newHooks := map[string][]geminiHookEntry{}

	for _, hp := range hookPorts {
		scriptPath, ok := hookScriptPath(hooksDir, hp.scriptName)
		if !ok {
			fmt.Fprintf(cfg.ErrWriter,
				"hooks install agy: %s.sh not found; skipping\n", hp.scriptName)
			skipped++
			continue
		}
		entry := geminiHookEntry{
			Matcher: matcherRegex(hp.matchers),
			Hooks: []geminiInnerHook{
				{
					Name:    "yakos-" + hp.scriptName,
					Type:    "command",
					Command: scriptPath,
					Timeout: 30000,
				},
			},
		}
		newHooks[hp.geminiEvt] = append(newHooks[hp.geminiEvt], entry)
		written++
	}

	merged := mergeGeminiHooks(existing.Hooks, newHooks)
	out := agyHooksFile{Hooks: merged}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return Result{}, fmt.Errorf("hooks install agy: marshal: %w", err)
	}
	data = append(data, '\n')
	if err := atomicWriteFile(target, data, 0644); err != nil {
		return Result{}, fmt.Errorf("hooks install agy: write %s: %w", target, err)
	}

	fmt.Fprintf(cfg.Writer, "wrote agy hooks into: %s (%d entries)\n", target, written)
	return Result{
		HooksWritten:   written,
		SkippedMissing: skipped,
		Runtime:        "agy",
		ConfigFile:     target,
	}, nil
}

// ---- status -----------------------------------------------------------------

// StatusReport describes the hooks status for a project.
type StatusReport struct {
	ProjectDir    string
	ClaudeEntries int
	ClaudePresent bool
	CodexEntries  int
	CodexPresent  bool
	GeminiEntries int
	GeminiPresent bool
}

func runStatus(cfg Config) (Result, error) {
	project := cfg.ProjectDir
	if project == "" {
		return Result{}, fmt.Errorf("hooks status: --project required (or run from inside a project)")
	}

	rpt := StatusReport{ProjectDir: project}

	// Claude: count entries in .claude/settings.json .hooks.
	claudeSettings := filepath.Join(project, ".claude", "settings.json")
	if raw, err := os.ReadFile(claudeSettings); err == nil { //nolint:gosec
		var settings map[string]interface{}
		if json.Unmarshal(raw, &settings) == nil {
			rpt.ClaudePresent = true
			if hooks, ok := settings["hooks"].(map[string]interface{}); ok {
				for _, v := range hooks {
					if arr, ok := v.([]interface{}); ok {
						rpt.ClaudeEntries += len(arr)
					}
				}
			}
		}
	}

	// Codex: count entries in .codex/hooks.json.
	codexHooks := filepath.Join(project, ".codex", "hooks.json")
	if raw, err := os.ReadFile(codexHooks); err == nil { //nolint:gosec
		var f codexHooksFile
		if json.Unmarshal(raw, &f) == nil {
			rpt.CodexPresent = true
			for _, arr := range f.Hooks {
				rpt.CodexEntries += len(arr)
			}
		}
	}

	// Gemini: count entries in .gemini/settings.json .hooks.
	geminiSettings := filepath.Join(project, ".gemini", "settings.json")
	if raw, err := os.ReadFile(geminiSettings); err == nil { //nolint:gosec
		var settings map[string]interface{}
		if json.Unmarshal(raw, &settings) == nil {
			if hooks, ok := settings["hooks"].(map[string]interface{}); ok {
				rpt.GeminiPresent = true
				for _, v := range hooks {
					if arr, ok := v.([]interface{}); ok {
						rpt.GeminiEntries += len(arr)
					}
				}
			}
		}
	}

	PrintStatus(cfg.Writer, rpt)
	return Result{}, nil
}

// PrintStatus writes the formatted status report to w.
func PrintStatus(w io.Writer, rpt StatusReport) {
	fmt.Fprintf(w, "yakos hooks status — %s\n\n", rpt.ProjectDir)
	fmt.Fprintln(w, "  claude:")
	if rpt.ClaudePresent {
		fmt.Fprintf(w, "    settings.json present: %d hook entries\n", rpt.ClaudeEntries)
	} else {
		fmt.Fprintln(w, "    settings.json absent (run 'yakos init')")
	}
	fmt.Fprintln(w, "  codex:")
	if rpt.CodexPresent {
		fmt.Fprintf(w, "    hooks.json present: %d hook entries\n", rpt.CodexEntries)
	} else {
		fmt.Fprintf(w, "    hooks.json absent (run 'yakos hooks install codex --project %s')\n", rpt.ProjectDir)
	}
	fmt.Fprintln(w, "  gemini:")
	if rpt.GeminiPresent {
		fmt.Fprintf(w, "    settings.json present: %d hook entries\n", rpt.GeminiEntries)
	} else {
		fmt.Fprintf(w, "    settings.json absent (run 'yakos hooks install gemini --project %s')\n", rpt.ProjectDir)
	}
}

// ---- utilities --------------------------------------------------------------

// backupIfExists creates <path>.yakos-bak-<timestamp> if path exists.
func backupIfExists(path, ts string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	bak := path + ".yakos-bak-" + ts
	raw, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return fmt.Errorf("backup read %s: %w", path, err)
	}
	return os.WriteFile(bak, raw, 0644) //nolint:gosec
}

// atomicWriteFile writes data to path using a temp-rename (Decision Q8).
func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil { //nolint:gosec
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
