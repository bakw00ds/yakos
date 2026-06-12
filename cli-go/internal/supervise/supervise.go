// Package supervise is the Go port of cli/lib/supervise.sh (rank 34).
//
// It manages the live shadow-agent supervisor — the scoring loop that
// dispatches a supervisor agent every N tool calls and writes findings to
// work/current/supervisor-findings.ndjson.  The ack gate
// (scripts/hooks/supervisor-ack-gate.sh) blocks the lead on CRITICAL findings
// when block_on_critical: true.
//
// # Synopsis
//
//	yakos supervise enable  [<project>]
//	yakos supervise disable [<project>]
//	yakos supervise status  [<project>]
//	yakos supervise tail    [<project>] [--watch] [--n <N>]
//	yakos supervise clear   [<project>]
//	yakos supervise set <key> <value> [<project>]
//	yakos supervise pending [<project>]
//	yakos supervise ack <finding-id> [<project>] [--note "..."]
//	yakos supervise ack-all [<project>] [--note "..."]
//
// # State files
//
//	<project>/.yakos.yml                                 — config (supervisor: block)
//	~/agent-control/<proj>/work/current/supervisor-buffer.ndjson
//	~/agent-control/<proj>/work/current/supervisor-findings.ndjson
//	~/agent-control/<proj>/work/current/.supervisor-counter
//	~/.yakos-state/supervisor-acks.ndjson                — ack log (append-only)
//
// # Finding IDs
//
//	f-<ts-colons-as-hyphens>-<project>-<linenum>
//
// # Atomic writes
//
// .yakos.yml edits are written via a temp-rename (Q8 Decision A).
// The ack file is append-only (O_APPEND|O_CREATE|O_WRONLY).
//
// # Parity with PRs #28–#39
//
// The supervisor was redesigned across PRs #28–#39:
//   - gate-on-CRITICAL (block_on_critical key)
//   - ack tracking (supervisor-acks.ndjson; finding ID derivation)
//   - pending / ack / ack-all subcommands
//   - emergency bypass via YAKOS_SUPERVISOR_DISABLE=1
//   - supervisor.enabled toggle in .yakos.yml
package supervise

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ---- public types -----------------------------------------------------------

// Config carries everything Run needs.
type Config struct {
	// Subcommand is one of: enable, disable, status, tail, clear, set, pending, ack, ack-all, help.
	Subcommand string

	// Project is the project slug (e.g. "yakos"). When empty, Run attempts to
	// infer from cwd via the ~/agent-control/ layout.
	Project string

	// Key is the config key for the set subcommand.
	Key string

	// Value is the config value for the set subcommand.
	Value string

	// FindingID is the finding ID for the ack subcommand.
	FindingID string

	// Note is the acknowledgement note for ack / ack-all.
	Note string

	// TailN is the number of recent findings to show in the tail subcommand.
	// Default: 10.
	TailN int

	// Watch enables tail -f mode.
	Watch bool

	// HomeDir overrides os.Getenv("HOME").
	HomeDir string

	// AgentControlRoot overrides ~/agent-control for testing.
	AgentControlRoot string

	// Writer receives normal output (default: os.Stdout).
	Writer io.Writer

	// ErrWriter receives error/warning output (default: os.Stderr).
	ErrWriter io.Writer

	// NowFn returns the current time; injectable for tests.
	NowFn func() time.Time

	// UserFn returns the current OS username; injectable for tests.
	UserFn func() string

	// ReadLineFn reads a line from the findings file for tail (injectable for
	// tests that don't have a real file watcher).  When nil, real file I/O is
	// used.
	ReadLineFn func(path string, n int) ([]string, error)
}

// Result summarises what Run did.
type Result struct {
	// Subcommand that was executed.
	Subcommand string

	// Project that was resolved.
	Project string

	// Enabled is the supervisor.enabled value after the operation (enable/disable/set).
	Enabled *bool

	// AckedCount is the number of findings acknowledged (ack-all).
	AckedCount int

	// AckedID is the finding ID acknowledged (ack).
	AckedID string

	// PendingCount is the number of unacknowledged escalation findings.
	PendingCount int

	// ClearedCount is the number of files removed by clear.
	ClearedCount int
}

// AckRecord is a single row in supervisor-acks.ndjson.
type AckRecord struct {
	TS        string `json:"ts"`
	FindingID string `json:"finding_id"`
	Project   string `json:"project"`
	User      string `json:"user"`
	Note      string `json:"note"`
}

// Finding is a parsed row from supervisor-findings.ndjson.
type Finding struct {
	TS                string `json:"ts"`
	RecommendedAction string `json:"recommended_action"`
	Rationale         string `json:"rationale"`
	LineNum           int    `json:"-"`
}

// ---- entry point ------------------------------------------------------------

