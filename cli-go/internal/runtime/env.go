package runtime

import (
	"log/slog"
	"os"
	"strings"
	"sync"
)

// env.go — allowlisted subprocess environment construction (M4,
// security-review-2026-09-14.md, and round-2 review findings R1/R2/R7).
//
// Before the original fix, every adapter's env builder started from the
// full parent environment (os.Environ()). That meant, for example, a codex
// or agy dispatch handed ANTHROPIC_API_KEY to a third-party binary (and a
// claude dispatch handed OPENAI_API_KEY / GEMINI_API_KEY to Anthropic's).
//
// Round 1 over-corrected in two ways the round-2 review caught:
//   - R1: the allowlist was an exact, case-sensitive match with no Windows
//     entries at all, so a Windows dispatch received an environment of
//     essentially one variable (no PATH, no SystemRoot, no TEMP) — every
//     Windows dispatch was broken.
//   - R2: the allowlist dropped egress-relocation variables
//     (ANTHROPIC_BASE_URL, CLAUDE_CODE_USE_BEDROCK/VERTEX, HTTPS_PROXY, …).
//     That fails OPEN on data egress: an operator who deliberately routes
//     model traffic through a private gateway or Bedrock/Vertex silently
//     starts sending prompts (their source code) direct to the public API
//     instead, which is worse than the credential-blast-radius problem M4
//     set out to fix.
//   - R7: the filter was applied to the dispatch adapters only, not to the
//     other places this package's binaries (or the Node SDK sidecar) are
//     spawned: internal/start's `--share-terminal` PTY launch
//     (start.go buildExecEnv) and internal/interactive's SDK sidecar
//     (sdk_engine.go, which set no cmd.Env at all — Go then inherits the
//     FULL parent environment unfiltered). FilterEnvFor is exported
//     specifically so those packages can apply the same allowlist.
//
// filterEnv builds the child environment from three tiers instead:
//  1. isAllowlistedEnvKey: generic vars the child process needs to function
//     at all, matched case-insensitively (fixes R1) and covering both the
//     Unix and Windows base sets, plus the network/tooling/git vars a
//     dispatched coding agent legitimately needs (fixes R2).
//  2. runtimeEnvSpec: the calling runtime's OWN provider namespace(s), by
//     prefix, so ANTHROPIC_BASE_URL/CLAUDE_CODE_USE_BEDROCK/etc. are kept
//     for claude without a second exact-name entry per variable, while
//     still never being kept for codex or agy (and vice versa).
//  3. The YAKOS_DISPATCH_ENV_PASSTHROUGH escape hatch, for installs that
//     need one more variable this table doesn't anticipate.

// passthroughEnvVar is the escape-hatch variable name (tier 3 above).
const passthroughEnvVar = "YAKOS_DISPATCH_ENV_PASSTHROUGH"

