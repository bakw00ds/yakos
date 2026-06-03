// Package session implements the Go port of `yakos session`.
//
// It manages session metadata for a yakOS project:
//
//   - list   <project>   — show work/current, archived sessions, and exports
//   - info   <project> <id>  — print details for one session-history entry (by index or ts prefix)
//   - resume <project> <id>  — emit the start flags to resume a session (prints args; caller execs)
//   - fork   <project> <id>  — emit the start flags to fork a session (prints args; caller execs)
//
// Source of truth for launched sessions is:
//
//	<home>/agent-control/<project>/work/current/.session-started-history.ndjson
//
// Each line is a JSON object with the schema from start.buildAuditEvent:
//
//	{ "type": "session_launched", "ts": "<ISO>", "project": "<name>",
//	  "repo": "<path>", "runtime": "<id>", "permission_mode": "<mode>",
//	  "agent_count": <n> }
//
// Atomic writes are used for any state-mutating operation (export sub-command
// from bash is NOT ported in this release; it is deferred pending tar/gzip
// plumbing scope approval).
package session

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
)

// Event is one line from .session-started-history.ndjson.
// Fields mirror the JSON schema written by start.buildAuditEvent.
type Event struct {
	Type           string `json:"type"`
	TS             string `json:"ts"`
	Project        string `json:"project"`
	Repo           string `json:"repo"`
	Runtime        string `json:"runtime"`
	PermissionMode string `json:"permission_mode"`
	AgentCount     int    `json:"agent_count"`
}

// ListResult holds the output of the list subcommand.
type ListResult struct {
	Project    string
	CurrentDir string
	HasCurrent bool
	// DecisionsMD size in bytes; -1 when file is absent.
	DecisionsMDBytes int64
	Archived         []string
	Exports          []string
}

// InfoResult holds the output of the info subcommand.
type InfoResult struct {
	Index  int
	Event  Event
	Source string // path to the .session-started-history.ndjson
}

// ResumeResult holds the suggested yakos-start flags for resuming a session.
type ResumeResult struct {
	Project string
	Runtime string
	// Args is the set of extra flags to pass to `yakos start`.
	// The caller is responsible for executing `yakos start <project> <args...>`.
	Args []string
}

// ForkResult holds the suggested yakos-start flags for forking a session.
type ForkResult struct {
	Project string
	Runtime string
	Args    []string
}

// Config controls all inputs to the session command.
type Config struct {
	// HomeDir defaults to os.Getenv("HOME").
	HomeDir string

	// Subcommand is one of: list, info, resume, fork.
	Subcommand string

	// Project is the agent-control project name.
	Project string

	// ID is an optional session identifier: a zero-based index (as string),
	// or a timestamp prefix matching a session-history entry.
	// Used by info, resume, fork.
	ID string

	// Writer is where human-readable output is written. Defaults to os.Stdout.
	Writer io.Writer

	// ErrWriter is where warnings are written. Defaults to os.Stderr.
	ErrWriter io.Writer
}

// Result is the union output of Run.
type Result struct {
	List   *ListResult
	Info   *InfoResult
	Resume *ResumeResult
	Fork   *ForkResult
}

// Run dispatches to the appropriate subcommand handler.
func Run(cfg Config) (*Result, error) {
	if cfg.HomeDir == "" {
		cfg.HomeDir = os.Getenv("HOME")
	}
	if cfg.HomeDir == "" {
		cfg.HomeDir = "/tmp"
	}
	if cfg.Writer == nil {
		cfg.Writer = os.Stdout
	}
	if cfg.ErrWriter == nil {
		cfg.ErrWriter = os.Stderr
	}

	switch cfg.Subcommand {
	case "list":
		return runList(cfg)
	case "info":
		return runInfo(cfg)
	case "resume":
		return runResume(cfg)
	case "fork":
		return runFork(cfg)
	case "":
		return nil, fmt.Errorf("session: subcommand required (list | info | resume | fork)")
	default:
		return nil, fmt.Errorf("session: unknown subcommand %q (try --help)", cfg.Subcommand)
	}
}

// ---- list -------------------------------------------------------------------

