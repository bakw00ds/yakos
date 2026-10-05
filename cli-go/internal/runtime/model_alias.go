package runtime

import (
	"fmt"
	"regexp"
)

// harnessModelAliases maps the semantic model aliases (cheap, balanced, best,
// reasoning, frontier) to model ids for the runtimes whose models are not
// Claude tiers. The source of truth is lib/settings/model-aliases.json, which
// only the bash CLI reads; this is the Go copy for the codex and agy adapters
// until the model registry replaces both (P1).
// TestHarnessModelAliasesMatchSettingsFile fails when the two drift.
//
// An empty value means "no mapping: use the harness default". codex is empty on
// purpose: the ids in its ChatGPT-login catalog (`codex debug models`) have
// undocumented tier semantics, and codex rejects an id outside that catalog with
// HTTP 400, so guessing would break dispatches that work with no -m. The agy ids
// are the ones `agy models` lists; the reasoning effort is the id's suffix.
var harnessModelAliases = map[string]map[string]string{
	"codex": {
		"cheap":     "",
		"balanced":  "",
		"best":      "",
		"reasoning": "",
		"frontier":  "",
	},
	"agy": {
		"cheap":     "gemini-3.8-flash-low",
		"balanced":  "gemini-3.8-flash-high",
		"best":      "claude-opus-5-5-medium",
		"reasoning": "gemini-3.1-pro-high",
		"frontier":  "claude-opus-5-5-high",
	},
}

// modelIDPattern is the argv-safety rule for a model id: it is passed as the
// value of -m / --model, so it must start with an alphanumeric (never a dash)
// and contain only characters model ids use.
var modelIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]{0,63}$`)

// ValidModelID reports whether id is safe to pass as a model flag value.
func ValidModelID(id string) bool { return modelIDPattern.MatchString(id) }

// HarnessModelID returns the concrete model id to hand to the codex or agy CLI
// for the requested model, or "" when no model flag should be passed.
//
//   - A semantic alias is expanded through the runtime's column of the alias
//     table (balanced on agy becomes gemini-3.8-flash-high). An alias mapped to
//     the empty string has no model for that runtime and yields "" (the harness
//     default), without a note.
//   - A Claude tier name (haiku, sonnet, opus, fable) yields "". The dispatch
//     layer still defaults every request to the Claude tier "sonnet", and that
//     is not a codex or agy model; the CLI's own default applies instead. The
//     drop is silent because it is the dispatch default, not an operator
//     mistake.
//   - A value that is not a valid model id yields "" with a note on stderr.
func HarnessModelID(runtimeName, model string) string {
	if model == "" {
		return ""
	}
	id := model
	if table, ok := harnessModelAliases[runtimeName]; ok {
		if concrete, ok := table[model]; ok {
			if concrete == "" {
				return ""
			}
			id = concrete
		}
	}
	if ValidateTier(id) {
		return ""
	}
	if !ValidModelID(id) {
		_, _ = fmt.Fprintf(modelDropLog, "yakos: %s model %q is not a valid model id (%s); not passing a model flag\n",
			runtimeName, model, modelIDPattern.String())
		return ""
	}
	return id
}
