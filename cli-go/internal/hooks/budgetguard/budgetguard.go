// Package budgetguard is the Go-native Tier-0 port of lib/hooks/budget-guard.sh.
//
// PreToolUse hook that enforces per-session budget caps:
//
//  1. max_tool_calls          — total tool-call count this session
//  2. max_wall_seconds        — wall-clock since first tool call
//  3. max_repeat_same_tool    — same tool repeated N times in a row (loop detection)
//
// Configuration in .yakos.yml:
//
//	budget:
//	  enabled: true
//	  max_tool_calls: 500
//	  max_wall_seconds: 7200
//	  max_repeat_same_tool: 8
//
// All caps optional; missing keys are not enforced.
// State is persisted atomically (temp-rename, Q8) in work/current/.budget-state.json.
//
// Emergency bypass: set YAKOS_BUDGET_DISABLE=1 in Env.
// Per-cap bypass: add a hook-bypass.md entry with the relevant cap name.
package budgetguard

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/bakw00ds/yakos/internal/hooks/hookbypass"
	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/hooklog"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

const hookName = "budget-guard"

// BudgetConfig holds the parsed budget section from .yakos.yml.
type BudgetConfig struct {
	Enabled           *bool `yaml:"enabled"`
	MaxToolCalls      *int  `yaml:"max_tool_calls"`
	MaxWallSeconds    *int  `yaml:"max_wall_seconds"`
	MaxRepeatSameTool *int  `yaml:"max_repeat_same_tool"`
}

// budgetState is the JSON state persisted across tool calls.
type budgetState struct {
	SessionID        string `json:"session_id"`
	StartedAt        int64  `json:"started_at"`
	ToolCallCount    int    `json:"tool_call_count"`
	LastTool         string `json:"last_tool"`
	LastToolRunCount int    `json:"last_tool_run_count"`
}

