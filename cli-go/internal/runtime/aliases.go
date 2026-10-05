// Package runtime provides the adapter interface and per-runtime implementations
// for yakOS dispatch. Each adapter wraps a specific external CLI (claude, codex,
// agy) behind a common interface so the dispatch orchestrator can be
// runtime-agnostic.
package runtime

import (
	_ "embed" // model-aliases.json
	"encoding/json"
	"fmt"
	"io"
	"os"
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

// DefaultAlias is the alias claude uses when neither the caller nor the agent
// names a model. codex and agy have no default model (see DefaultModelFor).
const DefaultAlias = "balanced"

// modelIDRe is the shape of a model id accepted for the non-Claude runtimes.
// The id travels as one argv element to a third-party CLI, so it is kept to a
// conservative alphabet (no spaces, no leading '-', bounded length). Examples:
// gpt-5, gemini-3.1-pro, claude-opus-4.6, o4-mini, qwen3-coder:30b.
var modelIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]{0,63}$`)

var (
	aliasMu sync.RWMutex
	// aliasTable maps alias -> runtime -> model id. nil until first use.
	aliasTable map[string]map[string]string
)

// parseAliasTable reads the embedded table. A table that fails to parse falls
// back to the Claude column alone, so a damaged file degrades to the previous
// Claude-only behaviour instead of panicking in a hot path.
func parseAliasTable() map[string]map[string]string {
	var doc struct {
		Aliases map[string]map[string]string `json:"aliases"`
	}
	if err := json.Unmarshal(modelAliasesJSON, &doc); err != nil || len(doc.Aliases) == 0 {
		doc.Aliases = map[string]map[string]string{}
		for _, a := range AliasNames {
			doc.Aliases[a] = map[string]string{"claude": ResolveAlias(a)}
		}
	}
	return doc.Aliases
}

func aliasTab() map[string]map[string]string {
	aliasMu.RLock()
	t := aliasTable
	aliasMu.RUnlock()
	if t != nil {
		return t
	}
	aliasMu.Lock()
	defer aliasMu.Unlock()
	if aliasTable == nil {
		aliasTable = parseAliasTable()
	}
	return aliasTable
}

// SetAliasTableForTest replaces the alias table (alias -> runtime -> model id)
// and returns a function that restores the previous one. It exists so tests in
// other packages can assert alias handling against known data instead of the
// ids in lib/settings/model-aliases.json, which change as vendors rename
// models. Not for production use.
func SetAliasTableForTest(table map[string]map[string]string) (restore func()) {
	aliasMu.Lock()
	prev := aliasTable
	aliasTable = table
	aliasMu.Unlock()
	return func() {
		aliasMu.Lock()
		aliasTable = prev
		aliasMu.Unlock()
	}
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

// AliasModelFor returns the model id a semantic alias maps to on runtime rt.
// found is false when name is not an alias, or when the table has no entry for
// that runtime or an empty one. An empty entry is deliberate: codex's column is
// empty because the ids in its catalog change faster than a table can track,
// and an alias with no mapping means "use the harness default" (no model flag).
// claude's column is the ResolveAlias switch, which a test keeps equal to the
// table.
func AliasModelFor(rt, name string) (id string, found bool) {
	if !IsAlias(name) {
		return "", false
	}
	if rt == "claude" {
		return ResolveAlias(name), true
	}
	id = aliasTab()[name][rt]
	return id, id != ""
}

// ResolveModelFor resolves a requested model for runtime rt. A semantic alias
// becomes that runtime's model id (balanced is sonnet on claude); a name that
// is not an alias passes through unchanged. An alias with no mapping for rt
// resolves to "" (the harness default) and prints one line on stderr:
//
//	yakos: WARN: alias balanced has no codex mapping; using harness default
//
// so an alias word is never handed to a CLI as if it were a model id. For
// claude the result is always identical to ResolveAlias.
func ResolveModelFor(rt, name string) string {
	return ResolveModelForTo(os.Stderr, rt, name)
}

// ResolveModelForTo is ResolveModelFor with the warning written to w.
func ResolveModelForTo(w io.Writer, rt, name string) string {
	if rt == "claude" {
		return ResolveAlias(name)
	}
	if !IsAlias(name) {
		return name
	}
	if id, ok := AliasModelFor(rt, name); ok {
		return id
	}
	_, _ = fmt.Fprintf(w, "yakos: WARN: alias %s has no %s mapping; using harness default\n", name, rt)
	return ""
}

// DefaultModelFor is the model a dispatch to rt carries when nothing names one.
// claude defaults to its balanced tier (sonnet), which keeps the relay session
// pinned (K-116). codex and agy have no default: it is "" so the adapter omits
// the model flag and the harness picks its own, because no id in a static table
// can be trusted to exist in the operator's account. Only an explicit pin (a
// flag, a pane choice or an agent's frontmatter) puts a model on a codex or agy
// command line. "" for a runtime this package does not know.
func DefaultModelFor(rt string) string {
	if rt == "claude" {
		return ResolveAlias(DefaultAlias)
	}
	return ""
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
	return "an alias (cheap|balanced|best|reasoning|frontier) or a model id from the harness's own catalog"
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