func runList(cfg Config) (*Result, error) {
	if cfg.Project == "" {
		return nil, fmt.Errorf("session list: <project> required")
	}
	controlDir := filepath.Join(cfg.HomeDir, "agent-control", cfg.Project)
	if _, err := os.Stat(controlDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("session list: project %q not found", cfg.Project)
	}

	res := &ListResult{Project: cfg.Project}

	// current
	currentDir := filepath.Join(controlDir, "work", "current")
	if fi, err := os.Stat(currentDir); err == nil && fi.IsDir() {
		res.HasCurrent = true
		res.CurrentDir = currentDir + string(os.PathSeparator)
		// decisions.md size
		decisionsPath := filepath.Join(currentDir, "decisions.md")
		if dfi, err := os.Stat(decisionsPath); err == nil {
			res.DecisionsMDBytes = dfi.Size()
		} else {
			res.DecisionsMDBytes = -1
		}
	}

	// archived
	archiveDir := filepath.Join(controlDir, "work", "archive")
	if entries, err := os.ReadDir(archiveDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				res.Archived = append(res.Archived, e.Name())
			}
		}
		sort.Strings(res.Archived)
	}

	// exports
	exportDir := filepath.Join(controlDir, "work", "exports")
	if entries, err := os.ReadDir(exportDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				res.Exports = append(res.Exports, e.Name())
			}
		}
		sort.Strings(res.Exports)
	}

	// print
	_, _ = fmt.Fprintf(cfg.Writer, "yakos session list — %s\n\n", cfg.Project)
	_, _ = fmt.Fprintln(cfg.Writer, "  current:")
	if res.HasCurrent {
		_, _ = fmt.Fprintf(cfg.Writer, "    %s\n", res.CurrentDir)
		if res.DecisionsMDBytes >= 0 {
			_, _ = fmt.Fprintf(cfg.Writer, "      decisions.md: %dB\n", res.DecisionsMDBytes)
		}
	} else {
		_, _ = fmt.Fprintln(cfg.Writer, "    (no current session directory)")
	}

	_, _ = fmt.Fprintln(cfg.Writer)
	_, _ = fmt.Fprintln(cfg.Writer, "  archived:")
	if len(res.Archived) == 0 {
		_, _ = fmt.Fprintln(cfg.Writer, "    (none yet)")
	} else {
		for _, a := range res.Archived {
			_, _ = fmt.Fprintf(cfg.Writer, "    %s\n", a)
		}
	}

	_, _ = fmt.Fprintln(cfg.Writer)
	_, _ = fmt.Fprintln(cfg.Writer, "  exports:")
	if len(res.Exports) == 0 {
		_, _ = fmt.Fprintln(cfg.Writer, "    (none yet)")
	} else {
		for _, e := range res.Exports {
			_, _ = fmt.Fprintf(cfg.Writer, "    %s\n", e)
		}
	}
	_, _ = fmt.Fprintln(cfg.Writer)

	return &Result{List: res}, nil
}

// ---- info -------------------------------------------------------------------

func runInfo(cfg Config) (*Result, error) {
	if cfg.Project == "" {
		return nil, fmt.Errorf("session info: <project> required")
	}
	controlDir := filepath.Join(cfg.HomeDir, "agent-control", cfg.Project)
	if _, err := os.Stat(controlDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("session info: project %q not found", cfg.Project)
	}

	histPath := historyPath(controlDir)
	events, err := readHistory(histPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("session info: reading history: %w", err)
	}

	if len(events) == 0 {
		return nil, fmt.Errorf("session info: no sessions recorded for project %q", cfg.Project)
	}

	ev, idx, err := resolveID(events, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("session info: %w", err)
	}

	res := &InfoResult{Index: idx, Event: ev, Source: histPath}

	_, _ = fmt.Fprintf(cfg.Writer, "session #%d\n", idx)
	_, _ = fmt.Fprintf(cfg.Writer, "  ts:              %s\n", ev.TS)
	_, _ = fmt.Fprintf(cfg.Writer, "  project:         %s\n", ev.Project)
	_, _ = fmt.Fprintf(cfg.Writer, "  repo:            %s\n", ev.Repo)
	_, _ = fmt.Fprintf(cfg.Writer, "  runtime:         %s\n", ev.Runtime)
	_, _ = fmt.Fprintf(cfg.Writer, "  permission_mode: %s\n", ev.PermissionMode)
	_, _ = fmt.Fprintf(cfg.Writer, "  agent_count:     %d\n", ev.AgentCount)

	return &Result{Info: res}, nil
}

// ---- resume -----------------------------------------------------------------

func runResume(cfg Config) (*Result, error) {
	if cfg.Project == "" {
		return nil, fmt.Errorf("session resume: <project> required")
	}
	controlDir := filepath.Join(cfg.HomeDir, "agent-control", cfg.Project)
	if _, err := os.Stat(controlDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("session resume: project %q not found", cfg.Project)
	}

	histPath := historyPath(controlDir)
	events, err := readHistory(histPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("session resume: reading history: %w", err)
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("session resume: no sessions recorded for project %q", cfg.Project)
	}

	ev, _, err := resolveID(events, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("session resume: %w", err)
	}

	// Build the yakos start args needed to resume this session.
	// The session ID for the runtime is the timestamp (used as a resume handle
	// for the claude and agy runtimes).
	args := []string{"--runtime", ev.Runtime, "--resume", ev.TS}
	if ev.PermissionMode == "safe" {
		args = append(args, "--safe")
	}

	res := &ResumeResult{
		Project: cfg.Project,
		Runtime: ev.Runtime,
		Args:    args,
	}

	_, _ = fmt.Fprintf(cfg.Writer, "# To resume session #%d (%s):\n", findIndex(events, ev.TS), ev.TS)
	_, _ = fmt.Fprintf(cfg.Writer, "yakos start %s", cfg.Project)
	for _, a := range args {
		_, _ = fmt.Fprintf(cfg.Writer, " %s", a)
	}
	_, _ = fmt.Fprintln(cfg.Writer)

	return &Result{Resume: res}, nil
}

