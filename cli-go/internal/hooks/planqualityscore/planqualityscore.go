// Package planqualityscore is the Go-native port of
// lib/hooks/plan-quality-score.sh (the PostToolUse half of the old
// plan-quality-gate hook, split out under K-81).
//
// PostToolUse (Edit|Write|MultiEdit): when a write targets
// work/current/plan.md, the hook scores THAT file at hook time, exactly as
// the bash hook does:
//
//  1. skip when plan_quality.enabled is false;
//  2. debounce: skip when plan.md's mtime is under 5 s old (the debounce is
//     keyed on the file's mtime, as in bash);
//  3. run lib/skills/plan-quality-eval/scripts/score-plan.sh on the file
//     from tool_input.file_path, with a throwaway HOME, and read the last
//     record it wrote there (an earlier version read the last PERSISTED
//     record from the log instead, so it scored a stale plan, and had no
//     debounce; K-99, #293 review finding 3);
//  4. route on the aggregate score and the dissent flag: dissent surfaces,
//     a pass logs, a fail surfaces (mode=surface) or writes the
//     .plan-blocked marker (mode=block).
//
// Like bash, a passing score never removes an existing .plan-blocked
// marker; that is the gate's and the operator's job.
//
// Emergency bypass: YAKOS_PLAN_QUALITY_DISABLE=1.
//
// Conservative failure mode: any infra error (script missing, bash missing,
// score-plan exit 2/3/4, no or invalid record) logs a WARN and passes
// (exit 0). The hook NEVER prevents the operator from saving a plan because
// scoring infrastructure broke. This is deliberately the opposite of
// planqualitygate, which fails closed.
package planqualityscore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/hooklog"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/yamlblock"
)

const hookName = "plan-quality-score"

// planQualityConfig holds the raw plan_quality values from .yakos.yml, read
// per key exactly as bash's awk does (see loadConfig). Values stay strings so
// the marker and log records echo the operator's own text.
type planQualityConfig struct {
	Enabled        string
	Mode           string
	Threshold      string
	CostCeilingUSD string
}

// planBlockedMarker is the JSON written to .plan-blocked. Field types match
// the bash marker (jq --arg makes every value a string).
type planBlockedMarker struct {
	PlanID         string `json:"plan_id"`
	AggregateScore string `json:"aggregate_score"`
	Threshold      string `json:"threshold"`
	Ts             string `json:"ts"`
	Reason         string `json:"reason"`
}

// logRecord is the minimal shape of the scoring record. The bash hook takes
// the last line of the scorer's log without filtering on type, and so does
// Go.
type logRecord struct {
	PlanID         string
	AggregateScore float64
	// AggregateText is the score's JSON literal exactly as written ("1.0",
	// "0.40"), which is what `jq -r` prints and therefore what bash echoes
	// into the marker and log records.
	AggregateText string
	Dissent       bool
}

const (
	defaultThreshold   = "0.75"
	defaultCostCeiling = "0.15"

	// debounceSeconds is bash's `[ "$age_s" -lt 5 ]`.
	debounceSeconds = 5
)

