package start

// routed.go: `yakos start --routed` (K-151) points the Claude Code child at the
// local Anthropic gateway. It sets exactly two variables in the child's
// environment and nothing else:
//
//	ANTHROPIC_BASE_URL=http://127.0.0.1:7897
//	CLAUDE_CODE_GATEWAY_HINT_HEADERS=1
//
// CLAUDE_CODE_GATEWAY_HINT_HEADERS is a real Claude Code switch (checked in the
// installed 2.1.293 binary): it makes the CLI add x-claude-code-request-class
// (main, subagent, workflow, compaction, auxiliary, side_reply) and
// x-claude-code-agent-type to each API request, which the gateway matches
// against gateway_classes. No credential, token or class value is put in the
// environment, and none of this enters a system prompt or --agents JSON, so the
// cached prompt prefix is unchanged (rule:cache-stability).

import (
	"fmt"
	"strings"
)

// Routed child-environment values.
const (
	RoutedBaseURL     = "http://127.0.0.1:7897"
	envBaseURL        = "ANTHROPIC_BASE_URL"
	envHintHeaders    = "CLAUDE_CODE_GATEWAY_HINT_HEADERS"
	routedHintEnabled = "1"
)

// applyRouted returns env with the two routed variables set, replacing any
// existing entry of the same name. It errors for a runtime other than claude:
// the gateway speaks only the Anthropic API.
func applyRouted(runtimeName string, env []string) ([]string, string, error) {
	if runtimeName != "claude" {
		return nil, "", fmt.Errorf("--routed applies to the claude runtime only (this launch is %s)", runtimeName)
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
		case envHintHeaders:
			continue
		}
		out = append(out, kv)
	}
	out = append(out, envBaseURL+"="+RoutedBaseURL, envHintHeaders+"="+routedHintEnabled)
	return out, note, nil
}
