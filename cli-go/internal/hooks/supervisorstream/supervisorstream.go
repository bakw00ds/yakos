// Package supervisorstream is the Go-native Tier-0 port of
// lib/hooks/supervisor-stream.sh.
//
// PostToolUse hook on Edit, Write, MultiEdit, and Bash.
// Implements the pre-filter stage of the supervisor redesign (Option 2):
//
//  1. Appends a compact event to work/current/supervisor-buffer.ndjson
//     (rolling 50-line window).
//  2. Runs a local pre-filter. Pre-filter triggers:
//     a. sensitive-path: file matches a deny glob in .claude/path-allowlist.json
//     b. large-diff: new_string/content line count > min_diff_lines (default 20)
//     c. out-of-scope: touched file not referenced in decisions.md or plan.md
//     d. risk-regex: content matches a dangerous-command pattern
//  3. If no trigger fires → buffer-only, no counter tick, exit 0.
//  4. If a trigger fires → increment escalation counter in
//     work/current/.supervisor-counter.
//  5. Every score_every_n_calls (default 10) escalations → write a
//     "dispatch-ready" marker to work/current/.supervisor-dispatch-ready so
//     that the Tier-2 bash hook or the lead monitor can invoke the supervisor.
//     (The Go binary does not fork a subprocess. See package doc.)
//
// Never blocks. Always exits 0. This is telemetry, not policy.
package supervisorstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

const (
	hookName            = "supervisor-stream"
	defaultMinDiffLines = 20
	defaultScoreEvery   = 10
	bufferMaxLines      = 50
)

