// Package pathallowlist is the Go-native Tier-0 port of
// lib/hooks/path-allowlist.sh.
//
// PreToolUse hook on Edit, Write, MultiEdit and NotebookEdit. Enforces
// per-agent path allowlists loaded from
// <project>/.claude/path-allowlist.json:
//
//	{ "<agent_type>": { "allow": [glob, ...], "deny": [glob, ...] }, ... }
//
// Decision order (mirrors the bash original, including its ordering):
//
//  1. No file path in tool_input, or no policy file, or no policy for this
//     agent: PASS.
//  2. Policy file (or the agent's entry) that exists but is not a JSON
//     object: BLOCK (fail closed, N4.1).
//  3. Path IS the project root, or is absolute and outside it (R2-5, N1):
//     BLOCK.
//  4. Path lexically escapes the root after normalization (C3), or resolves
//     through a symlink to outside it (M7): BLOCK. Both the lexically
//     normalized path AND the as-written path are resolved physically, so
//     "api/link/../x" (where the OS applies ".." to link's TARGET) cannot
//     smuggle a write out of the tree.
//  5. deny glob matches: BLOCK. allow present (an array, including the
//     empty array, which means deny-all) and no glob matches: BLOCK.
//  6. Otherwise PASS.
//
// Every BLOCK except the structural ones (malformed policy, NUL byte,
// unusable project root, malformed allow) honors a matching
// work/current/hook-bypass.md entry, exactly as bash does.
//
// Glob semantics are bash `case` semantics (see fnmatch.go), not Go's
// path.Match: '*' spans '/'. Matching is case-insensitive (H5b).
package pathallowlist

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hookbypass"
	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/hooklog"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

const hookName = "path-allowlist"

