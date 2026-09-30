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
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hookbypass"
	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/hooklog"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

const hookName = "secret-scan"

// Pattern describes one secret detection rule. Source is the literal regex
// TEXT as bash's PATTERNS array carries it (grep -E syntax) — logged
// verbatim in the "pattern" field of a BLOCK record, matching
// `--arg pat "$matched_pattern"` exactly, so Source must stay byte-identical
// to the string in lib/hooks/secret-scan.sh even though Regex is the
// compiled RE2 equivalent used for the actual match.
type Pattern struct {
	Name   string
	Source string
	Regex  *regexp.Regexp
}

// DefaultPatterns are the 8 high-confidence patterns from secret-scan.sh's
// PATTERNS array, in the same order (first match wins).
var DefaultPatterns = []Pattern{
	{Name: "AWS Access Key", Source: `AKIA[0-9A-Z]{16}`, Regex: regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{Name: "GitHub Token", Source: `ghp_[A-Za-z0-9]{36}`, Regex: regexp.MustCompile(`ghp_[A-Za-z0-9]{36}`)},
	{Name: "GitHub Token (fine-grained)", Source: `github_pat_[A-Za-z0-9_]{82}`, Regex: regexp.MustCompile(`github_pat_[A-Za-z0-9_]{82}`)},
	{Name: "PEM Private Key", Source: `-----BEGIN [A-Z0-9 ]*PRIVATE KEY`, Regex: regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY`)},
	{Name: "Slack Token", Source: `xox[baprs]-[A-Za-z0-9-]{10,}`, Regex: regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`)},
	{Name: "Stripe Secret Key", Source: `sk_live_[A-Za-z0-9]{24,}`, Regex: regexp.MustCompile(`sk_live_[A-Za-z0-9]{24,}`)},
	{Name: "Anthropic API Key", Source: `sk-ant-[A-Za-z0-9_-]{93}`, Regex: regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{93}`)},
	{Name: "Google API Key", Source: `AIza[0-9A-Za-z_-]{35}`, Regex: regexp.MustCompile(`AIza[0-9A-Za-z_-]{35}`)},
}

// RedactToken replaces every DefaultPatterns match in text (bash twin:
// supervisor-stream.sh, sed over lib/secret-patterns.sh).
const RedactToken = "[REDACTED]"

// Redact returns text with every DefaultPatterns match replaced by
// RedactToken, applied in table order. Used by supervisor-stream so
// secrets never reach the supervisor buffer (K-112).
func Redact(text string) string {
	for _, re := range redactBlock {
		text = re.ReplaceAllString(text, RedactToken)
	}
	for _, p := range DefaultPatterns {
		text = p.Regex.ReplaceAllString(text, RedactToken)
	}
	text, _ = RedactKeep(text, RedactToken)
	for _, re := range redactExtra {
		text = re.ReplaceAllString(text, RedactToken)
	}
	return text
}

// redactExtra is redaction-only: generic Bearer and KEY=VALUE shapes too
// loose to block a write on (secret-scan never reads them). Bash twin:
// YAKOS_REDACT_EXTRA_PATTERNS in lib/hooks/lib/secret-patterns.sh; a test
// keeps the two identical.
var redactExtraSources = []string{
	`[Bb][Ee][Aa][Rr][Ee][Rr][[:space:]]+[^[:space:]]{8,}`,
	`([Tt][Oo][Kk][Ee][Nn]|[Pp][Aa][Ss][Ss][Ww]([Oo][Rr])?[Dd]|[Ss][Ee][Cc][Rr][Ee][Tt]|[Aa][Pp][Ii][_-]?[Kk][Ee][Yy]).?[[:space:]]*[=:][[:space:]]*.?[^[:space:]]{8,}`,
	`://[^[:space:]/@:]*:[^[:space:]@]+@`,
}

// redactKeepSources are redaction-only rules whose group 1 is context to keep
// (replacement "${1}<token>"): a preview reads "curl -s -u [REDACTED] https://".
// Bash twin: YAKOS_REDACT_KEEP_PATTERNS. Each rule is applied repeatedly until
// nothing changes, because the kept prefix is greedy and one pass redacts only
// the last credential of "-u a:PW1 -u b:PW2". The third rule has no command
// context, for "x=curl; $x -u u:pw": it needs a standalone -u/--user and a
// value that is not numeric:numeric, so docker -u 1000:1000, sort -u 12:30 and
// ls -lu a:b stay readable. A command-name match is case-insensitive through
// bracket classes so the text stays identical to the bash table.
var redactKeepSources = []string{
	`(([Cc][Uu][Rr][Ll]|[Ww][Gg][Ee][Tt]|[Xx][Hh])[^|;&]*[[:space:]](-[A-Za-z]*[uU][[:space:]]*|--(proxy-)?user([[:space:]]+|=)))("[^"]*:[^"]*"|'[^']*:[^']*'|[^[:space:]:"']+:[^[:space:]]+)`,
	`(([Hh][Tt][Tt][Pp][Ss]?|[Xx][Hh][Ss]?)[[:space:]]+([^|;&]*[[:space:]])?(-a|--auth)([[:space:]]+|=))("[^"]*:[^"]*"|'[^']*:[^']*'|[^[:space:]:"']+:[^[:space:]]+)`,
	`([[:space:]](-u[[:space:]]*|--(proxy-)?user([[:space:]]+|=)))("[^"]*:[^"]*"|'[^']*:[^']*'|([^[:space:]:]*[^[:space:][:digit:]:][^[:space:]:]*:[^[:space:]]+|[^[:space:]:]+:[^[:space:]]*[^[:space:][:digit:]][^[:space:]]*))`,
}

var redactKeep = func() []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(redactKeepSources))
	for i, src := range redactKeepSources {
		out[i] = regexp.MustCompile(src)
	}
	return out
}()

