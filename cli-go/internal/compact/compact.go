// Package compact is the Go port of cli/lib/compact.sh (rank 27).
//
// It gives operators manual control over context compaction and lets them
// view/configure the notice threshold at which the active session hints that
// compaction should happen.
//
// # Synopsis
//
//	yakos compact now              # print the /compact slash-command to paste
//	yakos compact threshold [N]    # show or set notice threshold (1-99)
//	yakos compact history          # show last 50 compaction log entries
//
// # State files
//
//	~/.yakos-state/settings.json        — stores context_thresholds.notice
//	~/.yakos-state/compact-log.ndjson   — append-only compaction history
//
// # Atomic writes
//
// settings.json is written via temp-rename (atomicWrite) following the Q8
// decision from the port plan.  compact-log.ndjson is append-only; appends
// use O_APPEND|O_CREATE|O_WRONLY which is safe for single-process use.
//
// # M3.1 note
//
// `compact now` currently prints the slash command for the operator to paste.
// Auto-send via tmux integration is deferred to M3.1 (same advisory as the
// bash source).
package compact

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

// defaultNoticeThreshold is the context-fill percentage at which yakos advises
// compaction.  Matches the bash default.
const defaultNoticeThreshold = 75

// defaultWarningThreshold is the hard-warning threshold.
const defaultWarningThreshold = 90

// maxHistoryLines is the number of history entries shown by `compact history`.
const maxHistoryLines = 50

// ---- public types -----------------------------------------------------------

// Config carries everything Run needs.
type Config struct {
	// Subcommand is one of: now, threshold, history, help.
	Subcommand string

	// ThresholdArg is the optional N argument for `compact threshold N`.
	// Empty string means "show"; a decimal integer string means "set".
	ThresholdArg string

	// Runtime is used when logging a `now` compaction event.
	// Defaults to the YAKOS_RUNTIME env var value, or "claude".
	Runtime string

	// HomeDir overrides os.Getenv("HOME") for path resolution.
	// When empty, os.Getenv("HOME") is used.
	HomeDir string

	// Now overrides time.Now() for deterministic tests.
	Now func() time.Time

	// Writer receives normal output (default: os.Stdout).
	Writer io.Writer

	// ErrWriter receives advisory/warning messages (default: os.Stderr).
	ErrWriter io.Writer
}

// Result summarises what Run did.
type Result struct {
	// Subcommand that was executed.
	Subcommand string

	// NoticeThreshold is the effective notice threshold after the operation.
	NoticeThreshold int

	// WarningThreshold is the effective warning threshold after the operation.
	WarningThreshold int

	// ThresholdSet is true when a threshold was written (set subcommand only).
	ThresholdSet bool

	// LogEntry is the NDJSON object that was appended (now subcommand only).
	// Empty when no append occurred.
	LogEntry string

	// HistoryLines are the raw lines returned by the history subcommand.
	HistoryLines []string
}

// ---- public API -------------------------------------------------------------

// Run executes the compact subcommand described by cfg.
func Run(cfg Config) (*Result, error) {
	home := cfg.HomeDir
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		home = "/tmp"
	}

	w := cfg.Writer
	if w == nil {
		w = os.Stdout
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	settingsPath := filepath.Join(home, ".yakos-state", "settings.json")
	logPath := filepath.Join(home, ".yakos-state", "compact-log.ndjson")

	res := &Result{Subcommand: cfg.Subcommand}

	switch cfg.Subcommand {
	case "now":
		return runNow(cfg, res, w, logPath, now)

	case "threshold":
		return runThreshold(cfg, res, w, settingsPath)

	case "history":
		return runHistory(res, w, logPath)

	case "help", "--help", "-h", "":
		PrintHelp(w)
		return res, nil

	default:
		return nil, fmt.Errorf("unknown subcommand %q (try 'yakos compact help')", cfg.Subcommand)
	}
}

// PrintHelp writes the help text to w.
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos compact — manage context compaction

  yakos compact now              # invoke runtime's /compact (currently prints; M3.1 will auto-send)
  yakos compact threshold [N]    # show or set notice threshold (default 75)
  yakos compact history          # past compactions
`)
}

// ---- subcommand implementations ---------------------------------------------

func runNow(cfg Config, res *Result, w io.Writer, logPath string, now func() time.Time) (*Result, error) {
	_, _ = fmt.Fprint(w, `yakos compact now — manual compaction.

Currently this prints the slash command for you to paste into the
active session. Future versions may auto-send via tmux integration.

For Claude Code:    /compact
For Codex:          /compact (verify in M3.1)
For agy:            /compact (verify in M3.1)

