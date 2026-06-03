// Package retro is the Go port of cli/lib/retro.sh (rank 25).
//
// It gives operators manual control over the 10-cycle retrospective cadence.
// The cycle-counter hook (lib/hooks/cycle-counter.sh) increments a counter
// on every UserPromptSubmit; at every Nth cycle it drops a .retro-due marker.
// The lead's rule:retrospective-discipline tells the lead to dispatch the
// `librarian` agent in response.
//
// # Synopsis
//
//	yakos retro now      # write .retro-due marker now (manual trigger)
//	yakos retro disable  # disable auto-trigger (counter still increments)
//	yakos retro enable   # re-enable auto-trigger
//	yakos retro status   # current state
//
// Note: `last` and `history` from the bash source are not wired in the Go
// port because they depend on cycle-counter.ndjson log parsing that is
// out of scope for Phase 1 (the bash passthrough covers them). The subcommands
// are accepted and handled gracefully.
//
// # State file
//
// The auto-dispatch disabled flag is stored as an empty sentinel file at
// ~/.yakos-state/retro-disabled. Present → disabled; absent → enabled.
// This matches the task spec (Q8 atomic writes: temp-rename on creation).
//
// # Dispatch injection
//
// `retro now` needs to write the .retro-due marker. Actual dispatch of the
// librarian is the lead's responsibility (rule:retrospective-discipline); the
// CLI only writes the marker. DispatchFn is an injectable hook used only in
// tests that need to assert marker behaviour.
package retro

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// defaultCycleLength is how many user prompts between auto-retros.
const defaultCycleLength = 10

// ---- public types -----------------------------------------------------------

// Config carries everything Run needs.
type Config struct {
	// Subcommand is one of: now, disable, enable, status, last, history, help.
	Subcommand string

	// HomeDir overrides os.Getenv("HOME") for path resolution.
	// When empty, os.Getenv("HOME") is used.
	HomeDir string

	// ProjectName overrides YAKOS_PROJECT_NAME env lookup.
	// When empty, the env var is consulted.
	ProjectName string

	// ProjectDir overrides the work/current path resolution.
	// When non-empty, work/current is derived from this directory.
	// Useful for tests.
	ProjectDir string

	// AgentControlRoot overrides ~/agent-control for tests.
	AgentControlRoot string

	// Writer receives normal output (default: os.Stdout).
	Writer io.Writer

	// ErrWriter receives advisory/warning messages (default: os.Stderr).
	ErrWriter io.Writer
}

// Result summarises what Run did.
type Result struct {
	// Subcommand is the subcommand that was executed.
	Subcommand string

	// AutoDispatch is the current auto-dispatch state after the operation.
	AutoDispatch bool

	// MarkerWritten is true when the .retro-due marker was written by `now`.
	MarkerWritten bool

	// MarkerPath is the absolute path of the .retro-due marker (may be "" when
	// no active session is detected).
	MarkerPath string

	// MarkerPresent reflects whether .retro-due exists at status time.
	MarkerPresent bool

	// CurrentCycle is the value read from .cycle-count (0 when unknown).
	CurrentCycle int

	// CycleLength is the configured cycle length (default 10).
	CycleLength int
}

// ---- entry point ------------------------------------------------------------

// Run executes the retro command according to cfg.
func Run(cfg Config) (*Result, error) {
	if cfg.Writer == nil {
		cfg.Writer = os.Stdout
	}
	if cfg.ErrWriter == nil {
		cfg.ErrWriter = os.Stderr
	}

	home := cfg.HomeDir
	if home == "" {
		home = os.Getenv("HOME")
	}

	stateDir := filepath.Join(home, ".yakos-state")
	disabledFlag := filepath.Join(stateDir, "retro-disabled")

	switch cfg.Subcommand {
	case "now":
		return runNow(cfg, home)
	case "disable":
		return runDisable(cfg, disabledFlag)
	case "enable":
		return runEnable(cfg, disabledFlag)
	case "status":
		return runStatus(cfg, home, disabledFlag)
	case "last":
		return runLast(cfg, home)
	case "history":
		return runHistory(cfg, home)
	default:
		return nil, fmt.Errorf("retro: unknown subcommand %q (try 'yakos retro help')", cfg.Subcommand)
	}
}

// PrintHelp writes the help text to w.
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos retro — manage 10-cycle retrospectives

  yakos retro now           # manual trigger; writes .retro-due marker
  yakos retro last          # show last retro's outputs from scratchpad
  yakos retro history       # cadence stats from cycle-counter logs
  yakos retro status        # current state
  yakos retro disable       # disable auto-trigger
  yakos retro enable        # re-enable auto-trigger

