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
//     d. risk-regex: content, Bash command or description matches a
//     dangerous-command pattern
//  3. If no trigger fires → buffer-only, no counter tick, exit 0.
//  4. If a trigger fires → increment escalation counter in
//     work/current/.supervisor-counter.
//  5. Every score_every_n_calls (default 10) escalations → launch the
//     supervisor agent detached, exactly like the bash hook:
//     `yakos dispatch <agent> <task> --runtime R --model M`, stdout/stderr
//     appended to work/current/.supervisor-{stdout,stderr}.log. The CLI is
//     found via $YAKOS_CLI, $YAKOS_ROOT/cli/yakos, then PATH. (K-112 b: this
//     used to write a marker file nothing ever read, so the LLM tier never
//     ran under YAKOS_HOOKS=go.)
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

	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/secretscan"
)

const (
	hookName            = "supervisor-stream"
	defaultMinDiffLines = 20
	defaultScoreEvery   = 10
	bufferMaxLines      = 50

	// previewCap bounds every buffered preview; redactWindow is how much text
	// is redacted before that cut so a token cannot straddle it. The risk
	// regexes see the FULL command and description (K-112). Bash twin: the
	// head -c calls in supervisor-stream.sh.
	previewCap   = 300
	redactWindow = 4096
)

// built-in risk-regex patterns (case-insensitive)
var defaultRiskPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)drop\s+table`),
	regexp.MustCompile(`(?i)force.*push`),
	regexp.MustCompile(`(?i)rm\s+-rf`),
	regexp.MustCompile(`(?i)chmod\s+777`),
	regexp.MustCompile(`(?i)(password|secret|api_key|token)\s*=\s*[^$({][^\s]{8,}`),
	// K-112 (a): Bash-command shapes the patterns above miss. Keep in step
	// with default_patterns in supervisor-stream.sh.
	regexp.MustCompile(`(?i)git\s+push\s+([^;&|]*\s)?(--force[a-z-]*|-f)(\s|$)`),
	regexp.MustCompile(`(?i)git\s+push\s+([^;&|]*\s)?[+][^\s]`),
	regexp.MustCompile(`(?i)(curl|wget)[^|]*[|]\s*(sudo\s+)?((ba|z|da)?sh|python[0-9.]*|perl|ruby|node|php)(\s|$)`),
	regexp.MustCompile(`(?i)(sh|source)\s+<[(][^)]*(curl|wget)`),
	regexp.MustCompile(`(?i)base64[^|]*[|]\s*(sudo\s+)?(ba|z|da)?sh(\s|$)`),
	regexp.MustCompile(`(?i)>[|>]?\s*[^\s]*(\.env|\.ssh/|\.pem|credentials|\.claude/settings|hook-bypass|/etc/)`),
	regexp.MustCompile(`(?i)tee\s+([^;&|]*\s)?[^\s]*(\.env|\.ssh/|\.pem|credentials|\.claude/settings|hook-bypass|/etc/)`),
	regexp.MustCompile(`(?i)rm\s+-[a-z]*(fr|rf)`),
	regexp.MustCompile(`(?i)rm\s+-[a-z]*r[a-z]*\s+-[a-z]*f`),
	regexp.MustCompile(`(?i)rm\s+-[a-z]*f[a-z]*\s+-[a-z]*r`),
	regexp.MustCompile(`(?i)chmod\s+-[a-z]+\s+777`),
	// K-110: long-flag rm, sh -c "$(curl ...)", cp of .env. sudo/env prefixes
	// need no stripping: every pattern is an unanchored search.
	regexp.MustCompile(`(?i)rm\s+([^;&|]*\s)?(-[a-z]*r[a-z]*|--recursive)\s([^;&|]*\s)?(-[a-z]*f[a-z]*|--force)(\s|$)`),
	regexp.MustCompile(`(?i)rm\s+([^;&|]*\s)?(-[a-z]*f[a-z]*|--force)\s([^;&|]*\s)?(-[a-z]*r[a-z]*|--recursive)(\s|$)`),
	regexp.MustCompile(`(?i)(ba|z|da)?sh\s+-[a-z]*c\s+\S?([$][(]|\x60)\s*(curl|wget)`),
	regexp.MustCompile(`(?i)cp\s+([^;&|]*\s)?[^\s]*\.env(\.[^\s]*)?[^a-z0-9\s._/-]?(\s|$)`),
	regexp.MustCompile(`(?i)eval\s+\S?([$][(]|\x60)\s*(curl|wget)`),
	regexp.MustCompile(`(?i)find\s+([^;&|]*\s)?-delete(\s|$)`),
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

	// Launch starts the supervisor dispatch at the score threshold. New sets
	// the production detached launcher; a nil Launch (struct-literal Hooks in
	// tests) never spawns a process and logs a WARN instead.
	Launch Launcher
}

// New returns a Hook with sensible defaults.
func New(workCurrentDir, projectDir string) *Hook {
	return &Hook{
		WorkCurrentDir: workCurrentDir,
		ProjectDir:     projectDir,
		NowFn:          time.Now,
		Launch:         launchDetached,
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
	sessionID := hookio.SessionID(in) // bash hi_session_id: payload, not env

	// Scan text (unredacted, for the risk regexes) vs stored previews
	// (secret-table matches redacted, then capped): K-112.
	// Edit/Write text is scanned in full for risk (bounded: head + tail beyond
	// 64 KiB) so padding cannot hide a snippet; the 300-byte newScan/contentScan
	// still drive the large-diff check as before.
	newFull := hookio.ToolInputString(in, "new_string")
	contentFull := hookio.ToolInputString(in, "content")
	newScan := truncate(newFull, previewCap)
	contentScan := truncate(contentFull, previewCap)
	newRisk := boundRisk(newFull)
	contentRisk := boundRisk(contentFull)
	commandScan := hookio.ToolInputString(in, "command")
	descriptionScan := hookio.ToolInputString(in, "description")
	preview := func(text string) string {
		return truncate(secretscan.Redact(truncate(text, redactWindow)), previewCap)
	}
	newPreview := preview(newFull)
	contentPreview := preview(contentFull)
	commandPreview := preview(commandScan)
	descriptionPreview := preview(descriptionScan)

	// Build event JSON.
	evInput := map[string]any{
		"file_path":       filePath,
		"new_preview":     nilIfEmpty(newPreview),
		"content_preview": nilIfEmpty(contentPreview),
	}
	if commandPreview != "" {
		evInput["command_preview"] = commandPreview
	}
	if descriptionPreview != "" {
		evInput["description_preview"] = descriptionPreview
	}
	event := map[string]any{
		"ts":         ts,
		"agent":      agentType,
		"tool":       in.Tool,
		"input":      evInput,
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
			escalateReason = h.checkSensitivePath(&out, filePath, projectDir)
		}

		// 2b. Diff-size check.
		if escalateReason == "" {
			combined := newScan + contentScan
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
			// Newlines join to spaces so a line-continued "curl x \<nl>| sh"
			// matches like bash's joined text.
			combined := strings.ReplaceAll(strings.ReplaceAll(newRisk+"\n"+contentRisk+"\n"+commandScan+"\n"+descriptionScan, "\n", " "), "\\ ", "  ")
			if strings.TrimSpace(combined) != "" {
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
	cur, ok := incrementCounter(counterFile)
	if !ok {
		// No lock within the wait budget: skip this tick (bash: exit 0)
		// rather than block the hook or risk a double launch.
		h.appendLog(&out, logFile, "WARN", "pass",
			"counter lock busy or unremovable; skipping this escalation tick",
			map[string]any{"lock": counterFile + ".lock"})
		return out, nil
	}

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

	// Threshold hit — fork the supervisor (bash: same log records, same order).
	h.appendLog(&out, logFile, "REPORT", "pass",
		"escalation score threshold hit; forking supervisor dispatch (async)",
		map[string]any{"counter": cur, "score_every": scoreEvery, "will_score": true})

	h.launchSupervisor(&out, in, cfg, logFile, scoreEvery)

	return out, nil
}

// launchSupervisor mirrors the tail of supervisor-stream.sh: resolve
// runtime/agent/model (defaults claude / supervisor / haiku), locate the CLI,
// build the task, start `dispatch` detached, and log the same records.
func (h *Hook) launchSupervisor(out *hooktype.HookOutput, in hooktype.HookInput, cfg *supervisorConfig, logFile string, scoreEvery int) {
	runtime, agent, model := "claude", "supervisor", "haiku"
	if cfg != nil {
		if cfg.Runtime != "" {
			runtime = cfg.Runtime
		}
		if cfg.Agent != "" {
			agent = cfg.Agent
		}
		if cfg.Model != "" {
			model = cfg.Model
		}
	}

	cli := findCLI(in.Env)
	if cli == "" || h.Launch == nil {
		h.appendLog(out, logFile, "WARN", "pass",
			"could not locate yakos CLI to fork supervisor", map[string]any{})
		return
	}

	task := buildTask(
		filepath.Join(h.WorkCurrentDir, "supervisor-buffer.ndjson"),
		filepath.Join(h.WorkCurrentDir, "supervisor-findings.ndjson"),
		filepath.Join(h.WorkCurrentDir, "decisions.md"),
		scoreEvery)
	spec := LaunchSpec{
		CLI:        cli,
		Args:       []string{"dispatch", agent, task, "--runtime", runtime, "--model", model},
		StdoutPath: filepath.Join(h.WorkCurrentDir, ".supervisor-stdout.log"),
		StderrPath: filepath.Join(h.WorkCurrentDir, ".supervisor-stderr.log"),
	}
	if err := h.Launch(spec); err != nil {
		h.appendLog(out, logFile, "WARN", "pass",
			"supervisor dispatch launch failed",
			map[string]any{"error": err.Error(), "model": model, "runtime": runtime})
		return
	}
	h.appendLog(out, logFile, "REPORT", "pass",
		fmt.Sprintf("supervisor dispatch forked async (model=%s runtime=%s)", model, runtime),
		map[string]any{"dispatch": "async", "model": model, "runtime": runtime})
}

// ---- pre-filter checks -------------------------------------------------------

// checkSensitivePath returns an escalation reason if filePath matches a deny
// glob in .claude/path-allowlist.json under the "lead" key.
func (h *Hook) checkSensitivePath(out *hooktype.HookOutput, filePath, projectDir string) string {
	allowlistFile := filepath.Join(projectDir, ".claude", "path-allowlist.json")
	data, err := os.ReadFile(allowlistFile) //nolint:gosec
	if err != nil {
		return ""
	}
	// Decode per key: the stock template carries a top-level "_doc" string
	// that must not poison the whole document (bash: .lead.deny // []).
	var policies map[string]json.RawMessage
	if err := json.Unmarshal(data, &policies); err != nil {
		return ""
	}
	raw, ok := policies["lead"]
	if !ok {
		return ""
	}
	// Deny is decoded as []any so one non-string element ([5, ".env"]) does
	// not drop the whole list: bash's `.lead.deny | .[]` still yields the
	// remaining entries and escalates on ".env". Non-strings are skipped with
	// a WARN (bash would stringify them into a glob like "5", which no real
	// policy relies on).
	var leadPolicy struct {
		Deny []any `json:"deny"`
	}
	if err := json.Unmarshal(raw, &leadPolicy); err != nil {
		return ""
	}
	for i, el := range leadPolicy.Deny {
		glob, isStr := el.(string)
		if !isStr {
			out.Stderr = fmt.Appendf(out.Stderr,
				"%s: WARN: path-allowlist.json lead.deny[%d] is %T, not a string; skipped\n", hookName, i, el)
			continue
		}
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
	// Owner-only: the buffer holds command lines and feeds an LLM (K-112).
	f, err := os.OpenFile(bufferFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600) //nolint:gosec
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck
	_ = f.Chmod(0600)
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
	if err := os.WriteFile(tmp, buf.Bytes(), 0600); err != nil {
		return
	}
	_ = os.Chmod(tmp, 0600)
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

// counterLockStale is how old an abandoned counter lock must be before it is
// reaped (bash: find -mmin +1).
const counterLockStale = time.Minute

// incrementCounter is the K-110 atomic read-increment-write. It takes the
// same mkdir lock as supervisor-stream.sh (<counter>.lock), so concurrent
// bash and Go hooks serialize too: every caller gets a unique value and at
// most one crosses a score-every multiple. ok is false when the lock could
// not be taken within ~3 s.
func incrementCounter(counterFile string) (cur int, ok bool) {
	_ = os.MkdirAll(filepath.Dir(counterFile), 0755) //nolint:gosec
	lock := counterFile + ".lock"
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := os.Mkdir(lock, 0700); err == nil {
			break
		}
		// Every retry sleeps and is bounded by the deadline, reaping included,
		// so a stale lock that cannot be removed never spins this loop.
		if time.Now().After(deadline) {
			return 0, false
		}
		reapStaleLock(lock)
		time.Sleep(5 * time.Millisecond)
	}
	defer func() { _ = os.Remove(lock) }()
	cur = readCounter(counterFile) + 1
	writeCounter(counterFile, cur)
	return cur, true
}

// reapStaleLock removes a lock older than counterLockStale. It renames first
// (atomic, one winner) and re-checks the age of what it moved, so a waiter
// never deletes a lock another hook has just created. Mirrors the bash hook.
func reapStaleLock(lock string) {
	fi, err := os.Lstat(lock)
	if err != nil || time.Since(fi.ModTime()) <= counterLockStale {
		return
	}
	moved := fmt.Sprintf("%s.reap.%d", lock, os.Getpid())
	if os.Rename(lock, moved) != nil {
		return
	}
	if mfi, merr := os.Lstat(moved); merr == nil && time.Since(mfi.ModTime()) <= counterLockStale {
		if os.Rename(moved, lock) == nil {
			return // was fresh after all; put it back
		}
	}
	_ = os.RemoveAll(moved)
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

// globMatch mirrors bash supervisor-stream.sh, which uses `case` patterns:
// `*` matches any characters INCLUDING "/" (so "**" behaves as "*"), and the
// glob is tried both as written and with a leading "**/" stripped, the latter
// also as a "*/<bare>" suffix match. This is what makes ".env" or
// "**/credentials/**" hit absolute paths.
func globMatch(g, p string) bool {
	if shellMatch(g, p) {
		return true
	}
	bare := strings.TrimPrefix(g, "**/")
	return shellMatch("*/"+bare, p) || shellMatch(bare, p)
}

// shellMatch reports whether s matches the shell case-pattern pat (whole
// string): * any run, ? one char, [set] / [!set] classes, everything else
// literal.
func shellMatch(pat, s string) bool {
	var b strings.Builder
	b.WriteString("^(?s:")
	rs := []rune(pat)
	for i := 0; i < len(rs); i++ {
		switch c := rs[i]; c {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		case '[':
			j := i + 1
			if j < len(rs) && (rs[j] == '!' || rs[j] == '^') {
				j++
			}
			if j < len(rs) && rs[j] == ']' {
				j++
			}
			for j < len(rs) && rs[j] != ']' {
				j++
			}
			if j >= len(rs) {
				b.WriteString(regexp.QuoteMeta("["))
				continue
			}
			set := string(rs[i+1 : j])
			if strings.HasPrefix(set, "!") {
				set = "^" + set[1:]
			}
			b.WriteString("[" + strings.ReplaceAll(set, "\\", "\\\\") + "]")
			i = j
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString(")$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return false
	}
	return re.MatchString(s)
}

// riskBound is the most text scanned per Edit/Write field; longer text is
// scanned as its head and tail. Bash twin: _ss_bound.
const riskBound = 65536

func boundRisk(s string) string {
	if len(s) <= riskBound {
		return s
	}
	return s[:riskBound/2] + "\n" + s[len(s)-riskBound/2:]
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

// senderRole mirrors bash hi_sender_role: the payload agent_type ("lead"
// when absent), whitespace-trimmed, "yakos:" prefix stripped. Env is not read.
func senderRole(in hooktype.HookInput) string {
	return hookio.SenderRole(in)
}

func fileFromPayload(in hooktype.HookInput) string {
	return hookio.ToolFilePath(in)
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