// ---- fork -------------------------------------------------------------------

func runFork(cfg Config) (*Result, error) {
	if cfg.Project == "" {
		return nil, fmt.Errorf("session fork: <project> required")
	}
	controlDir := filepath.Join(cfg.HomeDir, "agent-control", cfg.Project)
	if _, err := os.Stat(controlDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("session fork: project %q not found", cfg.Project)
	}

	histPath := historyPath(controlDir)
	events, err := readHistory(histPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("session fork: reading history: %w", err)
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("session fork: no sessions recorded for project %q", cfg.Project)
	}

	ev, _, err := resolveID(events, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("session fork: %w", err)
	}

	args := []string{"--runtime", ev.Runtime, "--fork-session"}
	if ev.PermissionMode == "safe" {
		args = append(args, "--safe")
	}

	res := &ForkResult{
		Project: cfg.Project,
		Runtime: ev.Runtime,
		Args:    args,
	}

	_, _ = fmt.Fprintf(cfg.Writer, "# To fork from session #%d (%s):\n", findIndex(events, ev.TS), ev.TS)
	_, _ = fmt.Fprintf(cfg.Writer, "yakos start %s", cfg.Project)
	for _, a := range args {
		_, _ = fmt.Fprintf(cfg.Writer, " %s", a)
	}
	_, _ = fmt.Fprintln(cfg.Writer)

	return &Result{Fork: res}, nil
}

// ---- helpers ----------------------------------------------------------------

// historyPath returns the canonical path of the session-started-history NDJSON file.
func historyPath(controlDir string) string {
	return filepath.Join(controlDir, "work", "current", ".session-started-history.ndjson")
}

// readHistory reads all Event records from path.
// Returns an empty slice (and nil error) when the file does not exist.
// Malformed lines are silently skipped (matches bash's jq behaviour).
func readHistory(path string) ([]Event, error) {
	f, err := os.Open(path) //nolint:gosec
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var events []Event
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue // skip malformed lines
		}
		events = append(events, ev)
	}
	return events, scanner.Err()
}

// resolveID resolves a session ID string to an Event.
//
// Resolution order:
//  1. Empty ID → most-recent event (last entry).
//  2. Numeric string → zero-based index into events.
//  3. String prefix → first event whose TS starts with the prefix.
func resolveID(events []Event, id string) (Event, int, error) {
	if len(events) == 0 {
		return Event{}, 0, fmt.Errorf("no sessions recorded")
	}

	if id == "" {
		idx := len(events) - 1
		return events[idx], idx, nil
	}

	// Try numeric index first.
	if n, ok := parseUint(id); ok {
		if int(n) >= len(events) {
			return Event{}, 0, fmt.Errorf("session index %d out of range (0..%d)", n, len(events)-1)
		}
		return events[n], int(n), nil
	}

	// Prefix match on TS.
	for i, ev := range events {
		if strings.HasPrefix(ev.TS, id) {
			return ev, i, nil
		}
	}

	return Event{}, 0, fmt.Errorf("no session matching %q (use an index or a timestamp prefix)", id)
}

// findIndex returns the zero-based index of the event with the given ts in events.
// Returns -1 when not found.
func findIndex(events []Event, ts string) int {
	for i, ev := range events {
		if ev.TS == ts {
			return i
		}
	}
	return -1
}

// parseUint parses s as a non-negative integer.
func parseUint(s string) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	var n uint64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + uint64(c-'0')
	}
	return n, true
}

// ISONow returns the current UTC time in RFC3339 format.
// Exported for use in tests that need a reproducible timestamp.
func ISONow() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// PrintHelp writes the help text for `yakos session` to w, matching the
// bash session.sh usage() output and extending it with the new subcommands.
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos session <subcommand> [args...]

Subcommands:
  list <project>               List archived sessions and the current one.
  info <project> [<id>]        Print details for a session history entry.
                               <id> is a zero-based index or a timestamp
                               prefix. Defaults to the most-recent session.
  resume <project> [<id>]      Print the yakos start flags to resume a
                               recorded session. Defaults to most-recent.
  fork <project> [<id>]        Print the yakos start flags to fork from a
                               recorded session. Defaults to most-recent.

Session history is read from:
  ~/agent-control/<project>/work/current/.session-started-history.ndjson

Each line is a JSON object written by `+"`"+`yakos start`+"`"+` on every launch.

Examples:
  yakos session list myapp
  yakos session info myapp 0
  yakos session info myapp 2026-05-
  yakos session resume myapp
  yakos session fork myapp 3
`)
}
