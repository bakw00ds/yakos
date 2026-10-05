package auth

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bakw00ds/yakos/internal/statepath"
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
	// Note says something the check could not settle, for the reason text
	// (the OS keyring did not answer in time). Empty when there is nothing to add.
	Note string
}

// ProbeRuntime reports whether the CLI for runtime id is on PATH and whether it
// looks signed in. It makes no network call and runs no vendor CLI. The one
// lookup that can be slow, the OS keyring read for agy, is bounded to two
// seconds and ends when ctx does (macOS answers it by running
// /usr/bin/security); a read that times out counts as no keyring entry and the
// result says so in Note.
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
func ProbeRuntime(ctx context.Context, id string) ProbeResult {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = noHomeDir
	}
	kr := &timingKeyring{boundedKeyring: boundedKeyring{inner: probeKeyringBackend(), ctx: ctx, timeout: keyringProbeTimeout}}
	r := probeWith(id, Config{HomeDir: home, KeyringFn: kr})
	if kr.timedOut && !r.Authed {
		r.Note = "the OS keyring did not answer within " + keyringProbeTimeout.String()
	}
	return r
}

// probeKeyringBackend returns the keyring ProbeRuntime reads. A variable so a
// test can substitute one that blocks.
var probeKeyringBackend = defaultKeyringBackend

// timingKeyring records whether its bounded read gave up, so ProbeRuntime can
// say why a lookup found nothing.
type timingKeyring struct {
	boundedKeyring
	timedOut bool
}

func (k *timingKeyring) Get(service, account string) (string, error) {
	v, err := k.boundedKeyring.Get(service, account)
	if keyringTimedOut(err) {
		k.timedOut = true
	}
	return v, err
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
// <stateDir>/default-runtime, or "" when the file is absent, not trusted, empty,
// or not shaped like a runtime id. See ReadDefaultRuntime.
func DefaultRuntimeIn(stateDir string) string {
	name, _ := ReadDefaultRuntime(stateDir)
	return name
}

// ReadDefaultRuntime is DefaultRuntimeIn plus a one-line warning for a file
// that exists but was refused. The default steers every unpinned dispatch to a
// vendor, so the file is trusted only when no one else could have written it
// (statepath.ReadTrusted: not a symlink, owned by this user, not group- or
// world-writable, in a directory with the same properties). statepath.Dir()
// falls back to a path under the shared temp directory when HOME is unset,
// where another local user could plant it (sec-324 F2). Only the first line is
// read, and at most a few hundred bytes of it.
func ReadDefaultRuntime(stateDir string) (name, warning string) {
	if stateDir == "" {
		return "", ""
	}
	path := filepath.Join(stateDir, "default-runtime")
	data, err := statepath.ReadTrusted(path, 256)
	if err != nil {
		if statepath.IsUntrusted(err) {
			return "", "ignoring the default runtime in " + err.Error()
		}
		return "", "" // absent or unreadable: no default, nothing to report
	}
	line, _, _ := strings.Cut(string(data), "\n")
	line = strings.TrimSpace(line)
	if !defaultRuntimeRe.MatchString(line) {
		return "", ""
	}
	return line, ""
}
