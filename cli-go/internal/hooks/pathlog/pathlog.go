// Package pathlog is the Go-native Tier-0 port of lib/hooks/path-log.sh.
//
// Fires on PreToolUse for Edit, Write, and MultiEdit. Always passes (exit 0).
// Appends one NDJSON log entry per tool call to
// work/current/logs/path-log.ndjson recording the agent, file path, and tool.
//
// Defense-in-depth companion to pathallowlist: even if the allowlist hook is
// disabled or misconfigured, path-log records every file-write attempt for
// forensic review.
package pathlog

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

const hookName = "path-log"

// Hook implements runner.Hook for path logging.
type Hook struct {
	// WorkCurrentDir is the absolute path to work/current/ for the active
	// session. When empty, Run no-ops gracefully.
	WorkCurrentDir string

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

// Run logs the file-write attempt. Always returns ExitCode 0.
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

	entry := map[string]any{
		"ts":         h.NowFn().UTC().Format(time.RFC3339),
		"hook":       hookName,
		"severity":   "REPORT",
		"action":     "pass",
		"message":    "logged file-write attempt",
		"agent_type": agentType,
		"file_path":  filePath,
		"tool":       in.Tool,
	}

	if h.WorkCurrentDir != "" {
		logFile := filepath.Join(h.WorkCurrentDir, "logs", "path-log.ndjson")
		if err := appendNDJSON(logFile, entry); err != nil {
			out.Stderr = fmt.Appendf(out.Stderr, "path-log: log: %v\n", err)
		}
	}

	return out, nil
}

// ---- helpers -----------------------------------------------------------------

// senderRole extracts the agent/role from the hook environment or payload.
func senderRole(in hooktype.HookInput) string {
	if r, ok := in.Env["YAKOS_AGENT_ROLE"]; ok && r != "" {
		return r
	}
	if r := stringField(in.Payload, "agent_type"); r != "" {
		return r
	}
	return "unknown"
}

// fileFromPayload extracts the file path from the hook payload.
func fileFromPayload(in hooktype.HookInput) string {
	for _, key := range []string{"path", "file_path"} {
		if s := stringField(in.Payload, key); s != "" {
			return s
		}
	}
	return ""
}

// stringField extracts a string field from a payload map.
func stringField(payload map[string]any, key string) string {
	v, ok := payload[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// appendNDJSON opens logFile with O_APPEND and writes a single JSON line.
func appendNDJSON(logFile string, entry map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(logFile), 0755); err != nil { //nolint:gosec
		return err
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644) //nolint:gosec
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck
	_, err = f.Write(data)
	return err
}
