// Package planqualitygate is the Go-native Tier-0 port of
// lib/hooks/plan-quality-gate.sh: the PreToolUse gate that blocks
// TeamCreate|Agent while a work/current/.plan-blocked marker is present.
//
// It FAILS CLOSED (K-81). Split from the old dual-purpose hook so the gate no
// longer inherits the PostToolUse scorer's fail-open posture — the scorer now
// lives in the sibling planqualityscore package.
//
// Exit 2 (block) with a stderr reason on: an empty tool name, an unresolvable
// or uninspectable work/current, a marker that exists in any form (a
// directory or dangling symlink counts), and any panic. Exit 0 only for a
// tool other than exactly TeamCreate/Agent, a genuinely absent marker, the
// plan_quality.enabled=false opt-out, or YAKOS_PLAN_QUALITY_DISABLE=1.
//
// Undecodable stdin is handled one layer up (cmd_hook.failDegraded), keyed on
// the registry's FailClosed flag for this hook.
package planqualitygate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/hooklog"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

const hookName = "plan-quality-gate"

// planQualityConfig holds the parsed plan_quality block from .yakos.yml.
type planQualityConfig struct {
	Enabled *bool `yaml:"enabled"`
}

type yakosYMLPlanQuality struct {
	PlanQuality *planQualityConfig `yaml:"plan_quality"`
}

// planBlockedMarker is the JSON written to .plan-blocked by the scorer.
type planBlockedMarker struct {
	PlanID string `json:"plan_id"`
	Reason string `json:"reason"`
}

