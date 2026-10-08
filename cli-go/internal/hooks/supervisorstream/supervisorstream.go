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
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/hooklog"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/secretscan"
	"github.com/bakw00ds/yakos/internal/projfile"
)

const (
	hookName            = "supervisor-stream"
	defaultMinDiffLines = 20
	defaultScoreEvery   = 10

	bufferMaxLines = 50

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
	// K-117: the bare `force.*push` matched prose ("enforce push notification")
	// and could burn the high-risk ceiling; the git-command shapes below cover
	// real pushes.
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
	regexp.MustCompile(`(?i)(ba|z|da)?sh\s+-[a-z]*c\s+\S?([$][(]|\x60)\s*([^\s)]*/)?(curl|wget)`),
	regexp.MustCompile(`(?i)cp\s+([^;&|]*\s)?[^\s]*\.env(\.[^\s]*)?[^a-z0-9\s._/-]?(\s|$)`),
	regexp.MustCompile(`(?i)eval\s+\S?([$][(]|\x60)\s*([^\s)]*/)?(curl|wget)`),
	regexp.MustCompile(`(?i)find\s+(([^;&|'"]|"[^"]*"|'[^']*')*\s)?-delete([\s;&|]|$)`),
}

// defaultRiskLabels is each defaultRiskPatterns entry spelled the way the bash
// twin spells it (POSIX ERE), index for index. The escalation trigger is logged
// as "risk-regex:<label>", so Go and bash write identical bytes; matching still
// uses the compiled RE2 form. TestRiskLabelsMatchBash keeps the list in step
// with default_patterns in supervisor-stream.sh.
var defaultRiskLabels = []string{
	`drop[[:space:]]+table`,
	`rm[[:space:]]+-rf`,
	`chmod[[:space:]]+777`,
	`(password|secret|api_key|token)[[:space:]]*=[[:space:]]*[^$({][^[:space:]]{8,}`,
	`git[[:space:]]+push[[:space:]]([^;&|]*[[:space:]])?(--force[a-z-]*|-f)([[:space:]]|$)`,
	`git[[:space:]]+push[[:space:]]([^;&|]*[[:space:]])?[+][^[:space:]]`,
	`(curl|wget)[^|]*[|][[:space:]]*(sudo[[:space:]]+)?((ba|z|da)?sh|python[0-9.]*|perl|ruby|node|php)([[:space:]]|$)`,
	`(sh|source)[[:space:]]+<[(][^)]*(curl|wget)`,
	`base64[^|]*[|][[:space:]]*(sudo[[:space:]]+)?(ba|z|da)?sh([[:space:]]|$)`,
	`>[|>]?[[:space:]]*[^[:space:]]*(\.env|\.ssh/|\.pem|credentials|\.claude/settings|hook-bypass|/etc/)`,
	`tee[[:space:]]+([^;&|]*[[:space:]])?[^[:space:]]*(\.env|\.ssh/|\.pem|credentials|\.claude/settings|hook-bypass|/etc/)`,
	`rm[[:space:]]+-[a-z]*(fr|rf)`,
	`rm[[:space:]]+-[a-z]*r[a-z]*[[:space:]]+-[a-z]*f`,
	`rm[[:space:]]+-[a-z]*f[a-z]*[[:space:]]+-[a-z]*r`,
	`chmod[[:space:]]+-[a-z]+[[:space:]]+777`,
	`rm[[:space:]]+([^;&|]*[[:space:]])?(-[a-z]*r[a-z]*|--recursive)[[:space:]]([^;&|]*[[:space:]])?(-[a-z]*f[a-z]*|--force)([[:space:]]|$)`,
	`rm[[:space:]]+([^;&|]*[[:space:]])?(-[a-z]*f[a-z]*|--force)[[:space:]]([^;&|]*[[:space:]])?(-[a-z]*r[a-z]*|--recursive)([[:space:]]|$)`,
	"(ba|z|da)?sh[[:space:]]+-[a-z]*c[[:space:]]+[^[:space:]]?([$][(]|`)[[:space:]]*([^[:space:])]*/)?(curl|wget)",
	`cp[[:space:]]+([^;&|]*[[:space:]])?[^[:space:]]*\.env(\.[^[:space:]]*)?[^[:alnum:][:space:]._/-]?([[:space:]]|$)`,
	"eval[[:space:]]+[^[:space:]]?([$][(]|`)[[:space:]]*([^[:space:])]*/)?(curl|wget)",
	`find[[:space:]]+(([^;&|'"]|"[^"]*"|'[^']*')*[[:space:]])?-delete([[:space:];&|]|$)`,
}

