package auth

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// probe.go is the dispatch-time face of the status checks in auth.go: it
// answers "can this runtime run right now?" for the dispatcher's runtime chain
// without printing anything or prompting for anything.

// noHomeDir stands in for a missing home directory so the file checks in
// checkAuth cannot resolve to a path relative to the daemon's working
// directory (which can be a project the operator does not control).
var noHomeDir = filepath.Join(os.TempDir(), "yakos-no-home-dir")

// ProbeResult is what ProbeRuntime learned about one runtime.
type ProbeResult struct {
	// CLIPresent is true when the runtime's CLI binary is on PATH.
	CLIPresent bool
	// Authed is true when credentials look configured (see ProbeRuntime for
	// what that means per runtime).
	Authed bool
	// CLIHint is an install instruction, set when the CLI is missing.
	CLIHint string
	// AuthHint is a sign-in instruction, set when the CLI is present but
	// Authed is false.
	AuthHint string
}

// ProbeRuntime reports whether the CLI for runtime id is on PATH and whether it
// looks signed in. It makes no network call and spawns no process.
//
//   - codex: OPENAI_API_KEY, or auth.json under $CODEX_HOME (default ~/.codex).
//   - agy: ANTIGRAVITY_API_KEY or GEMINI_API_KEY, a yakos keyring entry, or the
//     ~/.gemini/antigravity-cli directory. This is the same best-effort check
//     as `yakos auth status`; the directory also exists after a sign-out, so a
//     signed-out agy can still pass until the CLI itself says otherwise.
//   - claude: the CLI being installed is enough. Claude Code credentials can
//     live in the OS keychain or arrive through the environment
//     (CLAUDE_CODE_OAUTH_TOKEN, Bedrock or Vertex settings), none of which can
//     be probed without launching the CLI. cli/lib/runtimes/claude.sh
//     (yk_rt_claude_check_auth) makes the same call. `yakos auth status` is
//     stricter because it reports rather than gates.
func ProbeRuntime(id string) ProbeResult {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = noHomeDir
	}
	return probeWith(id, Config{HomeDir: home, KeyringFn: defaultKeyringBackend()})
}

// probeWith is ProbeRuntime with the home directory and keyring injected.
func probeWith(id string, cfg Config) ProbeResult {
	var r ProbeResult
	if !cliPresent(id) {
		r.CLIHint = cliInstallHint(id)
		return r
	}
	r.CLIPresent = true
	switch id {
	case "claude", "claude-sdk":
		r.Authed = true
	default:
		r.Authed = checkAuth(id, cfg)
	}
	if !r.Authed {
		r.AuthHint = "run: yakos auth login " + id
	}
	return r
}

// defaultRuntimeRe is the shape of a runtime id in the default-runtime file.
var defaultRuntimeRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// DefaultRuntimeIn returns the runtime `yakos auth set-default` persisted in
// <stateDir>/default-runtime, or "" when the file is absent, not a regular
// file, empty, or not shaped like a runtime id. Only the first line is read,
// and at most a few hundred bytes of it.
func DefaultRuntimeIn(stateDir string) string {
	if stateDir == "" {
		return ""
	}
	path := filepath.Join(stateDir, "default-runtime")
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	f, err := os.Open(path) //nolint:gosec // fixed file name under the state dir
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, 256))
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(string(data), "\n")
	line = strings.TrimSpace(line)
	if !defaultRuntimeRe.MatchString(line) {
		return ""
	}
	return line
}