// built-in risk-regex patterns (case-insensitive)
var defaultRiskPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)drop\s+table`),
	regexp.MustCompile(`(?i)force.*push`),
	regexp.MustCompile(`(?i)rm\s+-rf`),
	regexp.MustCompile(`(?i)chmod\s+777`),
	regexp.MustCompile(`(?i)(password|secret|api_key|token)\s*=\s*[^$({][^\s]{8,}`),
}

// yakosYMLSupervisor holds the shape needed from .yakos.yml.
type yakosYMLSupervisor struct {
	Supervisor *supervisorConfig `yaml:"supervisor"`
}

type supervisorConfig struct {
	Enabled     *bool            `yaml:"enabled"`
	Model       string           `yaml:"model"`
	Runtime     string           `yaml:"runtime"`
	Agent       string           `yaml:"agent"`
	ScoreEveryN *int             `yaml:"score_every_n_calls"`
	PreFilter   *preFilterConfig `yaml:"pre_filter"`
}

type preFilterConfig struct {
	Enabled      *bool    `yaml:"enabled"`
	MinDiffLines *int     `yaml:"min_diff_lines"`
	RiskRegex    []string `yaml:"risk_regex"`
}

// Hook implements runner.Hook for supervisor stream.
type Hook struct {
	// WorkCurrentDir is the absolute path to work/current/.
	WorkCurrentDir string

	// ProjectDir is the project root for .yakos.yml and path-allowlist.json.
	// When empty, uses CLAUDE_PROJECT_DIR env var.
	ProjectDir string

	// NowFn is injected for tests.
	NowFn func() time.Time
}

// New returns a Hook with sensible defaults.
func New(workCurrentDir, projectDir string) *Hook {
	return &Hook{
		WorkCurrentDir: workCurrentDir,
		ProjectDir:     projectDir,
		NowFn:          time.Now,
	}
}

// Name returns the canonical hook name.
func (h *Hook) Name() string { return hookName }

// Run executes the supervisor-stream logic. Always returns ExitCode 0.
func (h *Hook) Run(_ context.Context, in hooktype.HookInput) (hooktype.HookOutput, error) {
	out := hooktype.HookOutput{ExitCode: 0}

	// Env bypass.
	if in.Env["YAKOS_SUPERVISOR_DISABLE"] == "1" {
		return out, nil
	}

	// Tool filter.
	switch in.Tool {
	case "Edit", "Write", "MultiEdit", "Bash":
	default:
		return out, nil
	}

	if h.WorkCurrentDir == "" {
		return out, nil
	}

	projectDir := h.resolveProjectDir(in)

	// Config disable check.
	cfg := h.loadConfig(projectDir)
	if cfg != nil && cfg.Enabled != nil && !*cfg.Enabled {
		return out, nil
	}

	logFile := filepath.Join(h.WorkCurrentDir, "logs", hookName+".ndjson")
	bufferFile := filepath.Join(h.WorkCurrentDir, "supervisor-buffer.ndjson")
	counterFile := filepath.Join(h.WorkCurrentDir, ".supervisor-counter")

	agentType := senderRole(in)
	filePath := fileFromPayload(in)
	ts := h.NowFn().UTC().Format(time.RFC3339)
	sessionID := in.Env["CLAUDE_SESSION_ID"]

	// Truncated previews to prevent buffer bloat.
	newPreview := truncate(stringField(in.Payload, "new_string"), 300)
	contentPreview := truncate(stringField(in.Payload, "content"), 300)

	// Build event JSON.
	event := map[string]any{
		"ts":    ts,
		"agent": agentType,
		"tool":  in.Tool,
		"input": map[string]any{
			"file_path":       filePath,
			"new_preview":     nilIfEmpty(newPreview),
			"content_preview": nilIfEmpty(contentPreview),
		},
		"session_id": sessionID,
	}

	// ---- 1. Append to buffer (rolling 50-line window) ----
	if err := appendBufferLine(bufferFile, event); err != nil {
		out.Stderr = fmt.Appendf(out.Stderr, "%s: buffer append: %v\n", hookName, err)
	}
	trimBuffer(bufferFile, bufferMaxLines)

	// ---- 2. Pre-filter ----
	pfEnabled := true
	minDiffLines := defaultMinDiffLines
	var extraRiskPatterns []string
	if cfg != nil && cfg.PreFilter != nil {
		if cfg.PreFilter.Enabled != nil && !*cfg.PreFilter.Enabled {
			pfEnabled = false
		}
		if cfg.PreFilter.MinDiffLines != nil {
			minDiffLines = *cfg.PreFilter.MinDiffLines
		}
		extraRiskPatterns = cfg.PreFilter.RiskRegex
	}

	if !pfEnabled {
		// Pre-filter disabled: every mutation counts.
		h.appendLog(&out, logFile, "REPORT", "pass",
			"pre-filter disabled; counting toward score threshold",
			map[string]any{"pre_filter": "disabled", "tool": in.Tool, "file": filePath})
		// Fall through to counter logic.
	} else {
		escalateReason := ""

		// 2a. Sensitive-path check.
		if escalateReason == "" && filePath != "" {
			escalateReason = h.checkSensitivePath(filePath, projectDir)
		}

		// 2b. Diff-size check.
		if escalateReason == "" {
			combined := newPreview + contentPreview
			if combined != "" {
				lineCount := strings.Count(combined, "\n") + 1
				if lineCount > minDiffLines {
					escalateReason = fmt.Sprintf("large-diff:%d-lines", lineCount)
				}
			}
		}

		// 2c. Out-of-scope check.
		if escalateReason == "" && filePath != "" {
			escalateReason = h.checkOutOfScope(filePath, projectDir)
		}

		// 2d. Risk-regex check.
		if escalateReason == "" {
			combined := newPreview + contentPreview
			if combined != "" {
				escalateReason = h.checkRiskRegex(combined, extraRiskPatterns)
			}
		}

		if escalateReason == "" {
			// No trigger — buffer-only, no counter tick.
			h.appendLog(&out, logFile, "REPORT", "pass",
				"pre-filter: no trigger; buffered without dispatch",
				map[string]any{"pre_filter": "pass", "tool": in.Tool, "file": filePath})
			return out, nil
		}

		// Trigger fired.
		h.appendLog(&out, logFile, "REPORT", "pass",
			"pre-filter: ESCALATE ("+escalateReason+"); counting toward score threshold",
			map[string]any{"pre_filter": "escalate", "trigger": escalateReason, "tool": in.Tool, "file": filePath})
	}

	// ---- 3. Increment escalation counter ----
	cur := readCounter(counterFile) + 1
	writeCounter(counterFile, cur)

	// ---- 4. Every N escalations → write dispatch-ready marker ----
	scoreEvery := defaultScoreEvery
	if cfg != nil && cfg.ScoreEveryN != nil {
		scoreEvery = *cfg.ScoreEveryN
	}
	if scoreEvery <= 0 {
		scoreEvery = defaultScoreEvery
	}

	if cur%scoreEvery != 0 {
		h.appendLog(&out, logFile, "REPORT", "pass",
			"escalation buffered; not yet at score-every threshold",
			map[string]any{"counter": cur, "score_every": scoreEvery, "will_score": false})
		return out, nil
	}

	// Threshold hit — write dispatch-ready marker.
	dispatchMarker := filepath.Join(h.WorkCurrentDir, ".supervisor-dispatch-ready")
	_ = atomicTouch(dispatchMarker)

	h.appendLog(&out, logFile, "REPORT", "pass",
		"escalation score threshold hit; dispatch-ready marker written (async scoring expected from Tier-2 or lead monitor)",
		map[string]any{"counter": cur, "score_every": scoreEvery, "will_score": true, "marker": dispatchMarker})

	return out, nil
}

// ---- pre-filter checks -------------------------------------------------------

// checkSensitivePath returns an escalation reason if filePath matches a deny
// glob in .claude/path-allowlist.json under the "lead" key.
func (h *Hook) checkSensitivePath(filePath, projectDir string) string {
	allowlistFile := filepath.Join(projectDir, ".claude", "path-allowlist.json")
	data, err := os.ReadFile(allowlistFile) //nolint:gosec
	if err != nil {
		return ""
	}
	var policies map[string]struct {
		Deny []string `json:"deny"`
	}
	if err := json.Unmarshal(data, &policies); err != nil {
		return ""
	}
	leadPolicy, ok := policies["lead"]
	if !ok {
		return ""
	}
	for _, glob := range leadPolicy.Deny {
		if globMatch(glob, filePath) {
			return "sensitive-path:" + glob
		}
	}
	return ""
}

// checkOutOfScope returns an escalation reason if filePath is not referenced
// in decisions.md or plan.md.
func (h *Hook) checkOutOfScope(filePath, projectDir string) string {
	decisions := filepath.Join(h.WorkCurrentDir, "decisions.md")
	plan := filepath.Join(h.WorkCurrentDir, "plan.md")

	// Only apply when at least one reference file exists.
	_, dErr := os.Stat(decisions)
	_, pErr := os.Stat(plan)
	if dErr != nil && pErr != nil {
		return "" // no reference files
	}

	baseName := filepath.Base(filePath)
	relFile := filePath
	if projectDir != "" && strings.HasPrefix(filePath, projectDir+"/") {
		relFile = filePath[len(projectDir)+1:]
	}

	for _, ref := range []string{decisions, plan} {
		if _, err := os.Stat(ref); err != nil {
			continue
		}
		data, err := os.ReadFile(ref) //nolint:gosec
		if err != nil {
			continue
		}
		content := string(data)
		if strings.Contains(content, baseName) || strings.Contains(content, relFile) {
			return "" // found in scope
		}
	}
	return "out-of-scope:" + baseName
}

// checkRiskRegex returns an escalation reason if combined text matches any
// built-in or extra risk patterns.
func (h *Hook) checkRiskRegex(combined string, extras []string) string {
	for _, re := range defaultRiskPatterns {
		if re.MatchString(combined) {
			return "risk-regex:" + re.String()
		}
	}
	for _, pat := range extras {
		re, err := regexp.Compile("(?i)" + pat)
		if err != nil {
			continue
		}
		if re.MatchString(combined) {
			return "risk-regex:" + pat
		}
	}
	return ""
}

// ---- config loading ----------------------------------------------------------

func (h *Hook) loadConfig(projectDir string) *supervisorConfig {
	if projectDir == "" {
		return nil
	}
	yakosYML := filepath.Join(projectDir, ".yakos.yml")
	data, err := os.ReadFile(yakosYML) //nolint:gosec
	if err != nil {
		return nil
	}
	var doc yakosYMLSupervisor
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil
	}
	return doc.Supervisor
}

// ---- buffer management -------------------------------------------------------

func appendBufferLine(bufferFile string, event map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(bufferFile), 0755); err != nil { //nolint:gosec
		return err
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.OpenFile(bufferFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644) //nolint:gosec
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck
	_, err = f.Write(data)
	return err
}

// trimBuffer keeps only the last maxLines lines of bufferFile.
func trimBuffer(bufferFile string, maxLines int) {
	data, err := os.ReadFile(bufferFile) //nolint:gosec
	if err != nil {
		return
	}
	lines := bytes.Split(data, []byte("\n"))
	// Drop empty trailing entry produced by trailing newline.
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	if len(lines) <= maxLines {
		return
	}
	keep := lines[len(lines)-maxLines:]
	var buf bytes.Buffer
	for _, l := range keep {
		buf.Write(l)
		buf.WriteByte('\n')
	}
	tmp := bufferFile + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0644); err != nil { //nolint:gosec
		return
	}
	_ = os.Rename(tmp, bufferFile)
}

// ---- counter -----------------------------------------------------------------

func readCounter(counterFile string) int {
	data, err := os.ReadFile(counterFile) //nolint:gosec
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return n
}

func writeCounter(counterFile string, n int) {
	_ = os.MkdirAll(filepath.Dir(counterFile), 0755) //nolint:gosec
	tmp := counterFile + ".tmp"
	_ = os.WriteFile(tmp, []byte(strconv.Itoa(n)+"\n"), 0644) //nolint:gosec
	_ = os.Rename(tmp, counterFile)
}

// ---- log helper --------------------------------------------------------------

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
		out.Stderr = fmt.Appendf(out.Stderr, "%s: open log: %v\n", hookName, err)
		return
	}
	defer f.Close() //nolint:errcheck
	_, _ = f.Write(data)
}

// ---- helpers -----------------------------------------------------------------

func atomicTouch(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { //nolint:gosec
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".yakos-ss-*.tmp")
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

func globMatch(g, p string) bool {
	if ok, _ := filepath.Match(g, p); ok {
		return true
	}
	g2 := strings.ReplaceAll(g, "**", "*")
	if ok, _ := filepath.Match(g2, p); ok {
		return true
	}
	bare := strings.TrimPrefix(g, "**/")
	if ok, _ := filepath.Match("*/"+bare, p); ok {
		return true
	}
	return false
}

func truncate(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	return s[:maxBytes]
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func senderRole(in hooktype.HookInput) string {
	if r, ok := in.Env["YAKOS_AGENT_ROLE"]; ok && r != "" {
		return r
	}
	if r := stringField(in.Payload, "agent_type"); r != "" {
		return r
	}
	return "unknown"
}

func fileFromPayload(in hooktype.HookInput) string {
	for _, key := range []string{"path", "file_path"} {
		if s := stringField(in.Payload, key); s != "" {
			return s
		}
	}
	return ""
}

func stringField(payload map[string]any, key string) string {
	v, ok := payload[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func (h *Hook) resolveProjectDir(in hooktype.HookInput) string {
	if h.ProjectDir != "" {
		return h.ProjectDir
	}
	if d := in.Env["CLAUDE_PROJECT_DIR"]; d != "" {
		return d
	}
	return in.WorkDir
}