// Run executes the supervise subcommand according to cfg.
func Run(cfg Config) (*Result, error) {
	if cfg.Writer == nil {
		cfg.Writer = os.Stdout
	}
	if cfg.ErrWriter == nil {
		cfg.ErrWriter = os.Stderr
	}
	if cfg.NowFn == nil {
		cfg.NowFn = func() time.Time { return time.Now().UTC() }
	}
	if cfg.UserFn == nil {
		cfg.UserFn = currentUser
	}
	if cfg.TailN <= 0 {
		cfg.TailN = 10
	}

	home := cfg.HomeDir
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		home = "/tmp"
	}

	acRoot := cfg.AgentControlRoot
	if acRoot == "" {
		acRoot = filepath.Join(home, "agent-control")
	}

	switch cfg.Subcommand {
	case "enable":
		return runEnable(cfg, home, acRoot)
	case "disable":
		return runDisable(cfg, home, acRoot)
	case "status":
		return runStatus(cfg, home, acRoot)
	case "tail":
		return runTail(cfg, home, acRoot)
	case "clear":
		return runClear(cfg, home, acRoot)
	case "set":
		return runSet(cfg, home, acRoot)
	case "pending":
		return runPending(cfg, home, acRoot)
	case "ack":
		return runAck(cfg, home, acRoot)
	case "ack-all":
		return runAckAll(cfg, home, acRoot)
	default:
		return nil, fmt.Errorf("supervise: unknown subcommand %q (try 'yakos supervise help')", cfg.Subcommand)
	}
}

// PrintHelp writes the help text to w.
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos supervise <subcommand> [args...]

Manage the live shadow-agent supervisor (v0.33+).

Subcommands:
  enable [<project>]          Flip .yakos.yml supervisor.enabled to true.
  disable [<project>]         Flip to false.
  set <key> <value> [<proj>]  Set a single supervisor config key in .yakos.yml.
                              Common keys: block_on_critical, score_every_n_calls,
                              runtime, agent, model, matcher. (supervisor-toggle skill.)
  status [<project>]          Config snapshot + buffer + recent findings count.
  tail [<project>] [--watch]  Print recent findings (--watch follows).
  clear [<project>]           Wipe buffer + findings + counter (keeps config).
  pending [<project>]         List unacknowledged surface_to_operator+ findings.
  ack <finding-id> [<proj>] [--note "..."]
                              Acknowledge a pending escalation finding.
  ack-all [<project>] [--note "..."]
                              Acknowledge all currently-pending escalation findings.

Project resolution: arg if given, else inferred from cwd (matches
'yakos start' / 'yakos peer status').

Emergency session bypass (no edit to .yakos.yml needed):
  export YAKOS_SUPERVISOR_DISABLE=1

Acknowledgement state:
  ~/.yakos-state/supervisor-acks.ndjson — append-only; one ack record per line.
  Finding IDs: f-<ts-safe>-<project>-<linenum>
  Format: {"ts":"<ISO>","finding_id":"<id>","project":"<name>","user":"<user>","note":"<...>"}

