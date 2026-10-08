package decision

import (
	"errors"
	"path/filepath"
)

// RoutingShadowSurface is the question-set name of the K-177 routing shadow
// (lib/decisions/routing-tier.yaml).
const RoutingShadowSurface = "routing-tier"

// RoutingShadow is whether the routing shadow may run for one project, and the
// ceilings it runs under.
type RoutingShadow struct {
	Enabled bool
	// Detail is a one-line, fixed-vocabulary reason for the state. It never holds
	// text read from a project or policy file. `yakos doctor` and `yakos router
	// explain` print it.
	Detail string
	// Config is the project config tightened by the user policy: the budget caps
	// and egress rules the shadow's engine runs under. Zero when not Enabled.
	Config Config
}

// ResolveRoutingShadow decides whether the routing shadow is on.
//
// It is on only when all of these hold:
//   - YAKOS_DECISION_DISABLE is not 1;
//   - the trusted user-level policy (stateDir/decision-policy.yml: a regular
//     file, ours, not group or world writable) sets routing_shadow: true;
//   - the project's .yakos.yml does not opt out (decisions.provider: none, or
//     decisions.surfaces.routing-tier.mode: off).
//
// A project file can therefore only turn the shadow off, never on. Every error
// reads as off. project may be empty (no project file is consulted).
func ResolveRoutingShadow(stateDir, project string, getenv func(string) string) RoutingShadow {
	off := func(detail string) RoutingShadow { return RoutingShadow{Detail: detail} }
	if KillSwitch(getenv) {
		return off("off: " + killSwitchEnvVar + "=1")
	}
	pol, err := LoadPolicy(StatePaths{Dir: stateDir}.Policy())
	switch {
	case errors.Is(err, ErrUntrustedPolicy):
		return off("off: " + PolicyFileName + " is not trusted (symlink, wrong owner, or group/world writable)")
	case err != nil:
		return off("off: " + PolicyFileName + " is unreadable")
	case !pol.RoutingShadow:
		return off("off (default): set routing_shadow: true in " + PolicyFileName + " to opt in")
	}
	cfg := DefaultConfig()
	if project != "" {
		c, cerr := LoadConfig(filepath.Join(project, ".yakos.yml"))
		if cerr != nil {
			return off("off: the project .yakos.yml is unreadable")
		}
		cfg = c
		if cfg.ProviderSet && cfg.Provider == ProviderNone {
			return off("off: the project .yakos.yml opts out (decisions.provider: none)")
		}
		if sc, ok := cfg.Surfaces[RoutingShadowSurface]; ok && sc.Mode == "off" {
			return off("off: the project .yakos.yml turns the routing-tier surface off")
		}
	}
	return RoutingShadow{
		Enabled: true,
		Detail:  "on: " + PolicyFileName + " sets routing_shadow; the first 2 KiB of each task, the agent name and the route class go to Jev, never a sensitive task",
		Config:  Tighten(cfg, pol),
	}
}
