package metrics

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/bakw00ds/yakos/internal/stackdetect"
)

// Config carries everything Run needs.
type Config struct {
	// Subcommand is one of: collect, report, trend, compare, serve, gate,
	// install-hook.
	Subcommand string

	// Trigger is the collect trigger label. One of: manual, git-hook, ci, release.
	Trigger string

	// NoWrite suppresses writing to history.ndjson (--no-write).
	NoWrite bool

	// SkipAnalyzers suppresses [T] tool invocations (--skip-analyzers).
	SkipAnalyzers bool

	// EmitJSON makes formatters emit JSON.
	EmitJSON bool

	// MetricPath is the dot-path for `trend --metric`.
	MetricPath string

	// LastN is the --last N limit for trend.
	LastN int

	// SinceTS is the --since timestamp for trend / collect.
	SinceTS string

	// ShaA and ShaB are the two commit SHAs for compare.
	ShaA, ShaB string

	// ProjectDir overrides project directory resolution.
	ProjectDir string

	// HomeDir overrides os.Getenv("HOME").
	HomeDir string

	// StateDir overrides the yakos state directory.
	StateDir string

	// Now overrides the current time (for tests).
	Now func() time.Time

	// GitRunner overrides git execution (for tests).
	GitRunner gitRunner

	// Writer receives normal output.
	Writer io.Writer

	// ErrWriter receives warning/advisory messages.
	ErrWriter io.Writer
}

// Result summarises what Run did.
type Result struct {
	Subcommand string
	Snapshot   *Snapshot
}

