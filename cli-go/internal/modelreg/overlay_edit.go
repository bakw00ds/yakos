package modelreg

// overlay_edit.go is the writer of the user overlay, ~/.yakos-state/model-registry.yml.
// The CLI (`yakos models enable|disable|alias|pricing`) and, later, the console
// call these functions; they all go through statepath.EditYAML, so the file is
// read with the same trust check LoadOverlay applies, keys they do not touch are
// kept, and the replacement is an atomic 0600 rename. A write that would make the
// overlay parse with more warnings than it had is refused.

import (
	"errors"
	"fmt"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/bakw00ds/yakos/internal/statepath"
)

// editOverlay applies edit to the overlay file in stateDir.
func editOverlay(stateDir string, edit func(top *yaml.Node) error) (statepath.EditResult, error) {
	if stateDir == "" || !filepath.IsAbs(stateDir) {
		return statepath.EditResult{}, errors.New("model registry: no usable state directory")
	}
	_, before := LoadOverlay(stateDir)
	return statepath.EditYAML(OverlayPath(stateDir), maxOverlayBytes, edit, func(data []byte) error {
		if _, after := ParseOverlay(data); len(after) > len(before) {
			return errors.New("model registry: that change would leave the overlay with an entry it ignores")
		}
		return nil
	})
}

// modelNode returns the mapping models.<id>, creating it.
func modelNode(top *yaml.Node, id string) (*yaml.Node, error) {
	models, ok := statepath.YAMLMap(top, "models")
	if !ok {
		return nil, errors.New("model registry: models: is not a mapping; fix it by hand first")
	}
	m, ok := statepath.YAMLMap(models, id)
	if !ok {
		return nil, fmt.Errorf("model registry: models.%s is not a mapping; fix it by hand first", id)
	}
	return m, nil
}

// pruneModel drops models.<id> (and models:) once they hold nothing.
func pruneModel(top *yaml.Node, id string) {
	models := statepath.YAMLGet(top, "models")
	if models == nil || models.Kind != yaml.MappingNode {
		return
	}
	if m := statepath.YAMLGet(models, id); m != nil && m.Kind == yaml.MappingNode && len(m.Content) == 0 {
		statepath.YAMLDelete(models, id)
	}
	if len(models.Content) == 0 {
		statepath.YAMLDelete(top, "models")
	}
}

func scalar(tag, v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: v} }

// SetEnabled switches a model on or off for every harness that lists id.
func SetEnabled(stateDir, id string, enabled bool) (statepath.EditResult, error) {
	if !ValidID(id) {
		return statepath.EditResult{}, errors.New("model registry: not a valid model id")
	}
	return editOverlay(stateDir, func(top *yaml.Node) error {
		m, err := modelNode(top, id)
		if err != nil {
			return err
		}
		statepath.YAMLSet(m, "enabled", scalar("!!bool", fmt.Sprint(enabled)))
		return nil
	})
}

// SetBilling says how id is billed: subscription, api or local.
func SetBilling(stateDir, id string, b Billing) (statepath.EditResult, error) {
	if !ValidID(id) || !b.Valid() {
		return statepath.EditResult{}, errors.New("model registry: want a valid model id and subscription, api or local")
	}
	return editOverlay(stateDir, func(top *yaml.Node) error {
		m, err := modelNode(top, id)
		if err != nil {
			return err
		}
		statepath.YAMLSet(m, "billing", scalar("!!str", string(b)))
		return nil
	})
}

// SetPricing sets id's price in dollars per million tokens; nil removes it.
func SetPricing(stateDir, id string, p *Pricing) (statepath.EditResult, error) {
	if !ValidID(id) {
		return statepath.EditResult{}, errors.New("model registry: not a valid model id")
	}
	if p != nil {
		if err := p.Validate(); err != nil {
			return statepath.EditResult{}, errors.New("model registry: the price is out of range")
		}
	}
	return editOverlay(stateDir, func(top *yaml.Node) error {
		m, err := modelNode(top, id)
		if err != nil {
			return err
		}
		if p == nil {
			statepath.YAMLDelete(m, "pricing")
			pruneModel(top, id)
			return nil
		}
		price := map[string]float64{"input": p.Input, "output": p.Output}
		if p.CacheRead != 0 {
			price["cache_read"] = p.CacheRead
		}
		if p.CacheWrite != 0 {
			price["cache_write"] = p.CacheWrite
		}
		var n yaml.Node
		if err := n.Encode(price); err != nil {
			return err
		}
		statepath.YAMLSet(m, "pricing", &n)
		return nil
	})
}

// SetAlias maps a tier alias to a model id on codex or agy; "" is the harness
// default.
func SetAlias(stateDir, alias, harness, id string) (statepath.EditResult, error) {
	if !IsAlias(alias) || (harness != "codex" && harness != agyHarness) || (id != "" && !ValidID(id)) {
		return statepath.EditResult{}, errors.New("model registry: want a tier alias, codex or agy, and a model id (or \"\")")
	}
	return editOverlay(stateDir, func(top *yaml.Node) error {
		aliases, ok := statepath.YAMLMap(top, "aliases")
		if !ok {
			return errors.New("model registry: aliases: is not a mapping; fix it by hand first")
		}
		cols, ok := statepath.YAMLMap(aliases, alias)
		if !ok {
			return fmt.Errorf("model registry: aliases.%s is not a mapping; fix it by hand first", alias)
		}
		statepath.YAMLSet(cols, harness, scalar("!!str", id))
		return nil
	})
}
