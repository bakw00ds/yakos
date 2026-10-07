package runtime

import (
	"os"
	"strings"
	"sync"

	"github.com/bakw00ds/yakos/internal/routerpolicy"
)

// gateway_alias.go -- env-alias request-class routing for Claude Code (K-141).
//
// The user-level router policy key gateway_classes maps Claude Code request
// classes to Claude model ids, and the only way yakOS realises it is through
// the documented Claude Code environment variables (routerpolicy.EnvNameFor).
// One helper, ApplyGatewayAliases, serves every Claude env builder (framed
// ExecCmd, ChatExecCmd, InteractiveExecCmd and `yakos start`) so they cannot
// drift.
//
// Cache stability: the table is loaded once per process per state directory and
// never re-read, so the variables are identical on every turn of a conversation
// (a different model is a different prompt cache). Editing the policy takes
// effect for the next process, which for the daemon means a restart.
//
// The operator's own value of one of these variables, already in the
// environment, always wins: the policy never overrides an explicit choice.

// RouteReasonEnvAlias is the ledger route_reason of a dispatch that ran with
// at least one class alias set.
const RouteReasonEnvAlias = "env-alias"

// GatewayAliases is what the policy sets for one Claude process.
type GatewayAliases struct {
	// Set are the aliases added to the environment, sorted by class.
	Set routerpolicy.GatewayClasses
	// Overridden are policy entries skipped because the operator's environment
	// already sets the variable.
	Overridden routerpolicy.GatewayClasses
	// SHA is the table digest of Set (routerpolicy.GatewayClasses.SHA); empty
	// when nothing is set.
	SHA string
}

// Env returns the KEY=VALUE entries of Set, in class order.
func (g GatewayAliases) Env() []string {
	out := make([]string, 0, len(g.Set))
	for _, c := range g.Set {
		out = append(out, c.EnvName+"="+c.Model)
	}
	return out
}

type gatewayLoad struct {
	classes  routerpolicy.GatewayClasses
	warnings []string
}

var (
	gatewayMu    sync.Mutex
	gatewayCache = map[string]gatewayLoad{}
)

// loadGatewayClasses returns the validated table for stateDir, loading it once.
// A refused file or key fails closed to an empty table; the reason is printed
// once, without any path.
func loadGatewayClasses(stateDir string) routerpolicy.GatewayClasses {
	if stateDir == "" {
		return nil
	}
	gatewayMu.Lock()
	defer gatewayMu.Unlock()
	if l, ok := gatewayCache[stateDir]; ok {
		return l.classes
	}
	classes, warnings, err := routerpolicy.LoadGatewayClasses(stateDir)
	if err != nil {
		warnings = []string{"router policy: gateway_classes not applied (the policy file was refused; run `yakos doctor --policy`)"}
		classes = nil
	}
	gatewayCache[stateDir] = gatewayLoad{classes: classes, warnings: warnings}
	for _, w := range warnings {
		noteOnce("gateway-classes:"+w, "yakos: %s\n", w)
	}
	return classes
}

func envHasName(env []string, name string) bool {
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if ok && v != "" && strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}

// ResolveGatewayAliases computes what the policy in stateDir sets on top of
// env (the environment the child would otherwise get).
func ResolveGatewayAliases(stateDir string, env []string) GatewayAliases {
	var g GatewayAliases
	for _, c := range loadGatewayClasses(stateDir) {
		if envHasName(env, c.EnvName) {
			g.Overridden = append(g.Overridden, c)
			continue
		}
		g.Set = append(g.Set, c)
	}
	g.SHA = g.Set.SHA()
	return g
}

// ApplyGatewayAliases appends the policy's aliases to env and reports what it
// did. env must already be the filtered child environment.
func ApplyGatewayAliases(stateDir string, env []string) ([]string, GatewayAliases) {
	g := ResolveGatewayAliases(stateDir, env)
	return append(env, g.Env()...), g
}

// applyClaudeAliases is the call every Claude env builder makes.
func applyClaudeAliases(env []string) []string {
	env, _ = ApplyGatewayAliases(routerpolicy.StateDir(), env)
	return env
}

// ClaudeGatewayAliases returns the aliases a Claude dispatch from this process
// gets, for the ledger and for dry runs. It resolves against the same filtered
// environment the builders use.
func ClaudeGatewayAliases() GatewayAliases {
	return ResolveGatewayAliases(routerpolicy.StateDir(), filterEnv(os.Environ(), claudeEnvSpec))
}
