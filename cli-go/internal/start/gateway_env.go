package start

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/bakw00ds/yakos/internal/routerpolicy"
	runtimeenv "github.com/bakw00ds/yakos/internal/runtime"
)

// gatewayStateDir is the directory the router policy is read from for this
// launch, or "" when the feature is off.
//
// In production (cfg.Env == nil) it is routerpolicy.StateDir(), the one trusted
// location dispatch uses: cfg.HomeDir is not consulted, because the launcher
// fills it with "/tmp" when HOME is empty and a shared temp directory must never
// decide which models a session gets (K-165, the K-129 class). With no trusted
// state directory the answer is "" and gateway aliases are off, as in dispatch.
// Tests inject Env, and then HomeDir (or HOME in Env) names the state directory;
// an empty injected home also means off.
func gatewayStateDir(cfg Config, env map[string]string) string {
	if cfg.Env == nil {
		return routerpolicy.StateDir()
	}
	home := cfg.HomeDir
	if home == "" {
		home = envGet(env, "HOME")
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".yakos-state")
}

// printGatewayEnv writes the class aliases the runtime would get, as
// NAME=model lines in class order, and says which the operator's own
// environment overrides. Names and model ids only.
func printGatewayEnv(w io.Writer, runtimeName, stateDir string, env map[string]string) {
	if runtimeName != "claude" {
		_, _ = fmt.Fprintf(w, "# %s: no class aliases (they apply to the claude runtime only)\n", runtimeName)
		return
	}
	g := GatewayAliasesFor(stateDir, env)
	for _, c := range g.Set {
		_, _ = fmt.Fprintf(w, "%s=%s  # class %s\n", c.EnvName, c.Model, c.Class)
	}
	for _, c := range g.Overridden {
		_, _ = fmt.Fprintf(w, "# %s not set: your environment already sets it (class %s)\n", c.EnvName, c.Class)
	}
	if len(g.Set) == 0 && len(g.Overridden) == 0 {
		_, _ = fmt.Fprintln(w, "# no gateway_classes aliases are active")
	}
}

// GatewayAliasesFor resolves the gateway_classes aliases the claude runtime would
// get from the policy in stateDir, on top of env (the operator's environment,
// filtered the way a claude launch filters it). It is what `yakos start
// --print-env` and `yakos router explain --class` both report.
func GatewayAliasesFor(stateDir string, env map[string]string) runtimeenv.GatewayAliases {
	base := make([]string, 0, len(env))
	for k, v := range env {
		base = append(base, k+"="+v)
	}
	return runtimeenv.ResolveGatewayAliases(stateDir, runtimeenv.FilterEnvFor("claude", base))
}
