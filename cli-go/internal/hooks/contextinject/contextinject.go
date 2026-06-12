// Package contextinject is the Go-native Tier-0 port of
// lib/hooks/context-inject.sh.
//
// UserPromptSubmit hook that injects useful session context for
// long-running work (v0.35+). Before each user prompt is processed,
// this hook surfaces:
//
//  1. Tail of decisions.md (last 20 lines) — what was decided recently
//  2. Budget remaining from .budget-state.json — heads-up on caps
//  3. Peer-status one-liner (if YAKOS_COORD_ENABLED is set) — who is active
//  4. Most recent supervisor CRITICAL finding — outstanding judgments
//
// All informational output goes to Stderr (Claude Code surfaces hook
// stderr as system-message context).
//
// Opt-in: requires context_inject.enabled: true in .yakos.yml.
// Emergency disable: YAKOS_CONTEXT_INJECT_DISABLE=1.
//
// Never blocks. Always exits 0.
package contextinject

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

const hookName = "context-inject"

// yakosYMLContextInject holds the parsed shape from .yakos.yml.
type yakosYMLContextInject struct {
	ContextInject *contextInjectConfig `yaml:"context_inject"`
	Budget        *budgetCapsConfig    `yaml:"budget"`
}

type contextInjectConfig struct {
	Enabled          *bool `yaml:"enabled"`
	InjectDecisions  *bool `yaml:"inject_decisions"`
	InjectBudget     *bool `yaml:"inject_budget"`
	InjectPeers      *bool `yaml:"inject_peers"`
	InjectSupervisor *bool `yaml:"inject_supervisor"`
}

type budgetCapsConfig struct {
	MaxToolCalls   *int `yaml:"max_tool_calls"`
	MaxWallSeconds *int `yaml:"max_wall_seconds"`
}

// budgetState matches the shape written by budgetguard.
type budgetState struct {
	StartedAt     int64  `json:"started_at"`
	ToolCallCount int    `json:"tool_call_count"`
	SessionID     string `json:"session_id"`
}

// supervisorFinding is a partial parse of supervisor-findings.ndjson lines.
type supervisorFinding struct {
	Ts        string `json:"ts"`
	Overall   string `json:"overall"`
	Rationale string `json:"rationale"`
}