See docs/supervisor-mode.md for the full guide.
`)
}

// ---- project resolution -----------------------------------------------------

// resolveProject returns the project slug from cfg.Project or from cwd inference.
func resolveProject(cfg Config, acRoot string) (string, error) {
	if cfg.Project != "" {
		return cfg.Project, nil
	}
	p, err := inferProjectFromCWD(acRoot)
	if err != nil || p == "" {
		return "", fmt.Errorf("supervise: could not infer project from cwd; pass <name> explicitly")
	}
	return p, nil
}

// inferProjectFromCWD mirrors the bash infer_project() function.
// It reads ~/agent-control/*/.project-path files and compares realpath to cwd.
func inferProjectFromCWD(acRoot string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = r
	}

	// Check if cwd is directly under agent-control/<slug>/
	if strings.HasPrefix(cwd, acRoot+string(filepath.Separator)) {
		rest := cwd[len(acRoot)+1:]
		idx := strings.IndexByte(rest, filepath.Separator)
		var slug string
		if idx >= 0 {
			slug = rest[:idx]
		} else {
			slug = rest
		}
		if slug != "" {
			return slug, nil
		}
	}

	entries, err := os.ReadDir(acRoot)
	if err != nil {
		return "", nil //nolint:nilerr // not an error — acRoot absent
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
			return e.Name(), nil
		}
	}
	return "", nil
}

// projectPaths returns the standard paths for a project.
type projectPaths struct {
	yakosYML    string
	workCurrent string
	buffer      string
	findings    string
	counter     string
}

func resolveProjectPaths(acRoot, project string) (projectPaths, error) {
	cdFile := filepath.Join(acRoot, project, ".project-path")
	if _, err := os.Stat(cdFile); err != nil {
		return projectPaths{}, fmt.Errorf("supervise: %s missing; run 'yakos init %s --project <repo>' first", cdFile, project)
	}
	data, err := os.ReadFile(cdFile) //nolint:gosec
	if err != nil {
		return projectPaths{}, fmt.Errorf("supervise: read %s: %w", cdFile, err)
	}
	projectRepo := strings.TrimSpace(string(data))
	if projectRepo == "" {
		return projectPaths{}, fmt.Errorf("supervise: %s is empty", cdFile)
	}
	if st, err := os.Stat(projectRepo); err != nil || !st.IsDir() {
		return projectPaths{}, fmt.Errorf("supervise: project repo %s not present on this host", projectRepo)
	}

	wc := filepath.Join(acRoot, project, "work", "current")
	return projectPaths{
		yakosYML:    filepath.Join(projectRepo, ".yakos.yml"),
		workCurrent: wc,
		buffer:      filepath.Join(wc, "supervisor-buffer.ndjson"),
		findings:    filepath.Join(wc, "supervisor-findings.ndjson"),
		counter:     filepath.Join(wc, ".supervisor-counter"),
	}, nil
}

// ---- enable / disable -------------------------------------------------------

func runEnable(cfg Config, home, acRoot string) (*Result, error) {
	proj, err := resolveProject(cfg, acRoot)
	if err != nil {
		return nil, err
	}
	paths, err := resolveProjectPaths(acRoot, proj)
	if err != nil {
		return nil, err
	}
	if err := flipSupervisorEnabled(paths.yakosYML, true, cfg.NowFn); err != nil {
		return nil, err
	}

	t := true
	res := &Result{Subcommand: "enable", Project: proj, Enabled: &t}

	_, _ = fmt.Fprintf(cfg.Writer, "\nSupervisor enabled. Next session in %s will:\n", proj)
	_, _ = fmt.Fprintf(cfg.Writer, "  - Stream every tool call to %s\n", paths.buffer)
	_, _ = fmt.Fprintf(cfg.Writer, "  - Every 10 lead tool calls, fork a supervisor dispatch (on 'claude'\n")
	_, _ = fmt.Fprintf(cfg.Writer, "    runtime by default — edit .yakos.yml to change)\n")
	_, _ = fmt.Fprintf(cfg.Writer, "  - The supervisor scores and writes findings to:\n")
	_, _ = fmt.Fprintf(cfg.Writer, "      %s\n", paths.findings)
	_, _ = fmt.Fprintf(cfg.Writer, "  - supervisor-gate.sh reads findings and blocks lead on CRITICAL\n")
	_, _ = fmt.Fprintf(cfg.Writer, "    (block_on_critical: true; set to false for surface-only mode)\n")
	_, _ = fmt.Fprintf(cfg.Writer, "\nEmergency disable for one session: export YAKOS_SUPERVISOR_DISABLE=1\n")
	_, _ = fmt.Fprintf(cfg.Writer, "See: docs/supervisor-mode.md\n")

	return res, nil
}

func runDisable(cfg Config, home, acRoot string) (*Result, error) {
	proj, err := resolveProject(cfg, acRoot)
	if err != nil {
		return nil, err
	}
	paths, err := resolveProjectPaths(acRoot, proj)
	if err != nil {
		return nil, err
	}
	if err := flipSupervisorEnabled(paths.yakosYML, false, cfg.NowFn); err != nil {
		return nil, err
	}

	f := false
	return &Result{Subcommand: "disable", Project: proj, Enabled: &f}, nil
}

// flipSupervisorEnabled atomically sets supervisor.enabled in .yakos.yml.
// It handles three cases:
//  1. supervisor: block exists with an enabled: line → replace the line
//  2. supervisor: block exists but no enabled: line → insert one
//  3. no supervisor: block → append a fresh minimal block
func flipSupervisorEnabled(yakosYML string, enabled bool, nowFn func() time.Time) error {
	target := "true"
	if !enabled {
		target = "false"
	}

	if _, err := os.Stat(yakosYML); err != nil {
		return fmt.Errorf("supervise: %s missing; run 'yakos init' first", yakosYML)
	}

	data, err := os.ReadFile(yakosYML) //nolint:gosec
	if err != nil {
		return fmt.Errorf("supervise: read %s: %w", yakosYML, err)
	}
	content := string(data)

	// Check if already at target state.
	if isSupervisorEnabledAs(content, target) {
		fmt.Printf("supervisor.enabled is already '%s' in %s — no change\n", target, yakosYML)
		return nil
	}

	var newContent string
	if hasSupervisorBlock(content) {
		if hasEnabledLine(content) {
			newContent = replaceEnabledLine(content, target)
		} else {
			newContent = insertEnabledAfterBlock(content, target)
		}
	} else {
		action := "enable"
		if !enabled {
			action = "disable"
		}
		ts := nowFn().UTC().Format(time.RFC3339)
		newContent = content + fmt.Sprintf(
			"\n# Added by `yakos supervise %s` on %s\nsupervisor:\n  enabled: %s\n  runtime: claude\n  agent: supervisor\n  score_every_n_calls: 10\n  block_on_critical: true\n",
			action, ts, target,
		)
	}

	if err := atomicWriteFile(yakosYML, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("supervise: write %s: %w", yakosYML, err)
	}
	fmt.Printf("set supervisor.enabled: %s in %s\n", target, yakosYML)
	return nil
}

// ---- status -----------------------------------------------------------------

func runStatus(cfg Config, home, acRoot string) (*Result, error) {
	proj, err := resolveProject(cfg, acRoot)
	if err != nil {
		return nil, err
	}
	paths, err := resolveProjectPaths(acRoot, proj)
	if err != nil {
		return nil, err
	}

	w := cfg.Writer
	_, _ = fmt.Fprintf(w, "yakos supervise status — project: %s\n", proj)
	_, _ = fmt.Fprintf(w, "  .yakos.yml:  %s\n", paths.yakosYML)

	var enabled *bool
	if _, err := os.Stat(paths.yakosYML); err != nil {
		_, _ = fmt.Fprintf(w, "  config:      (missing; run 'yakos init')\n")
	} else {
		data, _ := os.ReadFile(paths.yakosYML) //nolint:gosec
		content := string(data)
		if isSupervisorEnabledAs(content, "true") {
			_, _ = fmt.Fprintf(w, "  enabled:     YES\n")
			t := true
			enabled = &t
		} else {
			_, _ = fmt.Fprintf(w, "  enabled:     no\n")
			f := false
			enabled = &f
		}
		// Surface other supervisor keys.
		for _, key := range []string{"runtime", "agent", "score_every_n_calls", "block_on_critical", "model", "matcher"} {
			v := extractSupervisorKey(content, key)
			if v != "" {
				_, _ = fmt.Fprintf(w, "  %-20s %s\n", key+":", v)
			}
		}
	}

	if os.Getenv("YAKOS_SUPERVISOR_DISABLE") == "1" {
		_, _ = fmt.Fprintf(w, "  env override:  YAKOS_SUPERVISOR_DISABLE=1 (currently DISABLED in this shell)\n")
	}

	_, _ = fmt.Fprintf(w, "\n  buffer:      %s\n", paths.buffer)
	if st, err := os.Stat(paths.buffer); err == nil && st.Size() > 0 {
		n := countLines(paths.buffer)
		_, _ = fmt.Fprintf(w, "    entries:   %d\n", n)
		last := lastJSONField(paths.buffer, "ts")
		_, _ = fmt.Fprintf(w, "    last ts:   %s\n", last)
	} else {
		_, _ = fmt.Fprintf(w, "    (no entries yet — start a session)\n")
	}

	_, _ = fmt.Fprintf(w, "\n  counter:     %s\n", paths.counter)
	if data, err := os.ReadFile(paths.counter); err == nil { //nolint:gosec
		_, _ = fmt.Fprintf(w, "    value:     %s\n", strings.TrimSpace(string(data)))
	} else {
		_, _ = fmt.Fprintf(w, "    (not initialized — supervisor hasn't fired yet)\n")
	}

	_, _ = fmt.Fprintf(w, "\n  findings:    %s\n", paths.findings)
	if st, err := os.Stat(paths.findings); err == nil && st.Size() > 0 {
		n := countLines(paths.findings)
		_, _ = fmt.Fprintf(w, "    count:     %d\n", n)
		if n > 0 {
			_, _ = fmt.Fprintf(w, "    most recent:\n")
			last := lastLine(paths.findings)
			// Print the last line with indentation.
			_, _ = fmt.Fprintf(w, "      %s\n", last)
		}
	} else {
		_, _ = fmt.Fprintf(w, "    (no findings yet)\n")
	}

	return &Result{Subcommand: "status", Project: proj, Enabled: enabled}, nil
}

// ---- tail -------------------------------------------------------------------

func runTail(cfg Config, home, acRoot string) (*Result, error) {
	proj, err := resolveProject(cfg, acRoot)
	if err != nil {
		return nil, err
	}
	paths, err := resolveProjectPaths(acRoot, proj)
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(paths.findings); err != nil {
		_, _ = fmt.Fprintf(cfg.Writer, "supervise tail: no findings yet at %s\n", paths.findings)
		return &Result{Subcommand: "tail", Project: proj}, nil
	}

	if cfg.Watch {
		_, _ = fmt.Fprintf(cfg.Writer, "yakos supervise tail --watch — Ctrl+C to exit\n")
		if err := tailWatch(cfg.Writer, paths.findings, cfg.TailN); err != nil {
			return nil, fmt.Errorf("supervise tail: %w", err)
		}
	} else {
		lines, err := tailLines(paths.findings, cfg.TailN)
		if err != nil {
			return nil, fmt.Errorf("supervise tail: %w", err)
		}
		for _, line := range lines {
			_, _ = fmt.Fprintf(cfg.Writer, "%s\n---\n", line)
		}
	}

	return &Result{Subcommand: "tail", Project: proj}, nil
}

// tailLines returns the last n lines from path.
func tailLines(path string, n int) ([]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	lines := nonEmptyLines(string(data))
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

// tailWatch follows path, printing new lines until the process is killed.
// This is a simple polling approach (no inotify) adequate for CLI use.
func tailWatch(w io.Writer, path string, initialN int) error {
	// Print the last N lines first.
	lines, err := tailLines(path, initialN)
	if err != nil {
		return err
	}
	for _, line := range lines {
		_, _ = fmt.Fprintf(w, "%s\n---\n", line)
	}

	// Track file offset.
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	// Seek to end.
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("seek: %w", err)
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) != "" {
			_, _ = fmt.Fprintf(w, "%s\n---\n", line)
		}
	}
	return scanner.Err()
}

// ---- clear ------------------------------------------------------------------

func runClear(cfg Config, home, acRoot string) (*Result, error) {
	proj, err := resolveProject(cfg, acRoot)
	if err != nil {
		return nil, err
	}
	paths, err := resolveProjectPaths(acRoot, proj)
	if err != nil {
		return nil, err
	}

	removed := 0
	for _, f := range []string{
		paths.buffer,
		paths.findings,
		paths.counter,
		filepath.Join(paths.workCurrent, ".supervisor-gate-last-surfaced"),
	} {
		if _, err := os.Stat(f); err == nil {
			if err := os.Remove(f); err == nil {
				removed++
			}
		}
	}

	_, _ = fmt.Fprintf(cfg.Writer, "supervise clear: removed %d file(s) for %s (config in .yakos.yml preserved)\n",
		removed, proj)

	return &Result{Subcommand: "clear", Project: proj, ClearedCount: removed}, nil
}

// ---- set --------------------------------------------------------------------

var allowedSupervisorKeys = map[string]bool{
	"enabled":             true,
	"runtime":             true,
	"agent":               true,
	"score_every_n_calls": true,
	"block_on_critical":   true,
	"model":               true,
	"matcher":             true,
}

func runSet(cfg Config, home, acRoot string) (*Result, error) {
	if cfg.Key == "" {
		return nil, fmt.Errorf("supervise set: <key> <value> required (e.g. block_on_critical false)")
	}
	if cfg.Value == "" {
		return nil, fmt.Errorf("supervise set: <key> <value> required (e.g. block_on_critical false)")
	}

	if !allowedSupervisorKeys[cfg.Key] {
		return nil, fmt.Errorf("supervise set: unknown key %q (allowed: enabled / runtime / agent / score_every_n_calls / block_on_critical / model / matcher)", cfg.Key)
	}

	// Validate key-specific constraints.
	switch cfg.Key {
	case "enabled", "block_on_critical":
		if cfg.Value != "true" && cfg.Value != "false" {
			return nil, fmt.Errorf("supervise set %s: value must be 'true' or 'false' (got %q)", cfg.Key, cfg.Value)
		}
	case "score_every_n_calls":
		if _, err := strconv.Atoi(cfg.Value); err != nil || cfg.Value == "" {
			return nil, fmt.Errorf("supervise set %s: value must be a positive integer (got %q)", cfg.Key, cfg.Value)
		}
		if n, _ := strconv.Atoi(cfg.Value); n <= 0 {
			return nil, fmt.Errorf("supervise set %s: value must be a positive integer (got %q)", cfg.Key, cfg.Value)
		}
	case "model":
		switch cfg.Value {
		case "haiku", "sonnet", "opus", "fable":
		default:
			return nil, fmt.Errorf("supervise set model: value must be haiku, sonnet, opus, or fable (got %q)", cfg.Value)
		}
	}

	proj, err := resolveProject(cfg, acRoot)
	if err != nil {
		return nil, err
	}
	paths, err := resolveProjectPaths(acRoot, proj)
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(paths.yakosYML); err != nil {
		return nil, fmt.Errorf("supervise set: %s missing; run 'yakos init' first", paths.yakosYML)
	}

	data, err := os.ReadFile(paths.yakosYML) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("supervise set: read %s: %w", paths.yakosYML, err)
	}
	content := string(data)

	// No-op check.
	existing := extractSupervisorKey(content, cfg.Key)
	if existing == cfg.Value {
		_, _ = fmt.Fprintf(cfg.Writer, "supervisor.%s is already '%s' in %s — no change\n", cfg.Key, cfg.Value, paths.yakosYML)
		return &Result{Subcommand: "set", Project: proj}, nil
	}

	var newContent string
	if hasSupervisorBlock(content) {
		newContent = setSupervisorKey(content, cfg.Key, cfg.Value)
	} else {
		ts := cfg.NowFn().UTC().Format(time.RFC3339)
		newContent = content + fmt.Sprintf(
			"\n# Added by `yakos supervise set` on %s\nsupervisor:\n  %s: %s\n",
			ts, cfg.Key, cfg.Value,
		)
	}

	if err := atomicWriteFile(paths.yakosYML, []byte(newContent), 0644); err != nil {
		return nil, fmt.Errorf("supervise set: write %s: %w", paths.yakosYML, err)
	}

	_, _ = fmt.Fprintf(cfg.Writer, "set supervisor.%s: %s in %s\n", cfg.Key, cfg.Value, paths.yakosYML)
	return &Result{Subcommand: "set", Project: proj}, nil
}

// ---- pending ----------------------------------------------------------------

func runPending(cfg Config, home, acRoot string) (*Result, error) {
	proj, err := resolveProject(cfg, acRoot)
	if err != nil {
		return nil, err
	}
	paths, err := resolveProjectPaths(acRoot, proj)
	if err != nil {
		return nil, err
	}

	ackFile := ackFilePath(home)

	if _, err := os.Stat(paths.findings); err != nil {
		_, _ = fmt.Fprintf(cfg.Writer, "supervise pending: no findings file at %s\n", paths.findings)
		return &Result{Subcommand: "pending", Project: proj}, nil
	}

	pending, err := listPendingFindings(paths.findings, proj, ackFile)
	if err != nil {
		return nil, fmt.Errorf("supervise pending: %w", err)
	}

	_, _ = fmt.Fprintf(cfg.Writer, "yakos supervise pending — project: %s\n\n", proj)

	if len(pending) == 0 {
		_, _ = fmt.Fprintf(cfg.Writer, "  No unacknowledged escalation findings.\n")
		return &Result{Subcommand: "pending", Project: proj, PendingCount: 0}, nil
	}

	_, _ = fmt.Fprintf(cfg.Writer, "  Unacknowledged escalation findings (surface_to_operator+):\n\n")
	for _, f := range pending {
		fid := deriveFindingID(f.TS, proj, f.LineNum)
		_, _ = fmt.Fprintf(cfg.Writer, "  [%s]\n", fid)
		_, _ = fmt.Fprintf(cfg.Writer, "    action:   %s\n", f.RecommendedAction)
		_, _ = fmt.Fprintf(cfg.Writer, "    rationale: %s\n\n", f.Rationale)
	}

	_, _ = fmt.Fprintf(cfg.Writer, "Acknowledge with:\n")
	firstFID := deriveFindingID(pending[0].TS, proj, pending[0].LineNum)
	_, _ = fmt.Fprintf(cfg.Writer, "  yakos supervise ack %s [--note \"...\"]\n", firstFID)
	_, _ = fmt.Fprintf(cfg.Writer, "  yakos supervise ack-all [--note \"...\"]\n")

	return &Result{Subcommand: "pending", Project: proj, PendingCount: len(pending)}, nil
}

// ---- ack --------------------------------------------------------------------

func runAck(cfg Config, home, acRoot string) (*Result, error) {
	if cfg.FindingID == "" {
		return nil, fmt.Errorf("supervise ack: <finding-id> required (try 'yakos supervise pending')")
	}

	proj, err := resolveProject(cfg, acRoot)
	if err != nil {
		return nil, err
	}
	paths, err := resolveProjectPaths(acRoot, proj)
	if err != nil {
		return nil, err
	}

	ackFile := ackFilePath(home)

	if _, err := os.Stat(paths.findings); err != nil {
		return nil, fmt.Errorf("supervise ack: no findings file at %s", paths.findings)
	}

	pending, err := listPendingFindings(paths.findings, proj, ackFile)
	if err != nil {
		return nil, fmt.Errorf("supervise ack: %w", err)
	}

	if len(pending) == 0 {
		_, _ = fmt.Fprintf(cfg.Writer, "supervise ack: no pending escalation findings for %s — nothing to acknowledge\n", proj)
		return &Result{Subcommand: "ack", Project: proj}, nil
	}

	// Verify the finding_id is in pending list.
	found := false
	for _, f := range pending {
		fid := deriveFindingID(f.TS, proj, f.LineNum)
		if fid == cfg.FindingID {
			found = true
			break
		}
	}

	if !found {
		// Check if already acked.
		if isAlreadyAcked(ackFile, cfg.FindingID, proj) {
			_, _ = fmt.Fprintf(cfg.Writer, "supervise ack: %s is already acknowledged for %s\n", cfg.FindingID, proj)
			return &Result{Subcommand: "ack", Project: proj, AckedID: cfg.FindingID}, nil
		}
		return nil, fmt.Errorf("supervise ack: finding %q not found in pending escalations for %s (run 'yakos supervise pending' to list)", cfg.FindingID, proj)
	}

	rec := AckRecord{
		TS:        cfg.NowFn().UTC().Format(time.RFC3339),
		FindingID: cfg.FindingID,
		Project:   proj,
		User:      cfg.UserFn(),
		Note:      cfg.Note,
	}
	if err := appendAck(ackFile, rec); err != nil {
		return nil, fmt.Errorf("supervise ack: %w", err)
	}

	_, _ = fmt.Fprintf(cfg.Writer, "Acknowledged: %s (project: %s)\n", cfg.FindingID, proj)
	if cfg.Note != "" {
		_, _ = fmt.Fprintf(cfg.Writer, "  note: %s\n", cfg.Note)
	}
	_, _ = fmt.Fprintf(cfg.Writer, "  ack record appended to: %s\n", ackFile)

	return &Result{Subcommand: "ack", Project: proj, AckedID: cfg.FindingID, AckedCount: 1}, nil
}

// ---- ack-all ----------------------------------------------------------------

func runAckAll(cfg Config, home, acRoot string) (*Result, error) {
	proj, err := resolveProject(cfg, acRoot)
	if err != nil {
		return nil, err
	}
	paths, err := resolveProjectPaths(acRoot, proj)
	if err != nil {
		return nil, err
	}

	ackFile := ackFilePath(home)

	if _, err := os.Stat(paths.findings); err != nil {
		_, _ = fmt.Fprintf(cfg.Writer, "supervise ack-all: no findings file at %s — nothing to acknowledge\n", paths.findings)
		return &Result{Subcommand: "ack-all", Project: proj}, nil
	}

	pending, err := listPendingFindings(paths.findings, proj, ackFile)
	if err != nil {
		return nil, fmt.Errorf("supervise ack-all: %w", err)
	}

	if len(pending) == 0 {
		_, _ = fmt.Fprintf(cfg.Writer, "supervise ack-all: no pending escalation findings for %s\n", proj)
		return &Result{Subcommand: "ack-all", Project: proj}, nil
	}

	ts := cfg.NowFn().UTC().Format(time.RFC3339)
	user := cfg.UserFn()
	ackedCount := 0
	for _, f := range pending {
		fid := deriveFindingID(f.TS, proj, f.LineNum)
		rec := AckRecord{
			TS:        ts,
			FindingID: fid,
			Project:   proj,
			User:      user,
			Note:      cfg.Note,
		}
		if err := appendAck(ackFile, rec); err != nil {
			_, _ = fmt.Fprintf(cfg.ErrWriter, "supervise ack-all: warn: could not ack %s: %v\n", fid, err)
			continue
		}
		_, _ = fmt.Fprintf(cfg.Writer, "  Acknowledged: %s\n", fid)
		ackedCount++
	}

	_, _ = fmt.Fprintf(cfg.Writer, "\nack-all: acknowledged %d finding(s) for %s\n", ackedCount, proj)
	if cfg.Note != "" {
		_, _ = fmt.Fprintf(cfg.Writer, "  note: %s\n", cfg.Note)
	}
	_, _ = fmt.Fprintf(cfg.Writer, "  ack records appended to: %s\n", ackFile)

	return &Result{Subcommand: "ack-all", Project: proj, AckedCount: ackedCount}, nil
}

// ---- ack helpers ------------------------------------------------------------

func ackFilePath(home string) string {
	return filepath.Join(home, ".yakos-state", "supervisor-acks.ndjson")
}

// deriveFindingID matches the bash derive_finding_id() exactly:
//
//	f-<ts-colons-as-hyphens>-<project>-<linenum>
func deriveFindingID(rawTS, project string, linenum int) string {
	safeTS := strings.ReplaceAll(rawTS, ":", "-")
	return fmt.Sprintf("f-%s-%s-%d", safeTS, project, linenum)
}

// listPendingFindings reads the findings file and returns those with
// recommended_action in {surface_to_operator, block_next_tool, halt}
// that have not been acknowledged.
func listPendingFindings(findingsFile, project, ackFile string) ([]Finding, error) {
	data, err := os.ReadFile(findingsFile) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", findingsFile, err)
	}

	// Build the acked set for this project.
	ackedSet := loadAckedSet(ackFile, project)

	var result []Finding
	lineNum := 0
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		lineNum++
		if line == "" {
			continue
		}

		var rec map[string]interface{}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}

		action, _ := rec["recommended_action"].(string)
		switch action {
		case "surface_to_operator", "block_next_tool", "halt":
		default:
			continue
		}

		rawTS, _ := rec["ts"].(string)
		fid := deriveFindingID(rawTS, project, lineNum)
		if ackedSet[fid] {
			continue
		}

		rationale, _ := rec["rationale"].(string)
		if rationale == "" {
			rationale = "(no rationale)"
		}

		result = append(result, Finding{
			TS:                rawTS,
			RecommendedAction: action,
			Rationale:         rationale,
			LineNum:           lineNum,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", findingsFile, err)
	}
	return result, nil
}

// loadAckedSet returns a set of finding IDs that have been acknowledged for project.
func loadAckedSet(ackFile, project string) map[string]bool {
	set := make(map[string]bool)
	data, err := os.ReadFile(ackFile) //nolint:gosec
	if err != nil {
		return set
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec AckRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec.Project == project {
			set[rec.FindingID] = true
		}
	}
	return set
}

// isAlreadyAcked returns true if finding_id is in the ack file for project.
func isAlreadyAcked(ackFile, findingID, project string) bool {
	set := loadAckedSet(ackFile, project)
	return set[findingID]
}

// appendAck writes one AckRecord to the ack file (append-only NDJSON).
func appendAck(ackFile string, rec AckRecord) error {
	if err := os.MkdirAll(filepath.Dir(ackFile), 0755); err != nil { //nolint:gosec
		return fmt.Errorf("mkdir: %w", err)
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	f, err := os.OpenFile(ackFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644) //nolint:gosec
	if err != nil {
		return fmt.Errorf("open %s: %w", ackFile, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := fmt.Fprintf(f, "%s\n", b); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// ---- YAML helpers -----------------------------------------------------------
// These parse the supervisor: block without a full YAML library to keep
// the dependency footprint small (matches the bash awk approach).

// hasSupervisorBlock reports whether content has a supervisor: top-level key.
func hasSupervisorBlock(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimRight(line, " \t\r") == "supervisor:" ||
			strings.HasPrefix(line, "supervisor:") {
			return true
		}
	}
	return false
}

// hasEnabledLine reports whether the supervisor block contains an enabled: line.
func hasEnabledLine(content string) bool {
	inBlock := false
	for _, line := range strings.Split(content, "\n") {
		stripped := strings.TrimRight(line, " \t\r")
		if stripped == "supervisor:" || strings.HasPrefix(stripped, "supervisor: ") {
			inBlock = true
			continue
		}
		if inBlock {
			// Exiting block: top-level key (non-space, non-comment, non-empty)
			if len(line) > 0 && line[0] != ' ' && line[0] != '\t' && line[0] != '#' {
				inBlock = false
				continue
			}
			trimmed := strings.TrimLeft(line, " \t")
			if strings.HasPrefix(trimmed, "enabled:") {
				return true
			}
		}
	}
	return false
}

// isSupervisorEnabledAs returns true when supervisor.enabled == target in content.
func isSupervisorEnabledAs(content, target string) bool {
	inBlock := false
	for _, line := range strings.Split(content, "\n") {
		stripped := strings.TrimRight(line, " \t\r")
		if stripped == "supervisor:" || strings.HasPrefix(stripped, "supervisor: ") {
			inBlock = true
			continue
		}
		if inBlock {
			if len(line) > 0 && line[0] != ' ' && line[0] != '\t' && line[0] != '#' && line[0] != '\n' {
				inBlock = false
				continue
			}
			trimmed := strings.TrimLeft(line, " \t")
			if strings.HasPrefix(trimmed, "enabled:") {
				val := strings.TrimSpace(trimmed[len("enabled:"):])
				return val == target
			}
		}
	}
	return false
}

// replaceEnabledLine replaces the enabled: line inside the supervisor: block.
func replaceEnabledLine(content, target string) string {
	inBlock := false
	var out []string
	for _, line := range strings.Split(content, "\n") {
		stripped := strings.TrimRight(line, " \t\r")
		if stripped == "supervisor:" || strings.HasPrefix(stripped, "supervisor: ") {
			inBlock = true
			out = append(out, line)
			continue
		}
		if inBlock {
			if len(line) > 0 && line[0] != ' ' && line[0] != '\t' && line[0] != '#' && line[0] != '\n' {
				inBlock = false
			} else {
				trimmed := strings.TrimLeft(line, " \t")
				if strings.HasPrefix(trimmed, "enabled:") {
					// Preserve leading whitespace.
					indent := line[:len(line)-len(trimmed)]
					out = append(out, indent+"enabled: "+target)
					continue
				}
			}
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// insertEnabledAfterBlock adds "  enabled: <target>" right after the supervisor: line.
func insertEnabledAfterBlock(content, target string) string {
	var out []string
	for _, line := range strings.Split(content, "\n") {
		out = append(out, line)
		stripped := strings.TrimRight(line, " \t\r")
		if stripped == "supervisor:" || strings.HasPrefix(stripped, "supervisor: ") {
			out = append(out, "  enabled: "+target)
		}
	}
	return strings.Join(out, "\n")
}

// extractSupervisorKey returns the value of key inside the supervisor: block.
func extractSupervisorKey(content, key string) string {
	inBlock := false
	for _, line := range strings.Split(content, "\n") {
		stripped := strings.TrimRight(line, " \t\r")
		if stripped == "supervisor:" || strings.HasPrefix(stripped, "supervisor: ") {
			inBlock = true
			continue
		}
		if inBlock {
			if len(line) > 0 && line[0] != ' ' && line[0] != '\t' && line[0] != '#' {
				inBlock = false
				continue
			}
			trimmed := strings.TrimLeft(line, " \t")
			prefix := key + ":"
			if strings.HasPrefix(trimmed, prefix) {
				return strings.TrimSpace(trimmed[len(prefix):])
			}
		}
	}
	return ""
}

// setSupervisorKey sets or appends key: value inside the supervisor: block.
func setSupervisorKey(content, key, value string) string {
	// If the key exists inside the block, replace it.
	if extractSupervisorKey(content, key) != "" {
		return replaceKeyInBlock(content, key, value)
	}
	// Otherwise, insert the key right after the supervisor: line.
	var out []string
	inserted := false
	for _, line := range strings.Split(content, "\n") {
		out = append(out, line)
		stripped := strings.TrimRight(line, " \t\r")
		if !inserted && (stripped == "supervisor:" || strings.HasPrefix(stripped, "supervisor: ")) {
			out = append(out, "  "+key+": "+value)
			inserted = true
		}
	}
	return strings.Join(out, "\n")
}

// replaceKeyInBlock replaces key: <anything> inside the supervisor: block.
func replaceKeyInBlock(content, key, value string) string {
	inBlock := false
	var out []string
	for _, line := range strings.Split(content, "\n") {
		stripped := strings.TrimRight(line, " \t\r")
		if stripped == "supervisor:" || strings.HasPrefix(stripped, "supervisor: ") {
			inBlock = true
			out = append(out, line)
			continue
		}
		if inBlock {
			if len(line) > 0 && line[0] != ' ' && line[0] != '\t' && line[0] != '#' {
				inBlock = false
			} else {
				trimmed := strings.TrimLeft(line, " \t")
				prefix := key + ":"
				if strings.HasPrefix(trimmed, prefix) {
					indent := line[:len(line)-len(trimmed)]
					out = append(out, indent+key+": "+value)
					continue
				}
			}
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// ---- file utilities ---------------------------------------------------------

// countLines returns the number of non-empty lines in path.
func countLines(path string) int {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return 0
	}
	return len(nonEmptyLines(string(data)))
}

// lastLine returns the last non-empty line of path.
func lastLine(path string) string {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return "-"
	}
	lines := nonEmptyLines(string(data))
	if len(lines) == 0 {
		return "-"
	}
	return lines[len(lines)-1]
}

// lastJSONField returns the value of a JSON field from the last line of path.
func lastJSONField(path, field string) string {
	line := lastLine(path)
	if line == "-" {
		return "-"
	}
	var rec map[string]interface{}
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		return "-"
	}
	v, _ := rec[field].(string)
	if v == "" {
		return "-"
	}
	return v
}

// nonEmptyLines splits s on newlines and returns non-empty lines.
func nonEmptyLines(s string) []string {
	raw := strings.Split(s, "\n")
	out := make([]string, 0, len(raw))
	for _, l := range raw {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// atomicWriteFile writes data to path via a temp-rename (Q8 Decision A).
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil { //nolint:gosec
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".yakos-supervise-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temp→%s: %w", path, err)
	}
	return nil
}

// currentUser returns the OS username (matches bash $(id -un) or $USER).
func currentUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u := os.Getenv("LOGNAME"); u != "" {
		return u
	}
	return "unknown"
}