// maxKeepPasses bounds the apply-until-unchanged loop (bash loops to a
// fixpoint; eight covers any realistic command line).
const maxKeepPasses = 8

// RedactKeep redacts the credential of every keep-context rule in text,
// replacing only what follows group 1 with token, and returns the new text and
// the number of replacements. Shared by Redact and decision egress.
func RedactKeep(text, token string) (string, int) {
	// Redact with a colon-free placeholder and swap in the real token at the
	// end: a token such as "[REDACTED:basic-auth]" contains a colon, so the
	// credential shape would match it again and starve earlier credentials.
	const ph = "\x00REDACTED\x00"
	n := 0
	for _, re := range redactKeep {
		for i := 0; i < maxKeepPasses; i++ {
			c := len(re.FindAllStringIndex(text, -1))
			if c == 0 {
				break
			}
			text = re.ReplaceAllString(text, "${1}"+ph)
			n += c
		}
	}
	return strings.ReplaceAll(text, ph, token), n
}

// RedactKeepSources exposes the keep-context regex text (drift test).
func RedactKeepSources() []string { return append([]string(nil), redactKeepSources...) }

// redactBlockSources are the redaction-only multi-line block rules, applied
// before every other rule (the blocking table would otherwise eat the PEM
// header and leave the key body). Compiled dot-all; the source text stays
// byte-identical to YAKOS_REDACT_BLOCK_PATTERNS in secret-patterns.sh.
var redactBlockSources = []string{
	`-----BEGIN [A-Z0-9 ]*PRIVATE KEY( BLOCK)?-----.*-----END [A-Z0-9 ]*PRIVATE KEY( BLOCK)?-----`,
	`-----BEGIN [A-Z0-9 ]*PRIVATE KEY( BLOCK)?-----.*`,
}

var redactBlock = func() []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(redactBlockSources))
	for i, src := range redactBlockSources {
		out[i] = regexp.MustCompile("(?s)" + src)
	}
	return out
}()

// RedactBlockSources exposes the block-rule regex text (drift test).
func RedactBlockSources() []string { return append([]string(nil), redactBlockSources...) }

var redactExtra = func() []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(redactExtraSources))
	for i, src := range redactExtraSources {
		out[i] = regexp.MustCompile(src)
	}
	return out
}()

// RedactExtraSources exposes the redaction-only regex text (drift test).
func RedactExtraSources() []string { return append([]string(nil), redactExtraSources...) }

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

	// Only fire on Edit, Write, MultiEdit, NotebookEdit.
	switch in.Tool {
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
	default:
		return out, nil
	}

	now := time.Now()
	if h.NowFn != nil {
		now = h.NowFn()
	}
	agent := senderRole(in)
	file := fileFromPayload(in)

	// Extract the text to scan from the payload — unions every
	// content-bearing field (content, new_string, new_source, and each
	// edits[].new_string — NOT the whole edit object, so old_string being
	// replaced is never scanned) and recursively collects every string
	// leaf, mirroring `.. | strings` exactly (a nested array/object under
	// any of those fields is still fully explored).
	text := extractWriteText(in)
	if text == "" {
		h.appendLog(&out, in, now, "REPORT", "pass", "no content-bearing string fields to scan", map[string]any{
			"agent_type": agent,
			"file_path":  file,
			"tool":       in.Tool,
		})
		return out, nil
	}

	// Scan for patterns.
	matched := h.scan(text)
	if matched == nil {
		h.appendLog(&out, in, now, "REPORT", "pass", "no secret patterns matched", map[string]any{
			"agent_type": agent,
			"file_path":  file,
			"tool":       in.Tool,
		})
		return out, nil
	}

	// Check bypass (scope = file path, matching `ho_check_bypass
	// "secret-scan" "$file"` exactly).
	if h.isBypassed(file) {
		h.appendLog(&out, in, now, "WARN", "pass", "match but bypass active", map[string]any{
			"agent_type": agent,
			"file_path":  file,
			"matched":    matched.Name,
			"bypass":     true,
		})
		return out, nil
	}

	// Block.
	h.appendLog(&out, in, now, "BLOCK", "block", "secret pattern matched: "+matched.Name, map[string]any{
		"agent_type": agent,
		"file_path":  file,
		"matched":    matched.Name,
		"pattern":    matched.Source,
	})
	msg := fmt.Sprintf(
		"secret-scan: refused write to '%s': matches %s pattern. "+
			"Either remove the secret, or add a current bypass entry to work/current/hook-bypass.md if this is intentional.",
		file, matched.Name,
	)
	out.Stderr = append(out.Stderr, []byte(msg+"\n")...)
	out.ExitCode = 2
	return out, nil
}

