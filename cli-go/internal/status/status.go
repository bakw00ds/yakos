// Package status implements the Go port of `yakos status`.
//
// It reads the project's agent-control directory and produces a one-page
// dashboard: session age, scratchpad size, file ages, hook outcomes,
// bypass count, mailbox count, and MEMORY.md presence.
//
// The package is read-only: it never writes to any state file.
//
// Work-dir resolution mirrors paths.sh exactly:
//
//  1. YAKOS_WORK_DIR   — absolute override.
//  2. YAKOS_INPLACE_WORK=1 + CLAUDE_PROJECT_DIR — in-repo work/.
//  3. $HOME/agent-control/<project>/work — canonical Phase-1.5 §7 location.
//     Project name from YAKOS_PROJECT_NAME → basename(CLAUDE_PROJECT_DIR) → basename(cwd).
package status

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/kanban"
)

// Config controls what status reads and where it looks.
type Config struct {
	// Project is the agent-control project name (required positional arg from CLI).
	Project string

	// HomeDir is the user's home directory. Defaults to os.Getenv("HOME").
	HomeDir string

	// Env is the environment map used for path resolution. If nil, os.Getenv is used.
	// Tests inject a synthetic map here so they don't pollute the real environment.
	Env map[string]string

	// Now is the current time used for age calculations. If zero, time.Now() is used.
	// Tests inject a fixed time to produce deterministic output.
	Now time.Time

	// Writer is where the report is written. Defaults to os.Stdout.
	Writer io.Writer

	// ErrWriter is where error messages are written. Defaults to os.Stderr.
	ErrWriter io.Writer
}

// HookEntry is a summary of one hook log file.
type HookEntry struct {
	Name    string // basename without .ndjson
	Summary string // "N pass, M block" etc.
	LastTS  string // last .ts in the file, or ""
}

// Report is the structured result returned by Status.
// The Format function turns it into the human-readable dashboard.
type Report struct {
	Project      string
	ControlDir   string
	SessionAge   string // e.g. "started 2026-06-01T10:00:00Z (2h 5m ago)"
	SessionWarn  string // non-empty when age > 4h
	ScratchSize  string // e.g. "4.2M"
	ScratchWarn  string // non-empty when > 100 MB
	ArchiveSize  string // e.g. "12M" or "(empty)"
	PlanAge      string // e.g. "3h ago" or "absent"
	ContractsAge string
	DecisionsAge string
	DecisionsWarn string // "⚠" when decisions.md > 2h old
	Hooks        []HookEntry
	BypassCount  int
	MailboxCount int
	MemoryLabel  string // e.g. "3 MEMORY.md file(s) under ~/.claude/projects/"
}

// Status runs the full status query against the project's work directory.
// It returns a structured Report; use Format to render it.
func Status(cfg Config) (*Report, error) {
	env := cfg.Env
	if env == nil {
		env = osEnvMap()
	}

	home := cfg.HomeDir
	if home == "" {
		home = envGet(env, "HOME")
	}
	if home == "" {
		home = "/tmp"
	}

	now := cfg.Now
	if now.IsZero() {
		now = time.Now()
	}

	// Resolve control dir: $HOME/agent-control/<project>
	controlDir := filepath.Join(home, "agent-control", cfg.Project)

	// Resolve work dir following paths.sh resolution order.
	workDir := resolveWorkDir(cfg.Project, home, env)
	currentDir := filepath.Join(workDir, "current")
	archiveDir := filepath.Join(workDir, "archive")

	rpt := &Report{
		Project:    cfg.Project,
		ControlDir: controlDir,
	}

	// ---- session age --------------------------------------------------------
	sessionStartedFile := filepath.Join(currentDir, ".session-started")
	sessionHistoryFile := filepath.Join(currentDir, ".session-started-history.ndjson")
	rpt.SessionAge, rpt.SessionWarn = computeSessionAge(sessionStartedFile, sessionHistoryFile, now, cfg.Project)

	// ---- sizes --------------------------------------------------------------
	rpt.ScratchSize, rpt.ScratchWarn = computeDirSize(currentDir)
	rpt.ArchiveSize = computeArchiveSize(archiveDir)

	// ---- file ages ----------------------------------------------------------
	rpt.PlanAge = fileAgeLabel(filepath.Join(currentDir, "plan.md"), now)
	rpt.ContractsAge = fileAgeLabel(filepath.Join(currentDir, "contracts.md"), now)
	rpt.DecisionsAge = fileAgeLabel(filepath.Join(currentDir, "decisions.md"), now)

	// decisions stale flag (>2h)
	decisionsPath := filepath.Join(currentDir, "decisions.md")
	if fi, err := os.Stat(decisionsPath); err == nil {
		if now.Sub(fi.ModTime()) > 2*time.Hour {
			rpt.DecisionsWarn = " ⚠"
		}
	}

	// ---- hook outcomes ------------------------------------------------------
	rpt.Hooks = collectHooks(filepath.Join(currentDir, "logs"))

	// ---- bypasses -----------------------------------------------------------
	rpt.BypassCount = countBypasses(filepath.Join(currentDir, "hook-bypass.md"))

	// ---- mailbox messages ---------------------------------------------------
	rpt.MailboxCount = countLines(filepath.Join(currentDir, "messages.ndjson"))

	// ---- memory state -------------------------------------------------------
	rpt.MemoryLabel = computeMemoryLabel(home)

	return rpt, nil
}

