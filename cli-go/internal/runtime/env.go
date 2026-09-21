package runtime

import "strings"

// env.go — allowlisted subprocess environment construction (M4,
// security-review-2026-09-14.md).
//
// Before this fix, every adapter's env builder started from the full parent
// environment (os.Environ()). That meant, for example, a codex or agy
// dispatch handed ANTHROPIC_API_KEY to a third-party binary (and a claude
// dispatch handed OPENAI_API_KEY / ANTIGRAVITY_API_KEY / GEMINI_API_KEY to
// Anthropic's), and the same full environment reached the PTY
// (start/pump_unix.go), readable by anyone who gains terminal write access
// (see H2). No secret reached argv or logs — only the inherited env — but
// "every credential for every configured runtime, handed to every runtime"
// is still an unnecessary blast-radius expansion.
//
// filterEnv is the fix: build the child environment from an allowlist
// instead of the full parent environment.

// isAllowlistedEnvKey reports whether key is a generic process/locale/tooling
// variable, or one of yakOS's own operational variables, that every
// dispatched agent subprocess needs regardless of which runtime it is.
//
// The YAKOS_ prefix is NOT a credential-leak concern -- it is yakOS's own
// configuration -- and dropping it would silently break the hook system:
// lib/hooks/*.sh runs as a child of the claude/codex/agy CLI (invoked by the
// CLI itself for PreToolUse/PostToolUse/etc.) and reads a large number of
// YAKOS_* variables (YAKOS_ROOT, YAKOS_LIB, YAKOS_CLI, YAKOS_RUNTIME,
// YAKOS_PROJECT_NAME, YAKOS_DISPATCH_LOG*, YAKOS_GATE_*,
// YAKOS_BUDGET_DISABLE, YAKOS_SUPERVISOR_DISABLE, YAKOS_PLAN_*, and more --
// see lib/hooks/lib/hook-input.sh and lib/hooks/legacy/*.sh). Those
// subprocesses inherit whatever env this package hands to the claude/codex/
// agy binary, so the allowlist must pass the whole namespace through.
func isAllowlistedEnvKey(key string) bool {
	switch key {
	case "PATH", "HOME", "LANG", "TMPDIR", "TERM", "SHELL", "USER", "LOGNAME", "HOSTNAME":
		return true
	}
	return strings.HasPrefix(key, "LC_") ||
		strings.HasPrefix(key, "XDG_") ||
		strings.HasPrefix(key, "YAKOS_")
}

// filterEnv returns the subset of base ("KEY=VALUE" entries, typically
// os.Environ()) that isAllowlistedEnvKey allows, plus any variable named in
// extraExact regardless of its allowlist status. extraExact is how each
// adapter forwards its OWN runtime's auth/config variables (e.g.
// ANTHROPIC_API_KEY for claude; OPENAI_API_KEY + CODEX_HOME for codex;
// ANTIGRAVITY_API_KEY + GEMINI_API_KEY + GOOGLE_GENAI_USE_VERTEXAI for agy --
// see each adapter's Available()/internal/auth's checkAuth for how these
// names were identified) without opening the door to every OTHER runtime's
// credentials too.
func filterEnv(base []string, extraExact ...string) []string {
	allow := make(map[string]bool, len(extraExact))
	for _, e := range extraExact {
		allow[e] = true
	}
	out := make([]string, 0, len(base))
	for _, kv := range base {
		key, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if allow[key] || isAllowlistedEnvKey(key) {
			out = append(out, kv)
		}
	}
	return out
}

// appendDispatchEnv appends yakOS-injected dispatch metadata. These are
// always yakOS-controlled key=value pairs constructed here, never forwarded
// verbatim from the parent environment, so they need no filtering.
func appendDispatchEnv(env []string, req DispatchRequest) []string {
	if req.ModelOverride != "" {
		env = append(env, "YAKOS_MODEL_OVERRIDE="+req.ModelOverride)
	}
	if req.UsageOutPath != "" {
		env = append(env, "YAKOS_USAGE_OUT="+req.UsageOutPath)
	}
	if req.SessionOutPath != "" {
		env = append(env, "YAKOS_SESSION_OUT="+req.SessionOutPath)
	}
	if req.AllowRoot {
		env = append(env, "IS_SANDBOX=1") // PR #17
	}
	return env
}
