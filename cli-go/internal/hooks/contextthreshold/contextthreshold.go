// Package contextthreshold is the Go-native Tier-0 port of
// lib/hooks/context-threshold.sh.
//
// Fires on UserPromptSubmit. Estimates the context window usage percentage and:
//   - At >= WARNING_PCT (default 90): WARN + write a checkpoint manifest to
//     work/current/checkpoints/<iso>/ with copies of key scratchpad files.
//   - At >= NOTICE_PCT (default 75): WARN log + recommend compact.
//   - Below NOTICE_PCT: REPORT log only.
//
// Thresholds are operator-tunable via ~/.yakos-state/settings.json:
//
//	{ "context_thresholds": { "notice": 75, "warning": 90 } }
//
// Per-runtime probe strategy (bytes/4 ≈ tokens, same as bash original):
//   - claude: reads ~/.claude/projects/<encoded>/<session>.jsonl
//   - codex:  sizes the most-recent session dir under ~/.codex/sessions/
//   - agy:    sizes the most-recent .pb file under ~/.gemini/antigravity-cli/conversations/
//
// Telemetry hook — always exits 0.
package contextthreshold

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/hooklog"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

const (
	hookName = "context-threshold"

	defaultNoticePct  = 75
	defaultWarningPct = 90

	// DefaultCompactPct is the suggested auto-compact threshold (opt-in).
	// Sits between notice (75%) and warning (90%), giving the compact a
	// chance to run before the warning checkpoint fires.
	DefaultCompactPct = 85

	defaultWindowClaude = 200_000
	defaultWindowCodex  = 256_000
	defaultWindowAgy    = 1_000_000

	// CompactPendingMarker is the filename written by context-threshold to
	// work/current/ when the auto-compact threshold is crossed. The
	// auto-compact-trigger Stop hook reads this marker and injects "/compact".
	CompactPendingMarker = ".compact-pending"
)

// Settings mirrors the context_thresholds section of ~/.yakos-state/settings.json.
type Settings struct {
	ContextThresholds struct {
		Notice  *int `json:"notice"`
		Warning *int `json:"warning"`
		Auto    *int `json:"auto"`
	} `json:"context_thresholds"`
	CompactAutoDisabled bool `json:"compact_auto_disabled"`
}

// Hook implements runner.Hook for context threshold monitoring.
type Hook struct {
	// WorkCurrentDir is the absolute path to work/current/.
	WorkCurrentDir string

	// StateDir is the path to ~/.yakos-state/ for settings.json reads.
	// When empty, defaults to $HOME/.yakos-state/.
	StateDir string

	// HomeDir is used to locate runtime transcript directories.
	// When empty, defaults to $HOME.
	HomeDir string

	// NowFn is injected for tests.
	NowFn func() time.Time

	// MkdirAll is injected for tests (default: os.MkdirAll).
	MkdirAll func(path string, perm os.FileMode) error

	// WriteFile is injected for tests (default: os.WriteFile).
	WriteFile func(name string, data []byte, perm os.FileMode) error
}

// New returns a Hook with sensible defaults.
func New(workCurrentDir string) *Hook {
	return &Hook{
		WorkCurrentDir: workCurrentDir,
		NowFn:          time.Now,
	}
}

// Name returns the canonical hook name.
func (h *Hook) Name() string { return hookName }