// yakosYMLSupervisor holds the shape needed from .yakos.yml.
type yakosYMLSupervisor struct {
	Supervisor *supervisorConfig `yaml:"supervisor"`
	// Decisions carries only the provider name: the shadow call itself is a
	// `yakos decide` child that reads the full decisions: block.
	// Held as a raw node so a malformed decisions: block can never make the
	// supervisor: block unreadable (provider none must stay unchanged).
	Decisions yaml.Node `yaml:"decisions"`
}

type supervisorConfig struct {
	Enabled     *bool  `yaml:"enabled"`
	Model       string `yaml:"model"`
	Runtime     string `yaml:"runtime"`
	Agent       string `yaml:"agent"`
	ScoreEveryN optInt `yaml:"score_every_n_calls"`
	// K-117 launch gate: plain decimal whole numbers only; an invalid value is
	// ignored on its own (the rest of the block still applies).
	MaxLaunches    optInt           `yaml:"max_launches_per_session"`
	MinInterval    optInt           `yaml:"min_launch_interval_s"`
	RunDeadline    optInt           `yaml:"run_deadline_s"`
	SessionBackoff optInt           `yaml:"session_limit_backoff_min"`
	PreFilter      *preFilterConfig `yaml:"pre_filter"`
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

	// Self is this yakos executable, re-run as `hook supervisor-wrap` by the
	// production launcher. New sets it from os.Executable.
	Self string
}