// Hook implements runner.Hook for plan quality scoring.
type Hook struct {
	// WorkCurrentDir is the absolute path to work/current/ for the active session.
	WorkCurrentDir string

	// ProjectDir is the project root where .yakos.yml is located.
	ProjectDir string

	// PlanQualityLog overrides where the scored record is persisted.
	// When empty, $HOME/.yakos-state/plan-quality-log.ndjson (bash's real_log).
	PlanQualityLog string

	// HooksDir is the physical directory holding the framework's hook
	// scripts (bash's HOOK_DIR). The framework root defaults to
	// HooksDir/../.. exactly as the bash hook derives YAKOS_ROOT.
	HooksDir string

	// YakosRoot overrides the framework root (tests). $YAKOS_ROOT in the
	// hook's environment wins over both this and HooksDir.
	YakosRoot string

	// HomeDir overrides $HOME for path resolution.
	HomeDir string

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

// Run executes the plan-quality-score logic. It never blocks: ExitCode is
// always 0.
func (h *Hook) Run(c context.Context, in hooktype.HookInput) (hooktype.HookOutput, error) {
	out := hooktype.HookOutput{ExitCode: 0}

	// Emergency bypass.
	if in.Env["YAKOS_PLAN_QUALITY_DISABLE"] == "1" {
		return out, nil
	}

	if in.Event == "PostToolUse" {
		return h.runPostToolUse(c, out, in)
	}
	return out, nil
}

// ---- PostToolUse path -------------------------------------------------------

func (h *Hook) runPostToolUse(c context.Context, out hooktype.HookOutput, in hooktype.HookInput) (hooktype.HookOutput, error) {
	switch in.Tool {
	case "Edit", "Write", "MultiEdit":
	default:
		return out, nil
	}

	// Only act when the write targets plan.md.
	filePath := hookio.ToolFilePath(in)
	// Normalise to forward slashes before the suffix check so that Windows
	// paths (which use "\") and mixed-separator paths both match the
	// canonical "/work/current/plan.md" suffix.
	if !strings.HasSuffix(filepath.ToSlash(filePath), "/work/current/plan.md") {
		return out, nil
	}

	projectDir := h.resolveProjectDir(in)

	// Load plan_quality config (bash: awk over .yakos.yml).
	cfg, cfgErr := h.loadConfig(projectDir)
	if cfgErr != nil {
		h.warnf(&out, "plan-quality-score: .yakos.yml is not a readable file; using defaults")
		h.log(&out, in, "WARN", "pass", ".yakos.yml is not a readable file; using plan_quality defaults", map[string]any{})
	}
	if cfg.Enabled == "false" { // bash: [ "$PQ_ENABLED" = "false" ]
		h.log(&out, in, "REPORT", "pass", "plan_quality.enabled=false; skipping",
			map[string]any{"file_path": filePath})
		return out, nil
	}
	threshold := firstNonEmpty(cfg.Threshold, defaultThreshold)
	// bash's awk `thr+0` turns a non-numeric threshold into 0, which would
	// pass every plan. Fall back to the default and say so instead.
	if !isDecimal(threshold) {
		h.warnf(&out, "plan-quality-score: plan_quality.threshold %q is not a number; using %s", threshold, defaultThreshold)
		h.log(&out, in, "WARN", "pass",
			fmt.Sprintf("plan_quality.threshold %q is not a number; using default %s", threshold, defaultThreshold),
			map[string]any{"key": "threshold", "value": threshold})
		threshold = defaultThreshold
	}
	mode := firstNonEmpty(cfg.Mode, "surface")
	costCeiling := firstNonEmpty(cfg.CostCeilingUSD, in.Env["YAKOS_PLAN_EVAL_MAX_COST_USD"], defaultCostCeiling)

	// Debounce, keyed on plan.md's mtime like bash: a file modified less
	// than 5 s ago is skipped. A missing file has mtime 0 (bash's
	// `mtime1=0`), so it proceeds and the scorer reports the extraction
	// error. A negative age (mtime in the future) does not debounce.
	nowT := h.now()
	var mtime int64
	if fi, err := os.Stat(filePath); err == nil {
		mtime = fi.ModTime().Unix()
	}
	age := nowT.Unix() - mtime
	if age < debounceSeconds && age >= 0 {
		h.log(&out, in, "REPORT", "pass",
			fmt.Sprintf("debounced: plan.md mtime age=%ds < 5s; skipping this fire", age),
			map[string]any{"file_path": filePath, "age_s": age})
		return out, nil
	}

	// Locate the scorer.
	root := h.frameworkRoot(in)
	script := filepath.Join(root, "lib", "skills", "plan-quality-eval", "scripts", "score-plan.sh")
	if fi, err := os.Stat(script); err != nil || fi.IsDir() {
		h.warnf(&out, "plan-quality-score: score-plan.sh not found at %s; passing", script)
		h.log(&out, in, "WARN", "pass", "score-plan.sh not found; skipping scoring",
			map[string]any{"script_path": script})
		return out, nil
	}

	// Score the file that was just written, into a throwaway HOME.
	tmpHome, err := os.MkdirTemp("", "yakos-pqscore.")
	if err != nil {
		h.warnf(&out, "plan-quality-score: cannot create scoring sandbox: %v; passing", err)
		h.log(&out, in, "WARN", "pass", "cannot create scoring sandbox; no gate action",
			map[string]any{"error": err.Error()})
		return out, nil
	}
	defer func() { _ = os.RemoveAll(tmpHome) }()
	if err := os.MkdirAll(filepath.Join(tmpHome, ".yakos-state"), 0o755); err != nil { //nolint:gosec
		h.warnf(&out, "plan-quality-score: cannot create scoring sandbox: %v; passing", err)
		h.log(&out, in, "WARN", "pass", "cannot create scoring sandbox; no gate action",
			map[string]any{"error": err.Error()})
		return out, nil
	}

	yakosLib := firstNonEmpty(in.Env["YAKOS_LIB"], filepath.Join(root, "cli", "lib"))
	rc, runErr := runScorer(c, script, filePath, scorerEnv(in.Env, map[string]string{
		"HOME":                         tmpHome,
		"YAKOS_PLAN_EVAL_MAX_COST_USD": costCeiling,
		"YAKOS_ROOT":                   root,
		"YAKOS_LIB":                    yakosLib,
	}))
	// score-plan.sh exit codes: 0=pass, 1=fail/dissent, 2=extract error,
	// 3=cost exceeded, 4=judge failure. A scorer that cannot start at all
	// (no bash on PATH) is the same class of infra error.
	if runErr != nil {
		h.warnf(&out, "plan-quality-score: score-plan.sh could not run: %v; passing", runErr)
		h.log(&out, in, "WARN", "pass", "score-plan.sh could not run (infra error); no gate action",
			map[string]any{"error": runErr.Error()})
		return out, nil
	}
	if rc == 2 || rc == 3 || rc == 4 {
		h.warnf(&out, "plan-quality-score: score-plan.sh exited %d (infra error); passing", rc)
		h.log(&out, in, "WARN", "pass",
			fmt.Sprintf("score-plan.sh exited %d (infra error); no gate action", rc),
			map[string]any{"score_rc": rc})
		return out, nil
	}

	// Read the record the scorer just wrote.
	scoredLog := filepath.Join(tmpHome, ".yakos-state", "plan-quality-log.ndjson")
	if fi, err := os.Stat(scoredLog); err != nil || fi.Size() == 0 {
		h.warnf(&out, "plan-quality-score: log record not found after scoring; passing")
		h.log(&out, in, "WARN", "pass", "no log record after scoring; no gate action", map[string]any{})
		return out, nil
	}
	raw := lastLine(scoredLog)
	var rec logRecord
	if raw == "" || decodeRecord(raw, &rec) != nil {
		h.warnf(&out, "plan-quality-score: invalid log record JSON; passing")
		h.log(&out, in, "WARN", "pass", "invalid log record JSON; no gate action", map[string]any{})
		return out, nil
	}
	if rec.PlanID == "" {
		rec.PlanID = "unknown"
	}

	// Persist the record to the real state log (bash: real_log append).
	if real := h.persistLogPath(in); real != "" {
		_ = os.MkdirAll(filepath.Dir(real), 0o755)                                               //nolint:gosec
		if f, err := os.OpenFile(real, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil { //nolint:gosec
			_, _ = f.WriteString(raw + "\n")
			_ = f.Close()
		}
	}

	planID := rec.PlanID
	aggregate := rec.AggregateScore
	aggStr := rec.AggregateText
	currentDir := h.WorkCurrentDir

	// Dissent path (takes priority over the threshold path; never blocks).
	if rec.Dissent {
		var notesPath string
		if currentDir != "" {
			if notesFile, ok := h.notesFile(&out, in, currentDir, planID); ok {
				notesPath = notesFile
				_ = os.MkdirAll(filepath.Dir(notesFile), 0o755) //nolint:gosec
				content := fmt.Sprintf("# Plan Quality Surface — %s\n\n"+
					"**Reason:** Judge panel dissent (max-min spread >= 0.5 on at least one dimension).\n\n"+
					"## Score summary\n\nplan_id: %s\naggregate_score: %s\n",
					planID, planID, aggStr)
				_ = os.WriteFile(notesFile, []byte(content), 0o644) //nolint:gosec
			}
		}
		h.log(&out, in, "WARN", "surface_to_operator",
			"plan-quality-score: dissent detected; surfaced to operator",
			map[string]any{"plan_id": planID, "aggregate_score": aggStr,
				"decision": "surface_to_operator", "notes_file": notesPath})
		return out, nil
	}

	// Threshold pass path.
	if aggregate >= parseNum(threshold) {
		h.log(&out, in, "REPORT", "pass",
			fmt.Sprintf("plan-quality-score: aggregate=%s >= threshold=%s; PASS", aggStr, threshold),
			map[string]any{"plan_id": planID, "aggregate_score": aggStr,
				"threshold": threshold, "decision": "pass"})
		return out, nil
	}

	// Below-threshold path. The notes file is written in every mode.
	var notesPath string
	if currentDir != "" {
		if notesFile, ok := h.notesFile(&out, in, currentDir, planID); ok {
			notesPath = notesFile
			_ = os.MkdirAll(filepath.Dir(notesFile), 0o755) //nolint:gosec
			content := fmt.Sprintf("# Plan Quality Surface — %s\n\n"+
				"**Reason:** Aggregate score %.4f is below threshold %s.\n\n"+
				"## Score summary\n\nplan_id: %s\naggregate_score: %s\n",
				planID, aggregate, threshold, planID, aggStr)
			_ = os.WriteFile(notesFile, []byte(content), 0o644) //nolint:gosec
		}
	}

	if mode == "block" {
		blockedMarkerFile := filepath.Join(currentDir, ".plan-blocked")
		if currentDir != "" {
			marker := planBlockedMarker{
				PlanID:         planID,
				AggregateScore: aggStr,
				Threshold:      threshold,
				Ts:             nowT.UTC().Format("2006-01-02T15:04:05Z"),
				Reason:         fmt.Sprintf("aggregate %s < threshold %s; plan quality below bar", aggStr, threshold),
			}
			// No HTML escaping: jq (bash) writes "<" literally, and the
			// marker should be byte-comparable with bash's.
			var mb bytes.Buffer
			enc := json.NewEncoder(&mb)
			enc.SetEscapeHTML(false)
			_ = enc.Encode(marker)
			markerData := bytes.TrimRight(mb.Bytes(), "\n")
			_ = atomicWrite(blockedMarkerFile, markerData)
		}
		h.log(&out, in, "WARN", "block_next_tool",
			fmt.Sprintf("plan-quality-score: aggregate=%s < threshold=%s; mode=block; .plan-blocked written", aggStr, threshold),
			map[string]any{"plan_id": planID, "aggregate_score": aggStr,
				"threshold": threshold, "decision": "block_next_tool",
				"marker": blockedMarkerFile, "notes_file": notesPath})
		return out, nil
	}

	h.log(&out, in, "WARN", "surface_to_operator",
		fmt.Sprintf("plan-quality-score: aggregate=%s < threshold=%s; mode=surface; surfaced", aggStr, threshold),
		map[string]any{"plan_id": planID, "aggregate_score": aggStr,
			"threshold": threshold, "decision": "surface_to_operator", "notes_file": notesPath})
	return out, nil
}

// ---- scorer invocation ------------------------------------------------------

// runScorer runs `bash <script> <planFile>` with env and returns its exit
// code. A non-nil error means the process could not be started or was
// killed by a signal (an infra failure); a normal non-zero exit is (rc, nil).
func runScorer(c context.Context, script, planFile string, env []string) (int, error) {
	cmd := exec.CommandContext(c, "bash", script, planFile) //nolint:gosec
	cmd.Env = env
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() >= 0 {
		return ee.ExitCode(), nil
	}
	return -1, err
}

// scorerEnv builds the scorer's environment: the process environment,
// overlaid with the hook input's snapshot (identical in production), then
// with the fixed overrides bash passes. Sorted for determinism.
func scorerEnv(inEnv, overrides map[string]string) []string {
	m := map[string]string{}
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			m[kv[:i]] = kv[i+1:]
		}
	}
	for k, v := range inEnv {
		m[k] = v
	}
	for k, v := range overrides {
		m[k] = v
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, k := range keys {
		env = append(env, k+"="+m[k])
	}
	return env
}

// frameworkRoot mirrors bash: $YAKOS_ROOT, else HOOK_DIR/../..
func (h *Hook) frameworkRoot(in hooktype.HookInput) string {
	if r := in.Env["YAKOS_ROOT"]; r != "" {
		return r
	}
	if h.YakosRoot != "" {
		return h.YakosRoot
	}
	if h.HooksDir != "" {
		return filepath.Clean(filepath.Join(h.HooksDir, "..", ".."))
	}
	return ""
}

func lastLine(path string) string {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	return lines[len(lines)-1]
}

// decodeRecord fills rec from a JSON object, tolerating null or non-numeric
// aggregate_score the way jq's `// 0` does (a null or false value is 0).
func decodeRecord(raw string, rec *logRecord) error {
	var g struct {
		PlanID    any `json:"plan_id"`
		Aggregate any `json:"aggregate_score"`
		Dissent   any `json:"dissent"`
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&g); err != nil {
		return err
	}
	rec.AggregateText = "0"
	if s, ok := g.PlanID.(string); ok {
		rec.PlanID = s
	}
	if n, ok := g.Aggregate.(json.Number); ok {
		if f, err := n.Float64(); err == nil {
			rec.AggregateScore = f
			rec.AggregateText = n.String()
		}
	}
	if b, ok := g.Dissent.(bool); ok {
		rec.Dissent = b
	}
	return nil
}

// parseNum mirrors awk's `x + 0` on a threshold string: a non-numeric
// value is 0.
func parseNum(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return f
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func (h *Hook) now() time.Time {
	if h.NowFn != nil {
		return h.NowFn()
	}
	return time.Now()
}

// warnf is bash's ct_log "WARN: ..." — a timestamped stderr line.
func (h *Hook) warnf(out *hooktype.HookOutput, format string, args ...any) {
	out.Stderr = fmt.Appendf(out.Stderr, "[%s] WARN: %s\n",
		h.now().UTC().Format("2006-01-02T15:04:05Z"), fmt.Sprintf(format, args...))
}

// ---- config helpers ---------------------------------------------------------

// loadConfig ports bash's awk block reader for .yakos.yml (K-107: the reader
// is shared with plan-quality-gate as internal/hooks/yamlblock). It never
// parses the file as YAML, so a syntax or type error in an unrelated key cannot
// discard plan_quality settings (K-99 review): find the `plan_quality:` block,
// then read enabled/mode/threshold/cost_ceiling_usd from its DIRECT children
// only. A child map's own keys, and keys of a sibling under a common parent,
// never bleed in. Inline comments and quotes are stripped; a later duplicate
// key wins; an empty value is ignored.
func (h *Hook) loadConfig(projectDir string) (planQualityConfig, error) {
	var cfg planQualityConfig
	if projectDir == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(filepath.Join(projectDir, ".yakos.yml")) //nolint:gosec
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		// A directory or an unreadable file: fall back to defaults, but say so.
		return cfg, err
	}
	for _, kv := range yamlblock.Children(data, "plan_quality") {
		if kv.Value == "" {
			continue // bash: [ -n "$_v" ] && ...
		}
		switch kv.Key {
		case "enabled":
			cfg.Enabled = kv.Value
		case "mode":
			cfg.Mode = kv.Value
		case "threshold":
			cfg.Threshold = kv.Value
		case "cost_ceiling_usd":
			cfg.CostCeilingUSD = kv.Value
		}
	}
	return cfg, nil
}

// isDecimal reports whether s is a plain finite decimal number. It rejects
// what strconv.ParseFloat also accepts ("nan", "inf", hex floats), any of
// which would make every plan pass or fail.
func isDecimal(s string) bool { return reDecimal.MatchString(strings.TrimSpace(s)) }

var (
	reDecimal = regexp.MustCompile(`^[+-]?([0-9]+\.?[0-9]*|\.[0-9]+)([eE][+-]?[0-9]+)?$`)
)

// ---- persisted-record path -------------------------------------------------

// persistLogPath is where the scored record is appended for the report
// script and history: bash's $HOME/.yakos-state/plan-quality-log.ndjson.
func (h *Hook) persistLogPath(in hooktype.HookInput) string {
	if h.PlanQualityLog != "" {
		return h.PlanQualityLog
	}
	home := h.HomeDir
	if home == "" {
		home = in.Env["HOME"]
	}
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".yakos-state", "plan-quality-log.ndjson")
}