// ParseArgs parses os.Args-style arguments for `yakos metrics <args...>`
// and returns a populated Config. It does not call os.Exit.
func ParseArgs(args []string, homeDir string) (Config, error) {
	cfg := Config{HomeDir: homeDir, Trigger: "manual", LastN: 10, MetricPath: "efficiency.total_cost_usd"}

	if len(args) == 0 {
		cfg.Subcommand = "help"
		return cfg, nil
	}

	cfg.Subcommand = args[0]
	rest := args[1:]

	switch cfg.Subcommand {
	case "collect":
		for i := 0; i < len(rest); i++ {
			switch {
			case rest[i] == "--trigger":
				i++
				if i >= len(rest) {
					return cfg, fmt.Errorf("metrics collect: --trigger requires a value")
				}
				cfg.Trigger = rest[i]
			case hasPrefix(rest[i], "--trigger="):
				cfg.Trigger = rest[i][len("--trigger="):]
			case rest[i] == "--no-write":
				cfg.NoWrite = true
			case rest[i] == "--skip-analyzers":
				cfg.SkipAnalyzers = true
			case rest[i] == "--json":
				cfg.EmitJSON = true
			case rest[i] == "--since":
				i++
				if i >= len(rest) {
					return cfg, fmt.Errorf("metrics collect: --since requires a value")
				}
				cfg.SinceTS = rest[i]
			case hasPrefix(rest[i], "--since="):
				cfg.SinceTS = rest[i][len("--since="):]
			case rest[i] == "--project":
				i++
				if i >= len(rest) {
					return cfg, fmt.Errorf("metrics collect: --project requires a value")
				}
				cfg.ProjectDir = rest[i]
			case hasPrefix(rest[i], "--project="):
				cfg.ProjectDir = rest[i][len("--project="):]
			default:
				return cfg, fmt.Errorf("metrics collect: unknown flag %q", rest[i])
			}
		}

	case "report":
		for i := 0; i < len(rest); i++ {
			switch {
			case rest[i] == "--json":
				cfg.EmitJSON = true
			case rest[i] == "--project":
				i++
				if i >= len(rest) {
					return cfg, fmt.Errorf("metrics report: --project requires a value")
				}
				cfg.ProjectDir = rest[i]
			case hasPrefix(rest[i], "--project="):
				cfg.ProjectDir = rest[i][len("--project="):]
			default:
				return cfg, fmt.Errorf("metrics report: unknown flag %q", rest[i])
			}
		}

	case "trend":
		for i := 0; i < len(rest); i++ {
			switch {
			case rest[i] == "--metric":
				i++
				if i >= len(rest) {
					return cfg, fmt.Errorf("metrics trend: --metric requires a value")
				}
				cfg.MetricPath = rest[i]
			case hasPrefix(rest[i], "--metric="):
				cfg.MetricPath = rest[i][len("--metric="):]
			case rest[i] == "--last":
				i++
				if i >= len(rest) {
					return cfg, fmt.Errorf("metrics trend: --last requires a value")
				}
				n := 0
				for _, ch := range rest[i] {
					if ch < '0' || ch > '9' {
						return cfg, fmt.Errorf("metrics trend: --last must be a number")
					}
					n = n*10 + int(ch-'0')
				}
				cfg.LastN = n
			case hasPrefix(rest[i], "--last="):
				val := rest[i][len("--last="):]
				n := 0
				for _, ch := range val {
					if ch < '0' || ch > '9' {
						return cfg, fmt.Errorf("metrics trend: --last must be a number")
					}
					n = n*10 + int(ch-'0')
				}
				cfg.LastN = n
			case rest[i] == "--since":
				i++
				if i >= len(rest) {
					return cfg, fmt.Errorf("metrics trend: --since requires a value")
				}
				cfg.SinceTS = rest[i]
			case hasPrefix(rest[i], "--since="):
				cfg.SinceTS = rest[i][len("--since="):]
			case rest[i] == "--project":
				i++
				if i >= len(rest) {
					return cfg, fmt.Errorf("metrics trend: --project requires a value")
				}
				cfg.ProjectDir = rest[i]
			case hasPrefix(rest[i], "--project="):
				cfg.ProjectDir = rest[i][len("--project="):]
			default:
				return cfg, fmt.Errorf("metrics trend: unknown flag %q", rest[i])
			}
		}

	case "compare":
		positional := 0
		for i := 0; i < len(rest); i++ {
			if rest[i] == "--json" {
				cfg.EmitJSON = true
				continue
			}
			if rest[i] == "--project" {
				i++
				if i >= len(rest) {
					return cfg, fmt.Errorf("metrics compare: --project requires a value")
				}
				cfg.ProjectDir = rest[i]
				continue
			}
			if hasPrefix(rest[i], "--project=") {
				cfg.ProjectDir = rest[i][len("--project="):]
				continue
			}
			switch positional {
			case 0:
				cfg.ShaA = rest[i]
			case 1:
				cfg.ShaB = rest[i]
			default:
				return cfg, fmt.Errorf("metrics compare: unexpected argument %q", rest[i])
			}
			positional++
		}
		if cfg.ShaA == "" || cfg.ShaB == "" {
			return cfg, fmt.Errorf("metrics compare: requires <shaA> <shaB>")
		}

	case "serve", "gate", "install-hook":
		// Phase-2 stubs — no flags parsed.

	case "help", "--help", "-h":
		cfg.Subcommand = "help"

	default:
		return cfg, fmt.Errorf("metrics: unknown subcommand %q (try 'yakos metrics help')", cfg.Subcommand)
	}

	return cfg, nil
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// Run executes the metrics sub-subcommand described by cfg.
func Run(cfg Config) (*Result, error) {
	w := cfg.Writer
	if w == nil {
		w = os.Stdout
	}
	ew := cfg.ErrWriter
	if ew == nil {
		ew = os.Stderr
	}

	now := time.Now().UTC()
	if cfg.Now != nil {
		now = cfg.Now()
	}

	home := cfg.HomeDir
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		home = "/tmp"
	}

	runner := cfg.GitRunner
	if runner == nil {
		runner = realGitRunner{}
	}

	res := &Result{Subcommand: cfg.Subcommand}

	switch cfg.Subcommand {
	case "collect":
		snap, err := runCollect(cfg, runner, now, home, w, ew)
		if err != nil {
			return nil, err
		}
		res.Snapshot = snap
		return res, nil

	case "report":
		return res, runReport(cfg, home, w)

	case "trend":
		return res, runTrend(cfg, home, w)

	case "compare":
		return res, runCompare(cfg, home, w)

	case "serve", "gate", "install-hook":
		_, _ = fmt.Fprintf(ew, "metrics %s: not yet implemented (Phase-2)\n", cfg.Subcommand)
		return res, nil

	case "help", "":
		PrintHelp(w)
		return res, nil

	default:
		return nil, fmt.Errorf("metrics: unknown subcommand %q", cfg.Subcommand)
	}
}

