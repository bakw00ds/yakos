// Package runtime provides the adapter interface and per-runtime implementations
// for yakOS dispatch. Each adapter wraps a specific external CLI (claude, codex,
// agy) behind a common interface so the dispatch orchestrator can be
// runtime-agnostic.
package runtime

import (
	_ "embed" // model-aliases.json
	"encoding/json"
	"regexp"
	"sync"
)

// modelAliasesJSON is a byte-for-byte copy of lib/settings/model-aliases.json.
// go:embed cannot reach outside the package directory and the staged framework
// copy (internal/framework/embedded) is empty in a source checkout, so the
// table the Go dispatcher reads lives here too. TestEmbeddedAliasTableMatchesLib
// fails when the two files drift; refresh with
//
//	cp lib/settings/model-aliases.json cli-go/internal/runtime/model-aliases.json
//
//go:embed model-aliases.json
var modelAliasesJSON []byte

// AliasNames are the semantic model aliases in documentation order. An alias
// names a quality class; each runtime maps it to its own model id.
var AliasNames = []string{"cheap", "balanced", "best", "reasoning", "frontier"}

// DefaultAlias is the alias a dispatch uses when neither the caller nor the
// agent names a model.
const DefaultAlias = "balanced"

// modelIDRe is the shape of a model id accepted for the non-Claude runtimes.
// The id travels as one argv element to a third-party CLI, so it is kept to a
// conservative alphabet (no spaces, no leading '-', bounded length). Examples:
// gpt-5, gemini-3.1-pro, claude-opus-4.6, o4-mini, qwen3-coder:30b.
var modelIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]{0,63}$`)

var (
	aliasTableOnce sync.Once
	aliasTable     map[string]map[string]string // alias -> runtime -> model id
)

// aliasTab returns the parsed alias table. A table that fails to parse falls
// back to the Claude column alone, so a damaged file degrades to the previous
// Claude-only behaviour instead of panicking in a hot path.
func aliasTab() map[string]map[string]string {
	aliasTableOnce.Do(func() {
		var doc struct {
			Aliases map[string]map[string]string `json:"aliases"`
		}
		if err := json.Unmarshal(modelAliasesJSON, &doc); err != nil || len(doc.Aliases) == 0 {
			doc.Aliases = map[string]map[string]string{}
			for _, a := range AliasNames {
				doc.Aliases[a] = map[string]string{"claude": ResolveAlias(a)}
			}
		}
		aliasTable = doc.Aliases
	})
	return aliasTable
}

// ResolveAlias translates semantic model aliases used in agent frontmatter into
// concrete Claude tier names. Mirrors the mapping in cli/lib/dispatch.sh:_resolve_model_alias
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
//
// Use ResolveAliasFor when the runtime is not necessarily claude.
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

// IsAlias reports whether name is one of the semantic aliases.
func IsAlias(name string) bool {
	for _, a := range AliasNames {
		if a == name {
			return true
		}
	}
	return false
}

// ResolveAliasFor resolves a semantic alias to the model id of the given
// runtime (balanced is sonnet on claude and gpt-5-mini on codex). A name that
// is not an alias passes through unchanged. An alias the table has no entry for
// on that runtime resolves to "" so a caller can never hand the alias word
// itself to a CLI as if it were a model id.
//
// For claude the result is always identical to ResolveAlias.
func ResolveAliasFor(rt, name string) string {
	if rt == "claude" {
		return ResolveAlias(name)
	}
	if !IsAlias(name) {
		return name
	}
	return aliasTab()[name][rt]
}

// DefaultModelFor is the model a dispatch to rt uses when nothing names one:
// the runtime's balanced alias. It is "" for a runtime this package does not
// know. The value is what the dispatch log records; whether it is also passed
// to the CLI is the adapter's call (an unpinned chat keeps the CLI's own
// default).
func DefaultModelFor(rt string) string {
	if !isKnownRuntime(rt) {
		return ""
	}
	return ResolveAliasFor(rt, DefaultAlias)
}

// ValidateModelFor reports whether model, already alias-resolved for rt, is
// acceptable for that runtime. claude takes the four tiers (ValidateTier).
// Every other known runtime takes any id matching the model-id alphabet, which
// includes the alias words themselves. A runtime this package does not know
// accepts nothing.
func ValidateModelFor(rt, model string) bool {
	switch {
	case rt == "claude":
		return ValidateTier(model)
	case isKnownRuntime(rt):
		return modelIDRe.MatchString(model)
	default:
		return false
	}
}

// ModelHint describes what ValidateModelFor accepts for rt, for error text.
func ModelHint(rt string) string {
	if rt == "claude" {
		return "haiku|sonnet|opus|fable"
	}
	return "an alias (cheap|balanced|best|reasoning|frontier) or a model id like gpt-5"
}

// IsClaudeTier reports whether name is one of the bare Claude tier names. A
// tier name means nothing to any other runtime, which is how a dispatcher
// recognises a Claude-only pin on an agent that resolved elsewhere.
func IsClaudeTier(name string) bool { return ValidateTier(name) }

func isKnownRuntime(rt string) bool {
	for _, k := range Known {
		if k == rt {
			return true
		}
	}
	return false
}