The 10-cycle cadence is enforced by lib/hooks/cycle-counter.sh
(UserPromptSubmit hook). Lead behavior on the marker is governed
by lib/rules/retrospective-discipline.md. On marker detection the
lead dispatches the librarian agent per rule:retrospective-discipline.
`)
}

// ---- subcommand implementations ---------------------------------------------

func runNow(cfg Config, home string) (*Result, error) {
	res := &Result{Subcommand: "now", CycleLength: defaultCycleLength}
	res.AutoDispatch = !isDisabled(disabledFlagPath(home))

	cur, err := resolveCurrentDir(cfg, home)
	if err != nil {
		return nil, err
	}
	if cur == "" {
		return nil, fmt.Errorf("retro: no active session detected")
	}

	markerPath := filepath.Join(cur, ".retro-due")
	if err := atomicTouch(markerPath); err != nil {
		return nil, fmt.Errorf("retro: writing .retro-due: %w", err)
	}

	res.MarkerWritten = true
	res.MarkerPath = markerPath
	res.MarkerPresent = true

	_, _ = fmt.Fprintf(cfg.Writer, "retro: marker written at %s\n", markerPath)
	_, _ = fmt.Fprintln(cfg.Writer, "      lead should detect the marker and dispatch 'librarian' on next action")
	return res, nil
}

func runDisable(cfg Config, disabledFlag string) (*Result, error) {
	res := &Result{Subcommand: "disable", CycleLength: defaultCycleLength, AutoDispatch: false}

	if err := os.MkdirAll(filepath.Dir(disabledFlag), 0755); err != nil { //nolint:gosec
		return nil, fmt.Errorf("retro: mkdir state dir: %w", err)
	}
	if err := atomicTouch(disabledFlag); err != nil {
		return nil, fmt.Errorf("retro: writing disabled flag: %w", err)
	}

	_, _ = fmt.Fprintln(cfg.Writer, "retro: auto-dispatch DISABLED (cycle counter still increments)")
	return res, nil
}

func runEnable(cfg Config, disabledFlag string) (*Result, error) {
	res := &Result{Subcommand: "enable", CycleLength: defaultCycleLength, AutoDispatch: true}

	// Remove the flag file — absence means enabled.
	if err := os.Remove(disabledFlag); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("retro: removing disabled flag: %w", err)
	}

	_, _ = fmt.Fprintln(cfg.Writer, "retro: auto-dispatch ENABLED")
	return res, nil
}

func runStatus(cfg Config, home, disabledFlag string) (*Result, error) {
	res := &Result{
		Subcommand:   "status",
		CycleLength:  defaultCycleLength,
		AutoDispatch: !isDisabled(disabledFlag),
	}

	autoStr := "true"
	if !res.AutoDispatch {
		autoStr = "false"
	}

	cur, _ := resolveCurrentDir(cfg, home)
	if cur != "" {
		slug := activeSlug(cfg, home)
		cycle, _ := readCycleCount(cur)
		res.CurrentCycle = cycle

		markerPath := filepath.Join(cur, ".retro-due")
		markerPresent := fileExists(markerPath)
		res.MarkerPresent = markerPresent
		res.MarkerPath = markerPath

		markerStr := "absent"
		if markerPresent {
			markerStr = "PRESENT (lead will dispatch librarian)"
		}

		_, _ = fmt.Fprintf(cfg.Writer,
			"Retro status:\n  Active project session: %s\n  Auto-dispatch:          %s\n  Cycle length:           %d prompts\n  Current cycle:          %d\n  .retro-due marker:      %s\n",
			slug, autoStr, defaultCycleLength, cycle, markerStr,
		)
	} else {
		_, _ = fmt.Fprintf(cfg.Writer,
			"Retro status:\n  Active project session: (none detected)\n  Auto-dispatch:          %s\n  Cycle length:           %d prompts\n",
			autoStr, defaultCycleLength,
		)
	}

	return res, nil
}

func runLast(cfg Config, home string) (*Result, error) {
	res := &Result{Subcommand: "last", CycleLength: defaultCycleLength}
	res.AutoDispatch = !isDisabled(disabledFlagPath(home))

	cur, err := resolveCurrentDir(cfg, home)
	if err != nil {
		return nil, err
	}
	if cur == "" {
		return nil, fmt.Errorf("retro: no active session detected")
	}

	outputs := []string{
		"lessons.md",
		"mistakes.md",
		"skill-candidates.md",
		"drift-report.md",
		"soul-proposed-edits.md",
	}

	for _, f := range outputs {
		path := filepath.Join(cur, f)
		_, _ = fmt.Fprintf(cfg.Writer, "=== %s ===\n", f)
		data, err := os.ReadFile(path) //nolint:gosec
		if os.IsNotExist(err) {
			_, _ = fmt.Fprintln(cfg.Writer, "(none yet)")
		} else if err != nil {
			_, _ = fmt.Fprintf(cfg.Writer, "(error reading: %v)\n", err)
		} else {
			lines := strings.Split(string(data), "\n")
			// Match bash tail -40 behaviour.
			start := 0
			if len(lines) > 40 {
				start = len(lines) - 40
			}
			_, _ = fmt.Fprintln(cfg.Writer, strings.Join(lines[start:], "\n"))
		}
		_, _ = fmt.Fprintln(cfg.Writer, "")
	}

	return res, nil
}

func runHistory(cfg Config, home string) (*Result, error) {
	res := &Result{Subcommand: "history", CycleLength: defaultCycleLength}
	res.AutoDispatch = !isDisabled(disabledFlagPath(home))

	cur, err := resolveCurrentDir(cfg, home)
	if err != nil {
		return nil, err
	}
	if cur == "" {
		return nil, fmt.Errorf("retro: no active session detected")
	}

	logPath := filepath.Join(cur, "logs", "cycle-counter.ndjson")
	if !fileExists(logPath) {
		_, _ = fmt.Fprintln(cfg.Writer, "(no cycle-counter log yet)")
		return res, nil
	}

	data, err := os.ReadFile(logPath) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("retro: reading cycle log: %w", err)
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	total := 0
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			total++
		}
	}

	// Count retro_due==true entries (simple JSON field scan without full parse).
	retroCount := 0
	for _, l := range lines {
		if strings.Contains(l, `"retro_due":true`) || strings.Contains(l, `"retro_due": true`) {
			retroCount++
		}
	}

	cycle, _ := readCycleCount(cur)
	res.CurrentCycle = cycle
	nextRetro := ((cycle / defaultCycleLength) + 1) * defaultCycleLength

	_, _ = fmt.Fprintf(cfg.Writer,
		"Retro cadence stats (this session):\n  Current cycle:    %d\n  Cycle length:     %d\n  Next retro at:    cycle %d\n  Retros triggered: %d\n  Total prompts:    %d\n",
		cycle, defaultCycleLength, nextRetro, retroCount, total,
	)

	return res, nil
}

// ---- helpers ----------------------------------------------------------------

// resolveCurrentDir returns ~/agent-control/<slug>/work/current for the
// active session, or "" when no session is detected.
func resolveCurrentDir(cfg Config, home string) (string, error) {
	// Direct override for tests.
	if cfg.ProjectDir != "" {
		return cfg.ProjectDir, nil
	}

	slug := activeSlug(cfg, home)
	if slug == "" {
		return "", nil
	}

	acRoot := agentControlRoot(cfg, home)
	return filepath.Join(acRoot, slug, "work", "current"), nil
}

// activeSlug returns the project slug for the active session.
func activeSlug(cfg Config, home string) string {
	// Config override.
	if cfg.ProjectName != "" {
		return cfg.ProjectName
	}
	// Environment variable.
	if v := os.Getenv("YAKOS_PROJECT_NAME"); v != "" {
		return v
	}

	// Walk ~/agent-control/*/project-path files (same logic as bash).
	acRoot := agentControlRoot(cfg, home)
	entries, err := os.ReadDir(acRoot)
	if err != nil {
		return ""
	}

	cwd, _ := os.Getwd()
	if r, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = r
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
		if p == "" {
			continue
		}
		pr, err := filepath.EvalSymlinks(p)
		if err != nil {
			pr = p
		}
		if cwd == pr || strings.HasPrefix(cwd, pr+string(filepath.Separator)) {
			return e.Name()
		}
	}

	return ""
}

// agentControlRoot returns the agent-control directory root.
func agentControlRoot(cfg Config, home string) string {
	if cfg.AgentControlRoot != "" {
		return cfg.AgentControlRoot
	}
	return filepath.Join(home, "agent-control")
}

// disabledFlagPath returns the path to the retro-disabled sentinel file.
func disabledFlagPath(home string) string {
	return filepath.Join(home, ".yakos-state", "retro-disabled")
}

// isDisabled returns true when the retro-disabled sentinel file exists.
func isDisabled(flagPath string) bool {
	return fileExists(flagPath)
}

// fileExists returns true when path exists (any file type).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// readCycleCount reads .cycle-count from the current dir. Returns 0 on error.
func readCycleCount(currentDir string) (int, error) {
	data, err := os.ReadFile(filepath.Join(currentDir, ".cycle-count")) //nolint:gosec
	if err != nil {
		return 0, nil //nolint:nilerr // missing file is normal; return default
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, nil //nolint:nilerr // unparseable is treated as 0
	}
	return n, nil
}

// atomicTouch creates or truncates path using temp-rename (Q8 / Decision A).
// The file is created as an empty sentinel.
func atomicTouch(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil { //nolint:gosec
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".yakos-retro-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()

	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temp: %w", err)
	}
	return nil
}
