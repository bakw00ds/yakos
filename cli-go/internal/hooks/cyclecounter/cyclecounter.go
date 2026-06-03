// Package cyclecounter is the Go-native Tier-0 port of lib/hooks/cycle-counter.sh.
//
// On every UserPromptSubmit event it:
//  1. Reads work/current/.cycle-count (or starts at 0).
//  2. Increments the counter atomically (temp-rename write, Q8).
//  3. If count % CycleLength == 0 AND auto-retro is enabled, touches
//     work/current/.retro-due (same atomicTouch used by internal/retro).
//  4. Emits a REPORT-level NDJSON log entry to work/current/logs/cycle-counter.ndjson.
//  5. Always exits 0 (telemetry hook, never blocks).
//
// CycleLength defaults to 10 and can be overridden via Config.CycleLength.
// AutoRetro defaults to true and can be overridden via Config.AutoRetro.
package cyclecounter

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

const (
	// DefaultCycleLength is the number of user prompts between auto-retros.
	DefaultCycleLength = 10

	hookName = "cycle-counter"
)

// Hook implements runner.Hook for the cycle-counter baseline logic.
type Hook struct {
	// WorkCurrentDir is the absolute path to work/current/ for the active
	// session. When empty, Run returns immediately (no active session).
	WorkCurrentDir string

	// CycleLength overrides the default 10-prompt cadence.
	// 0 means use DefaultCycleLength.
	CycleLength int

	// AutoRetro controls whether the .retro-due marker is written at cadence.
	// Default true. Set false to simulate `yakos retro disable`.
	AutoRetro bool

	// NowFn is injected for tests to control timestamps.
	NowFn func() time.Time
}

// New returns a Hook with sensible defaults.
func New(workCurrentDir string) *Hook {
	return &Hook{
		WorkCurrentDir: workCurrentDir,
		CycleLength:    DefaultCycleLength,
		AutoRetro:      true,
		NowFn:          time.Now,
	}
}

// Name returns the canonical hook name.
func (h *Hook) Name() string { return hookName }

// Run executes the cycle-counter logic.
func (h *Hook) Run(_ context.Context, _ hooktype.HookInput) (hooktype.HookOutput, error) {
	out := hooktype.HookOutput{ExitCode: 0}

	// No active session — no-op.
	if h.WorkCurrentDir == "" {
		return out, nil
	}
	if _, err := os.Stat(h.WorkCurrentDir); err != nil {
		return out, nil
	}

	cycleLen := h.CycleLength
	if cycleLen <= 0 {
		cycleLen = DefaultCycleLength
	}

	counterFile := filepath.Join(h.WorkCurrentDir, ".cycle-count")
	markerFile := filepath.Join(h.WorkCurrentDir, ".retro-due")
	logFile := filepath.Join(h.WorkCurrentDir, "logs", "cycle-counter.ndjson")

	// Read current count.
	count := readCount(counterFile)
	count++

	// Write incremented count atomically.
	if err := atomicWriteInt(counterFile, count); err != nil {
		// Log but don't fail — telemetry hook.
		out.Stderr = fmt.Appendf(out.Stderr, "cycle-counter: write count: %v\n", err)
	}

	// Emit .retro-due marker at cadence.
	retroDue := false
	if h.AutoRetro && count%cycleLen == 0 {
		retroDue = true
		if err := atomicTouch(markerFile); err != nil {
			out.Stderr = fmt.Appendf(out.Stderr, "cycle-counter: touch .retro-due: %v\n", err)
		} else {
			out.Stderr = fmt.Appendf(out.Stderr,
				"NOTE: cycle %d — retrospective due. Lead should dispatch 'librarian' agent. (See rule:retrospective-discipline.)\n",
				count)
		}
	}

	// Write NDJSON log entry (O_APPEND so concurrent writes don't corrupt).
	ts := h.NowFn().UTC().Format(time.RFC3339)
	entry := map[string]any{
		"ts":           ts,
		"hook":         hookName,
		"severity":     "REPORT",
		"action":       "counted",
		"cycle":        count,
		"cycle_length": cycleLen,
		"retro_due":    retroDue,
		"auto_retro":   h.AutoRetro,
	}
	if err := appendNDJSON(logFile, entry); err != nil {
		out.Stderr = fmt.Appendf(out.Stderr, "cycle-counter: log: %v\n", err)
	}

	return out, nil
}

// ---- helpers -----------------------------------------------------------------

// readCount reads .cycle-count, returning 0 on any error.
func readCount(path string) int {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return n
}

// atomicWriteInt writes n to path using a temp-rename (Q8).
func atomicWriteInt(path string, n int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { //nolint:gosec
		return err
	}
	tmp := path + ".tmp"
	data := []byte(strconv.Itoa(n) + "\n")
	if err := os.WriteFile(tmp, data, 0644); err != nil { //nolint:gosec
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// atomicTouch creates an empty file at path using temp-rename (Q8).
func atomicTouch(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { //nolint:gosec
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".yakos-cc-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

// appendNDJSON opens logFile with O_APPEND and writes a single JSON line.
func appendNDJSON(logFile string, entry map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(logFile), 0755); err != nil { //nolint:gosec
		return err
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644) //nolint:gosec
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck
	_, err = f.Write(data)
	return err
}