// Run executes the context threshold logic. Always returns ExitCode 0.
func (h *Hook) Run(_ context.Context, in hooktype.HookInput) (hooktype.HookOutput, error) {
	out := hooktype.HookOutput{ExitCode: 0}

	if h.WorkCurrentDir == "" {
		return out, nil
	}
	if _, err := os.Stat(h.WorkCurrentDir); err != nil {
		return out, nil
	}

	noticePct, warningPct, compactPct, autoDisabled := h.loadThresholds()
	homeDir := h.homeDir()
	runtime := in.Env["YAKOS_RUNTIME"]
	if runtime == "" {
		runtime = "claude"
	}
	sessionID := hookio.SessionID(in) // bash hi_session_id: payload, not env
	projectDir := in.Env["CLAUDE_PROJECT_DIR"]
	if projectDir == "" {
		projectDir = in.WorkDir
	}

	// Probe context usage.
	pct, probeErr := h.probeContextPct(runtime, sessionID, projectDir, homeDir, hookio.PayloadString(in, "transcript_path"))
	if probeErr != nil || pct < 0 {
		h.appendLog(in, "REPORT", "probe_unavailable",
			"runtime="+runtime,
			map[string]any{"runtime": runtime})
		return out, nil
	}

	ts := h.NowFn().UTC().Format(time.RFC3339)

	// Write the .compact-pending marker when crossing the auto-compact threshold.
	// This is opt-in: compactPct == 0 means the operator has not enabled auto-compact.
	// The marker is written atomically (temp-rename) and consumed by the Stop hook
	// (autocompacttrigger) which injects "/compact" as the next Claude Code turn.
	if !autoDisabled && compactPct > 0 && pct >= compactPct {
		h.writeCompactPendingMarker()
	}

	if pct >= warningPct {
		// Auto-checkpoint.
		// Replace colons in the ISO timestamp so the directory name is legal on
		// Windows (colons are forbidden in path components there).
		checkpointDirName := strings.ReplaceAll(ts, ":", "-")
		checkpointDir := filepath.Join(h.WorkCurrentDir, "checkpoints", checkpointDirName)
		if err := os.MkdirAll(checkpointDir, 0755); err == nil { //nolint:gosec
			for _, f := range []string{"plan.md", "contracts.md", "decisions.md", "status.md"} {
				src := filepath.Join(h.WorkCurrentDir, f)
				if _, statErr := os.Stat(src); statErr == nil {
					if data, readErr := os.ReadFile(src); readErr == nil { //nolint:gosec
						_ = os.WriteFile(filepath.Join(checkpointDir, f), data, 0644) //nolint:gosec
					}
				}
			}
			manifest := fmt.Sprintf("checkpoint: %s\ncontext_pct: %d\nsession_id: %s\nruntime: %s\ncreated: %s\n",
				filepath.Base(checkpointDir), pct, sessionID, runtime, ts)
			_ = os.WriteFile(filepath.Join(checkpointDir, "manifest.txt"), []byte(manifest), 0644) //nolint:gosec
		}
		out.Stderr = fmt.Appendf(out.Stderr,
			"WARN: context at %d%% (threshold %d%%). Auto-checkpoint created at %s. Recommend 'yakos compact now' or '/compact'.\n",
			pct, warningPct, checkpointDir)
		h.appendChecked(in, "WARN", pct, noticePct, warningPct, compactPct, runtime)
	} else if pct >= noticePct {
		out.Stderr = fmt.Appendf(out.Stderr,
			"NOTE: context at %d%% (threshold %d%%). Recommend 'yakos compact now' or '/compact' before continuing.\n",
			pct, noticePct)
		h.appendChecked(in, "WARN", pct, noticePct, warningPct, compactPct, runtime)
	} else {
		h.appendChecked(in, "REPORT", pct, noticePct, warningPct, compactPct, runtime)
	}

	return out, nil
}

// probeContextPct returns the estimated context usage percentage (0–100) for
// the given runtime. Returns -1 on failure.
func (h *Hook) probeContextPct(runtime, sessionID, projectDir, homeDir, transcriptPath string) (int, error) {
	switch runtime {
	case "claude":
		return h.probeClaude(sessionID, projectDir, homeDir, transcriptPath)
	case "codex":
		return h.probeCodex(homeDir)
	case "agy", "gemini":
		return h.probeAgy(homeDir)
	}
	return -1, fmt.Errorf("unknown runtime %q", runtime)
}

// probeClaude estimates usage from the transcript JSONL file size.
func (h *Hook) probeClaude(sessionID, projectDir, homeDir, transcriptPath string) (int, error) {
	// K-110: the payload's transcript_path is the real path; prefer it when it
	// names a regular file. Bash twin: _probe_context_pct_claude.
	var info os.FileInfo
	if transcriptPath != "" {
		if fi, err := os.Stat(transcriptPath); err == nil && fi.Mode().IsRegular() {
			info = fi
		}
	}
	if info == nil {
		if sessionID == "" || projectDir == "" {
			return -1, fmt.Errorf("session_id or project_dir missing")
		}
		encoded := encodeProjectPath(projectDir)
		// K-112: Claude Code names the file <session>.jsonl; "transcript-<session>"
		// never existed, so this probe always failed.
		transcript := filepath.Join(homeDir, ".claude", "projects", encoded,
			sessionID+".jsonl")
		fi, err := os.Stat(transcript)
		if err != nil {
			return -1, err
		}
		info = fi
	}
	estimatedTokens := info.Size() / 4
	pct := int(estimatedTokens * 100 / defaultWindowClaude)
	if pct > 100 {
		pct = 100
	}
	return pct, nil
}

