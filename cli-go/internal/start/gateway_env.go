package start

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/bakw00ds/yakos/internal/routerpolicy"
	runtimeenv "github.com/bakw00ds/yakos/internal/runtime"
)

// gatewayStateDir is the directory the router policy is read from for this
// launch. An injected HomeDir or Env (tests) wins; otherwise the same trusted
// directory dispatch uses. It never falls back to a temp directory.
func gatewayStateDir(cfg Config, env map[string]string) string {
	home := cfg.HomeDir
	if home == "" && cfg.Env != nil {
		home = envGet(env, "HOME")
	}
	if home != "" {
		return filepath.Join(home, ".yakos-state")
	}
	return routerpolicy.StateDir()
}

// printGatewayEnv writes the class aliases the runtime would get, as
// NAME=model lines in class order, and says which the operator's own
// environment overrides. Names and model ids only.
func printGatewayEnv(w io.Writer, runtimeName, stateDir string, env map[string]string) {
	if runtimeName != "claude" {
		_, _ = fmt.Fprintf(w, "# %s: no class aliases (they apply to the claude runtime only)\n", runtimeName)
		return
	}
	base := make([]string, 0, len(env))
	for k, v := range env {
		base = append(base, k+"="+v)
	}
	g := runtimeenv.ResolveGatewayAliases(stateDir, runtimeenv.FilterEnvFor("claude", base))
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
