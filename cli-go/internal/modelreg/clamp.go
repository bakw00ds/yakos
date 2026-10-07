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
// to on that harness, skipping one that is also mapped under a dearer alias, and
// true. It never raises a model and never leaves the
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
			// An id the model already is would not lower it, and an id that is also
			// mapped under a dearer alias (an overlay can do that) is not below the
			// ceiling either: it counts as the dearer one, as ClassOf says. Either
			// way the walk goes on to the next candidate down.
			id := r.aliases[a][harness]
			if id == "" || id == model {
				continue
			}
			if _, rank, _ := r.ClassOf(harness, id); rank > ceilRank {
				continue
			}
			return id, true
		}
	}
	return model, false
}

// CeilingStatus is what EnforceCeiling found.
type CeilingStatus int

const (
	// CeilingWithin: nothing to do. The ceiling is empty or not a tier word, or the
	// model's class is at or below it.
	CeilingWithin CeilingStatus = iota
	// CeilingLowered: the model was above the ceiling and was replaced by the model
	// of the highest class at or below it on the same harness.
	CeilingLowered
	// CeilingUnranked: the model has no class on the harness (a full id nothing maps
	// an alias to, the harness default, or any model of a harness whose column is
	// empty). Its cost is unknown, so it cannot be compared with the ceiling.
	CeilingUnranked
	// CeilingNoLowerModel: the model is above the ceiling and the harness maps no
	// class at or below it, so there is nothing cheaper to run instead.
	CeilingNoLowerModel
)

// EnforceCeiling is Clamp for a caller that enforces the ceiling as a cost control
// (the dispatcher, for an agent's max_model). Clamp leaves what it cannot rank or
// cannot lower alone, and says so only by returning lowered=false; that is right
// for a convenience and wrong for a control, so this says which of the four cases
// it was. The replacement, when there is one, is Clamp's: a model of the same
// harness, never another harness's. The caller refuses on CeilingUnranked and
// CeilingNoLowerModel; neither is ever passed through.
func (r *Registry) EnforceCeiling(harness, model, ceiling string) (string, CeilingStatus) {
	ceilRank, ok := r.ceilingClass(ceiling)
	if !ok {
		return model, CeilingWithin
	}
	_, modelRank, ok := r.ClassOf(harness, model)
	if !ok {
		return model, CeilingUnranked
	}
	if modelRank <= ceilRank {
		return model, CeilingWithin
	}
	if clamped, lowered := r.Clamp(harness, model, ceiling); lowered {
		return clamped, CeilingLowered
	}
	return model, CeilingNoLowerModel
}

// Matches reports whether the word listed names model on harness, for a list
// that switches models off (a project's router.disable_models). It is true when:
//
//   - the two are the same word; or
//   - they resolve to a common id through this registry: a tier alias resolves to
//     the id it maps to on the harness, so listing `balanced` names the model
//     `sonnet` on claude, and listing that id names the alias word; or
//   - listed is a tier alias the harness maps to nothing (codex ships every alias
//     empty, which means the harness default) and model is "", the harness default,
//     or another alias that is also unmapped.
//
// It never matches across harnesses (a model is judged on the harness that runs
// it) and reads the effective table, so an overlay that remaps an alias moves what
// the word names. A Claude tier word is an id like any other: the claude column
// cannot be remapped, so `sonnet` and `balanced` always name each other there.
func (r *Registry) Matches(harness, listed, model string) bool {
	if listed == model {
		return true
	}
	lf := r.forms(harness, listed)
	for f := range r.forms(harness, model) {
		if lf[f] {
			return true
		}
	}
	return false
}

// forms is the set of ids a word can stand for on harness: itself, and, for a tier
// alias, what it maps to ("" is the harness default).
func (r *Registry) forms(harness, word string) map[string]bool {
	out := map[string]bool{word: true}
	if IsAlias(word) {
		out[r.aliases[word][harness]] = true
	}
	return out
}