// ---- atomic write -----------------------------------------------------------

func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { //nolint:gosec
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0644); err != nil { //nolint:gosec
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// ---- helpers ----------------------------------------------------------------

func (h *Hook) resolveProjectDir(in hooktype.HookInput) string {
	if h.ProjectDir != "" {
		return h.ProjectDir
	}
	if d := in.Env["CLAUDE_PROJECT_DIR"]; d != "" {
		return d
	}
	return in.WorkDir
}

func stringField(payload map[string]any, key string) string {
	v, ok := payload[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// safePlanID admits only [A-Za-z0-9._-]+ with no leading dot. plan_id is
// model-written (plan.md frontmatter), so it must never reach a path join
// unsanitized.
var safePlanID = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]*$`)

// notesFile returns the notes path for planID under currentDir/notes, or
// ("", false) with a WARN log when planID is not a safe filename component.
func (h *Hook) notesFile(out *hooktype.HookOutput, in hooktype.HookInput, currentDir, planID string) (string, bool) {
	if !safePlanID.MatchString(planID) {
		h.log(out, in, "WARN", "skip_notes",
			"plan-quality-score: unsafe plan_id; notes file not written",
			map[string]any{"plan_id": planID})
		return "", false
	}
	return filepath.Join(currentDir, "notes", fmt.Sprintf("plan-quality-%s.md", planID)), true
}

// log writes one record in bash ho_log's schema.
func (h *Hook) log(out *hooktype.HookOutput, in hooktype.HookInput, severity, decision, reason string, extra map[string]any) {
	agent := strings.TrimSpace(hookio.PayloadString(in, "agent_type"))
	if agent == "" {
		agent = "lead"
	}
	err := hooklog.Append(h.WorkCurrentDir, hooklog.Entry{
		Hook:      hookName,
		Severity:  severity,
		Decision:  decision,
		Reason:    reason,
		Agent:     strings.TrimPrefix(agent, "yakos:"),
		SessionID: hookio.PayloadString(in, "session_id"),
		Event:     in.Event,
		Extra:     extra,
	}, h.now())
	if err != nil {
		out.Stderr = fmt.Appendf(out.Stderr, "%s: log: %v\n", hookName, err)
	}
}
