package modelreg

// Tier classes generalise the one ordering the program has always had, the Claude
// tiers haiku < sonnet < opus < fable that budget.ClampModel applies to an agent's
// max_model ceiling. A class is a tier alias (cheap, balanced, best, reasoning,
// frontier); each harness maps every alias to one of its own model ids; and the
// cost order of two models on a harness is the order of the classes that name
// them. On claude the result is the old ordering exactly, because the claude
// column maps cheap to haiku, balanced to sonnet, best and reasoning to opus and
// frontier to fable (a differential test runs budget.ClampModel against Clamp for
// every tier and ceiling).
//
// A model with no class on its harness (a full id nothing maps an alias to, or
// anything on codex, whose column is empty) has no rank and is never changed:
// better to leave a model alone than to guess its cost.
//
// That is a gap for any caller that enforces a ceiling as a cost control rather
// than a convenience. On claude every model has a class, so nothing escapes
// today; on agy only the five models the aliases name have one, and an unranked
// sibling (claude-opus-5-5-low under a `cheap` ceiling) passes unchanged. A
// caller that wires Clamp to a harness other than claude (the router, K-139,
// K-142) must decide what an unranked model under a ceiling means (refuse it, or
// replace it with the ceiling's model) and use ClassOf to tell the two cases
// apart; this package does not guess. TestClamp_UnrankedModelsPassThroughByDesign
// pins the behaviour so a change to it is deliberate.

// ClassOf returns the tier class of id on harness: the alias that maps to it, and
// that alias's rank. When several aliases map to the same id the highest rank
// wins (a ceiling must not undercount what a model costs), and among aliases of
// that rank the first in AliasNames order names the class. ok is false when no
// alias maps to the id on that harness.
func (r *Registry) ClassOf(harness, id string) (alias string, rank int, ok bool) {
	if id == "" {
		return "", 0, false
	}
	for _, a := range AliasNames {
		if r.aliases[a][harness] != id {
			continue
		}
		if rk := aliasRank[a]; rk > rank {
			alias, rank, ok = a, rk, true
		}
	}
	return alias, rank, ok
}

// ceilingClass interprets a ceiling: a tier alias names its own class, and a
// Claude tier name (the vocabulary budget max_model has always used) names the
// class whose claude column is that tier. ok is false for any other word, which
// leaves the model alone.
func (r *Registry) ceilingClass(ceiling string) (rank int, ok bool) {
	if rk, isAlias := aliasRank[ceiling]; isAlias {
		return rk, true
	}
	for _, a := range AliasNames {
		if ceiling != "" && r.aliases[a]["claude"] == ceiling {
			return aliasRank[a], true
		}
	}
	return 0, false
}

// Clamp lowers model to the ceiling's class on harness. It returns the model it
// was given, and false, when:
//
//   - the ceiling is empty or is not a tier alias or Claude tier name;
//   - the model has no class on the harness (see ClassOf);
//   - the model's class is at or below the ceiling;
//   - no model at or below the ceiling is mapped on the harness.
//
// Otherwise it returns the model the highest class at or below the ceiling maps
// to on that harness, and true. It never raises a model and never leaves the
// harness. It reads the mapping only, so a replacement that is disabled or
// unavailable is the caller's to notice (Lookup and Entry.Usable), and it builds
// no message: callers word their own note, which keeps budget.ClampModel's text
// unchanged when it adopts this.
func (r *Registry) Clamp(harness, model, ceiling string) (clamped string, lowered bool) {
	ceilRank, ok := r.ceilingClass(ceiling)
	if !ok {
		return model, false
	}
	_, modelRank, ok := r.ClassOf(harness, model)
	if !ok || modelRank <= ceilRank {
		return model, false
	}
	for rank := ceilRank; rank >= 1; rank-- {
		for _, a := range AliasNames {
			if aliasRank[a] != rank {
				continue
			}
			// An id the model already is would not lower it: a model mapped under two
			// aliases of different rank counts as the higher, and the walk goes on
			// to the next one down.
			if id := r.aliases[a][harness]; id != "" && id != model {
				return id, true
			}
		}
	}
	return model, false
}
