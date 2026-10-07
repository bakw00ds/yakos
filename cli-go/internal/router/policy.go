package router

// policy.go turns the `rules:` key of the user-level router policy file into
// validated rules. The file itself is read by internal/routerpolicy (one reader,
// one trust check); this file only decodes and checks what that reader hands
// over. A rule that fails a check is dropped with a warning and the rest stay, so
// one typo never switches the whole router off, and a bad rule can only ever mean
// "this rule does not apply", which is the safe direction.

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/bakw00ds/yakos/internal/routerpolicy"
	"github.com/bakw00ds/yakos/internal/runtime"
)

// MaxRules is the number of policy rules read. They are numbered R1..R6 in file
// order; R0 is the built-in default chain, which is always last.
const MaxRules = 6

// maxFallbacks bounds one rule's fallback list.
const maxFallbacks = 4

// RuleDefault is the id of the default rule: the P0a resolve chain.
const RuleDefault = "R0"

var (
	// identRe is the shape of a class, domain or tag in a rule.
	identRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)
	// agentRe is the shape of an agent name in a rule.
	agentRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
)

// Match is what a rule selects on. Every key that is set must hold (AND); a key
// left out matches anything.
type Match struct {
	Class       string   // route class, e.g. "default" or "chat"
	Agent       string   // exact agent name
	Domain      string   // exact agent domain
	TaskBytesGT int64    // task is longer than this many bytes; 0 = off
	Tags        []string // every listed tag is present
}

// Action is what a matching rule does. Unset keys leave the default chain's
// choice alone.
type Action struct {
	Runtime   string   // runtime to prefer
	Model     string   // model alias or id, validated per runtime at dispatch
	Fallbacks []string // runtimes to try after Runtime, replacing the default's
}

// Rule is one validated policy rule.
type Rule struct {
	ID           string // R1..R6, by position among the rules that were read
	Match        Match
	Action       Action
	OverridePins bool // outrank the agent's frontmatter runtime: and model:
}

// Policy is the router's view of the policy file.
type Policy struct {
	Rules []Rule
	// SHA is the hex SHA-256 of the policy file, "" when no rule was read. It is
	// what a decision cites as policy_sha.
	SHA string
	// Warnings are the notices to show the operator: an ignored file, a dropped
	// rule. They never contain a path.
	Warnings []string
}

// Active reports whether any rule is in force.
func (p Policy) Active() bool { return len(p.Rules) > 0 }

// ruleSpec is the YAML shape of one rule.
type ruleSpec struct {
	Match struct {
		Class       string   `yaml:"class"`
		Agent       string   `yaml:"agent"`
		Domain      string   `yaml:"domain"`
		TaskBytesGT int64    `yaml:"task_bytes_gt"`
		Tags        []string `yaml:"tags"`
	} `yaml:"match"`
	Action struct {
		Runtime   string   `yaml:"runtime"`
		Model     string   `yaml:"model"`
		Fallbacks []string `yaml:"fallbacks"`
	} `yaml:"action"`
	OverridePins bool `yaml:"override_pins"`
}

// LoadPolicy reads the router policy from stateDir through routerpolicy.Load. A
// missing file is no policy and no warning. A file that is untrusted (a symlink,
// not a regular file, another user's, group or world writable, too large) or
// unreadable is ignored with one warning that does not name the path.
func LoadPolicy(stateDir string) Policy {
	return loadPolicyWith(stateDir, routerpolicy.Load)
}

func loadPolicyWith(stateDir string, load func(string) (routerpolicy.File, error)) Policy {
	f, err := load(stateDir)
	if err != nil {
		if errors.Is(err, routerpolicy.ErrUntrusted) {
			return Policy{Warnings: []string{"router policy ignored: the file must be a regular file you own that is not group or world writable (chmod 600); no routing rules are applied"}}
		}
		return Policy{Warnings: []string{"router policy ignored: it could not be read or parsed; no routing rules are applied"}}
	}
	return BuildPolicy(f)
}