// ---- path resolution -------------------------------------------------------

// resolveWorkDir mirrors paths.sh's yakos_work_dir function exactly.
func resolveWorkDir(project, home string, env map[string]string) string {
	// 1. $YAKOS_WORK_DIR — absolute override.
	if v := envGet(env, "YAKOS_WORK_DIR"); v != "" {
		return v
	}
	// 2. $YAKOS_INPLACE_WORK=1 + $CLAUDE_PROJECT_DIR — in-repo work/.
	if envGet(env, "YAKOS_INPLACE_WORK") == "1" {
		if pd := envGet(env, "CLAUDE_PROJECT_DIR"); pd != "" {
			return filepath.Join(pd, "work")
		}
	}
	// 3. $HOME/agent-control/<name>/work — canonical.
	name := resolveProjectName(project, env)
	return filepath.Join(home, "agent-control", name, "work")
}

// resolveProjectName mirrors paths.sh's yakos_project_name function.
// The explicit project arg (from CLI) always takes precedence.
func resolveProjectName(explicit string, env map[string]string) string {
	if explicit != "" {
		return explicit
	}
	if v := envGet(env, "YAKOS_PROJECT_NAME"); v != "" {
		return v
	}
	if pd := envGet(env, "CLAUDE_PROJECT_DIR"); pd != "" {
		return filepath.Base(pd)
	}
	// Last resort: basename of cwd.
	if cwd, err := os.Getwd(); err == nil {
		return filepath.Base(cwd)
	}
	return "unknown"
}

// ---- session age ------------------------------------------------------------

// computeSessionAge reads the session-started file and/or history and returns
// a human-readable age label and an optional warning string.
// project is used in the restart-suggestion warning message.
func computeSessionAge(startedFile, historyFile string, now time.Time, project string) (label, warn string) {
	lastStarted := ""

	// Prefer .session-started (single-line ISO-8601).
	if data, err := os.ReadFile(startedFile); err == nil {
		line := strings.TrimSpace(string(data))
		if line != "" {
			lastStarted = line
		}
	}

	// Fall back to the most recent team_created event in NDJSON history.
	if lastStarted == "" {
		if f, err := os.Open(historyFile); err == nil {
			lastStarted = lastTeamCreatedTS(f)
			_ = f.Close()
		}
	}

	if lastStarted == "" {
		return "not started", ""
	}

	// Parse the ISO-8601 timestamp.
	t, err := time.Parse(time.RFC3339, lastStarted)
	if err != nil {
		// Try a few fallback layouts.
		for _, layout := range []string{"2006-01-02T15:04:05Z", "2006-01-02T15:04:05"} {
			if t2, e2 := time.Parse(layout, lastStarted); e2 == nil {
				t = t2
				err = nil
				break
			}
		}
	}
	if err != nil {
		return fmt.Sprintf("started %s (age unknown)", lastStarted), ""
	}

	ageS := int64(now.Sub(t).Seconds())
	if ageS < 0 {
		ageS = 0
	}

	var ageStr string
	switch {
	case ageS < 60:
		ageStr = fmt.Sprintf("%ds ago", ageS)
	case ageS < 3600:
		ageStr = fmt.Sprintf("%dm ago", ageS/60)
	default:
		h := ageS / 3600
		m := (ageS % 3600) / 60
		ageStr = fmt.Sprintf("%dh %dm ago", h, m)
	}

	label = fmt.Sprintf("started %s (%s)", lastStarted, ageStr)

	if ageS > 14400 { // 4 hours
		warn = fmt.Sprintf("session age >4h. consider 'yakos team restart %s' for context hygiene.", project)
	}

	return label, warn
}

