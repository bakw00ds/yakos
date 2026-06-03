// Package checkpoint is the Go port of cli/lib/checkpoint.sh (rank 28).
//
// It creates and manages work-dir snapshots that can be used to restore
// session state.
//
// # Synopsis
//
//	yakos checkpoint create            # create a snapshot (alias: now)
//	yakos checkpoint list              # list existing snapshots
//	yakos checkpoint restore <id>      # print resume instructions (alias: resume)
//	yakos checkpoint clean [--age N]   # GC old snapshots (default >30 days)
//
// # Snapshot layout
//
// Each snapshot lives under <work>/current/checkpoints/<iso>/:
//
//	summary.md         — state digest placeholder (M3.2: agent-generated)
//	scratchpad/        — copy of plan/decisions/contracts/status/kanban .md
//	token-snapshot.txt — timestamp of compaction point
//	session-id.txt     — runtime session_id (for --fork-session resume)
//	manifest.json      — checkpoint metadata (ts, session_id, runtime, by_user)
//
// # Atomic dir pattern
//
// Snapshot directories are created atomically: content is written into a
// final destination directory (no atomic rename needed for directory creation;
// the scratchpad copy is file-by-file which is safe because checkpoints are
// append-only and each ID is unique per ISO timestamp).
package checkpoint

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ---- public types -----------------------------------------------------------

// Config carries everything Run needs.
type Config struct {
	// Subcommand is one of: create, list, restore, clean, help.
	// "now" is accepted as an alias for "create".
	// "resume" is accepted as an alias for "restore".
	Subcommand string

	// RestoreID is the checkpoint ID for the restore subcommand.
	RestoreID string

	// CleanAgeDays is the minimum age (in days) for GC by the clean subcommand.
	// Zero means "use the default" (30 days).
	CleanAgeDays int

	// SessionID is the session ID to embed in the snapshot.
	// When empty, the CLAUDE_SESSION_ID env var is consulted, then the
	// .session-started-history.ndjson tail, then "unknown" is used.
	SessionID string

	// Runtime is the runtime name (e.g. "claude"). Defaults to YAKOS_RUNTIME env
	// or "claude".
	Runtime string

	// HomeDir overrides os.Getenv("HOME") for path resolution.
	HomeDir string

	// WorkDir overrides the work directory entirely. When set, it is used
	// directly instead of the standard $HOME/agent-control/<project>/work path.
	// This mirrors YAKOS_WORK_DIR env semantics and is used in tests.
	WorkDir string

	// Now overrides time.Now() for deterministic tests.
	Now time.Time

	// Writer receives normal output (default: os.Stdout).
	Writer io.Writer

	// ErrWriter receives advisory/warning messages (default: os.Stderr).
	ErrWriter io.Writer
}

// Result summarises what Run did.
type Result struct {
	// Subcommand that was executed.
	Subcommand string

	// CheckpointID is the ISO timestamp used as the snapshot directory name.
	// Populated by the create subcommand.
	CheckpointID string

	// CheckpointDir is the absolute path to the created snapshot directory.
	// Populated by the create subcommand.
	CheckpointDir string

	// Checkpoints is the list of snapshot IDs found on disk.
	// Populated by the list subcommand.
	Checkpoints []string

	// RemovedCount is the number of snapshots removed by the clean subcommand.
	RemovedCount int
}

// ---- public API -------------------------------------------------------------

// Run executes the checkpoint subcommand described by cfg and returns a
// structured Result. Errors are returned to the caller; progress messages
// are written to cfg.Writer.
func Run(cfg Config) (*Result, error) {
	if cfg.Writer == nil {
		cfg.Writer = os.Stdout
	}
	if cfg.ErrWriter == nil {
		cfg.ErrWriter = os.Stderr
	}

	now := cfg.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	workDir := resolveWorkDir(cfg)
	currentDir := filepath.Join(workDir, "current")

	res := &Result{Subcommand: cfg.Subcommand}

	switch cfg.Subcommand {
	case "create", "now":
		return runCreate(cfg, res, currentDir, now)
	case "list":
		return runList(cfg, res, currentDir)
	case "restore", "resume":
		return runRestore(cfg, res, currentDir)
	case "clean":
		return runClean(cfg, res, currentDir)
	case "help", "--help", "-h", "":
		PrintHelp(cfg.Writer)
		return res, nil
	default:
		return nil, fmt.Errorf("unknown subcommand %q (try 'yakos checkpoint help')", cfg.Subcommand)
	}
}