// Hook implements runner.Hook for the plan-quality PreToolUse gate.
type Hook struct {
	// WorkCurrentDir is the absolute path to work/current/ for the active session.
	WorkCurrentDir string

	// ProjectDir is the project root where .yakos.yml is located.
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

// Run executes the gate. It never returns ExitCode 0 alongside an error, and a
// panic is converted to a block: every failure path is exit 2.
func (h *Hook) Run(_ context.Context, in hooktype.HookInput) (out hooktype.HookOutput, err error) {
	defer func() {
		if r := recover(); r != nil {
			out = blocked(fmt.Sprintf("internal error: %v", r))
			err = fmt.Errorf("%s: panic: %v", hookName, r)
		}
	}()
	return h.run(in), nil
}

func blocked(reason string) hooktype.HookOutput {
	msg := fmt.Sprintf("%s: BLOCKED — %s; failing closed rather than passing the dispatch.\n"+
		"%s: emergency override: export YAKOS_PLAN_QUALITY_DISABLE=1\n", hookName, reason, hookName)
	return hooktype.HookOutput{ExitCode: 2, Stderr: []byte(msg)}
}

func (h *Hook) run(in hooktype.HookInput) hooktype.HookOutput {
	// Emergency bypass, honored before anything else.
	if in.Env["YAKOS_PLAN_QUALITY_DISABLE"] == "1" {
		h.appendLog(in, "WARN", "pass", "YAKOS_PLAN_QUALITY_DISABLE=1: gate bypassed", nil)
		return hooktype.HookOutput{ExitCode: 0}
	}

	if in.Tool == "" {
		return blocked("payload has no tool_name; cannot decide whether this dispatch is gated")
	}
	// Exact match only: "AgentX" is not gated.
	if in.Tool != "TeamCreate" && in.Tool != "Agent" {
		return hooktype.HookOutput{ExitCode: 0}
	}

	currentDir := h.WorkCurrentDir
	if currentDir == "" {
		return blocked("work/current directory could not be resolved")
	}
	marker := filepath.Join(currentDir, ".plan-blocked")

	// Lstat: a dangling symlink or a directory is still "a marker".
	if _, statErr := os.Lstat(marker); statErr != nil {
		if !errors.Is(statErr, fs.ErrNotExist) {
			// EACCES, ENOTDIR, ELOOP ...: cannot tell whether the marker exists.
			return blocked(fmt.Sprintf("cannot inspect %s (%v)", marker, statErr))
		}
		// ENOENT is truthful here (Go reports EACCES separately). If the
		// directory exists it must also be listable, matching the bash gate.
		d, openErr := os.Open(currentDir) //nolint:gosec
		if openErr == nil {
			fi, dirErr := d.Stat()
			_ = d.Close()
			// Windows reports a path under a regular file as not-exist
			// rather than ENOTDIR, so confirm work/current is a directory.
			if dirErr != nil || !fi.IsDir() {
				return blocked(fmt.Sprintf("%s exists but is not a directory", currentDir))
			}
		} else if !errors.Is(openErr, fs.ErrNotExist) {
			return blocked(fmt.Sprintf("cannot read %s (%v)", currentDir, openErr))
		}
		h.appendLog(in, "REPORT", "pass", "no .plan-blocked marker; proceeding", nil)
		return hooktype.HookOutput{ExitCode: 0}
	}

	// Read the marker for the message (best effort; never changes the decision).
	planID, reason := "", ""
	if fi, sErr := os.Stat(marker); sErr == nil && fi.Mode().IsRegular() {
		planID, reason = readBlockedMarker(marker)
	} else {
		reason = fmt.Sprintf("marker '%s' exists but is not a regular file; remove it by hand", marker)
	}

	// Per-project opt-out: enabled:false clears the marker and passes.
	if projectDir := h.resolveProjectDir(in); projectDir != "" && isGateDisabledByYAML(projectDir) {
		_ = os.Remove(marker)
		h.appendLog(in, "REPORT", "pass", "plan_quality.enabled=false; .plan-blocked marker cleared", nil)
		return hooktype.HookOutput{ExitCode: 0}
	}

	overrideHint := "  yakos plan score override <plan_id> --reason \"reviewed\""
	if planID != "" {
		overrideHint = fmt.Sprintf("  yakos plan score override %s --reason \"reviewed\"", planID)
	}

	pidForLog := planID
	if pidForLog == "" {
		pidForLog = "unknown"
	}
	h.appendLog(in, "BLOCK", "block", "plan quality gate: .plan-blocked marker present",
		map[string]any{"plan_id": pidForLog, "reason": reason})

	// bash's ho_block prefixes the message with "<hook>: ".
	msg := fmt.Sprintf(
		hookName+": Plan quality gate: plan scored below threshold — dispatch blocked.\n"+
			"Plan ID: %s\n"+
			"Reason:  %s\n"+
			"Override with:\n"+
			"%s\n"+
			"Or review the score:  yakos plan score show\n"+
			"Or view history:      yakos plan score history",
		orDefault(planID, "unknown"), orDefault(reason, "plan scored below threshold"), overrideHint)
	return hooktype.HookOutput{ExitCode: 2, Stderr: []byte(msg + "\n")}
}

// ---- helpers ----------------------------------------------------------------

// orDefault mirrors bash's ${v:-default}.
func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func isGateDisabledByYAML(projectDir string) bool {
	data, err := os.ReadFile(filepath.Join(projectDir, ".yakos.yml")) //nolint:gosec
	if err != nil {
		return false
	}
	var doc yakosYMLPlanQuality
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return false
	}
	return doc.PlanQuality != nil && doc.PlanQuality.Enabled != nil && !*doc.PlanQuality.Enabled
}

func readBlockedMarker(path string) (planID, reason string) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return "", ""
	}
	var m planBlockedMarker
	if json.Unmarshal(data, &m) == nil {
		return m.PlanID, m.Reason
	}
	// Plain-text fallback: first line of the file.
	lines := splitLines(string(data), 3)
	if len(lines) > 0 {
		return "", lines[0]
	}
	return "", ""
}

func splitLines(s string, max int) []string {
	var out []string
	start := 0
	for i := 0; i < len(s) && len(out) < max; i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) && len(out) < max {
		out = append(out, s[start:])
	}
	return out
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

// appendLog writes one record in bash ho_log's schema via the shared hooklog
// writer. A log failure must never change the gate decision, so errors are
// dropped.
func (h *Hook) appendLog(in hooktype.HookInput, severity, decision, reason string, extra map[string]any) {
	now := h.NowFn
	if now == nil {
		now = time.Now
	}
	_ = hooklog.Append(h.WorkCurrentDir, hooklog.Entry{
		Hook:      hookName,
		Severity:  severity,
		Decision:  decision,
		Reason:    reason,
		Agent:     senderRole(in),
		SessionID: hookio.PayloadString(in, "session_id"),
		Event:     in.Event,
		Extra:     extra,
	}, now())
}

// senderRole matches hi_sender_role: agent_type (trimmed, "yakos:" prefix
// stripped), or "lead" when absent.
func senderRole(in hooktype.HookInput) string {
	return hookio.SenderRole(in)
}
