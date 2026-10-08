package dispatch

// model_ceiling.go: the two checks that run on the model routeDispatchAt settled
// on, after the agent's pin, the router and any --model were applied: the agent's
// max_model ceiling (K-119, now registry-aware, K-139c) and the project's
// router.disable_models.
//
// Both read the model registry. It is loaded only when one of them has something to
// say (an agent with a ceiling, a project with a disable list), so a dispatch with
// neither does no registry work.

import (
	"fmt"

	"github.com/bakw00ds/yakos/internal/modelreg"
	"github.com/bakw00ds/yakos/internal/projectcfg"
)

// modelRegistryFor loads the registry a dispatch judges models against: the
// embedded catalog and the user overlay. Tests replace it.
var modelRegistryFor = func() (*modelreg.Registry, error) {
	return modelreg.Load(modelreg.Options{StateDir: modelreg.DefaultStateDir()})
}

// modelLabel is how an error names a model: "" is the harness default.
func modelLabel(model string) string {
	if model == "" {
		return "the harness default model"
	}
	return fmt.Sprintf("%q", model)
}

// modelHint is the command that says what the registry knows of model.
func modelHint(model string) string {
	if model == "" || !modelreg.ValidID(model) {
		return "yakos models list"
	}
	return "yakos models show " + model
}

// overlayHint says how to make a model rankable: map the aliases in the user
// overlay. It names the file, never its absolute path: a refusal is printed to
// terminals and logs, and the path carries the home directory (K-168). An
// unpinned model ("") never ranks, with or without an overlay, so the hint for it
// is to pin one.
func overlayHint(runtimeName, model string) string {
	if model == "" {
		return fmt.Sprintf("pin a model in the agent's frontmatter: the harness default of %s has no cost class, and an overlay cannot rank it", runtimeName)
	}
	return fmt.Sprintf("map %s's tier aliases to model ids in the user overlay %s in the yakos state directory", runtimeName, modelreg.OverlayFileName)
}

// enforceCeiling applies agent's max_model ceiling to model on the runtime that
// will run it. The ceiling is a Claude tier word and is read as a cost class
// (modelreg ceilingClass), so it governs every harness:
//
//   - a model at or below the class is kept;
//   - a model above it is replaced by the model the registry maps the highest class
//     at or below the ceiling to on the SAME harness, and a note says so. claude:
//     the ceiling's own tier (sonnet for the supervisor). agy: that class's agy
//     alias (balanced is gemini-3.8-flash-high). codex: none while its alias
//     column is empty (the overlay can fill it), so see the next case;
//   - a model the registry cannot rank (a full id no alias names, the harness
//     default, any codex model) is REFUSED, not passed through: its cost is
//     unknown and a ceiling that lets an unknown model past is no ceiling;
//   - a model above the ceiling with nothing at or below it mapped is refused too.
//
// The error names the model, the ceiling and the command that explains the model.
func enforceCeiling(reg *modelreg.Registry, runtimeName, agent, ceiling, model string) (replaced, note string, err error) {
	got, status := reg.EnforceCeiling(runtimeName, model, ceiling)
	switch status {
	case modelreg.CeilingLowered:
		return got, fmt.Sprintf("model %q lowered to %q: agent %s has max_model %s (budget-policy.yml or built-in)", model, got, agent, ceiling), nil
	case modelreg.CeilingUnranked:
		return "", "", fmt.Errorf("dispatch: model %s on %s has no cost class in the model registry, so agent %s's max_model ceiling %s cannot be applied and it is refused (see: %s; %s)",
			modelLabel(model), runtimeName, agent, ceiling, modelHint(model), overlayHint(runtimeName, model))
	case modelreg.CeilingNoLowerModel:
		return "", "", fmt.Errorf("dispatch: model %s on %s is above agent %s's max_model ceiling %s and the registry maps no model at or below it on %s, so it is refused (see: %s; %s)",
			modelLabel(model), runtimeName, agent, ceiling, runtimeName, modelHint(model), overlayHint(runtimeName, model))
	}
	return model, "", nil
}

// projectDisablesModel returns the router.disable_models entry that switches model
// off on runtimeName, if one does. An entry is a concrete id, a tier alias, or a
// Claude tier word; modelreg.Registry.Matches resolves them (see its comment for
// the rule), so disabling `balanced` also blocks the model it maps to and, where
// the harness maps it to nothing, the harness default.
func projectDisablesModel(reg *modelreg.Registry, cfg projectcfg.Config, runtimeName, model string) (string, bool) {
	return cfg.ModelDisabledBy(func(listed string) bool { return reg.Matches(runtimeName, listed, model) })
}

// EnforceModelCeiling is enforceCeiling for callers outside the dispatcher (the
// bash-passthrough clamp in cmd/yakos): it applies ceiling, an agent's max_model,
// to model on runtimeName against the same registry a native dispatch uses, and
// returns the model to run, a note when it was lowered, or an error when the
// model is refused. ceiling "" means no ceiling.
func EnforceModelCeiling(runtimeName, agent, ceiling, model string) (replaced, note string, err error) {
	if ceiling == "" {
		return model, "", nil
	}
	r, err := modelRegistryFor()
	if err != nil {
		return "", "", fmt.Errorf("dispatch: model registry: %w", err)
	}
	return enforceCeiling(r, runtimeName, agent, ceiling, model)
}