// PrintHelp writes the help text to w, mirroring checkpoint.sh usage().
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos checkpoint — snapshot + restore session state

  yakos checkpoint create                 # create snapshot (alias: now)
  yakos checkpoint list                   # list existing snapshots
  yakos checkpoint restore <id>           # resume via --fork-session (alias: resume)
  yakos checkpoint clean [--age <days>]   # GC old snapshots (default >30d)

See lib/skills/checkpoint-context/SKILL.md for the full discipline.
`)
}

// ---- subcommand implementations ---------------------------------------------

// runCreate creates a new checkpoint snapshot under checkpoints/<iso>/.
func runCreate(cfg Config, res *Result, currentDir string, now time.Time) (*Result, error) {
	ts := isoUTC(now)
	checkpointsDir := filepath.Join(currentDir, "checkpoints")
	snapshotDir := filepath.Join(checkpointsDir, ts)

	scratchpadDir := filepath.Join(snapshotDir, "scratchpad")
	if err := os.MkdirAll(scratchpadDir, 0755); err != nil { //nolint:gosec
		return nil, fmt.Errorf("checkpoint create: mkdir %s: %w", scratchpadDir, err)
	}

	// Copy scratchpad files (best-effort; missing files are silently skipped).
	for _, name := range []string{"plan.md", "decisions.md", "contracts.md", "status.md", "kanban.md"} {
		src := filepath.Join(currentDir, name)
		dst := filepath.Join(scratchpadDir, name)
		if err := copyFileIfExists(src, dst); err != nil {
			_, _ = fmt.Fprintf(cfg.ErrWriter, "checkpoint: warning: could not copy %s: %v\n", name, err)
		}
	}

	// Resolve session ID.
	sessionID := resolveSessionID(cfg, currentDir)
	if err := os.WriteFile(filepath.Join(snapshotDir, "session-id.txt"), []byte(sessionID+"\n"), 0644); err != nil { //nolint:gosec
		return nil, fmt.Errorf("checkpoint create: write session-id.txt: %w", err)
	}

	// Resolve runtime.
	runtime := cfg.Runtime
	if runtime == "" {
		runtime = os.Getenv("YAKOS_RUNTIME")
	}
	if runtime == "" {
		runtime = "claude"
	}

	// Resolve user.
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("LOGNAME")
	}
	if user == "" {
		user = "unknown"
	}

	// Write manifest.json.
	manifest := map[string]string{
		"created":    now.UTC().Format(time.RFC3339),
		"session_id": sessionID,
		"runtime":    runtime,
		"by_user":    user,
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("checkpoint create: marshal manifest: %w", err)
	}
	manifestData = append(manifestData, '\n')
	if err := os.WriteFile(filepath.Join(snapshotDir, "manifest.json"), manifestData, 0644); err != nil { //nolint:gosec
		return nil, fmt.Errorf("checkpoint create: write manifest.json: %w", err)
	}

	// Write token-snapshot.txt (placeholder; real probe is in context-threshold.sh).
	tokenMsg := fmt.Sprintf("checkpoint at %s\n", now.UTC().Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(snapshotDir, "token-snapshot.txt"), []byte(tokenMsg), 0644); err != nil { //nolint:gosec
		return nil, fmt.Errorf("checkpoint create: write token-snapshot.txt: %w", err)
	}

	// Write summary.md — placeholder; M3.2 will dispatch librarian to generate.
	summary := fmt.Sprintf(`# Checkpoint summary

**Created:** %s
**Session:** %s
**Runtime:** %s