// Hook implements runner.Hook for path allowlist enforcement.
type Hook struct {
	// WorkCurrentDir is the absolute path to work/current/ for bypass checks
	// and the NDJSON log.
	WorkCurrentDir string

	// ProjectDir is a fallback project root, used ONLY when the invocation
	// carries no environment snapshot at all (in.Env == nil — unit tests
	// and embedders). A real `yakos hook run` always snapshots the process
	// environment, and then CLAUDE_PROJECT_DIR is read from it exactly as
	// the bash hook reads $CLAUDE_PROJECT_DIR: unset means "no prefix to
	// strip, policy file at ./.claude/", NOT "use the cwd as the root".
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

// decision is the bundle of fields every exit path logs.
type ctx struct {
	h     *Hook
	in    hooktype.HookInput
	out   hooktype.HookOutput
	agent string
	now   time.Time
}

// Run executes the path allowlist enforcement.
func (h *Hook) Run(_ context.Context, in hooktype.HookInput) (hooktype.HookOutput, error) {
	switch in.Tool {
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
	default:
		return hooktype.HookOutput{}, nil
	}

	now := time.Now()
	if h.NowFn != nil {
		now = h.NowFn()
	}
	c := &ctx{h: h, in: in, agent: senderRole(in), now: now}
	c.run()
	return c.out, nil
}

func (c *ctx) run() {
	agent := c.agent
	rawFile := rawFileFromPayload(c.in)

	// A NUL or newline anywhere in the path is refused outright (no bypass).
	// bash's command substitution drops NUL (so it would evaluate a different
	// path than the tool receives) and its normalizer used to see only the
	// first line of a path with a newline; both are rejected there too. The
	// check runs on the raw string, before the trailing-newline strip. The
	// logged path has NUL removed, as bash's command substitution leaves it.
	if strings.ContainsAny(rawFile, "\x00\n") {
		c.log("BLOCK", "block", "file_path contains a NUL or newline byte",
			map[string]any{"agent_type": agent, "file_path": strings.TrimRight(strings.ReplaceAll(rawFile, "\x00", ""), "\n")})
		c.block(fmt.Sprintf("agent '%s' file_path contains a NUL or newline byte \u2014 refused regardless of allow/deny policy", agent))
		return
	}
	file := strings.TrimRight(rawFile, "\n")

	if file == "" {
		c.log("REPORT", "pass", "no file_path in tool_input",
			map[string]any{"agent_type": agent, "tool": c.in.Tool})
		return
	}

	// CLAUDE_PROJECT_DIR with ALL trailing slashes stripped (R2-1/R3-1),
	// never reducing "/" itself.
	// Separators are normalized to "/" first (a no-op on POSIX, where a
	// backslash is a legal filename character) so a Windows project dir
	// compares equal to the "/"-normalized tool path below.
	cpd := filepath.ToSlash(c.projectDirEnv())
	for cpd != "" && cpd != "/" && strings.HasSuffix(cpd, "/") {
		cpd = strings.TrimSuffix(cpd, "/")
	}

	// Project-relative form: strip "<cpd>/" if it is a literal prefix.
	file = filepath.ToSlash(file)
	relFile := file
	if cpd != "" && strings.HasPrefix(file, cpd+"/") {
		relFile = file[len(cpd)+1:]
	}

	allowlistFile := filepath.Join(orDot(cpd), ".claude", "path-allowlist.json")
	if fi, err := os.Stat(allowlistFile); err != nil || !fi.Mode().IsRegular() {
		// Mirrors `[ ! -f ]`: absent, dangling, or a directory.
		c.log("WARN", "pass", "no allowlist file at .claude/path-allowlist.json",
			map[string]any{"agent_type": agent, "file_path": relFile, "note": "no path-allowlist.json"})
		return
	}

	// N4.1: a policy file that exists but is unreadable / not a JSON object
	// BLOCKS instead of disabling enforcement.
	data, err := os.ReadFile(allowlistFile) //nolint:gosec
	var root map[string]any
	if err == nil {
		var v any
		if jerr := json.Unmarshal(data, &v); jerr == nil {
			root, _ = v.(map[string]any)
		}
	}
	if root == nil {
		c.log("BLOCK", "block", "path-allowlist.json unreadable or not a JSON object",
			map[string]any{"agent_type": agent, "file_path": relFile,
				"note": "path-allowlist.json exists but did not parse as a JSON object"})
		c.block(".claude/path-allowlist.json exists but could not be parsed as a JSON object (truncated write? bad permissions? wrong top-level type?) — refusing rather than silently disabling enforcement. Fix or remove the file.")
		return
	}

	// `.[$agent] // empty`: jq's // treats null and false as absent.
	pv := hookio.JQAlt(root[agent])
	if pv == nil {
		c.log("REPORT", "pass", "no policy for agent_type",
			map[string]any{"agent_type": agent, "file_path": relFile, "note": "no policy for agent"})
		return
	}
	policy, ok := pv.(map[string]any)
	if !ok {
		c.log("BLOCK", "block", "policy value for agent_type is not a JSON object",
			map[string]any{"agent_type": agent, "file_path": relFile,
				"note": "policy value for agent_type is not a JSON object"})
		c.block(fmt.Sprintf(".claude/path-allowlist.json's entry for '%s' is not a JSON object ({\"allow\":[...],\"deny\":[...]}) — refusing rather than silently disabling enforcement.", agent))
		return
	}

	// R2-5: the path IS the project root.
	if cpd != "" && relFile == cpd {
		if c.bypassedExact(relFile) {
			c.log("WARN", "pass", "file_path is the project root but bypass active",
				map[string]any{"agent_type": agent, "file_path": relFile,
					"note": "file_path is the project root but bypass active", "bypass": true})
			return
		}
		c.log("BLOCK", "block", "file_path is the project root itself",
			map[string]any{"agent_type": agent, "file_path": relFile})
		c.block(fmt.Sprintf("agent '%s' path '%s' IS the project root — a file cannot be written over a directory", agent, relFile))
		return
	}

	// N1: an absolute path that survived the prefix strip is outside the
	// project root; refuse before normalization can rewrite it.
	if isAbs(relFile) {
		if c.bypassedExact(relFile) {
			c.log("WARN", "pass", "absolute out-of-root path but bypass active",
				map[string]any{"agent_type": agent, "file_path": relFile,
					"note": "absolute out-of-root path but bypass active", "bypass": true})
			return
		}
		c.log("BLOCK", "block", "absolute path outside project root",
			map[string]any{"agent_type": agent, "file_path": relFile})
		c.block(fmt.Sprintf("agent '%s' path '%s' is absolute and outside the project root — refused regardless of allow/deny policy", agent, relFile))
		return
	}

	// C3: lexical traversal guard.
	asWritten := relFile
	norm := lexicalNormalize(relFile)
	if escapesRoot(norm) {
		if c.bypassedExact(relFile) {
			c.log("WARN", "pass", "path traversal detected but bypass active",
				map[string]any{"agent_type": agent, "file_path": relFile, "normalized": norm,
					"note": "traversal but bypass active", "bypass": true})
			return
		}
		c.log("BLOCK", "block", "path lexically escapes project root",
			map[string]any{"agent_type": agent, "file_path": relFile, "normalized": norm})
		c.block(fmt.Sprintf("agent '%s' path '%s' normalizes to '%s', which escapes the project root — refused regardless of allow/deny policy", agent, relFile, norm))
		return
	}
	relFile = norm

	// M7: symlink-escape guard. Unconditional: fall back to the cwd when
	// CLAUDE_PROJECT_DIR is unusable; block if nothing usable exists.
	projectRoot := cpd
	if !isDir(projectRoot) {
		if wd, werr := os.Getwd(); werr == nil {
			projectRoot = wd
		} else {
			projectRoot = ""
		}
	}
	if !isDir(projectRoot) {
		c.log("BLOCK", "block", "no project root available to check for symlink escapes",
			map[string]any{"agent_type": agent, "file_path": relFile,
				"note": "no usable project root for symlink check"})
		c.block(fmt.Sprintf("cannot determine a project root (CLAUDE_PROJECT_DIR unset/invalid, $PWD unusable) to check '%s' for a symlink escape — refusing rather than skipping the check", relFile))
		return
	}
	projectReal, pok := realpathM(projectRoot)
	targetReal, tok := realpathM(joinSlash(filepath.ToSlash(projectRoot), relFile))
	// Also resolve the path AS WRITTEN: the OS applies ".." to the
	// already-resolved prefix ("link/.." is the parent of link's target),
	// which the lexical collapse above cannot see.
	writtenReal, wok := realpathM(joinSlash(filepath.ToSlash(projectRoot), asWritten))
	if !pok || !tok || !wok || !isWithin(projectReal, targetReal) || !isWithin(projectReal, writtenReal) {
		resolved := targetReal
		if tok && wok && !isWithin(projectReal, writtenReal) {
			resolved = writtenReal
		}
		// K-99: probe the path as written; the normalized path hides "lnk/..".
		if c.bypassedExact(asWritten) {
			c.log("WARN", "pass", "symlink escape detected but bypass active",
				map[string]any{"agent_type": agent, "file_path": relFile, "resolved": resolved,
					"note": "symlink escape but bypass active", "bypass": true})
			return
		}
		c.log("BLOCK", "block", "path resolves outside project root via symlink",
			map[string]any{"agent_type": agent, "file_path": relFile, "resolved": resolved, "project_root": projectReal})
		c.block(fmt.Sprintf("agent '%s' path '%s' resolves (following symlinks) to '%s', which is outside the project root — refused regardless of allow/deny policy", agent, relFile, resolved))
		return
	}

	// ---- deny patterns first ----
	denyRaw := policy["deny"]
	if denyRaw != nil {
		arr, isArr := denyRaw.([]any)
		if !isArr {
			// The bash hook's `.deny // [] | .[]` silently yields NO deny
			// patterns for a non-array deny (string/number/bool) and
			// iterates an object's VALUES. Fail closed instead: a deny key
			// that is present but malformed must not disable enforcement.
			c.log("BLOCK", "block", "policy 'deny' for agent_type is not an array",
				map[string]any{"agent_type": agent, "file_path": relFile,
					"note": "policy 'deny' is not a JSON array"})
			c.block(fmt.Sprintf(".claude/path-allowlist.json's 'deny' for '%s' is not an array — refusing rather than silently disabling deny enforcement.", agent))
			return
		}
		for _, g := range expandGlobs(arr) {
			if !globMatchDeny(g, relFile) {
				continue
			}
			if c.bypassed(relFile) {
				c.log("WARN", "pass", "deny matched but bypass active",
					map[string]any{"agent_type": agent, "file_path": relFile, "matched_deny": g, "bypass": true})
				return
			}
			c.log("BLOCK", "block", "deny pattern matched",
				map[string]any{"agent_type": agent, "file_path": relFile, "matched_deny": g})
			c.block(fmt.Sprintf("agent '%s' is forbidden from editing '%s' (deny: %s)", agent, relFile, g))
			return
		}
	}

	// ---- allow patterns ----
	//
	// K-81: an allow key that is an ARRAY constrains the agent — including
	// the empty array, which means deny-all (bash used to read `allow: []`
	// as "no constraint", silently granting every path). A missing or null
	// allow means no allow-list. Any other type is malformed: block.
	if allowRaw := policy["allow"]; allowRaw != nil {
		arr, isArr := allowRaw.([]any)
		if !isArr {
			c.log("BLOCK", "block", "policy 'allow' for agent_type is not an array",
				map[string]any{"agent_type": agent, "file_path": relFile,
					"note": "policy 'allow' is not a JSON array"})
			c.block(fmt.Sprintf(".claude/path-allowlist.json's 'allow' for '%s' is not an array — refusing rather than silently disabling allow enforcement.", agent))
			return
		}
		matched := ""
		for _, g := range expandGlobs(arr) {
			if globMatchAllow(g, relFile) {
				matched = g
				break
			}
		}
		if matched == "" {
			if c.bypassed(relFile) {
				c.log("WARN", "pass", "outside allow but bypass active",
					map[string]any{"agent_type": agent, "file_path": relFile, "note": "outside allow", "bypass": true})
				return
			}
			note := "outside allow"
			msg := fmt.Sprintf("agent '%s' may only edit paths in the allow-list; '%s' is outside it", agent, relFile)
			if len(arr) == 0 {
				note = "allow-list is empty (deny-all)"
				msg += " (the allow-list is empty: deny-all)"
			}
			c.log("BLOCK", "block", "path outside agent's allow-list",
				map[string]any{"agent_type": agent, "file_path": relFile, "note": note})
			c.block(msg)
			return
		}
		c.log("REPORT", "pass", "allow matched",
			map[string]any{"agent_type": agent, "file_path": relFile, "matched_allow": matched})
		return
	}

	c.log("REPORT", "pass", "no allow/deny match",
		map[string]any{"agent_type": agent, "file_path": relFile})
}

// expandGlobs renders each array element the way `jq -r '.[]'` does and
// splits on newlines (bash reads the result line by line), dropping empty
// lines.
func expandGlobs(arr []any) []string {
	var out []string
	for _, el := range arr {
		for _, line := range strings.Split(hookio.JQRawOrJSON(el), "\n") {
			if line != "" {
				out = append(out, line)
			}
		}
	}
	return out
}

// ---- output helpers ---------------------------------------------------------

// block writes "path-allowlist: <reason>" to stderr and sets exit 2, like
// ho_block. The log record must already have been written.
func (c *ctx) block(reason string) {
	c.out.Stderr = append(c.out.Stderr, []byte(hookName+": "+reason+"\n")...)
	c.out.ExitCode = 2
}

func (c *ctx) log(severity, decision, reason string, extra map[string]any) {
	err := hooklog.Append(c.h.WorkCurrentDir, hooklog.Entry{
		Hook:      hookName,
		Severity:  severity,
		Decision:  decision,
		Reason:    reason,
		Agent:     c.agent,
		SessionID: hookio.JQRawOrJSON(hookio.JQAlt(c.in.Payload["session_id"])),
		Event:     c.in.Event,
		Extra:     extra,
	}, c.now)
	if err != nil {
		c.out.Stderr = fmt.Appendf(c.out.Stderr, "%s: log: %v\n", hookName, err)
	}
}

// bypassedExact mirrors ho_check_bypass_exact "path-allowlist" <scope>: the
// escape guards (project root, absolute, "..", symlink) accept only a
// literal Scope, never a glob (K-99).
func (c *ctx) bypassedExact(scope string) bool {
	if c.h.WorkCurrentDir == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(c.h.WorkCurrentDir, "hook-bypass.md")) //nolint:gosec
	if err != nil {
		return false
	}
	return hookbypass.CheckExact(string(data), hookName, scope)
}

