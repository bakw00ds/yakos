// Package pathallowlist is the Go-native Tier-0 port of
// lib/hooks/path-allowlist.sh.
//
// PreToolUse hook on Edit, Write, and MultiEdit. Enforces per-agent path
// allowlists loaded from <project>/.claude/path-allowlist.json:
//
//	{ "<agent_type>": { "allow": [glob, ...], "deny": [glob, ...] }, ... }
//
// Decision rule (mirrors bash original exactly):
//   - No policy for this agent → PASS (no enforcement).
//   - deny glob matches → BLOCK (unless bypass active).
//   - allow list present and no glob matches → BLOCK (unless bypass active).
//   - Otherwise → PASS.
//
// Glob matching uses Go's filepath.Match (same fnmatch semantics as bash's
// case statement with `**` collapsed to `*` for v0.1 parity with the bash
// implementation's known simplification).
package pathallowlist

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

const hookName = "path-allowlist"

// Policy is the allowlist/denylist for one agent type.
type Policy struct {
	Allow []string `json:"allow"`
	Deny  []string `json:"deny"`
}

// Hook implements runner.Hook for path allowlist enforcement.
type Hook struct {
	// WorkCurrentDir is the absolute path to work/current/ for bypass checks.
	WorkCurrentDir string

	// ProjectDir is the project root where .claude/path-allowlist.json is located.
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

// Run executes the path allowlist enforcement.
// Returns ExitCode=2 (block) when a deny or out-of-allow policy matches.
func (h *Hook) Run(_ context.Context, in hooktype.HookInput) (hooktype.HookOutput, error) {
	out := hooktype.HookOutput{ExitCode: 0}

	// Only fire on Edit, Write, MultiEdit.
	switch in.Tool {
	case "Edit", "Write", "MultiEdit":
	default:
		return out, nil
	}

	agentType := senderRole(in)
	filePath := fileFromPayload(in)
	logFile := h.logFile()

	if filePath == "" {
		h.appendLog(&out, logFile, "REPORT", "pass", "no file_path in tool_input",
			map[string]any{"agent_type": agentType, "tool": in.Tool})
		return out, nil
	}

	// Make path repo-relative for matching.
	// Normalise to forward slashes so glob patterns (which always use "/")
	// match correctly on Windows where filepath.Join produces backslashes.
	projectDir := h.resolveProjectDir(in)
	relFile := filepath.ToSlash(filePath)
	projSlash := filepath.ToSlash(projectDir)
	if projSlash != "" && strings.HasPrefix(relFile, projSlash+"/") {
		relFile = relFile[len(projSlash)+1:]
	}

	// Load the allowlist.
	allowlistFile := filepath.Join(projectDir, ".claude", "path-allowlist.json")
	policies, err := loadPolicies(allowlistFile)
	if err != nil || policies == nil {
		h.appendLog(&out, logFile, "WARN", "pass",
			"no allowlist file at .claude/path-allowlist.json",
			map[string]any{"agent_type": agentType, "file_path": relFile, "note": "no path-allowlist.json"})
		return out, nil
	}

	// Look up agent policy.
	policy, ok := policies[agentType]
	if !ok {
		h.appendLog(&out, logFile, "REPORT", "pass", "no policy for agent_type",
			map[string]any{"agent_type": agentType, "file_path": relFile, "note": "no policy for agent"})
		return out, nil
	}

	// ---- check deny patterns first ----
	for _, g := range policy.Deny {
		if globMatch(g, relFile) {
			if h.isBypassed(relFile) {
				h.appendLog(&out, logFile, "WARN", "pass", "deny matched but bypass active",
					map[string]any{"agent_type": agentType, "file_path": relFile, "matched_deny": g, "bypass": true})
				return out, nil
			}
			h.appendLog(&out, logFile, "BLOCK", "block", "deny pattern matched",
				map[string]any{"agent_type": agentType, "file_path": relFile, "matched_deny": g})
			msg := fmt.Sprintf("path-allowlist: agent '%s' is forbidden from editing '%s' (deny: %s)",
				agentType, relFile, g)
			out.Stderr = append(out.Stderr, []byte(msg+"\n")...)
			out.ExitCode = 2
			return out, nil
		}
	}

	// ---- check allow patterns ----
	if len(policy.Allow) > 0 {
		matched := ""
		for _, g := range policy.Allow {
			if globMatch(g, relFile) {
				matched = g
				break
			}
		}
		if matched == "" {
			if h.isBypassed(relFile) {
				h.appendLog(&out, logFile, "WARN", "pass", "outside allow but bypass active",
					map[string]any{"agent_type": agentType, "file_path": relFile, "note": "outside allow", "bypass": true})
				return out, nil
			}
			h.appendLog(&out, logFile, "BLOCK", "block", "path outside agent's allow-list",
				map[string]any{"agent_type": agentType, "file_path": relFile, "note": "outside allow"})
			msg := fmt.Sprintf("path-allowlist: agent '%s' may only edit paths in the allow-list; '%s' is outside it",
				agentType, relFile)
			out.Stderr = append(out.Stderr, []byte(msg+"\n")...)
			out.ExitCode = 2
			return out, nil
		}
		h.appendLog(&out, logFile, "REPORT", "pass", "allow matched",
			map[string]any{"agent_type": agentType, "file_path": relFile, "matched_allow": matched})
		return out, nil
	}

	// No allow/deny match — permissive pass.
	h.appendLog(&out, logFile, "REPORT", "pass", "no allow/deny match",
		map[string]any{"agent_type": agentType, "file_path": relFile})
	return out, nil
}

// ---- glob matching -----------------------------------------------------------

// globMatch returns true if g matches p using filepath.Match semantics,
// with a v0.1 simplification: '**' is collapsed to '*' (parity with bash
// implementation).
//
// Both g and p are expected to use forward slashes (callers normalise via
// filepath.ToSlash before calling). We use path.Match (not filepath.Match) so
// the separator is always "/" regardless of the host OS, making glob patterns
// platform-agnostic.
func globMatch(g, p string) bool {
	// Normalise to forward slashes so patterns defined with "/" match on
	// Windows where paths might still contain backslashes.
	g = filepath.ToSlash(g)
	p = filepath.ToSlash(p)

	// Direct match.
	if ok, _ := filepath.Match(g, p); ok {
		return true
	}
	// Collapse ** → * (v0.1 simplification).
	g2 := strings.ReplaceAll(g, "**", "*")
	if ok, _ := filepath.Match(g2, p); ok {
		return true
	}
	// Try matching just the basename.
	base := filepath.Base(p)
	if ok, _ := filepath.Match(g, base); ok {
		return true
	}
	return false
}

// ---- bypass ------------------------------------------------------------------

func (h *Hook) isBypassed(filePath string) bool {
	if h.WorkCurrentDir == "" {
		return false
	}
	bypassFile := filepath.Join(h.WorkCurrentDir, "hook-bypass.md")
	data, err := os.ReadFile(bypassFile) //nolint:gosec
	if err != nil {
		return false
	}
	content := string(data)
	return strings.Contains(content, hookName) && strings.Contains(content, filePath)
}

// ---- log helpers -------------------------------------------------------------

func (h *Hook) logFile() string {
	if h.WorkCurrentDir == "" {
		return ""
	}
	return filepath.Join(h.WorkCurrentDir, "logs", hookName+".ndjson")
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
	if logFile == "" {
		return
	}
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

// ---- payload helpers ---------------------------------------------------------

func senderRole(in hooktype.HookInput) string {
	if r, ok := in.Env["YAKOS_AGENT_ROLE"]; ok && r != "" {
		return r
	}
	if r := stringField(in.Payload, "agent_type"); r != "" {
		return r
	}
	return "lead"
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

// loadPolicies reads and parses .claude/path-allowlist.json.
// Returns nil when the file is absent.
func loadPolicies(path string) (map[string]*Policy, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var policies map[string]*Policy
	if err := json.Unmarshal(data, &policies); err != nil {
		return nil, err
	}
	return policies, nil
}