// BuildPolicy validates the rules of an already-loaded policy file.
func BuildPolicy(f routerpolicy.File) Policy {
	var p Policy
	node := f.Rules
	if node.Kind == 0 || (node.Kind == yaml.ScalarNode && node.Tag == "!!null") {
		return p
	}
	if node.Kind != yaml.SequenceNode {
		p.Warnings = append(p.Warnings, "router policy: rules: want a list; ignored")
		return p
	}
	for i, n := range node.Content {
		if len(p.Rules) >= MaxRules {
			p.Warnings = append(p.Warnings, fmt.Sprintf("router policy: only the first %d rules are read", MaxRules))
			break
		}
		var spec ruleSpec
		if err := n.Decode(&spec); err != nil {
			p.Warnings = append(p.Warnings, fmt.Sprintf("router policy: rule %d is malformed; dropped", i+1))
			continue
		}
		r, why := validate(spec)
		if why != "" {
			p.Warnings = append(p.Warnings, fmt.Sprintf("router policy: rule %d dropped: %s", i+1, why))
			continue
		}
		r.ID = fmt.Sprintf("R%d", len(p.Rules)+1)
		p.Rules = append(p.Rules, r)
	}
	if p.Active() {
		p.SHA = f.SHA
	}
	return p
}

// validate checks one rule. why is "" when the rule is good.
func validate(s ruleSpec) (r Rule, why string) {
	m := s.Match
	if m.Class != "" && !identRe.MatchString(m.Class) {
		return r, "match.class is not an identifier"
	}
	if m.Agent != "" && !agentRe.MatchString(m.Agent) {
		return r, "match.agent is not an agent name"
	}
	if m.Domain != "" && !identRe.MatchString(m.Domain) {
		return r, "match.domain is not an identifier"
	}
	if m.TaskBytesGT < 0 {
		return r, "match.task_bytes_gt is negative"
	}
	for _, t := range m.Tags {
		if !identRe.MatchString(t) {
			return r, "match.tags has an entry that is not an identifier"
		}
	}
	a := s.Action
	if a.Runtime != "" && !knownRuntime(a.Runtime) {
		return r, "action.runtime is not a runtime this build can run (" + strings.Join(runtime.Known, ", ") + ")"
	}
	if a.Model != "" {
		if !runtime.ValidModelID(a.Model) {
			return r, "action.model is not a model id"
		}
		// Claude Code takes the four tiers and their aliases, never another
		// vendor's model id.
		if a.Runtime == "claude" && !runtime.ValidateTier(runtime.ResolveAlias(a.Model)) {
			return r, "action.model is not a Claude model but action.runtime is claude"
		}
	}
	var fb []string
	for _, f := range a.Fallbacks {
		if !knownRuntime(f) {
			return r, "action.fallbacks names a runtime this build cannot run"
		}
		if f != a.Runtime && !has(fb, f) {
			fb = append(fb, f)
		}
	}
	if len(fb) > maxFallbacks {
		return r, "action.fallbacks has too many entries"
	}
	if a.Runtime == "" && a.Model == "" && len(fb) == 0 {
		return r, "action sets no runtime, model or fallbacks"
	}
	r.Match = Match{Class: m.Class, Agent: m.Agent, Domain: m.Domain, TaskBytesGT: m.TaskBytesGT}
	if len(m.Tags) > 0 {
		r.Match.Tags = append([]string(nil), m.Tags...)
		sort.Strings(r.Match.Tags)
	}
	r.Action = Action{Runtime: a.Runtime, Model: a.Model, Fallbacks: fb}
	r.OverridePins = s.OverridePins
	return r, ""
}

func knownRuntime(name string) bool {
	_, err := runtime.Resolve(name)
	return err == nil
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Input is what the rules are matched against.
type Input struct {
	Class     string
	Agent     string
	Domain    string
	TaskBytes int64
	// Tags are the task's tags. v1 has no source for them, so a rule that lists
	// tags never matches until one exists.
	Tags []string
}

// Matches reports whether every key the rule sets holds for in.
func (r Rule) Matches(in Input) bool {
	m := r.Match
	if m.Class != "" && m.Class != in.Class {
		return false
	}
	if m.Agent != "" && m.Agent != in.Agent {
		return false
	}
	if m.Domain != "" && m.Domain != in.Domain {
		return false
	}
	if m.TaskBytesGT > 0 && in.TaskBytes <= m.TaskBytesGT {
		return false
	}
	for _, t := range m.Tags {
		if !has(in.Tags, t) {
			return false
		}
	}
	return true
}

// Select returns the first rule that matches in, in file order.
func (p Policy) Select(in Input) (Rule, bool) {
	for _, r := range p.Rules {
		if r.Matches(in) {
			return r, true
		}
	}
	return Rule{}, false
}
