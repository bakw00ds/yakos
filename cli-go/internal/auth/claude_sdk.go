package auth

// claude_sdk.go — what `yakos auth` says about the claude-sdk runtime (K-137).
//
// claude-sdk is the Anthropic Agent SDK. Anthropic's terms (2026-02-19) allow a
// Pro or Max subscription's login only in Claude Code and claude.ai, not in the
// Agent SDK, so the SDK engine runs on an API key in the environment and never
// uses the claude login (runtime.SDKSidecarEnv and the bash adapter enforce
// that). `yakos auth` used to say the opposite: that logging into claude covered
// claude-sdk. These strings replace that, and cli/lib/auth.sh carries the same
// bytes; cmd/yakos/auth_claude_sdk_twin_test.go compares the two implementations.
//
// check_auth for claude-sdk stays claude's chain (checkAuth) on purpose:
// `yakos start --runtime claude-sdk` launches Claude Code through claude.sh, which
// does use the claude login. Status says so (claudeSDKNote).

import (
	"errors"
	"os"

	yakruntime "github.com/bakw00ds/yakos/internal/runtime"
)

const (
	claudeSDKAuthOK    = "ANTHROPIC_API_KEY is set; the SDK engine uses it, not the claude login"
	claudeSDKAuthUnset = "ANTHROPIC_API_KEY is not set; the SDK engine needs an API key and does not use the claude login (run: yakos auth login claude-sdk)"
	claudeSDKAuthOAuth = "ANTHROPIC_API_KEY holds a subscription OAuth token, not an API key; the SDK engine needs an API key (run: yakos auth login claude-sdk)"

	claudeSDKNote = "'yakos start --runtime claude-sdk' launches Claude Code, which uses the claude login; the SDK engine does not"

	claudeSDKLoginAllSkip = "skip (needs ANTHROPIC_API_KEY in the environment, which yakOS never stores; see 'yakos auth login claude-sdk')"
)

// claudeSDKLoginText is what `yakos auth login claude-sdk` prints.
const claudeSDKLoginText = `claude-sdk runs the Anthropic Agent SDK, which needs an API key in the environment.
Anthropic does not allow a claude.ai subscription login in the Agent SDK, so the
SDK engine does not use the claude login.

  1. Set an API key from the Anthropic Console in the shell that starts yakOS:
        export ANTHROPIC_API_KEY="sk-ant-api03-..."
     yakOS never stores it.

  2. Subscription login only? Use the claude runtime, which is Claude Code itself:
        yakos auth login claude
     In the console, use the CLI engine (interactive chat without structured questions).

After setting the key, run 'yakos auth status claude-sdk' to verify.
'yakos start --runtime claude-sdk' launches Claude Code, which does use the claude login.
`

// claudeSDKLogoutText is what `yakos auth logout claude-sdk` prints. The SDK engine
// never used the claude login, so logging out of claude-sdk must not remove it.
const claudeSDKLogoutText = `claude-sdk uses ANTHROPIC_API_KEY from your environment, which yakOS never stores.
To stop using it, unset ANTHROPIC_API_KEY and remove it from your shell rc.
The claude login is not used by the SDK engine, so it was left alone.
'yakos auth logout claude' signs out of it.
`

// claudeSDKAuth reports whether ANTHROPIC_API_KEY holds an API key, as the state
// and hint `auth status` prints. It reads only that variable and never returns
// its value.
func claudeSDKAuth() (state, hint string) {
	err := yakruntime.CheckSDKAPIKey(os.Getenv)
	switch {
	case err == nil:
		return "OK", claudeSDKAuthOK
	case errors.Is(err, yakruntime.ErrSDKAPIKeyIsOAuthToken):
		return "not configured", claudeSDKAuthOAuth
	}
	return "not configured", claudeSDKAuthUnset
}