// bypassed mirrors ho_check_bypass "path-allowlist" <scope>.
func (c *ctx) bypassed(scope string) bool {
	if c.h.WorkCurrentDir == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(c.h.WorkCurrentDir, "hook-bypass.md")) //nolint:gosec
	if err != nil {
		return false
	}
	return hookbypass.Check(string(data), hookName, scope)
}

// projectDirEnv is $CLAUDE_PROJECT_DIR as the bash hook sees it. See
// Hook.ProjectDir for the in.Env == nil fallback.
func (c *ctx) projectDirEnv() string {
	if c.in.Env == nil {
		return c.h.ProjectDir
	}
	return c.in.Env["CLAUDE_PROJECT_DIR"]
}

// ---- payload helpers --------------------------------------------------------

// senderRole mirrors hi_sender_role: .agent_type (default "lead"), trimmed,
// "yakos:" prefix stripped.
func senderRole(in hooktype.HookInput) string {
	raw := hookio.JQRawOrJSON(hookio.JQAlt(in.Payload["agent_type"]))
	if raw == "" {
		raw = "lead"
	}
	raw = strings.TrimSpace(raw)
	return strings.TrimPrefix(raw, "yakos:")
}

// rawFileFromPayload mirrors hi_file_path:
// .tool_input.file_path // .tool_input.notebook_path, rendered as `jq -r`
// does, with command-substitution's trailing-newline stripping.
func rawFileFromPayload(in hooktype.HookInput) string {
	v := hookio.JQAlt(hookio.ToolInputField(in, "file_path"), hookio.ToolInputField(in, "notebook_path"))
	return hookio.JQRawOrJSON(v)
}

func isAbs(p string) bool {
	return strings.HasPrefix(p, "/") || filepath.VolumeName(p) != ""
}

func isDir(p string) bool {
	if p == "" {
		return false
	}
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func orDot(p string) string {
	if p == "" {
		return "."
	}
	return p
}