// New returns a Hook with sensible defaults.
func New(workCurrentDir, projectDir string) *Hook {
	return &Hook{
		WorkCurrentDir: workCurrentDir,
		ProjectDir:     projectDir,
		NowFn:          time.Now,
		Launch:         launchDetached,
		Self:           selfExecutable(),
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
	doc, refusal := h.loadDoc(projectDir)
	if refusal != "" {
		out.Stderr = fmt.Appendf(out.Stderr, "%s: %s\n", hookName, refusal)
	}
	var cfg *supervisorConfig
	if doc != nil {
		cfg = doc.Supervisor
	}
	if cfg != nil && cfg.Enabled != nil && !*cfg.Enabled {
		return out, nil
	}

	logFile := filepath.Join(h.WorkCurrentDir, "logs", hookName+".ndjson")
	bufferFile := filepath.Join(h.WorkCurrentDir, "supervisor-buffer.ndjson")
	counterFile := filepath.Join(h.WorkCurrentDir, ".supervisor-counter")

	agentType := senderRole(in)
	filePath := bashStr(fileFromPayload(in))
	ts := h.NowFn().UTC().Format(time.RFC3339)
	sessionID := hookio.SessionID(in) // bash hi_session_id: payload, not env

	// Scan text (unredacted, for the risk regexes) vs stored previews
	// (secret-table matches redacted, then capped): K-112.
	// Edit/Write text is scanned in full for risk (bounded: head + tail beyond
	// 64 KiB) so padding cannot hide a snippet; the 300-byte newScan/contentScan
	// still drive the large-diff check as before.
	newFull := bashStr(hookio.ToolInputString(in, "new_string"))
	contentFull := bashStr(hookio.ToolInputString(in, "content"))
	newScan := truncate(newFull, previewCap)
	contentScan := truncate(contentFull, previewCap)
	newRisk := boundRisk(newFull)
	contentRisk := boundRisk(contentFull)
	commandScan := bashStr(hookio.ToolInputString(in, "command"))
	descriptionScan := bashStr(hookio.ToolInputString(in, "description"))
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

	triggerHigh := false
	if !pfEnabled {
		// Pre-filter disabled: every mutation counts.
		h.appendLog(&out, in, logFile, "REPORT", "pass",
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
				// bash: printf '%s' | wc -l counts newlines, not lines.
				lineCount := strings.Count(combined, "\n")
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
			h.appendLog(&out, in, logFile, "REPORT", "pass",
				"pre-filter: no trigger; buffered without dispatch",
				map[string]any{"pre_filter": "pass", "tool": in.Tool, "file": filePath})
			h.shadowDecision(in, doc, projectDir, shadowInput{
				Tool: in.Tool, Verdict: "pass", FilePath: filePath, Command: commandScan,
				New: newFull, Content: contentFull, Session: sessionID,
			})
			return out, nil
		}

		// K-117: a trigger is HIGH-risk when a sensitive-path or risk-regex match
		// exists, whichever check fired first (a curl|sh inside a large diff is
		// still high-risk). High-risk triggers bypass the launch cap.
		triggerHigh = strings.HasPrefix(escalateReason, "risk-regex:") || strings.HasPrefix(escalateReason, "sensitive-path:")
		if !triggerHigh {
			var scratch hooktype.HookOutput
			if filePath != "" && h.checkSensitivePath(&scratch, filePath, projectDir) != "" {
				triggerHigh = true
			} else if c := strings.ReplaceAll(strings.ReplaceAll(newRisk+"\n"+contentRisk+"\n"+commandScan+"\n"+descriptionScan, "\n", " "), "\\ ", "  "); strings.TrimSpace(c) != "" && h.checkRiskRegex(c, extraRiskPatterns) != "" {
				triggerHigh = true
			}
		}

		// Trigger fired.
		h.appendLog(&out, in, logFile, "REPORT", "pass",
			"pre-filter: ESCALATE ("+escalateReason+"); counting toward score threshold",
			map[string]any{"pre_filter": "escalate", "trigger": escalateReason, "tool": in.Tool, "file": filePath})
		h.shadowDecision(in, doc, projectDir, shadowInput{
			Tool: in.Tool, Verdict: "escalate", Trigger: escalateReason, FilePath: filePath, Command: commandScan,
			New: newFull, Content: contentFull, Session: sessionID,
		})
	}

	// ---- 3. Increment escalation counter ----
	// K-128: the increment is never dropped. If the lock cannot be taken within
	// its ceiling, incrementCounter has journaled it for the next lock holder; a
	// high-risk trigger also owes the session's run state a record.
	cur, folded, res := incrementCounter(counterFile)
	switch res {
	case counterBusy, counterBusyUnjournaled:
		if triggerHigh {
			journalGate(filepath.Join(h.WorkCurrentDir, ".supervisor-run."+sessionKey(sessionID)), true, event)
		}
		note := "increment journaled for the next lock holder"
		if res == counterBusyUnjournaled {
			note = "could not journal the increment"
		}
		h.appendLog(&out, in, logFile, "WARN", "pass",
			"counter lock busy or unremovable; "+note+", skipping this escalation tick",
			map[string]any{"lock": counterFile + ".lock"})
		return out, nil
	case counterWriteFailed:
		// The counter cannot be written (a directory in its place, say), so this
		// tick cannot be counted. Say so, and still record a high-risk trigger
		// in the session's run state. Bash twin: _ss_counter_unwritable.
		h.appendLog(&out, in, logFile, "WARN", "pass",
			"counter not writable; skipping this escalation tick",
			map[string]any{"counter": counterFile})
		if triggerHigh {
			h.gateNote(&out, in, cfg, logFile, event)
		}
		return out, nil
	}

	// ---- 4. Every N escalations → write dispatch-ready marker ----
	scoreEvery := defaultScoreEvery
	if cfg != nil && cfg.ScoreEveryN.ok && cfg.ScoreEveryN.v > 0 {
		scoreEvery = cfg.ScoreEveryN.v
	}

	// A threshold is crossed when a multiple of scoreEvery lies in
	// (cur-1-folded, cur]: with no journaled records that is cur%scoreEvery == 0.
	if cur/scoreEvery <= (cur-1-folded)/scoreEvery {
		h.appendLog(&out, in, logFile, "REPORT", "pass",
			"escalation buffered; not yet at score-every threshold",
			map[string]any{"counter": cur, "score_every": scoreEvery, "will_score": false})
		// A high-risk event is still recorded (and bumps the high-risk flag) so
		// the next run covers it even if the cap has been reached by then.
		if triggerHigh {
			h.gateNote(&out, in, cfg, logFile, event)
		}
		return out, nil
	}

	// Threshold hit — fork the supervisor (bash: same log records, same order).
	h.appendLog(&out, in, logFile, "REPORT", "pass",
		"escalation score threshold hit; forking supervisor dispatch (async)",
		map[string]any{"counter": cur, "score_every": scoreEvery, "will_score": true})

	h.launchSupervisor(&out, in, cfg, logFile, scoreEvery, triggerHigh, event)

	return out, nil
}

// launchSupervisor mirrors the tail of supervisor-stream.sh: resolve
// runtime/agent/model (defaults claude / supervisor / haiku), locate the CLI,
// build the task, then hand the launch to the K-117 gate.
func (h *Hook) launchSupervisor(out *hooktype.HookOutput, in hooktype.HookInput, cfg *supervisorConfig, logFile string, scoreEvery int, high bool, event map[string]any) {
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
	model, badModel := resolveModel(model)

	cli := findCLI(in.Env)
	if cli == "" || h.Launch == nil {
		h.appendLog(out, in, logFile, "WARN", "pass",
			"could not locate yakos CLI to fork supervisor", map[string]any{})
		return
	}

	task := buildTask(
		filepath.Join(h.WorkCurrentDir, "supervisor-buffer.ndjson"),
		filepath.Join(h.WorkCurrentDir, "supervisor-findings.ndjson"),
		filepath.Join(h.WorkCurrentDir, "decisions.md"),
		scoreEvery)
	if badModel != "" {
		h.appendLog(out, in, logFile, "WARN", "pass",
			"supervisor.model is not a known tier or alias; using haiku",
			map[string]any{"model": badModel})
	}
	h.launchGate(out, in, cfg, logFile, gateCall{
		crossed: true, high: high, event: event,
		spec: LaunchSpec{
			CLI:        cli,
			Args:       []string{"dispatch", agent, task, "--runtime", runtime, "--model", model},
			StdoutPath: filepath.Join(h.WorkCurrentDir, ".supervisor-stdout.log"),
			StderrPath: filepath.Join(h.WorkCurrentDir, ".supervisor-stderr.log"),
		},
		runtime: runtime, model: model, agent: agent,
	})
}

// gateNote records a high-risk escalation that did not cross the score
// threshold: bump the high-risk flag and keep its preview for the next run.
func (h *Hook) gateNote(out *hooktype.HookOutput, in hooktype.HookInput, cfg *supervisorConfig, logFile string, event map[string]any) {
	model := "haiku"
	if cfg != nil && cfg.Model != "" {
		model = cfg.Model
	}
	model, _ = resolveModel(model)
	h.launchGate(out, in, cfg, logFile, gateCall{crossed: false, high: true, event: event, model: model})
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
	for i, re := range defaultRiskPatterns {
		if re.MatchString(combined) {
			return "risk-regex:" + defaultRiskLabels[i]
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

// loadDoc reads the project's supervisor config. A file projfile refuses (a link,
// not a regular file, over the cap) is ABSENT, as in the budget package and the bash
// twin, so the supervisor keeps its default agent name and with it the built-in
// budget; the second result is then the path-free notice for stderr.
func (h *Hook) loadDoc(projectDir string) (*yakosYMLSupervisor, string) {
	if projectDir == "" {
		return nil, ""
	}
	data, err := projfile.Read(projectDir)
	if err != nil {
		if projfile.IsRefused(err) {
			return nil, projfile.Notice(err)
		}
		return nil, ""
	}
	var doc yakosYMLSupervisor
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, ""
	}
	return &doc, ""
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

// ---- log helper --------------------------------------------------------------

// logExtraOrder is the order bash's ho_log extras appear in (jq keeps the
// object literal's insertion order). One list covers every record because no
// two records order the same pair of keys differently; TestLogExtraOrder
// compares each record against bash's literal order. Keep it in step with
// lib/hooks/legacy/supervisor-stream.sh.
var logExtraOrder = []string{
	"ignored_keys", "invalid_keys", "lock", "age_s",
	"coalesced", "high_risk", "throttled", "backoff_until", "capped", "cap", "ceiling",
	"agent", "spent_usd", "limit_usd", "ceiling_usd", "spent_tokens", "limit_tokens", "ceiling_tokens", "budget_reason", "cause",
	"pre_filter", "trigger", "tool", "file",
	"counter", "score_every", "will_score",
	"dispatch", "model", "runtime", "deadline_s", "deferred_s", "pending", "session_key", "kind",
}

// appendLog writes one record through the shared hooklog writer, so the
// field set (ts, hook, severity, decision, reason, agent, session_id, event,
// then extras) matches bash's ho_log. decision and reason replace the old
// action/message names; agent, session_id and event come from the payload
// exactly as bash's hi_sender_role / hi_session_id / hi_event do.
func (h *Hook) appendLog(out *hooktype.HookOutput, in hooktype.HookInput, _ string, severity, decision, reason string, extra map[string]any) {
	if err := hooklog.Append(h.WorkCurrentDir, hooklog.Entry{
		Hook:       hookName,
		Severity:   severity,
		Decision:   decision,
		Reason:     reason,
		Agent:      senderRole(in),
		SessionID:  hookio.SessionID(in),
		Event:      in.Event,
		Extra:      extra,
		ExtraOrder: logExtraOrder,
	}, h.NowFn()); err != nil {
		out.Stderr = fmt.Appendf(out.Stderr, "%s: open log: %v\n", hookName, err)
	}
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

// bashStr mirrors how bash reads a payload string: through $(jq ...), which
// drops NUL bytes and trailing newlines before the risk regexes and the
// redactor run. sh drops NUL when it executes a script, so a NUL-split
// `cu\0rl ... | sh` is a real risk and must still escalate (K-122 security
// review). Scan and store the same bytes bash does.
func bashStr(s string) string {
	return strings.TrimRight(strings.ReplaceAll(s, "\x00", ""), "\n")
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