// scan returns the first pattern that matches text, or nil.
func (h *Hook) scan(text string) *Pattern {
	pats := h.Patterns
	if len(pats) == 0 {
		pats = DefaultPatterns
	}
	for i := range pats {
		if pats[i].Regex.MatchString(text) {
			return &pats[i]
		}
	}
	return nil
}

// isBypassed replicates ho_check_bypass("secret-scan", filePath): an entry
// under the "## Active entries" heading whose **Hook:** value CONTAINS
// "secret-scan" AND whose **Scope:** equals filePath or is a glob over it
// (K-99; no substring matching, empty scope matches nothing). filePath is
// the path exactly as the payload carries it, normally ABSOLUTE.
func (h *Hook) isBypassed(filePath string) bool {
	if h.WorkCurrentDir == "" {
		return false
	}
	bypassFile := filepath.Join(h.WorkCurrentDir, "hook-bypass.md")
	data, err := os.ReadFile(bypassFile) //nolint:gosec
	if err != nil {
		return false
	}
	return hookbypass.Check(string(data), hookName, filePath)
}

// appendLog writes an NDJSON log entry via the shared hooklog writer —
// field set/order matches bash's ho_log exactly. agent/session_id/event
// come from the hook's own base-field derivation (hi_sender_role/
// hi_session_id/hi_event), independent of whatever "agent_type" happens
// to also be duplicated into extra for bash's own extra-object shape.
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

// ---- payload extraction helpers ---------------------------------------------

// extractWriteText returns the text content that would be written to disk,
// mirroring secret-scan.sh's jq expression exactly:
//
//	[.tool_input | (.content, .new_string, .new_source,
//	  ((.edits // []) | if type == "array" then map(.new_string) else [] end))
//	 | .. | strings] | join("\n")
//
// Every one of content/new_string/new_source/each edit's new_string is
// recursively walked (a nested array or object anywhere under any of
// those fields is still fully explored) collecting every string leaf —
// NOT switched on tool name, so a payload with a "swapped" shape (e.g. a
// Write carrying .new_string) is still caught. edits[].old_string (the
// text being REPLACED, not written) is never scanned.
func extractWriteText(in hooktype.HookInput) string {
	ti := hookio.ToolInput(in)
	if ti == nil {
		return ""
	}
	var leaves []string
	leaves = append(leaves, collectStringLeaves(ti["content"])...)
	leaves = append(leaves, collectStringLeaves(ti["new_string"])...)
	leaves = append(leaves, collectStringLeaves(ti["new_source"])...)
	if editsRaw, ok := ti["edits"]; ok {
		if edits, ok := editsRaw.([]any); ok {
			for _, e := range edits {
				if m, ok := e.(map[string]any); ok {
					leaves = append(leaves, collectStringLeaves(m["new_string"])...)
				}
			}
		}
	}
	return strings.Join(leaves, "\n")
}

// collectStringLeaves is the Go equivalent of jq's `.. | strings`: recurse
// into v and return every string value found at any depth. Map iteration
// order doesn't affect correctness here — the result is only ever regex-
// matched (order-independent) or joined for that purpose, never itself
// logged or compared.
func collectStringLeaves(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		var out []string
		for _, e := range x {
			out = append(out, collectStringLeaves(e)...)
		}
		return out
	case map[string]any:
		var out []string
		for _, e := range x {
			out = append(out, collectStringLeaves(e)...)
		}
		return out
	default:
		return nil
	}
}

// senderRole extracts the agent/role, matching hi_sender_role exactly:
// hi_field_or '.agent_type' 'lead' (top-level, fallback "lead" when
// absent/empty), trimmed, then the "yakos:" namespace prefix stripped.
func senderRole(in hooktype.HookInput) string {
	return hookio.SenderRole(in)
}

// fileFromPayload extracts the file path, matching hi_file_path:
// .tool_input.file_path // .tool_input.notebook_path.
func fileFromPayload(in hooktype.HookInput) string {
	return hookio.ToolFilePath(in)
}