// Hook implements runner.Hook for context injection.
type Hook struct {
	// WorkCurrentDir is the absolute path to work/current/ for the active session.
	WorkCurrentDir string

	// ProjectDir is the project root where .yakos.yml lives.
	// When empty, CLAUDE_PROJECT_DIR env var is used.
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

// Run executes the context-inject logic. Always returns ExitCode 0.
func (h *Hook) Run(_ context.Context, in hooktype.HookInput) (hooktype.HookOutput, error) {
	out := hooktype.HookOutput{ExitCode: 0}

	// Emergency disable.
	if in.Env["YAKOS_CONTEXT_INJECT_DISABLE"] == "1" {
		return out, nil
	}

	projectDir := h.resolveProjectDir(in)
	yakosYML := filepath.Join(projectDir, ".yakos.yml")

	cfg, err := loadContextInjectConfig(yakosYML)
	if err != nil || cfg == nil {
		// No file or no section: feature is off by default.
		return out, nil
	}
	if cfg.ContextInject == nil || cfg.ContextInject.Enabled == nil || !*cfg.ContextInject.Enabled {
		return out, nil
	}

	if h.WorkCurrentDir == "" {
		return out, nil
	}

	ci := cfg.ContextInject
	emitted := false

	// --- 1. Recent decisions ---
	if sectionEnabled(ci.InjectDecisions) {
		decisionsFile := filepath.Join(h.WorkCurrentDir, "decisions.md")
		if lines, err := tailFile(decisionsFile, 20); err == nil && len(lines) > 0 {
			out.Stderr = fmt.Appendf(out.Stderr,
				"[yakos context-inject] Recent decisions (decisions.md tail):\n")
			for _, l := range lines {
				out.Stderr = fmt.Appendf(out.Stderr, "  %s\n", l)
			}
			out.Stderr = append(out.Stderr, '\n')
			emitted = true
		}
	}

	// --- 2. Budget remaining ---
	if sectionEnabled(ci.InjectBudget) {
		stateFile := filepath.Join(h.WorkCurrentDir, ".budget-state.json")
		data, readErr := os.ReadFile(stateFile) //nolint:gosec
		if readErr == nil {
			var state budgetState
			if jsonErr := json.Unmarshal(data, &state); jsonErr == nil && state.StartedAt > 0 {
				now := h.NowFn().Unix()
				elapsed := int(now - state.StartedAt)
				if cfg.Budget != nil && (cfg.Budget.MaxToolCalls != nil || cfg.Budget.MaxWallSeconds != nil) {
					out.Stderr = fmt.Appendf(out.Stderr, "[yakos context-inject] Budget:\n")
					if cfg.Budget.MaxToolCalls != nil {
						out.Stderr = fmt.Appendf(out.Stderr,
							"  tool calls: %d / %d\n", state.ToolCallCount, *cfg.Budget.MaxToolCalls)
					}
					if cfg.Budget.MaxWallSeconds != nil {
						out.Stderr = fmt.Appendf(out.Stderr,
							"  wall-clock: %ds / %ds\n", elapsed, *cfg.Budget.MaxWallSeconds)
					}
					out.Stderr = append(out.Stderr, '\n')
					emitted = true
				}
			}
		}
	}

	// --- 3. Peer status (multi-dev) ---
	// Mirror bash logic: check YAKOS_COORD_ENABLED env gate; read sessions dir.
	if sectionEnabled(ci.InjectPeers) {
		if in.Env["YAKOS_COORD_ENABLED"] == "1" {
			sessionsDir := resolveCoordSessionsDir(in)
			if sessionsDir != "" {
				if entries, readErr := os.ReadDir(sessionsDir); readErr == nil {
					n := 0
					for _, e := range entries {
						if strings.HasSuffix(e.Name(), ".ndjson") {
							n++
						}
					}
					if n > 1 {
						out.Stderr = fmt.Appendf(out.Stderr,
							"[yakos context-inject] Multi-dev: %d peer session(s) active.\n", n)
						out.Stderr = fmt.Appendf(out.Stderr,
							"  Run 'yakos peer status' for details, 'yakos peer log' for recent activity.\n\n")
						emitted = true
					}
				}
			}
		}
	}

	// --- 4. Recent supervisor CRITICAL finding ---
	if sectionEnabled(ci.InjectSupervisor) {
		findingsFile := filepath.Join(h.WorkCurrentDir, "supervisor-findings.ndjson")
		if lastCritical, readErr := lastCriticalFinding(findingsFile); readErr == nil && lastCritical != nil {
			out.Stderr = fmt.Appendf(out.Stderr,
				"[yakos context-inject] Most recent supervisor CRITICAL (%s):\n", lastCritical.Ts)
			out.Stderr = fmt.Appendf(out.Stderr,
				"  %s\n", lastCritical.Rationale)
			out.Stderr = fmt.Appendf(out.Stderr,
				"  (review: yakos supervise tail)\n\n")
			emitted = true
		}
	}

	// Audit log entry.
	logFile := filepath.Join(h.WorkCurrentDir, "logs", hookName+".ndjson")
	if emitted {
		h.appendLog(&out, logFile, "REPORT", "pass", "injected session context",
			map[string]any{"emitted": true})
	} else {
		h.appendLog(&out, logFile, "REPORT", "pass", "no context to inject",
			map[string]any{"emitted": false})
	}

	return out, nil
}

// ---- helpers -----------------------------------------------------------------

func loadContextInjectConfig(yakosYML string) (*yakosYMLContextInject, error) {
	data, err := os.ReadFile(yakosYML) //nolint:gosec
	if err != nil {
		return nil, err
	}
	var doc yakosYMLContextInject
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// sectionEnabled returns true when the toggle is nil (default on when outer
// enabled=true) or explicitly true.
func sectionEnabled(toggle *bool) bool {
	return toggle == nil || *toggle
}

// tailFile returns the last n lines of path.
func tailFile(path string, n int) ([]string, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(lines) <= n {
		return lines, nil
	}
	return lines[len(lines)-n:], nil
}

// lastCriticalFinding scans findings file for the last CRITICAL finding.
func lastCriticalFinding(path string) (*supervisorFinding, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, err
	}
	var last *supervisorFinding
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var f supervisorFinding
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			continue
		}
		if f.Overall == "CRITICAL" {
			f2 := f
			last = &f2
		}
	}
	return last, nil
}

// resolveCoordSessionsDir derives the coord sessions directory from env.
// Mirrors bash yakos_coord_sessions_dir: $YAKOS_COORD_DIR/sessions or
// /var/lib/yakos/<project>/coord/sessions.
func resolveCoordSessionsDir(in hooktype.HookInput) string {
	if d := in.Env["YAKOS_COORD_DIR"]; d != "" {
		return filepath.Join(d, "sessions")
	}
	proj := in.Env["YAKOS_PROJECT_NAME"]
	if proj == "" {
		return ""
	}
	return filepath.Join("/var/lib/yakos", proj, "coord", "sessions")
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
	defer func() { _ = f.Close() }()
	_, _ = f.Write(data)
}