// isAllowlistedEnvKey reports whether key is a generic process/locale/
// network/tooling variable, or one of yakOS's own operational variables,
// that every dispatched agent subprocess needs regardless of which runtime
// or OS it is running on. Matching is case-insensitive: Windows environment
// keys are conventionally mixed-case (Path, ComSpec, SystemRoot) and are
// case-insensitive to the OS itself (R1).
//
// The YAKOS_ prefix is NOT a credential-leak concern — it is yakOS's own
// configuration — and dropping it would silently break the hook system:
// lib/hooks/*.sh runs as a child of the claude/codex/agy CLI (invoked by the
// CLI itself for PreToolUse/PostToolUse/etc.) and reads a large number of
// YAKOS_* variables (YAKOS_ROOT, YAKOS_LIB, YAKOS_CLI, YAKOS_RUNTIME,
// YAKOS_PROJECT_NAME, YAKOS_DISPATCH_LOG*, YAKOS_GATE_*,
// YAKOS_BUDGET_DISABLE, YAKOS_SUPERVISOR_DISABLE, YAKOS_PLAN_*, and more —
// see lib/hooks/lib/hook-input.sh and lib/hooks/legacy/*.sh).
//
// The GIT_/GH_TOKEN/GITHUB_TOKEN/SSH_AUTH_SOCK entries are a deliberate,
// documented choice (round-2 review R2): yakOS's dispatched agents routinely
// need to `git commit`/`git push`/use `gh` as their entire workflow, so
// withholding these would break the product for every runtime. This does
// hand a GitHub-scoped credential to whichever CLI is dispatched; that is
// the accepted tradeoff, not an oversight.
func isAllowlistedEnvKey(key string) bool {
	k := strings.ToUpper(key)
	switch k {
	// Unix generic.
	case "PATH", "HOME", "USER", "LOGNAME", "SHELL", "TERM", "LANG", "TZ",
		"TMPDIR", "HOSTNAME", "PWD":
		return true
	// Windows generic (case-insensitive match makes the natural mixed-case
	// spellings — Path, ComSpec, SystemRoot, UserProfile, … — match too).
	case "SYSTEMROOT", "WINDIR", "TEMP", "TMP", "USERPROFILE", "APPDATA",
		"LOCALAPPDATA", "PROGRAMDATA", "PROGRAMFILES", "PROGRAMFILES(X86)",
		"PATHEXT", "COMSPEC", "SYSTEMDRIVE", "HOMEDRIVE", "HOMEPATH",
		"USERNAME", "USERDOMAIN", "NUMBER_OF_PROCESSORS", "PROCESSOR_ARCHITECTURE", "OS":
		return true
	// Network egress / TLS / proxy — dropping these fails OPEN on data
	// egress (R2): an operator who deliberately routes model traffic
	// through a proxy or private gateway would otherwise have that egress
	// control silently bypassed, sending prompts direct to the public API.
	case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY",
		"SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS", "NODE_OPTIONS":
		return true
	// Git / GitHub — see doc comment above; without these the dispatched
	// agent cannot do this repo's entire workflow (commit, push, gh).
	case "GH_TOKEN", "GITHUB_TOKEN", "SSH_AUTH_SOCK":
		return true
	// Terminal/CI rendering hints — no credential content, but their
	// absence degrades output or silently flips CI-conditional behavior.
	case "CI", "COLORTERM", "TERM_PROGRAM":
		return true
	}
	for _, prefix := range []string{"LC_", "XDG_", "YAKOS_", "GIT_"} {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

// runtimeEnvSpec describes one runtime's own provider-credential/config
// namespace, layered on top of isAllowlistedEnvKey. Matching is by
// case-insensitive prefix so a single entry (e.g. "ANTHROPIC_") covers every
// variable that provider's CLI reads (API key, base URL, auth token, custom
// headers, …) without a growing exact-name list that silently misses the
// next one the CLI adds.
type runtimeEnvSpec struct {
	prefixes []string
	// exact holds case-insensitive exact-name matches for narrow
	// allowances that a full prefix would over-capture (round-2 review
	// N3): e.g. claude's Vertex support needs GOOGLE_APPLICATION_CREDENTIALS
	// and GOOGLE_CLOUD_PROJECT specifically, but a bare "GOOGLE_" prefix
	// also matches GOOGLE_API_KEY (the Gemini API key), handing that
	// credential to the Anthropic CLI on every claude dispatch whenever an
	// operator has Gemini configured.
	exact []string
}

func (s runtimeEnvSpec) allows(key string) bool {
	k := strings.ToUpper(key)
	for _, p := range s.prefixes {
		if strings.HasPrefix(k, strings.ToUpper(p)) {
			return true
		}
	}
	for _, e := range s.exact {
		if k == strings.ToUpper(e) {
			return true
		}
	}
	return false
}

// claudeEnvSpec: ANTHROPIC_* covers the API key, ANTHROPIC_BASE_URL (private
// gateway relocation), ANTHROPIC_AUTH_TOKEN (non-API-key auth mode),
// ANTHROPIC_CUSTOM_HEADERS, and ANTHROPIC_VERTEX_PROJECT_ID. CLAUDE_* covers
// CLAUDE_CONFIG_DIR and every CLAUDE_CODE_* flag (including the
// Bedrock/Vertex deployment switches CLAUDE_CODE_USE_BEDROCK/
// CLAUDE_CODE_USE_VERTEX). AWS_*/AZURE_* are the credential families the
// Bedrock/Foundry deployment modes need once selected.
//
// Vertex needs three more names, listed exactly rather than by prefix
// (round-2 review N3): a bare "GOOGLE_" prefix also captures
// GOOGLE_API_KEY, the Gemini API key — narrow but real over-capture of the
// exact M4 class this allowlist exists to close, since claude's CLI never
// reads GOOGLE_API_KEY. GCLOUD_* was dropped outright: nothing in Claude
// Code's Vertex support reads a GCLOUD_-prefixed variable.
var claudeEnvSpec = runtimeEnvSpec{
	prefixes: []string{
		"ANTHROPIC_", "CLAUDE_",
		"AWS_", "AZURE_",
	},
	exact: []string{
		"GOOGLE_APPLICATION_CREDENTIALS", "GOOGLE_CLOUD_PROJECT", "CLOUD_ML_REGION",
	},
}

// codexEnvSpec: OPENAI_* covers the API key, OPENAI_BASE_URL, OPENAI_ORG_ID.
// CODEX_* covers CODEX_HOME and any other codex-cli-specific variable.
var codexEnvSpec = runtimeEnvSpec{
	prefixes: []string{"OPENAI_", "CODEX_"},
}

// agyEnvSpec: GEMINI_* covers GEMINI_API_KEY. GOOGLE_*/GCLOUD_* cover the
// Vertex/ADC credential families agy's headless mode can use. ANTIGRAVITY_*
// covers ANTIGRAVITY_API_KEY, which internal/auth's checkAuth("agy") checks
// ahead of GEMINI_API_KEY — demonstrably read by this codebase's own agy
// auth detection, so it belongs here even though it is not itself a
// generic/OS variable.
var agyEnvSpec = runtimeEnvSpec{
	prefixes: []string{"GEMINI_", "GOOGLE_", "GCLOUD_", "ANTIGRAVITY_"},
}

// specForRuntime maps a runtime identifier (as used in ChatDispatchRequest/
// DispatchRequest call sites and in start.Config.Runtime) to its
// runtimeEnvSpec. Unknown names get no extra namespace beyond the generic
// allowlist — fail toward withholding credentials, not toward forwarding
// everything.
func specForRuntime(name string) runtimeEnvSpec {
	switch strings.ToLower(name) {
	case "claude", "claude-sdk":
		return claudeEnvSpec
	case "codex":
		return codexEnvSpec
	case "agy", "antigravity-sdk", "gemini":
		return agyEnvSpec
	default:
		return runtimeEnvSpec{}
	}
}

// escapeHatchOnce ensures the "passthrough is active" notice is logged at
// most once per process, no matter how many subprocesses get spawned.
var escapeHatchOnce sync.Once

// escapeHatchAllows checks passthroughRaw (the value of
// YAKOS_DISPATCH_ENV_PASSTHROUGH, passed in rather than read from os.Getenv
// here so callers/tests can control it explicitly): a comma-separated list
// of extra variable names, or name-prefixes (a trailing '*' means prefix
// match), forwarded regardless of the allowlist above. This is the
// documented escape hatch for an install that legitimately needs one more
// variable this table doesn't anticipate, rather than an operator having to
// patch the allowlist itself.
func escapeHatchAllows(passthroughRaw, key string) bool {
	if strings.TrimSpace(passthroughRaw) == "" {
		return false
	}
	k := strings.ToUpper(key)
	for _, entry := range strings.Split(passthroughRaw, ",") {
		entry = strings.ToUpper(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		if strings.HasSuffix(entry, "*") {
			if strings.HasPrefix(k, strings.TrimSuffix(entry, "*")) {
				return true
			}
			continue
		}
		if k == entry {
			return true
		}
	}
	return false
}

// getenvPassthrough reads the escape-hatch variable from the current
// process environment. A function (not inlined) so tests can't accidentally
// mistake it for reading from base.
func getenvPassthrough() string {
	return os.Getenv(passthroughEnvVar)
}

// filterEnv returns the subset of base ("KEY=VALUE" entries, typically
// os.Environ()) allowed by isAllowlistedEnvKey, spec's runtime-specific
// namespace, or the YAKOS_DISPATCH_ENV_PASSTHROUGH escape hatch.
func filterEnv(base []string, spec runtimeEnvSpec) []string {
	passthrough := getenvPassthrough()
	if passthrough != "" {
		escapeHatchOnce.Do(func() {
			slog.Info("runtime: YAKOS_DISPATCH_ENV_PASSTHROUGH is set; extra variables will be forwarded to dispatched subprocesses",
				"value", passthrough)
		})
	}
	out := make([]string, 0, len(base))
	for _, kv := range base {
		key, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if isAllowlistedEnvKey(key) || spec.allows(key) || escapeHatchAllows(passthrough, key) {
			out = append(out, kv)
		}
	}
	return out
}

// FilterEnvFor returns the allowlisted subprocess environment for the named
// runtime ("claude", "codex", "agy"/"gemini", or anything else — generic
// allowlist only, no extra provider namespace).
//
// Exported (R7) so every place that spawns one of these CLIs applies the
// same M4 allowlist, not only the dispatch adapters in this package:
// internal/start (the `--share-terminal` PTY launch — start.go buildExecEnv)
// and internal/interactive's Node SDK sidecar (sdk_engine.go) both call this
// instead of forwarding os.Environ()/inheriting it implicitly.
func FilterEnvFor(runtimeName string, base []string) []string {
	return filterEnv(base, specForRuntime(runtimeName))
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
