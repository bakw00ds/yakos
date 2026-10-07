package router

// explain.go renders a RouteDecision for `yakos router explain` and `yakos
// dispatch --explain`. The output is deterministic: fixed key order, no
// timestamps, no seconds left on a cooldown, no paths. Route metadata is for the
// operator and the ledger; it never goes into a prompt (rule:cache-stability).

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// RuleOverride is the rule shown when the operator's own --runtime or --model
// decided the route: the policy rules (and R0) were outranked.
const RuleOverride = "override"

// EnvAlias is one Claude Code class alias the gateway_classes policy would set.
type EnvAlias struct {
	Name       string `json:"name"`
	Model      string `json:"model"`
	Class      string `json:"class"`
	Overridden bool   `json:"overridden"` // the operator's environment already sets Name, so it is not set
}

// ExplainView is what an explain command shows.
type ExplainView struct {
	Agent    string
	Decision RouteDecision
	// Overrides names the operator's explicit inputs ("runtime", "model") that
	// were in effect. When set, the rule is shown as RuleOverride and the rule
	// that would have applied without them as underlying_rule.
	Overrides []string
	// EnvClass and Env are set when the class is a Claude Code request class.
	EnvClass string
	Env      []EnvAlias
}

func (v ExplainView) overrides() []string {
	o := append([]string(nil), v.Overrides...)
	sort.Strings(o)
	return o
}

func (v ExplainView) rule() string {
	if len(v.Overrides) > 0 {
		return RuleOverride
	}
	return v.Decision.RuleID
}

func routeLabel(d RouteDecision) string {
	if d.ModelID == "" {
		return d.Runtime
	}
	return d.Runtime + "/" + d.ModelID
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func chainText(c []string) string { return "[" + strings.Join(c, " ") + "]" }

// WriteExplain writes the text form: one summary line, then key: value lines.
func WriteExplain(w io.Writer, v ExplainView) {
	d := v.Decision
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }
	p("%s rule=%s chain=%s\n", routeLabel(d), v.rule(), chainText(d.Chain))
	p("agent: %s\n", v.Agent)
	p("runtime: %s\n", d.Runtime)
	if d.ModelID == "" {
		p("model: (harness default)\n")
	} else {
		p("model: %s\n", d.ModelID)
	}
	p("provider: %s\n", dash(d.Provider))
	p("rule: %s\n", v.rule())
	if o := v.overrides(); len(o) > 0 {
		p("overrides: %s\n", strings.Join(o, ","))
		p("underlying_rule: %s\n", d.RuleID)
	}
	p("chain: %s\n", chainText(d.Chain))
	p("reason: %s\n", dash(d.Reason))
	p("fallback_from: %s\n", dash(d.FallbackFrom))
	p("route_class: %s\n", dash(d.RouteClass))
	p("policy_sha: %s\n", dash(d.PolicySHA))
	for _, s := range d.Skipped {
		if s.Cooling {
			p("skipped: %s (cooling)\n", s.Runtime)
		} else {
			p("skipped: %s (%s)\n", s.Runtime, s.Reason)
		}
	}
	if v.EnvClass != "" {
		p("env_class: %s\n", v.EnvClass)
		for _, a := range v.Env {
			if a.Overridden {
				p("env: %s not set (your environment already sets it)\n", a.Name)
			} else {
				p("env: %s=%s\n", a.Name, a.Model)
			}
		}
		if len(v.Env) == 0 {
			p("env: (no gateway_classes alias for this class)\n")
		}
	}
}

type jsonSkip struct {
	Runtime string `json:"runtime"`
	Reason  string `json:"reason"`
	Cooling bool   `json:"cooling"`
}

type jsonExplain struct {
	Agent          string     `json:"agent"`
	Runtime        string     `json:"runtime"`
	Model          string     `json:"model"`
	Provider       string     `json:"provider"`
	Rule           string     `json:"rule"`
	UnderlyingRule string     `json:"underlying_rule,omitempty"`
	Overrides      []string   `json:"overrides"`
	Chain          []string   `json:"chain"`
	Reason         string     `json:"reason"`
	FallbackFrom   string     `json:"fallback_from"`
	RouteClass     string     `json:"route_class"`
	PolicySHA      string     `json:"policy_sha"`
	Skipped        []jsonSkip `json:"skipped"`
	EnvClass       string     `json:"env_class,omitempty"`
	Env            []EnvAlias `json:"env,omitempty"`
}

// ExplainJSON returns the JSON form, one object and a newline. Every array is
// present (empty, not null), so a consumer needs no nil check.
func ExplainJSON(v ExplainView) ([]byte, error) {
	d := v.Decision
	j := jsonExplain{
		Agent: v.Agent, Runtime: d.Runtime, Model: d.ModelID, Provider: d.Provider,
		Rule: v.rule(), Overrides: v.overrides(), Chain: append([]string{}, d.Chain...),
		Reason: d.Reason, FallbackFrom: d.FallbackFrom, RouteClass: d.RouteClass,
		PolicySHA: d.PolicySHA, Skipped: []jsonSkip{}, EnvClass: v.EnvClass, Env: v.Env,
	}
	if len(v.Overrides) > 0 {
		j.UnderlyingRule = d.RuleID
	}
	if j.Overrides == nil {
		j.Overrides = []string{}
	}
	for _, s := range d.Skipped {
		reason := s.Reason
		if s.Cooling {
			reason = "cooling"
		}
		j.Skipped = append(j.Skipped, jsonSkip{Runtime: s.Runtime, Reason: reason, Cooling: s.Cooling})
	}
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