// lastTeamCreatedTS scans an NDJSON history file and returns the .ts field
// of the last line whose .event == "team_created".
func lastTeamCreatedTS(r io.Reader) string {
	type histLine struct {
		Event string `json:"event"`
		TS    string `json:"ts"`
	}
	var last string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var h histLine
		if err := json.Unmarshal([]byte(line), &h); err != nil {
			continue
		}
		if h.Event == "team_created" && h.TS != "" {
			last = h.TS
		}
	}
	return last
}

// ---- sizes ------------------------------------------------------------------

// computeDirSize returns a human-readable size string and an optional warning.
// Returns "(empty)" when the directory does not exist.
func computeDirSize(dir string) (sizeStr, warn string) {
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return "(empty)", ""
	}

	var totalBytes int64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			totalBytes += info.Size()
		}
		return nil
	})

	sizeStr = formatBytes(totalBytes)

	if totalBytes > 100*1024*1024 { // 100 MB
		warn = "scratchpad >100MB. archive recommended."
	}
	return sizeStr, warn
}

// computeArchiveSize returns a human-readable size for the archive dir, or "(empty)".
func computeArchiveSize(dir string) string {
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return "(empty)"
	}
	// Check whether the dir has any entries.
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		return "(empty)"
	}

	var totalBytes int64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			totalBytes += info.Size()
		}
		return nil
	})
	return formatBytes(totalBytes)
}

// formatBytes formats a byte count in a human-readable form matching du -sh.
// du -sh uses 1024-based units: K, M, G.
// We replicate the most common du output patterns (1 decimal for M/G, whole
// for K). Values under 1K are shown as "0" (du -sh shows "0" for empty dirs).
func formatBytes(n int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case n == 0:
		return "0"
	case n < kb:
		return fmt.Sprintf("%dB", n)
	case n < mb:
		return fmt.Sprintf("%dK", (n+kb/2)/kb)
	case n < gb:
		// du -sh uses 1-decimal for megabytes.
		return fmt.Sprintf("%.1fM", float64(n)/float64(mb))
	default:
		return fmt.Sprintf("%.1fG", float64(n)/float64(gb))
	}
}

// ---- file ages --------------------------------------------------------------

// fileAgeLabel returns a human-readable age for a file, or "absent" if the
// file does not exist. Matches bash's file_age_label function.
func fileAgeLabel(path string, now time.Time) string {
	fi, err := os.Stat(path)
	if err != nil {
		return "absent"
	}
	diff := int64(now.Sub(fi.ModTime()).Seconds())
	if diff < 0 {
		diff = 0
	}
	switch {
	case diff < 60:
		return fmt.Sprintf("%ds ago", diff)
	case diff < 3600:
		return fmt.Sprintf("%dm ago", diff/60)
	case diff < 86400:
		return fmt.Sprintf("%dh ago", diff/3600)
	default:
		return fmt.Sprintf("%dd ago", diff/86400)
	}
}

// ---- hooks ------------------------------------------------------------------

// collectHooks reads all *.ndjson files under logsDir and builds a summary
// per-file.  Files are returned in sorted (alphabetical) order, mirroring
// bash's `find ... | sort` pipeline.
//
// If logsDir does not exist or is empty, a single placeholder entry is returned.
func collectHooks(logsDir string) []HookEntry {
	entries, err := os.ReadDir(logsDir)
	if err != nil || len(entries) == 0 {
		return []HookEntry{{Name: "(no hook logs yet)", Summary: ""}}
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".ndjson") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return []HookEntry{{Name: "(no hook logs yet)", Summary: ""}}
	}
	sort.Strings(names)

	var hooks []HookEntry
	for _, name := range names {
		path := filepath.Join(logsDir, name)
		hookName := strings.TrimSuffix(name, ".ndjson")
		summary, lastTS := summarizeHookLog(path)
		hooks = append(hooks, HookEntry{
			Name:    hookName,
			Summary: summary,
			LastTS:  lastTS,
		})
	}
	return hooks
}

