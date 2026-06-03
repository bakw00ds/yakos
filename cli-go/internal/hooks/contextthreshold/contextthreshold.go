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
//   - claude: reads ~/.claude/projects/<encoded>/transcript-<session>.jsonl
//   - codex:  sizes the most-recent session dir under ~/.codex/sessions/
//   - agy:    sizes the most-recent .pb file under ~/.gemini/antigravity-cli/conversations/
//
// Telemetry hook — always exits 0.
package contextthreshold

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

const (
	hookName           = "context-threshold"
	defaultNoticePct   = 75
	defaultWarningPct  = 90
	defaultWindowClaude = 200_000
	defaultWindowCodex  = 256_000
	defaultWindowAgy    = 1_000_000
)

// Settings mirrors the context_thresholds section of ~/.yakos-state/settings.json.
type Settings struct {
	ContextThresholds struct {
		Notice  *int `json:"notice"`
		Warning *int `json:"warning"`
	} `json:"context_thresholds"`
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

	logFile := filepath.Join(h.WorkCurrentDir, "logs", hookName+".ndjson")
	noticePct, warningPct := h.loadThresholds()
	homeDir := h.homeDir()
	runtime := in.Env["YAKOS_RUNTIME"]
	if runtime == "" {
		runtime = "claude"
	}
	sessionID := in.Env["CLAUDE_SESSION_ID"]
	projectDir := in.Env["CLAUDE_PROJECT_DIR"]
	if projectDir == "" {
		projectDir = in.WorkDir
	}

	// Probe context usage.
	pct, probeErr := h.probeContextPct(runtime, sessionID, projectDir, homeDir)
	if probeErr != nil || pct < 0 {
		h.appendLog(&out, logFile, "REPORT", "probe_unavailable",
			"runtime="+runtime,
			map[string]any{"runtime": runtime})
		return out, nil
	}

	ts := h.NowFn().UTC().Format(time.RFC3339)

	if pct >= warningPct {
		// Auto-checkpoint.
		checkpointDir := filepath.Join(h.WorkCurrentDir, "checkpoints", ts)
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
		h.appendLog(&out, logFile, "WARN", "checked",
			fmt.Sprintf("pct=%d notice=%d warning=%d runtime=%s", pct, noticePct, warningPct, runtime),
			map[string]any{"pct": pct, "notice": noticePct, "warning": warningPct, "runtime": runtime, "checkpoint": checkpointDir})
	} else if pct >= noticePct {
		out.Stderr = fmt.Appendf(out.Stderr,
			"NOTE: context at %d%% (threshold %d%%). Recommend 'yakos compact now' or '/compact' before continuing.\n",
			pct, noticePct)
		h.appendLog(&out, logFile, "WARN", "checked",
			fmt.Sprintf("pct=%d notice=%d warning=%d runtime=%s", pct, noticePct, warningPct, runtime),
			map[string]any{"pct": pct, "notice": noticePct, "warning": warningPct, "runtime": runtime})
	} else {
		h.appendLog(&out, logFile, "REPORT", "checked",
			fmt.Sprintf("pct=%d notice=%d warning=%d runtime=%s", pct, noticePct, warningPct, runtime),
			map[string]any{"pct": pct, "notice": noticePct, "warning": warningPct, "runtime": runtime})
	}

	return out, nil
}

// probeContextPct returns the estimated context usage percentage (0–100) for
// the given runtime. Returns -1 on failure.
func (h *Hook) probeContextPct(runtime, sessionID, projectDir, homeDir string) (int, error) {
	switch runtime {
	case "claude":
		return h.probeClaude(sessionID, projectDir, homeDir)
	case "codex":
		return h.probeCodex(homeDir)
	case "agy", "gemini":
		return h.probeAgy(homeDir)
	}
	return -1, fmt.Errorf("unknown runtime %q", runtime)
}

// probeClaude estimates usage from the transcript JSONL file size.
func (h *Hook) probeClaude(sessionID, projectDir, homeDir string) (int, error) {
	if sessionID == "" || projectDir == "" {
		return -1, fmt.Errorf("session_id or project_dir missing")
	}
	encoded := encodeProjectPath(projectDir)
	transcript := filepath.Join(homeDir, ".claude", "projects", encoded,
		"transcript-"+sessionID+".jsonl")
	info, err := os.Stat(transcript)
	if err != nil {
		return -1, err
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
func (h *Hook) loadThresholds() (notice, warning int) {
	notice, warning = defaultNoticePct, defaultWarningPct
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
	return
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

// appendLog writes an NDJSON log entry.
func (h *Hook) appendLog(out *hooktype.HookOutput, logFile, severity, action, message string, extra map[string]any) {
	ts := h.NowFn().UTC().Format(time.RFC3339)
	entry := map[string]any{
		"ts":       ts,
		"hook":     hookName,
		"severity": severity,
		"action":   action,
		"message":  message,
	}
	for k, v := range extra {
		entry[k] = v
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(logFile), 0755); err != nil { //nolint:gosec
		return
	}
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644) //nolint:gosec
	if err != nil {
		return
	}
	defer f.Close() //nolint:errcheck
	_, _ = f.Write(data)
}

// ---- filesystem helpers ------------------------------------------------------

// encodeProjectPath encodes a project directory path the same way Claude does:
// replace path separators and colons with hyphens and strip leading slashes.
func encodeProjectPath(projectDir string) string {
	// Mirrors Claude's project encoding: remove leading slash, replace / with -.
	s := strings.TrimLeft(projectDir, "/")
	s = strings.ReplaceAll(s, "/", "-")
	return s
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