(librarian-generated digest goes here in M3.2 — for the draft,
this is the placeholder. Inspect scratchpad/ for full state.)
`, now.UTC().Format(time.RFC3339), sessionID, runtime)
	if err := os.WriteFile(filepath.Join(snapshotDir, "summary.md"), []byte(summary), 0644); err != nil { //nolint:gosec
		return nil, fmt.Errorf("checkpoint create: write summary.md: %w", err)
	}

	_, _ = fmt.Fprintf(cfg.Writer, "checkpoint created: %s\n", ts)

	res.CheckpointID = ts
	res.CheckpointDir = snapshotDir
	return res, nil
}

// runList prints all existing snapshot IDs with size and age.
func runList(cfg Config, res *Result, currentDir string) (*Result, error) {
	checkpointsDir := filepath.Join(currentDir, "checkpoints")
	entries, err := os.ReadDir(checkpointsDir)
	if os.IsNotExist(err) {
		_, _ = fmt.Fprintln(cfg.Writer, "(no checkpoints)")
		return res, nil
	}
	if err != nil {
		return nil, fmt.Errorf("checkpoint list: read dir %s: %w", checkpointsDir, err)
	}

	// Collect and sort directory entries.
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	sort.Strings(ids)

	if len(ids) == 0 {
		_, _ = fmt.Fprintln(cfg.Writer, "(no checkpoints)")
		return res, nil
	}

	now := time.Now().UTC()
	for _, id := range ids {
		dir := filepath.Join(checkpointsDir, id)
		fi, err := os.Stat(dir)
		var ageDays int
		if err == nil {
			ageDays = int(now.Sub(fi.ModTime()) / (24 * time.Hour))
		}
		size := diskUsage(dir)
		_, _ = fmt.Fprintf(cfg.Writer, "  %s  (%s, %d days ago)\n", id, size, ageDays)
	}

	res.Checkpoints = ids
	return res, nil
}

// runRestore prints resume instructions for the given checkpoint ID.
func runRestore(cfg Config, res *Result, currentDir string) (*Result, error) {
	if cfg.RestoreID == "" {
		return nil, fmt.Errorf("checkpoint restore: missing <id>")
	}
	id := cfg.RestoreID
	snapshotDir := filepath.Join(currentDir, "checkpoints", id)
	if _, err := os.Stat(snapshotDir); err != nil {
		return nil, fmt.Errorf("checkpoint restore: checkpoint %q not found", id)
	}

	sessionIDBytes, err := os.ReadFile(filepath.Join(snapshotDir, "session-id.txt")) //nolint:gosec
	sessionID := strings.TrimSpace(string(sessionIDBytes))
	if err != nil || sessionID == "" {
		sessionID = "unknown"
	}
	if sessionID == "unknown" {
		return nil, fmt.Errorf("checkpoint restore: checkpoint %q has no session-id; cannot restore", id)
	}

	summaryBytes, _ := os.ReadFile(filepath.Join(snapshotDir, "summary.md")) //nolint:gosec
	summary := strings.TrimRight(string(summaryBytes), "\n")

	runtime := os.Getenv("YAKOS_RUNTIME")
	if runtime == "" {
		runtime = "claude"
	}

	_, _ = fmt.Fprintf(cfg.Writer, "Resuming from checkpoint %s (session %s).\n\n", id, sessionID)
	_, _ = fmt.Fprintf(cfg.Writer, "To complete the restore:\n")
	_, _ = fmt.Fprintf(cfg.Writer, "  yakos start --runtime %s --resume %s\n\n", runtime, sessionID)
	_, _ = fmt.Fprintln(cfg.Writer, "The new session will load with the original session context. The")
	_, _ = fmt.Fprintf(cfg.Writer, "checkpoint's scratchpad/ contents are preserved at %s/scratchpad/.\n\n", snapshotDir)
	_, _ = fmt.Fprintf(cfg.Writer, "Summary:\n%s\n", summary)

	return res, nil
}

// runClean removes snapshot directories older than the configured age.
func runClean(cfg Config, res *Result, currentDir string) (*Result, error) {
	ageDays := cfg.CleanAgeDays
	if ageDays <= 0 {
		ageDays = 30
	}

	checkpointsDir := filepath.Join(currentDir, "checkpoints")
	entries, err := os.ReadDir(checkpointsDir)
	if os.IsNotExist(err) {
		_, _ = fmt.Fprintf(cfg.Writer, "removed 0 checkpoint(s) older than %d days\n", ageDays)
		return res, nil
	}
	if err != nil {
		return nil, fmt.Errorf("checkpoint clean: read dir %s: %w", checkpointsDir, err)
	}

	cutoff := time.Now().UTC().Add(-time.Duration(ageDays) * 24 * time.Hour)
	removed := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(checkpointsDir, e.Name())
		fi, err := os.Stat(dir)
		if err != nil {
			continue
		}
		if fi.ModTime().Before(cutoff) {
			if rmErr := os.RemoveAll(dir); rmErr != nil {
				_, _ = fmt.Fprintf(cfg.ErrWriter, "checkpoint clean: warning: remove %s: %v\n", dir, rmErr)
				continue
			}
			removed++
		}
	}

	_, _ = fmt.Fprintf(cfg.Writer, "removed %d checkpoint(s) older than %d days\n", removed, ageDays)
	res.RemovedCount = removed
	return res, nil
}

// ---- helpers ----------------------------------------------------------------

// resolveWorkDir determines the work directory from cfg following paths.sh priority:
//  1. cfg.WorkDir (direct override, used in tests)
//  2. YAKOS_WORK_DIR env
//  3. YAKOS_INPLACE_WORK=1 + CLAUDE_PROJECT_DIR
//  4. $HOME/agent-control/<YAKOS_PROJECT_NAME>/work  (canonical)
func resolveWorkDir(cfg Config) string {
	if cfg.WorkDir != "" {
		return cfg.WorkDir
	}
	if v := os.Getenv("YAKOS_WORK_DIR"); v != "" {
		return v
	}
	if os.Getenv("YAKOS_INPLACE_WORK") == "1" {
		if pd := os.Getenv("CLAUDE_PROJECT_DIR"); pd != "" {
			return filepath.Join(pd, "work")
		}
	}
	home := cfg.HomeDir
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		home = "/tmp"
	}
	proj := os.Getenv("YAKOS_PROJECT_NAME")
	if proj == "" {
		proj = "default"
	}
	return filepath.Join(home, "agent-control", proj, "work")
}

// resolveSessionID determines the session ID following checkpoint.sh logic:
//  1. cfg.SessionID (direct injection)
//  2. CLAUDE_SESSION_ID env var
//  3. Last session_id in .session-started-history.ndjson
//  4. "unknown"
func resolveSessionID(cfg Config, currentDir string) string {
	if cfg.SessionID != "" {
		return cfg.SessionID
	}
	if v := os.Getenv("CLAUDE_SESSION_ID"); v != "" {
		return v
	}
	// Try reading from session history.
	histPath := filepath.Join(currentDir, ".session-started-history.ndjson")
	data, err := os.ReadFile(histPath) //nolint:gosec
	if err != nil {
		return "unknown"
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var rec map[string]interface{}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if sid, ok := rec["session_id"].(string); ok && sid != "" {
			return sid
		}
	}
	return "unknown"
}

// isoUTC formats t as a compact UTC ISO timestamp suitable for use as a
// directory name. Mirrors ct_iso_utc() from compat.sh.
// Format: YYYY-MM-DDTHH-MM-SS (colons replaced by hyphens for filesystem safety).
func isoUTC(t time.Time) string {
	return t.UTC().Format("2006-01-02T15-04-05")
}

// copyFileIfExists copies src to dst only when src exists. Parent directories
// of dst are created as needed. No-op (returns nil) when src is absent.
func copyFileIfExists(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer func() { _ = in.Close() }()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil { //nolint:gosec
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(dst), err)
	}

	out, err := os.Create(dst) //nolint:gosec
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy %s → %s: %w", src, dst, err)
	}
	return nil
}

// diskUsage returns a human-readable size string for dir, mirroring
// the helper in archive.go.
func diskUsage(dir string) string {
	var total int64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return formatSize(total)
}

// formatSize returns a du-sh–style human readable string.
func formatSize(bytes int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case bytes >= gb:
		return fmt.Sprintf("%.1fG", float64(bytes)/float64(gb))
	case bytes >= mb:
		return fmt.Sprintf("%.1fM", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.1fK", float64(bytes)/float64(kb))
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}