// probeCodex estimates from the most-recent session dir size.
func (h *Hook) probeCodex(homeDir string) (int, error) {
	sessionsRoot := filepath.Join(homeDir, ".codex", "sessions")
	latest, err := latestEntryByMtime(sessionsRoot, true)
	if err != nil {
		return -1, err
	}
	size, err := dirSize(latest)
	if err != nil || size <= 0 {
		return -1, fmt.Errorf("codex: empty session dir")
	}
	estimatedTokens := size / 4
	pct := int(estimatedTokens * 100 / defaultWindowCodex)
	if pct > 100 {
		pct = 100
	}
	return pct, nil
}

// probeAgy estimates from the most-recent .pb conversation file size.
func (h *Hook) probeAgy(homeDir string) (int, error) {
	convRoot := filepath.Join(homeDir, ".gemini", "antigravity-cli", "conversations")
	latest, err := latestEntryByMtime(convRoot, false)
	if err != nil {
		return -1, err
	}
	info, err := os.Stat(latest)
	if err != nil || info.Size() <= 0 {
		return -1, fmt.Errorf("agy: empty .pb file")
	}
	estimatedTokens := info.Size() / 4
	pct := int(estimatedTokens * 100 / defaultWindowAgy)
	if pct > 100 {
		pct = 100
	}
	return pct, nil
}

// loadThresholds reads thresholds from ~/.yakos-state/settings.json.
// Returns (notice, warning, compact, autoDisabled).
// compact == 0 means auto-compact is not configured (opt-in default OFF).
func (h *Hook) loadThresholds() (notice, warning, compact int, autoDisabled bool) {
	notice, warning, compact = defaultNoticePct, defaultWarningPct, 0
	stateDir := h.StateDir
	if stateDir == "" {
		stateDir = filepath.Join(h.homeDir(), ".yakos-state")
	}
	settingsFile := filepath.Join(stateDir, "settings.json")
	data, err := os.ReadFile(settingsFile) //nolint:gosec
	if err != nil {
		return
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return
	}
	if s.ContextThresholds.Notice != nil {
		notice = *s.ContextThresholds.Notice
	}
	if s.ContextThresholds.Warning != nil {
		warning = *s.ContextThresholds.Warning
	}
	if s.ContextThresholds.Auto != nil {
		compact = *s.ContextThresholds.Auto
	}
	autoDisabled = s.CompactAutoDisabled
	return
}

// writeCompactPendingMarker writes work/current/.compact-pending atomically
// (temp-rename, Q8). The auto-compact-trigger Stop hook reads this marker and
// injects "/compact" as the next Claude Code turn.
func (h *Hook) writeCompactPendingMarker() {
	if h.WorkCurrentDir == "" {
		return
	}
	mkdirAll := h.MkdirAll
	if mkdirAll == nil {
		mkdirAll = os.MkdirAll
	}
	writeFile := h.WriteFile
	if writeFile == nil {
		writeFile = os.WriteFile
	}
	if err := mkdirAll(h.WorkCurrentDir, 0755); err != nil { //nolint:gosec
		return
	}
	markerPath := filepath.Join(h.WorkCurrentDir, CompactPendingMarker)
	tmp := markerPath + ".tmp"
	if err := writeFile(tmp, []byte(h.NowFn().UTC().Format(time.RFC3339)+"\n"), 0644); err != nil { //nolint:gosec
		return
	}
	_ = os.Rename(tmp, markerPath)
}

func (h *Hook) homeDir() string {
	if h.HomeDir != "" {
		return h.HomeDir
	}
	if home := os.Getenv("HOME"); home != "" {
		return home
	}
	return "."
}

