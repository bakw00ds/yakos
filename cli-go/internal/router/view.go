package router

// view.go is the read-only shape of the router policy for the operator: `yakos
// router policy get` and the console's Models page render it. It carries the
// validated rules, the per-agent pins, the policy sha and the on/off state of the
// privileged keys, and nothing that names a path or holds a secret.

import (
	"sort"

	"github.com/bakw00ds/yakos/internal/hooks/secretscan"
	"github.com/bakw00ds/yakos/internal/routerpolicy"
)

// GatewayClassView is one gateway_classes assignment: the Claude Code class, the
// NAME of the variable it sets, and the model it sets it to.
type GatewayClassView struct {
	Class string `json:"class"`
	Env   string `json:"env"`
	Model string `json:"model"`
}

// SensitiveView summarises the sensitive class (K-140) for the operator.
type SensitiveView struct {
	Class             string `json:"class"`
	SecretPatterns    int    `json:"secret_patterns"`
	NeverPathPatterns int    `json:"never_path_patterns"`
	Behavior          string `json:"behavior"`
}

// Sensitive describes the built-in sensitive classifier; a project may add
// never-paths but can remove none of these.
func Sensitive() SensitiveView {
	return SensitiveView{
		Class: ClassSensitive, SecretPatterns: len(secretscan.DefaultPatterns), NeverPathPatterns: len(mergedNeverPaths(nil)),
		Behavior: "a request holding a secret-shaped string or naming a credential file is routed only to the primary runtime or a local one",
	}
}

// KnownClasses lists, sorted, the route classes a rule or an explain may name:
// the built-in one, the Claude Code request classes and every class a policy rule
// matches on.
func KnownClasses(stateDir string) []string {
	set := map[string]bool{ClassDefault: true}
	for _, c := range routerpolicy.ClassNames() {
		set[c] = true
	}
	for _, r := range LoadPolicy(stateDir).Rules {
		if r.Match.Class != "" {
			set[r.Match.Class] = true
		}
	}
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// PolicyView is the router policy as shown to the operator.
type PolicyView struct {
	// Present is true when a trusted policy file was read.
	Present bool `json:"present"`
	// SHA is the sha of the trusted file, "" when there is none. It is what a
	// write is checked against and what an audit line cites.
	SHA   string `json:"sha"`
	Rules []Rule `json:"rules"`
	// Pins are the rules `yakos models pin` wrote: one agent to one runtime and model.
	Pins []routerpolicy.Pin `json:"pins"`
	// The privileged keys. They are shown, never written by the model or router
	// commands: the operator edits them in the file.
	AllowUnsandboxedRuntimes []string           `json:"allow_unsandboxed_runtimes"`
	HooksEndpoint            bool               `json:"hooks_endpoint"`
	OpenAIEndpoint           bool               `json:"openai_endpoint"`
	GatewayClasses           []GatewayClassView `json:"gateway_classes"`
	Warnings                 []string           `json:"warnings"`
}

// ViewOf reads the policy in stateDir. A missing file is an empty view; an
// untrusted or unparsable one is an empty view with a warning (the same notice
// LoadPolicy gives).
func ViewOf(stateDir string) PolicyView {
	v := PolicyView{Rules: []Rule{}, Pins: []routerpolicy.Pin{}, AllowUnsandboxedRuntimes: []string{}, GatewayClasses: []GatewayClassView{}, Warnings: []string{}}
	f, err := routerpolicy.Load(stateDir)
	if err != nil {
		v.Warnings = LoadPolicy(stateDir).Warnings
		return v
	}
	p := BuildPolicy(f)
	v.Present = f.SHA != ""
	v.SHA = f.SHA
	if p.Rules != nil {
		v.Rules = p.Rules
	}
	if pins := routerpolicy.Pins(f); pins != nil {
		v.Pins = pins
	}
	if f.AllowUnsandboxedRuntimes != nil {
		v.AllowUnsandboxedRuntimes = append(v.AllowUnsandboxedRuntimes, f.AllowUnsandboxedRuntimes...)
	}
	v.HooksEndpoint = f.HooksEndpoint
	v.OpenAIEndpoint = f.OpenAIEndpoint()
	classes, _ := f.Classes()
	for _, c := range classes {
		v.GatewayClasses = append(v.GatewayClasses, GatewayClassView{Class: c.Class, Env: c.EnvName, Model: c.Model})
	}
	if p.Warnings != nil {
		v.Warnings = p.Warnings
	}
	return v
}
