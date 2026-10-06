package runtime

// billing.go classifies how a harness run is paid for (K-136).
//
// Tokens are the primary accounting unit; dollars exist only for runs billed per
// API call. So every dispatch_finished event records a billing mode, and the
// budget and the cost views count dollars only when it is "api" (cost.CountsAsSpend).
// A run under a subscription login costs the operator no dollars per call, so its
// reported figure is kept as an API-equivalent only.
//
// The mode is read from the credentials the child process will actually inherit,
// the same allowlisted environment the adapters build (FilterEnvFor). That is the
// decisive fact for the harnesses we spawn: claude -p, codex exec and agy bill the
// API key when one is in their environment, and the operator's login otherwise.
// Only the presence of a variable is read, never its value, and the result is a
// fixed constant, so no credential can reach the log through this function.
//
// Limits, stated so nobody trusts this more than it deserves: a harness signed in
// through a pay-per-token console login rather than a subscription carries no key
// in its environment and reads as a subscription. The model registry (plan phase
// P1) will let an operator state the billing of a model in a user-level file; until
// then such an operator should add a token limit, which counts every run.

import (
	"os"
	"strings"

	"github.com/bakw00ds/yakos/internal/cost"
)

// BillingFor returns the billing mode of a run on the named runtime under the
// current process environment: cost.BillingAPI when the harness would inherit an
// API credential, cost.BillingSubscription when it would use the operator's login,
// and "" for a runtime this package does not know (a plugin runtime), which readers
// treat like a pre-K-136 row.
func BillingFor(runtimeName string) string {
	return billingFromEnv(runtimeName, os.Environ())
}

// billingFromEnv is BillingFor over an explicit environment.
func billingFromEnv(runtimeName string, base []string) string {
	env := FilterEnvFor(runtimeName, base)
	set := func(names ...string) bool {
		for _, kv := range env {
			key, val, ok := strings.Cut(kv, "=")
			if !ok || !envTruthy(val) {
				continue
			}
			for _, n := range names {
				if strings.EqualFold(key, n) {
					return true
				}
			}
		}
		return false
	}
	switch strings.ToLower(runtimeName) {
	case "claude", "claude-sdk":
		// Console API key, a gateway bearer token, or a cloud provider the CLI is
		// pointed at (all billed per token).
		if set("ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN",
			"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY") {
			return cost.BillingAPI
		}
		return cost.BillingSubscription
	case "codex":
		if set("CODEX_API_KEY", "OPENAI_API_KEY") {
			return cost.BillingAPI
		}
		return cost.BillingSubscription
	case "agy", "antigravity-sdk", "gemini":
		if set("ANTIGRAVITY_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_GENAI_USE_VERTEXAI") {
			return cost.BillingAPI
		}
		return cost.BillingSubscription
	}
	return ""
}

// envTruthy reports whether an environment value counts as set: non-empty and
// not an explicit "off" spelling (a deployment switch such as
// CLAUDE_CODE_USE_BEDROCK=0 is off).
func envTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}