// Hook implements runner.Hook for budget enforcement.
type Hook struct {
	// WorkCurrentDir is the absolute path to work/current/ for the active session.
	WorkCurrentDir string

	// ProjectDir is the project root where .yakos.yml is located.
	// When empty, uses CLAUDE_PROJECT_DIR env var or falls back to "".
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

// Run executes the budget-guard logic.
// Returns ExitCode=2 (block) when a cap is exceeded and no bypass is active.
func (h *Hook) Run(_ context.Context, in hooktype.HookInput) (hooktype.HookOutput, error) {
	out := hooktype.HookOutput{ExitCode: 0}

	// Emergency env bypass.
	if in.Env["YAKOS_BUDGET_DISABLE"] == "1" {
		return out, nil
	}

	// No active session.
	if h.WorkCurrentDir == "" {
		return out, nil
	}

	// Locate .yakos.yml.
	projectDir := h.ProjectDir
	if projectDir == "" {
		projectDir = in.Env["CLAUDE_PROJECT_DIR"]
	}
	if projectDir == "" {
		projectDir = in.WorkDir
	}
	yakosYML := filepath.Join(projectDir, ".yakos.yml")
	cfg, err := loadBudgetConfig(yakosYML)
	if err != nil || cfg == nil {
		// No config or parse error → no enforcement.
		return out, nil
	}
	if cfg.Enabled != nil && !*cfg.Enabled {
		return out, nil
	}
	// No caps configured at all.
	if cfg.MaxToolCalls == nil && cfg.MaxWallSeconds == nil && cfg.MaxRepeatSameTool == nil {
		return out, nil
	}

	stateFile := filepath.Join(h.WorkCurrentDir, ".budget-state.json")
	now := time.Now()
	if h.NowFn != nil {
		now = h.NowFn()
	}
	nowEpoch := now.Unix()
	tool := in.Tool
	// session_id comes from the stdin payload (hi_session_id), not an env
	sessionID := hookio.PayloadString(in, "session_id")

	// Load or initialize state.
	state := loadBudgetState(stateFile, sessionID, nowEpoch)

	// Increment counters.
	state.ToolCallCount++
	if tool == state.LastTool {
		state.LastToolRunCount++
	} else {
		state.LastToolRunCount = 1
	}
	state.LastTool = tool

	// Persist updated state atomically.
	if err := saveBudgetState(stateFile, state); err != nil {
		out.Stderr = fmt.Appendf(out.Stderr, "budget-guard: save state: %v\n", err)
		// Non-fatal; continue to enforcement.
	}

	elapsed := int(nowEpoch - state.StartedAt)
	// ---- check max_tool_calls ----
	if cfg.MaxToolCalls != nil && state.ToolCallCount > *cfg.MaxToolCalls {
		if h.isBypassed("cap=max_tool_calls") {
			h.appendLog(&out, in, now, "WARN", "pass",
				"max_tool_calls exceeded but bypass active",
				map[string]any{"cap": "max_tool_calls", "tool_call_count": state.ToolCallCount,
					"elapsed_seconds": elapsed, "repeat_count": state.LastToolRunCount, "tool": tool, "bypass": true})
			return out, nil
		} else {
			h.appendLog(&out, in, now, "BLOCK", "block",
				fmt.Sprintf("session tool-call count %d exceeds cap %d", state.ToolCallCount, *cfg.MaxToolCalls),
				map[string]any{"cap": "max_tool_calls", "tool_call_count": state.ToolCallCount,
					"elapsed_seconds": elapsed, "repeat_count": state.LastToolRunCount, "tool": tool, "bypass": false})
			// Indentation and wording match bash's literal heredoc-style
			// message byte for byte (ho_block prints "${hook}: ${reason}"
			// verbatim, including the source's embedded leading whitespace
			// on continuation lines).
			msg := fmt.Sprintf(
				"budget-guard: session tool-call budget exceeded: %d > %d.\n"+
					"       This is a session-level cap configured in .yakos.yml budget.max_tool_calls.\n"+
					"       Options:\n"+
					"         1. Wait for the next session (counters reset per session)\n"+
					"         2. Add a bypass entry in work/current/hook-bypass.md with\n"+
					"            Scope: cap=max_tool_calls\n"+
					"         3. Raise the cap in .yakos.yml budget.max_tool_calls\n"+
					"         4. Emergency: export YAKOS_BUDGET_DISABLE=1",
				state.ToolCallCount, *cfg.MaxToolCalls)
			out.Stderr = append(out.Stderr, []byte(msg+"\n")...)
			out.ExitCode = 2
			return out, nil
		}
	}

	// ---- check max_wall_seconds ----
	if cfg.MaxWallSeconds != nil && elapsed > *cfg.MaxWallSeconds {
		if h.isBypassed("cap=max_wall_seconds") {
			h.appendLog(&out, in, now, "WARN", "pass",
				"max_wall_seconds exceeded but bypass active",
				map[string]any{"cap": "max_wall_seconds", "tool_call_count": state.ToolCallCount,
					"elapsed_seconds": elapsed, "repeat_count": state.LastToolRunCount, "tool": tool, "bypass": true})
			return out, nil
		} else {
			h.appendLog(&out, in, now, "BLOCK", "block",
				fmt.Sprintf("session elapsed %ds exceeds cap %ds", elapsed, *cfg.MaxWallSeconds),
				map[string]any{"cap": "max_wall_seconds", "tool_call_count": state.ToolCallCount,
					"elapsed_seconds": elapsed, "repeat_count": state.LastToolRunCount, "tool": tool, "bypass": false})
			msg := fmt.Sprintf(
				"budget-guard: session wall-clock budget exceeded: %ds > %ds.\n"+
					"       This is a session-level cap configured in .yakos.yml budget.max_wall_seconds.\n"+
					"       Options:\n"+
					"         1. End the current session (counters reset)\n"+
					"         2. Add a bypass entry with Scope: cap=max_wall_seconds\n"+
					"         3. Raise the cap in .yakos.yml budget.max_wall_seconds\n"+
					"         4. Emergency: export YAKOS_BUDGET_DISABLE=1",
				elapsed, *cfg.MaxWallSeconds)
			out.Stderr = append(out.Stderr, []byte(msg+"\n")...)
			out.ExitCode = 2
			return out, nil
		}
	}

	// ---- check max_repeat_same_tool ----
	if cfg.MaxRepeatSameTool != nil && state.LastToolRunCount > *cfg.MaxRepeatSameTool {
		if h.isBypassed("cap=max_repeat_same_tool") {
			h.appendLog(&out, in, now, "WARN", "pass",
				fmt.Sprintf("%s repeated %d times in a row but bypass active", tool, state.LastToolRunCount),
				map[string]any{"cap": "max_repeat_same_tool", "tool_call_count": state.ToolCallCount,
					"elapsed_seconds": elapsed, "repeat_count": state.LastToolRunCount, "tool": tool, "bypass": true})
			return out, nil
		} else {
			h.appendLog(&out, in, now, "BLOCK", "block",
				fmt.Sprintf("%s repeated %d times in a row, exceeds cap %d (likely loop)", tool, state.LastToolRunCount, *cfg.MaxRepeatSameTool),
				map[string]any{"cap": "max_repeat_same_tool", "tool_call_count": state.ToolCallCount,
					"elapsed_seconds": elapsed, "repeat_count": state.LastToolRunCount, "tool": tool, "bypass": false})
			msg := fmt.Sprintf(
				"budget-guard: loop suspected: '%s' invoked %d times in a row, exceeds cap %d.\n"+
					"       This is loop-detection cap configured in .yakos.yml budget.max_repeat_same_tool.\n"+
					"       Common causes:\n"+
					"         - Agent stuck retrying a failing tool call\n"+
					"         - Agent reading many files sequentially when a glob would suffice\n"+
					"         - Genuine workflow that happens to use the same tool a lot\n"+
					"       Options:\n"+
					"         1. Reconsider whether the work needs %s repeatedly\n"+
					"         2. Add bypass with Scope: cap=max_repeat_same_tool (if this is\n"+
					"            a genuine batch operation, not a loop)\n"+
					"         3. Raise the cap in .yakos.yml budget.max_repeat_same_tool\n"+
					"         4. Emergency: export YAKOS_BUDGET_DISABLE=1",
				tool, state.LastToolRunCount, *cfg.MaxRepeatSameTool, tool)
			out.Stderr = append(out.Stderr, []byte(msg+"\n")...)
			out.ExitCode = 2
			return out, nil
		}
	}

	// All caps respected.
	h.appendLog(&out, in, now, "REPORT", "pass", "within all budget caps",
		map[string]any{"tool_call_count": state.ToolCallCount,
			"elapsed_seconds": elapsed, "repeat_count": state.LastToolRunCount, "tool": tool})

	return out, nil
}

// isBypassed checks hook-bypass.md for an entry mentioning this hook and scope.
// isBypassed replicates `ho_check_bypass "budget" "$scope"` exactly — note
// the probe hook-name bash actually uses is the literal string "budget",
// NOT "budget-guard" (the hook's own registered name). An operator's
// hook-bypass.md entry for this cap is therefore written with
// `**Hook:** budget`, and would never have matched the previous ad hoc
// strings.Contains(content, "budget-guard") check.
func (h *Hook) isBypassed(scope string) bool {
	if h.WorkCurrentDir == "" {
		return false
	}
	bypassFile := filepath.Join(h.WorkCurrentDir, "hook-bypass.md")
	data, err := os.ReadFile(bypassFile) //nolint:gosec
	if err != nil {
		return false
	}
	return hookbypass.Check(string(data), "budget", scope)
}

// appendLog writes an NDJSON log entry via the shared hooklog writer —
// field set/order matches bash's ho_log exactly (agent/session_id/event
// were previously entirely absent, and decision/reason were named
// action/message).
func (h *Hook) appendLog(out *hooktype.HookOutput, in hooktype.HookInput, now time.Time, severity, decision, reason string, extra map[string]any) {
	err := hooklog.Append(h.WorkCurrentDir, hooklog.Entry{
		Hook:      hookName,
		Severity:  severity,
		Decision:  decision,
		Reason:    reason,
		Agent:     senderRole(in),
		SessionID: hookio.PayloadString(in, "session_id"),
		Event:     in.Event,
		Extra:     extra,
	}, now)
	if err != nil {
		out.Stderr = fmt.Appendf(out.Stderr, "%s: log: %v\n", hookName, err)
	}
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

// ---- state helpers -----------------------------------------------------------

func loadBudgetState(stateFile, sessionID string, nowEpoch int64) *budgetState {
	data, err := os.ReadFile(stateFile) //nolint:gosec
	if err == nil {
		var s budgetState
		if err := json.Unmarshal(data, &s); err == nil && s.StartedAt > 0 {
			return &s
		}
	}
	return &budgetState{
		SessionID: sessionID,
		StartedAt: nowEpoch,
	}
}

func saveBudgetState(stateFile string, state *budgetState) error {
	if err := os.MkdirAll(filepath.Dir(stateFile), 0755); err != nil { //nolint:gosec
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	tmp := stateFile + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0644); err != nil { //nolint:gosec
		return err
	}
	if err := os.Rename(tmp, stateFile); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// ---- YAML helpers ------------------------------------------------------------

// yakosYMLBudget is the minimal shape we need to unmarshal from .yakos.yml.
type yakosYMLBudget struct {
	Budget *BudgetConfig `yaml:"budget"`
}

// loadBudgetConfig reads and parses the budget section from .yakos.yml.
// Returns nil (no error) when the file is absent or has no budget section.
func loadBudgetConfig(path string) (*BudgetConfig, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var doc yakosYMLBudget
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return doc.Budget, nil
}