// runCollect executes the collect subcommand.
func runCollect(cfg Config, runner gitRunner, now time.Time, home string, w, ew io.Writer) (*Snapshot, error) {
	projectDir := ResolveProjectDir(cfg.ProjectDir)
	if projectDir == "" {
		// Fall back to cwd.
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("metrics collect: cannot determine project dir")
		}
		projectDir = cwd
		_, _ = fmt.Fprintf(ew, "metrics: no project dir resolved; using cwd %s\n", projectDir)
	}

	stateDir := cfg.StateDir
	if stateDir == "" {
		stateDir = ResolveStateDir(home)
	}

	// Git info.
	gi := getGitInfo(runner, projectDir)

	// Detect profiles.
	profiles := stackdetect.Detect(projectDir)

	snap := newSnapshot(now, gi.Commit, gi.Branch, cfg.Trigger, profiles)

	// [E] collectors.
	collectEfficiency(stateDir, projectDir, cfg.SinceTS, &snap.Metrics)
	collectModelRouting(stateDir, &snap.Metrics)
	collectDORA(runner, projectDir, cfg.SinceTS, &snap.Metrics)
	collectSizeChurn(runner, projectDir, &snap.Metrics)

	// hook-bypass: look for work/current/hook-bypass.md relative to project.
	// The work dir is typically <project>/work (inplace) or ~/agent-control/<proj>/work.
	collectHookBypass(projectDir, now, &snap.Metrics)
	// Also try ~/agent-control/<proj> relative path.
	if snap.Metrics.Dispatch.HookBypassCount == nil {
		// Try to find work dir from project name via agent-control.
		acPath := findAgentControlWorkDir(home, projectDir)
		if acPath != "" {
			collectHookBypass(acPath, now, &snap.Metrics)
		}
	}

	// [T] analyzers (unless skipped).
	if !cfg.SkipAnalyzers {
		queue := analyzerListFor(profiles)
		if len(queue) > 0 {
			runAnalyzerList(projectDir, queue, &snap.Metrics, snap.ToolStatus)
		}
	}

	if cfg.EmitJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(snap); err != nil {
			return nil, fmt.Errorf("metrics collect: encode JSON: %w", err)
		}
	} else {
		_, _ = fmt.Fprintf(w, "metrics: collected snapshot at commit %s (trigger: %s)\n",
			short(snap.Commit), snap.Trigger)
		if !cfg.NoWrite {
			_, _ = fmt.Fprintf(w, "metrics: profiles: %v\n", snap.Profiles)
		}
	}

	if !cfg.NoWrite {
		if err := AppendSnapshot(projectDir, snap); err != nil {
			return nil, fmt.Errorf("metrics collect: append snapshot: %w", err)
		}
		if !cfg.EmitJSON {
			_, _ = fmt.Fprintf(w, "metrics: written to %s\n", HistoryPath(projectDir))
		}
	} else {
		if !cfg.EmitJSON {
			_, _ = fmt.Fprintln(w, "metrics: --no-write; snapshot not persisted")
		}
	}

	return &snap, nil
}

// findAgentControlWorkDir looks for ~/agent-control/<basename-of-projectDir>.
func findAgentControlWorkDir(home, projectDir string) string {
	// Extract the last path component as the project name.
	name := projectDir
	for i := len(projectDir) - 1; i >= 0; i-- {
		if projectDir[i] == '/' || projectDir[i] == '\\' {
			name = projectDir[i+1:]
			break
		}
	}
	if name == "" {
		return ""
	}
	candidate := home + "/agent-control/" + name
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return ""
}

// runReport executes the report subcommand.
func runReport(cfg Config, home string, w io.Writer) error {
	projectDir := ResolveProjectDir(cfg.ProjectDir)
	if projectDir == "" {
		cwd, _ := os.Getwd()
		projectDir = cwd
	}

	snaps, err := ReadHistory(projectDir)
	if err != nil {
		return fmt.Errorf("metrics report: %w", err)
	}

	return PrintReport(w, snaps, cfg.EmitJSON)
}

// runTrend executes the trend subcommand.
func runTrend(cfg Config, home string, w io.Writer) error {
	projectDir := ResolveProjectDir(cfg.ProjectDir)
	if projectDir == "" {
		cwd, _ := os.Getwd()
		projectDir = cwd
	}

	snaps, err := ReadHistory(projectDir)
	if err != nil {
		return fmt.Errorf("metrics trend: %w", err)
	}

	return PrintTrend(w, snaps, cfg.MetricPath, cfg.LastN, cfg.SinceTS)
}

// runCompare executes the compare subcommand.
func runCompare(cfg Config, home string, w io.Writer) error {
	projectDir := ResolveProjectDir(cfg.ProjectDir)
	if projectDir == "" {
		cwd, _ := os.Getwd()
		projectDir = cwd
	}

	snaps, err := ReadHistory(projectDir)
	if err != nil {
		return fmt.Errorf("metrics compare: %w", err)
	}

	return PrintCompare(w, snaps, cfg.ShaA, cfg.ShaB, cfg.EmitJSON)
}

// PrintHelp writes the help text for `yakos metrics` to w.
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos metrics <subcommand> [flags]

Record and view per-project code-quality, effectiveness, and security signals.

Subcommands:
  collect [--trigger T] [--no-write] [--skip-analyzers] [--json] [--since TS]
                         Collect a snapshot. Trigger: manual|git-hook|ci|release.
                         --no-write: compute but do not persist the snapshot.
                         --skip-analyzers: skip [T] tool invocations.

  report [--json]        Print the latest snapshot with Δ vs previous.

  trend [--metric PATH] [--last N] [--since TS]
                         Show metric over time with sparkline.
                         --metric: dot-path like efficiency.total_cost_usd.
                         --last N: limit to last N snapshots (default 10).

  compare <shaA> <shaB>  Side-by-side diff of two snapshots by commit SHA prefix.

  serve                  (Phase-2, not yet implemented)
  gate                   (Phase-2, not yet implemented)
  install-hook           (Phase-2, not yet implemented)

Storage:
  <project>/.yakos/metrics/history.ndjson  — append-only NDJSON, one line/snapshot.

Null vs 0:
  Missing tool → metric is null (not measured), status → tool-missing.
  Tool ran but found nothing → metric is 0 (measured, result is zero).
`)
}
