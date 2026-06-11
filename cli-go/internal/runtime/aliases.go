// Package runtime provides the adapter interface and per-runtime implementations
// for yakOS dispatch. Each adapter wraps a specific external CLI (claude, codex,
// agy, gemini) behind a common interface so the dispatch orchestrator can be
// runtime-agnostic.
package runtime

// ResolveAlias translates semantic model aliases used in agent frontmatter into
// concrete tier names. Mirrors the mapping in cli/lib/dispatch.sh:_resolve_model_alias
// and cli/lib/agents-compose.sh:_compose_one_agent. The three sources are kept in sync
// manually; if a fourth call-site appears, factor into a shared config.
//
// Alias → tier mapping:
//
//	cheap      → haiku
//	balanced   → sonnet
//	best       → opus
//	reasoning  → opus
//	frontier   → fable
//	(other)    → unchanged (pass-through)
func ResolveAlias(alias string) string {
	switch alias {
	case "cheap":
		return "haiku"
	case "balanced":
		return "sonnet"
	case "best", "reasoning":
		return "opus"
	case "frontier":
		return "fable"
	default:
		return alias
	}
}

// ValidateTier returns true when tier is one of the concrete tier names the
// dispatch system accepts. Aliases are NOT valid here; callers must resolve
// aliases first.
func ValidateTier(tier string) bool {
	switch tier {
	case "haiku", "sonnet", "opus", "fable":
		return true
	default:
		return false
	}
}
