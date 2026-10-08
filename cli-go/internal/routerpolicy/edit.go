package routerpolicy

// edit.go is the writer of the router policy file, ~/.yakos-state/router-policy.yml.
// It is the only one: the CLI (`yakos router policy set`, `yakos models pin`) and,
// later, the console go through Edit, which reads the file through the same trust
// check Load applies, keeps every key the edit did not touch (the privileged ones,
// allow_unsandboxed_runtimes, hooks_endpoint and openai_endpoint, are never set
// here) and replaces the file atomically with mode 0600 (statepath.EditYAML).

import (
	"errors"
	"fmt"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/bakw00ds/yakos/internal/statepath"
)

// MaxRules mirrors router.MaxRules (a test keeps them equal): the writer must not
// produce a file whose last rules the router would drop.
const MaxRules = 6

// Edit applies edit to the policy file in stateDir and writes the result. check,
// when non-nil, sees the parsed new policy and may refuse it (the file is then
// untouched). stateDir must be an absolute path.
func Edit(stateDir string, edit func(top *yaml.Node) error, check func(File) error) (statepath.EditResult, error) {
	if stateDir == "" || !filepath.IsAbs(stateDir) {
		return statepath.EditResult{}, errors.New("router policy: no usable state directory")
	}
	return statepath.EditYAML(Path(stateDir), maxPolicyBytes, edit, func(data []byte) error {
		f, err := Parse(data)
		if err != nil {
			return errors.New("router policy: the result is not valid YAML")
		}
		if check != nil {
			return check(f)
		}
		return nil
	})
}

// SetRules replaces the rules: list with the YAML list in rulesYAML. Nothing else
// in the file changes. An empty list removes the key.
func SetRules(stateDir string, rulesYAML []byte, check func(File) error) (statepath.EditResult, error) {
	var seq yaml.Node
	if err := yaml.Unmarshal(rulesYAML, &seq); err != nil {
		return statepath.EditResult{}, errors.New("router policy: the rules are not valid YAML")
	}
	var list *yaml.Node
	if seq.Kind == yaml.DocumentNode && len(seq.Content) == 1 {
		list = seq.Content[0]
	}
	if list == nil || list.Kind != yaml.SequenceNode {
		return statepath.EditResult{}, errors.New("router policy: the rules must be a YAML list")
	}
	if len(list.Content) > MaxRules {
		return statepath.EditResult{}, fmt.Errorf("router policy: at most %d rules are read", MaxRules)
	}
	return Edit(stateDir, func(top *yaml.Node) error {
		if len(list.Content) == 0 {
			statepath.YAMLDelete(top, "rules")
			return nil
		}
		statepath.YAMLSet(top, "rules", list)
		return nil
	}, check)
}

// Pin is a per-agent model pin: a rule that sends one agent to one runtime and
// model, ahead of the agent's frontmatter pin.
type Pin struct {
	Agent   string `json:"agent"`
	Runtime string `json:"runtime"`
	Model   string `json:"model"`
}

type pinShape struct {
	Match        map[string]any `yaml:"match"`
	Action       map[string]any `yaml:"action"`
	OverridePins bool           `yaml:"override_pins"`
}

// pinOf reads a rule node as a pin: match has only agent, action only runtime and
// model, override_pins is true. Other rules are the operator's own and are not
// pins.
func pinOf(n *yaml.Node) (Pin, bool) {
	var s pinShape
	if n.Decode(&s) != nil || !s.OverridePins || len(s.Match) != 1 || len(s.Action) != 2 {
		return Pin{}, false
	}
	a, ok1 := s.Match["agent"].(string)
	rt, ok2 := s.Action["runtime"].(string)
	m, ok3 := s.Action["model"].(string)
	if !ok1 || !ok2 || !ok3 {
		return Pin{}, false
	}
	return Pin{Agent: a, Runtime: rt, Model: m}, true
}

// Pins lists the pin rules of f in file order.
func Pins(f File) []Pin {
	var out []Pin
	if f.Rules.Kind == yaml.SequenceNode {
		for _, n := range f.Rules.Content {
			if p, ok := pinOf(n); ok {
				out = append(out, p)
			}
		}
	}
	return out
}

func pinNode(p Pin) *yaml.Node {
	var n yaml.Node
	_ = n.Encode(map[string]any{
		"match":         map[string]any{"agent": p.Agent},
		"action":        map[string]any{"runtime": p.Runtime, "model": p.Model},
		"override_pins": true,
	})
	return &n
}

// SetPin writes p: it replaces the agent's pin rule in place, or puts a new one
// first so a broader rule written earlier cannot shadow it. The rules list may
// hold MaxRules at most.
func SetPin(stateDir string, p Pin, check func(File) error) (statepath.EditResult, error) {
	return Edit(stateDir, func(top *yaml.Node) error {
		rules := statepath.YAMLGet(top, "rules")
		if rules == nil || (rules.Kind == yaml.ScalarNode && rules.Tag == "!!null") {
			rules = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			statepath.YAMLSet(top, "rules", rules)
		}
		if rules.Kind != yaml.SequenceNode {
			return errors.New("router policy: rules: is not a list; fix it by hand first")
		}
		for i, n := range rules.Content {
			if old, ok := pinOf(n); ok && old.Agent == p.Agent {
				rules.Content[i] = pinNode(p)
				return nil
			}
		}
		if len(rules.Content) >= MaxRules {
			return fmt.Errorf("router policy: all %d rule slots are in use", MaxRules)
		}
		rules.Content = append([]*yaml.Node{pinNode(p)}, rules.Content...)
		return nil
	}, check)
}

// ClearPin removes the agent's pin rule; it is not an error when there is none.
func ClearPin(stateDir, agent string, check func(File) error) (statepath.EditResult, error) {
	return Edit(stateDir, func(top *yaml.Node) error {
		rules := statepath.YAMLGet(top, "rules")
		if rules == nil || rules.Kind != yaml.SequenceNode {
			return nil
		}
		kept := rules.Content[:0:0]
		for _, n := range rules.Content {
			if p, ok := pinOf(n); ok && p.Agent == agent {
				continue
			}
			kept = append(kept, n)
		}
		rules.Content = kept
		if len(kept) == 0 {
			statepath.YAMLDelete(top, "rules")
		}
		return nil
	}, check)
}