Recommendation: also run `+"`"+`yakos checkpoint now`+"`"+` first to snapshot
state if you want to be able to rewind from this point.
`)

	runtime := cfg.Runtime
	if runtime == "" {
		runtime = os.Getenv("YAKOS_RUNTIME")
	}
	if runtime == "" {
		runtime = "claude"
	}

	entry, err := appendCompactLog(logPath, "manual-recommend", "", runtime, now)
	if err != nil {
		// Non-fatal: log failure does not abort the command output.
		_, _ = fmt.Fprintf(cfg.ErrWriter, "compact: warning: could not write log: %v\n", err)
	}
	res.LogEntry = entry
	return res, nil
}

func runThreshold(cfg Config, res *Result, w io.Writer, settingsPath string) (*Result, error) {
	if cfg.ThresholdArg == "" {
		// Show current thresholds.
		notice, warning, err := readThresholds(settingsPath)
		if err != nil {
			// Gracefully fall back to defaults when settings are absent/corrupt.
			notice = defaultNoticeThreshold
			warning = defaultWarningThreshold
		}
		_, _ = fmt.Fprintf(w, "notice = %d%%, warning = %d%%\n", notice, warning)
		res.NoticeThreshold = notice
		res.WarningThreshold = warning
		return res, nil
	}

	// Validate the new threshold value.
	n, err := strconv.Atoi(cfg.ThresholdArg)
	if err != nil {
		return nil, fmt.Errorf("threshold must be a number (1-99); got %q", cfg.ThresholdArg)
	}
	if n < 1 || n > 99 {
		return nil, fmt.Errorf("threshold must be 1-99; got %d", n)
	}

	// Read existing settings, update, write back atomically.
	data, err := readSettingsJSON(settingsPath)
	if err != nil {
		data = map[string]interface{}{}
	}

	thresholds, _ := data["context_thresholds"].(map[string]interface{})
	if thresholds == nil {
		thresholds = map[string]interface{}{}
	}
	thresholds["notice"] = n
	data["context_thresholds"] = thresholds

	if err := writeSettingsJSON(settingsPath, data); err != nil {
		return nil, fmt.Errorf("write settings: %w", err)
	}

	_, _ = fmt.Fprintf(w, "notice threshold set to %d%%\n", n)
	res.NoticeThreshold = n
	res.WarningThreshold = defaultWarningThreshold
	res.ThresholdSet = true
	return res, nil
}

func runHistory(res *Result, w io.Writer, logPath string) (*Result, error) {
	f, err := os.Open(logPath) //nolint:gosec
	if os.IsNotExist(err) {
		_, _ = fmt.Fprintln(w, "(no compaction history)")
		return res, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open compact log: %w", err)
	}
	defer func() { _ = f.Close() }()

	// Read all lines, keep last maxHistoryLines, then format them.
	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan compact log: %w", err)
	}

	if len(lines) > maxHistoryLines {
		lines = lines[len(lines)-maxHistoryLines:]
	}

	var formatted []string
	for _, l := range lines {
		s := formatHistoryLine(l)
		formatted = append(formatted, s)
		_, _ = fmt.Fprintln(w, s)
	}
	res.HistoryLines = formatted
	return res, nil
}

// ---- helpers ----------------------------------------------------------------

// logRecord mirrors the NDJSON shape written by _log_compaction in compact.sh.
type logRecord struct {
	TS      string `json:"ts"`
	Kind    string `json:"kind"`
	Pct     string `json:"pct"`
	Runtime string `json:"runtime"`
	ByUser  string `json:"by_user"`
}

// appendCompactLog appends one NDJSON line to the log file and returns the
// serialised line.
func appendCompactLog(path, kind, pct, runtime string, now func() time.Time) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { //nolint:gosec
		return "", fmt.Errorf("mkdir: %w", err)
	}

	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("LOGNAME")
	}
	if user == "" {
		user = "unknown"
	}

	rec := logRecord{
		TS:      now().UTC().Format(time.RFC3339),
		Kind:    kind,
		Pct:     pct,
		Runtime: runtime,
		ByUser:  user,
	}

	b, err := json.Marshal(rec)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}
	line := string(b)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644) //nolint:gosec
	if err != nil {
		return "", fmt.Errorf("open log: %w", err)
	}
	defer func() { _ = f.Close() }()

	if _, err := fmt.Fprintf(f, "%s\n", line); err != nil {
		return "", fmt.Errorf("write log: %w", err)
	}
	return line, nil
}

// formatHistoryLine formats one raw NDJSON line from the compact log for
// human display.  Mirrors `jq -r '.ts + " " + .kind + " " + (.pct // "-") + " " + .runtime'`.
func formatHistoryLine(raw string) string {
	var rec logRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return raw // return as-is on parse failure
	}
	pct := rec.Pct
	if pct == "" {
		pct = "-"
	}
	return rec.TS + " " + rec.Kind + " " + pct + " " + rec.Runtime
}

// readThresholds reads context_thresholds from settings.json.
// Returns (notice, warning, error).
func readThresholds(settingsPath string) (notice, warning int, err error) {
	data, err := readSettingsJSON(settingsPath)
	if err != nil {
		return defaultNoticeThreshold, defaultWarningThreshold, err
	}
	thresholds, _ := data["context_thresholds"].(map[string]interface{})

	notice = defaultNoticeThreshold
	if v, ok := thresholds["notice"].(float64); ok {
		notice = int(v)
	}
	warning = defaultWarningThreshold
	if v, ok := thresholds["warning"].(float64); ok {
		warning = int(v)
	}
	return notice, warning, nil
}

// readSettingsJSON reads and unmarshals settings.json, returning an empty map
// when the file does not exist.
func readSettingsJSON(path string) (map[string]interface{}, error) {
	b, err := os.ReadFile(path) //nolint:gosec
	if os.IsNotExist(err) {
		return map[string]interface{}{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read settings: %w", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse settings: %w", err)
	}
	return m, nil
}

// writeSettingsJSON marshals m and writes it atomically (temp-rename, Q8).
func writeSettingsJSON(path string, m map[string]interface{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { //nolint:gosec
		return fmt.Errorf("mkdir: %w", err)
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0644); err != nil { //nolint:gosec
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}