// appendChecked writes the "checked" record. Bash's record carries
// pct/notice/warning/runtime only; auto-compact is a Go-only opt-in, so its
// threshold is added (to reason and extra) only when it is enabled, which keeps
// the default record byte-for-byte what bash writes.
func (h *Hook) appendChecked(in hooktype.HookInput, severity string, pct, noticePct, warningPct, compactPct int, runtime string) {
	reason := fmt.Sprintf("pct=%d notice=%d warning=%d runtime=%s", pct, noticePct, warningPct, runtime)
	extra := map[string]any{"pct": pct, "notice": noticePct, "warning": warningPct, "runtime": runtime}
	if compactPct > 0 {
		reason = fmt.Sprintf("pct=%d notice=%d warning=%d auto=%d runtime=%s", pct, noticePct, warningPct, compactPct, runtime)
		extra["auto"] = compactPct
	}
	h.appendLog(in, severity, "checked", reason, extra)
}

// appendLog writes an NDJSON log record through the shared hooklog writer, so
// the field set and order are bash ho_log's: {ts, hook, severity, decision,
// reason, agent, session_id, event} + extra (K-107; it used to write
// action/message and no agent/session_id/event).
func (h *Hook) appendLog(in hooktype.HookInput, severity, decision, reason string, extra map[string]any) {
	// Best effort, like bash's `ho_log ... || true`: a log failure never changes
	// the hook's decision or its stderr.
	_ = hooklog.Append(h.WorkCurrentDir, hooklog.Entry{
		Hook:      hookName,
		Severity:  severity,
		Decision:  decision,
		Reason:    reason,
		Agent:     senderRole(in),
		SessionID: hookio.SessionID(in),
		Event:     in.Event,
		Extra:     extra,
	}, h.NowFn())
}

// senderRole is bash hi_sender_role.
func senderRole(in hooktype.HookInput) string { return hookio.SenderRole(in) }

// ---- filesystem helpers ------------------------------------------------------

// encodeProjectPath encodes a project directory path the way bash's
// ct_encode_project_path (lib/compat.sh) does, which is how Claude Code names
// ~/.claude/projects/<encoded>/: '/' and '.' become '-', one trailing '/' is
// dropped, and the leading '/' becomes a leading '-'
// (/Users/tw/code/panda-os-3.0 -> -Users-tw-code-panda-os-3-0). K-107: this used
// to strip the leading '-' and keep dots, so Go never found the transcript bash
// finds. A Windows drive colon (never present in bash's Unix paths) also
// becomes '-'.
func encodeProjectPath(projectDir string) string {
	s := filepath.ToSlash(projectDir)
	if !strings.HasPrefix(s, "/") && !hasDriveLetter(s) {
		if abs, err := filepath.Abs(projectDir); err == nil {
			s = filepath.ToSlash(abs)
		}
	}
	if runtime.GOOS == "windows" {
		s = strings.ReplaceAll(s, ":", "-")
	}
	s = strings.TrimSuffix(s, "/")
	s = strings.ReplaceAll(s, "/", "-")
	return strings.ReplaceAll(s, ".", "-")
}

func hasDriveLetter(s string) bool {
	return len(s) >= 2 && s[1] == ':' && ((s[0] >= 'A' && s[0] <= 'Z') || (s[0] >= 'a' && s[0] <= 'z'))
}

type mtimeEntry struct {
	path  string
	mtime time.Time
}

// latestEntryByMtime returns the most-recently-modified entry in dir.
// If dirs=true it looks at subdirectories; otherwise at files.
func latestEntryByMtime(dir string, dirs bool) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var candidates []mtimeEntry
	for _, e := range entries {
		if dirs && !e.IsDir() {
			continue
		}
		if !dirs && e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		candidates = append(candidates, mtimeEntry{
			path:  filepath.Join(dir, e.Name()),
			mtime: info.ModTime(),
		})
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("no entries in %s", dir)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].mtime.After(candidates[j].mtime)
	})
	return candidates[0].path, nil
}

// dirSize returns the total byte size of all regular files in dir (non-recursive).
func dirSize(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			info, err := d.Info()
			if err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total, err
}
