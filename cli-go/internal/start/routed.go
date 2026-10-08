package start

// routed.go: `yakos start --routed` (K-151) points the Claude Code child at the
// local Anthropic gateway. In the child's environment it sets three variables
// and removes one:
//
//	ANTHROPIC_BASE_URL=http://127.0.0.1:7897
//	CLAUDE_CODE_GATEWAY_HINT_HEADERS=1
//	ANTHROPIC_AUTH_TOKEN=<gateway token>   (sent as `Authorization: Bearer ...`)
//	ANTHROPIC_API_KEY                      (removed)
//
// The operator's API key never enters the child: the gateway, which holds it,
// attaches it upstream once the request proves it carries the gateway token. A
// project or process that talks to the port without the token gets a 401.
// CLAUDE_CODE_GATEWAY_HINT_HEADERS is a real Claude Code switch (checked in the
// installed 2.1.293 binary): it makes the CLI add x-claude-code-request-class
// (main, subagent, workflow, compaction, auxiliary, side_reply) and
// x-claude-code-agent-type to each API request, which the gateway matches
// against gateway_classes. None of this enters a system prompt or --agents
// JSON, so the cached prompt prefix is unchanged (rule:cache-stability). The
// token is per-install, not per-turn, so the environment is stable too.

import (
	"fmt"
	"strings"
)

// Routed child-environment values.
const (
	RoutedBaseURL     = "http://127.0.0.1:7897"
	envBaseURL        = "ANTHROPIC_BASE_URL"
	envHintHeaders    = "CLAUDE_CODE_GATEWAY_HINT_HEADERS"
	envAuthToken      = "ANTHROPIC_AUTH_TOKEN"
	envAPIKey         = "ANTHROPIC_API_KEY"
	routedHintEnabled = "1"
)

// applyRouted returns env with the routed variables set, replacing any existing
// entry of the same name, and without ANTHROPIC_API_KEY. It errors for a runtime
// other than claude (the gateway speaks only the Anthropic API) and for an empty
// gateway token.
func applyRouted(runtimeName string, env []string, gatewayToken string) ([]string, string, error) {
	if runtimeName != "claude" {
		return nil, "", fmt.Errorf("--routed applies to the claude runtime only (this launch is %s)", runtimeName)
	}
	if gatewayToken == "" {
		return nil, "", fmt.Errorf("--routed has no gateway token")
	}
	var note string
	out := make([]string, 0, len(env)+2)
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case envBaseURL:
			if v != RoutedBaseURL {
				note = "--routed replaces the ANTHROPIC_BASE_URL in your environment with the local gateway"
			}
			continue
		case envHintHeaders, envAuthToken, envAPIKey:
			continue
		}
		out = append(out, kv)
	}
	out = append(out, envBaseURL+"="+RoutedBaseURL, envHintHeaders+"="+routedHintEnabled, envAuthToken+"="+gatewayToken)
	return out, note, nil
}
