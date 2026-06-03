// Package secretscan is the Go-native Tier-0 port of lib/hooks/secret-scan.sh.
//
// It fires on PreToolUse for Edit, Write, and MultiEdit tool calls.
// If the content to be written matches a high-confidence secret pattern the
// hook exits with code 2 (block), which prevents the tool call from proceeding.
//
// Pattern philosophy (unchanged from the bash original):
//   - Conservative: false positives are worse than false negatives here.
//   - Well-known format prefixes only (AKIA…, ghp_…, github_pat_…, etc.).
//   - Specialized scanners (gitleaks, trufflehog) belong in CI, not here.
//
// Bypass: if work/current/hook-bypass.md contains a line mentioning "secret-scan"
// the block is logged as WARN+bypass and the tool call proceeds.
package secretscan

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

const hookName = "secret-scan"

// Pattern describes one secret detection rule.
type Pattern struct {
	Name  string
	Regex *regexp.Regexp
}

// DefaultPatterns are the high-confidence patterns ported from secret-scan.sh.
var DefaultPatterns = []Pattern{
	{Name: "AWS Access Key", Regex: regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{Name: "GitHub Token", Regex: regexp.MustCompile(`ghp_[A-Za-z0-9]{36}`)},
	{Name: "GitHub Token (fine-grained)", Regex: regexp.MustCompile(`github_pat_[A-Za-z0-9_]{82}`)},
	{Name: "PEM Private Key", Regex: regexp.MustCompile(`-----BEGIN (RSA |EC |OPENSSH |DSA |)PRIVATE KEY-----`)},
	{Name: "Slack Token", Regex: regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`)},
	{Name: "Stripe Secret Key", Regex: regexp.MustCompile(`sk_live_[A-Za-z0-9]{24,}`)},
}

// Hook implements runner.Hook for secret scanning.
type Hook struct {
	// WorkCurrentDir is the absolute path to work/current/ for the active
	// session. Used to locate the bypass file.
	WorkCurrentDir string

	// Patterns is the set of patterns to scan. Defaults to DefaultPatterns.
	Patterns []Pattern

	// NowFn is injected for tests to control timestamps.
	NowFn func() time.Time
}

// New returns a Hook with the default patterns and sensible defaults.
func New(workCurrentDir string) *Hook {
	return &Hook{
		WorkCurrentDir: workCurrentDir,
		Patterns:       DefaultPatterns,
		NowFn:          time.Now,
	}
}

// Name returns the canonical hook name.
func (h *Hook) Name() string { return hookName }

// Run executes the secret-scan logic.
// Returns ExitCode=2 (block) on a match; 0 (pass) otherwise.
func (h *Hook) Run(_ context.Context, in hooktype.HookInput) (hooktype.HookOutput, error) {
	out := hooktype.HookOutput{ExitCode: 0}

	// Only fire on Edit, Write, MultiEdit.
	switch in.Tool {
	case "Edit", "Write", "MultiEdit":
	default:
		return out, nil
	}

	// Extract the text to scan from the payload.
	text := extractWriteText(in)
	if text == "" {
		return out, nil
	}

	// Scan for patterns.
	matchedName, matched := h.scan(text)
	if !matched {
		// Log REPORT-level pass.
		h.appendLog(&out, "REPORT", "pass", "no secret patterns matched", map[string]any{
			"tool":     in.Tool,
			"file":     fileFromPayload(in),
			"matched":  false,
		})
		return out, nil
	}

	filePath := fileFromPayload(in)

	// Check bypass.
	if h.isBypassed() {
		h.appendLog(&out, "WARN", "pass", "match but bypass active", map[string]any{
			"tool":    in.Tool,
			"file":    filePath,
			"matched": matchedName,
			"bypass":  true,
		})
		return out, nil
	}

	// Block.
	h.appendLog(&out, "BLOCK", "block", "secret pattern matched: "+matchedName, map[string]any{
		"tool":    in.Tool,
		"file":    filePath,
		"matched": matchedName,
	})
	msg := fmt.Sprintf(
		"secret-scan: refused write to %q: matches %s pattern. "+
			"Either remove the secret, or add a current bypass entry to work/current/hook-bypass.md if this is intentional.",
		filePath, matchedName,
	)
	out.Stderr = append(out.Stderr, []byte(msg+"\n")...)
	out.ExitCode = 2
	return out, nil
}

// scan returns (matchedPatternName, true) for the first pattern that matches text.
func (h *Hook) scan(text string) (string, bool) {
	pats := h.Patterns
	if len(pats) == 0 {
		pats = DefaultPatterns
	}
	for _, p := range pats {
		if p.Regex.MatchString(text) {
			return p.Name, true
		}
	}
	return "", false
}

// isBypassed returns true when hook-bypass.md mentions "secret-scan".
func (h *Hook) isBypassed() bool {
	if h.WorkCurrentDir == "" {
		return false
	}
	bypassFile := filepath.Join(h.WorkCurrentDir, "hook-bypass.md")
	data, err := os.ReadFile(bypassFile) //nolint:gosec
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "secret-scan")
}

// appendLog writes an NDJSON log entry to out.Stdout (matching bash hook
// log convention: NDJSON to stdout, human message to stderr).
func (h *Hook) appendLog(out *hooktype.HookOutput, severity, action, message string, extra map[string]any) {
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
	out.Stdout = append(out.Stdout, data...)
	out.Stdout = append(out.Stdout, '\n')
}

// ---- payload extraction helpers ---------------------------------------------

// extractWriteText returns the text content that would be written to disk.
// Mirrors the bash hook's logic for each tool.
func extractWriteText(in hooktype.HookInput) string {
	switch in.Tool {
	case "Write":
		return stringField(in.Payload, "content")
	case "Edit":
		return stringField(in.Payload, "new_string")
	case "MultiEdit":
		return extractMultiEditText(in.Payload)
	}
	return ""
}

// extractMultiEditText collects all new_string values from a MultiEdit edits array.
func extractMultiEditText(payload map[string]any) string {
	editsRaw, ok := payload["edits"]
	if !ok {
		return ""
	}
	// The payload may come as []any from JSON-decoded input.
	edits, ok := editsRaw.([]any)
	if !ok {
		// Try JSON round-trip.
		b, err := json.Marshal(editsRaw)
		if err != nil {
			return ""
		}
		if err := json.Unmarshal(b, &edits); err != nil {
			return ""
		}
	}
	var sb strings.Builder
	for _, e := range edits {
		if m, ok := e.(map[string]any); ok {
			if ns, ok := m["new_string"].(string); ok {
				sb.WriteString(ns)
				sb.WriteByte('\n')
			}
		}
	}
	return sb.String()
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

// fileFromPayload extracts the file path from the hook payload.
func fileFromPayload(in hooktype.HookInput) string {
	// Edit/Write use "path" or "file_path".
	for _, key := range []string{"path", "file_path"} {
		if s := stringField(in.Payload, key); s != "" {
			return s
		}
	}
	return "(unknown)"
}