// summarizeHookLog reads a hook NDJSON log and counts records by .decision.
// Returns a summary string like "3 pass, 1 block" and the last .ts value.
func summarizeHookLog(path string) (summary, lastTS string) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return "(empty log)", ""
	}
	defer func() { _ = f.Close() }()

	counts := make(map[string]int)
	var last string

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec struct {
			Decision string `json:"decision"`
			TS       string `json:"ts"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		d := rec.Decision
		if d == "" {
			d = "report"
		}
		counts[d]++
		if rec.TS != "" {
			last = rec.TS
		}
	}

	if len(counts) == 0 {
		return "(empty log)", ""
	}

	// Build "N decision, M decision, ..." in sorted order for determinism.
	// bash uses: sort | uniq -c | awk '{printf "%s %s, ", $1, $2}' | sed 's/, $//'
	// That produces ascending alphabetical order of decision names.
	type kv struct {
		d string
		n int
	}
	var pairs []kv
	for d, n := range counts {
		pairs = append(pairs, kv{d, n})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].d < pairs[j].d })

	var parts []string
	for _, p := range pairs {
		parts = append(parts, fmt.Sprintf("%d %s", p.n, p.d))
	}
	summary = strings.Join(parts, ", ")
	return summary, last
}

// ---- bypasses ---------------------------------------------------------------

// countBypasses counts active bypass entries in hook-bypass.md.
// Mirrors bash's awk: count "## bypass:" headings after "## Active entries".
func countBypasses(path string) int {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()

	active := false
	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmedHeading(trimmed) == "Active entries" {
			active = true
			continue
		}
		if active && strings.HasPrefix(trimmed, "## bypass:") {
			count++
		}
	}
	return count
}

// trimmedHeading extracts the heading text from "## <text>" lines.
func trimmedHeading(line string) string {
	if !strings.HasPrefix(line, "## ") {
		return ""
	}
	return strings.TrimSpace(line[3:])
}

// ---- mailbox ----------------------------------------------------------------

// countLines counts the number of non-empty lines in a file.
func countLines(path string) int {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()

	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) != "" {
			count++
		}
	}
	return count
}

// ---- memory -----------------------------------------------------------------

// computeMemoryLabel mirrors bash's memory detection logic.
// It looks for MEMORY.md files under $HOME/.claude/projects/*/MEMORY.md.
func computeMemoryLabel(home string) string {
	memRoot := filepath.Join(home, ".claude", "projects")
	entries, err := os.ReadDir(memRoot)
	if err != nil {
		return "not configured"
	}

	count := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		memPath := filepath.Join(memRoot, e.Name(), "MEMORY.md")
		if _, err := os.Stat(memPath); err == nil {
			count++
		}
	}

	if count == 0 {
		return "not configured"
	}
	return fmt.Sprintf("%d MEMORY.md file(s) under ~/.claude/projects/", count)
}

// ---- kanban summary helper --------------------------------------------------

// KanbanSummary parses the kanban.md in currentDir and returns a one-line
// summary, or "" if the file does not exist.
func KanbanSummary(currentDir string) string {
	path := filepath.Join(currentDir, "kanban.md")
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()

	board, err := kanban.Parse(f)
	if err != nil {
		return ""
	}
	return board.Summary()
}

// ---- env helpers ------------------------------------------------------------

// osEnvMap builds a map from os.Environ() for use where Env is nil.
func osEnvMap() map[string]string {
	env := os.Environ()
	m := make(map[string]string, len(env))
	for _, kv := range env {
		if idx := strings.IndexByte(kv, '='); idx >= 0 {
			m[kv[:idx]] = kv[idx+1:]
		}
	}
	return m
}

// envGet retrieves a value from an env map, or "" if absent.
func envGet(env map[string]string, key string) string {
	if env == nil {
		return os.Getenv(key)
	}
	return env[key]
}
