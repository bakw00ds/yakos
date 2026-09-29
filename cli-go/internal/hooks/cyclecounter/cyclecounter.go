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
// CycleLength defaults to 10 and can be overridden via Config.CycleLength,
// or via ~/.yakos-state/settings.json's .retro.cycle_length (StateDir),
// mirroring bash's own settings-file read — see loadSettings /
// settingsCycleLength / settingsAutoRetro below.
// AutoRetro defaults to true and can be overridden via Config.AutoRetro, or
// via settings.json's .retro.auto_dispatch: null/absent means true, any
// other value is taken as-is (boolean false and the string "false" both
// disable). K-89: bash used to read this with jq's `// true`, which treats
// a literal false as null, so `yakos retro disable` never took effect; both
// sides now use `if . == null then true else . end` semantics.
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

	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/hooklog"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

const (
	// DefaultCycleLength is the number of user prompts between auto-retros.
	DefaultCycleLength = 10

	// MaxCycleLength is the largest accepted cycle_length. Anything above it
	// (K-106: "absurdly large", e.g. 1e9) is treated as invalid and falls
	// back to DefaultCycleLength, matching bash's digit-count guard.
	MaxCycleLength = 100000

	hookName = "cycle-counter"
)

// Hook implements runner.Hook for the cycle-counter baseline logic.
type Hook struct {
	// WorkCurrentDir is the absolute path to work/current/ for the active
	// session. When empty, Run returns immediately (no active session).
	WorkCurrentDir string

	// StateDir is the yakOS state directory (bash's ~/.yakos-state/). When
	// non-empty and <StateDir>/settings.json exists and parses, its
	// .retro.cycle_length / .retro.auto_dispatch values override
	// CycleLength / AutoRetro for this run, mirroring bash's own
	// per-invocation settings-file read (cycle-counter.sh never caches
	// the setting either). Empty StateDir, or a missing/invalid file,
	// leaves CycleLength/AutoRetro as constructed — the same fallback
	// bash uses when the settings file is absent.
	StateDir string

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
func New(workCurrentDir, stateDir string) *Hook {
	return &Hook{
		WorkCurrentDir: workCurrentDir,
		StateDir:       stateDir,
		CycleLength:    DefaultCycleLength,
		AutoRetro:      true,
		NowFn:          time.Now,
	}
}

// Name returns the canonical hook name.
func (h *Hook) Name() string { return hookName }

// Run executes the cycle-counter logic.
func (h *Hook) Run(_ context.Context, in hooktype.HookInput) (hooktype.HookOutput, error) {
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
	autoRetro := h.AutoRetro

	// Operator-tunable cadence override, matching bash's own read of
	// ~/.yakos-state/settings.json on every invocation (not cached).
	if settings, ok := loadSettings(h.StateDir); ok {
		n, bad, present, valid := settingsCycleLength(settings)
		switch {
		case present && valid:
			cycleLen = n
		case present:
			// K-106: one WARN naming the bad value; keep the default.
			out.Stderr = fmt.Appendf(out.Stderr,
				"WARN: ignoring invalid retro.cycle_length %s (need an integer 1..%d); using default %d\n",
				displayBad(bad), MaxCycleLength, DefaultCycleLength)
		}
		autoRetro = settingsAutoRetro(settings)
	}
	// K-106: the guard MUST come after every assignment to cycleLen. It used
	// to run before the settings override, so a settings-supplied 0 reached
	// count%cycleLen and panicked (exit 2 = a blocking error).
	if cycleLen <= 0 {
		cycleLen = DefaultCycleLength
	}

	counterFile := filepath.Join(h.WorkCurrentDir, ".cycle-count")
	markerFile := filepath.Join(h.WorkCurrentDir, ".retro-due")

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
	if autoRetro && count%cycleLen == 0 {
		retroDue = true
		if err := atomicTouch(markerFile); err != nil {
			out.Stderr = fmt.Appendf(out.Stderr, "cycle-counter: touch .retro-due: %v\n", err)
		} else {
			out.Stderr = fmt.Appendf(out.Stderr,
				"NOTE: cycle %d — retrospective due. Lead should dispatch 'librarian' agent. (See rule:retrospective-discipline.)\n",
				count)
		}
	}

	// Write NDJSON log entry via the shared hooklog writer — field set/order
	// matches bash's ho_log exactly (ho_log "cycle-counter" REPORT "counted"
	// "cycle=$count cycle_length=$CYCLE_LENGTH retro_due=$retro_due" \
	// "{cycle, cycle_length, retro_due, auto_retro}").
	now := time.Now()
	if h.NowFn != nil {
		now = h.NowFn()
	}
	reason := fmt.Sprintf("cycle=%d cycle_length=%d retro_due=%t", count, cycleLen, retroDue)
	err := hooklog.Append(h.WorkCurrentDir, hooklog.Entry{
		Hook:      hookName,
		Severity:  "REPORT",
		Decision:  "counted",
		Reason:    reason,
		Agent:     senderRole(in),
		SessionID: hookio.PayloadString(in, "session_id"),
		Event:     in.Event,
		Extra: map[string]any{
			"cycle":        count,
			"cycle_length": cycleLen,
			"retro_due":    retroDue,
			"auto_retro":   autoRetro,
		},
	}, now)
	if err != nil {
		out.Stderr = fmt.Appendf(out.Stderr, "cycle-counter: log: %v\n", err)
	}

	return out, nil
}

// senderRole extracts the agent/role, matching hi_sender_role exactly:
// hi_field_or '.agent_type' 'lead' (top-level, fallback "lead" when
// absent/empty), trimmed, then the "yakos:" namespace prefix stripped.
func senderRole(in hooktype.HookInput) string {
	raw := hookio.PayloadString(in, "agent_type")
	if raw == "" {
		raw = "lead"
	}
	raw = strings.TrimSpace(raw)
	return strings.TrimPrefix(raw, "yakos:")
}

// ---- settings.json overrides --------------------------------------------------

// loadSettings reads and parses <stateDir>/settings.json, mirroring bash's
// `[ -f "$settings_file" ] && command -v jq >/dev/null` guard: a missing
// StateDir, missing file, or unparseable JSON all resolve to "no
// override" (ok=false) rather than an error — this is a best-effort,
// telemetry-hook read, never a hard dependency.
func loadSettings(stateDir string) (map[string]any, bool) {
	if stateDir == "" {
		return nil, false
	}
	data, err := os.ReadFile(filepath.Join(stateDir, "settings.json")) //nolint:gosec
	if err != nil {
		return nil, false
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, false
	}
	return m, true
}

// settingsCycleLength reads .retro.cycle_length the way bash does:
//
//	raw="$(jq -r '.retro.cycle_length | if . == null then "absent" else "v " + tostring end' ...)"
//
// present=false means null/absent (silently keep the default). Otherwise
// present=true and either valid=true with n in 1..MaxCycleLength, or bad
// holds the raw rendering (possibly "") of an unusable value for the WARN.
// Only digit strings of at most six characters are candidates; the value
// is parsed base-10 so "010" is 10 on both sides (bash needs 10#).
func settingsCycleLength(settings map[string]any) (n int, bad string, present, valid bool) {
	retro, _ := settings["retro"].(map[string]any)
	v := retro["cycle_length"]
	if v == nil {
		return 0, "", false, false
	}
	raw := hookio.JQRawOrJSON(v)
	if raw == "" || len(raw) > 6 {
		return 0, raw, true, false
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return 0, raw, true, false
		}
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > MaxCycleLength {
		return 0, raw, true, false
	}
	return n, "", true, true
}

// displayBad renders a rejected value for the WARN line: empty becomes "",
// newlines collapse to spaces, and it is cut to 40 bytes. Mirrors bash.
func displayBad(raw string) string {
	if raw == "" {
		return `""`
	}
	raw = strings.ReplaceAll(raw, "\n", " ")
	if len(raw) > 40 {
		raw = raw[:40]
	}
	return raw
}

// settingsAutoRetro reproduces:
//
//	val="$(jq -r 'if .retro.auto_dispatch == null then true else .retro.auto_dispatch end' "$settings_file")"
//	[ "$val" = "false" ] && auto_retro=false
//
// null/absent (or a non-object .retro, where jq errors and bash falls back
// to "true") yields true; boolean false and the string "false" both render
// as "false" and disable. This deliberately does NOT use hookio.JQAlt: jq's
// `//` treats boolean false as falsy, which was the K-89 bug.
func settingsAutoRetro(settings map[string]any) bool {
	retro, _ := settings["retro"].(map[string]any)
	v := retro["auto_dispatch"]
	if v == nil {
		return true
	}
	return hookio.JQRawOrJSON(v) != "false"
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
