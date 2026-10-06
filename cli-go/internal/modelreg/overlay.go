package modelreg

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/bakw00ds/yakos/internal/statepath"
)

// The user overlay, ~/.yakos-state/model-registry.yml, is the one place an
// operator changes what the registry believes. It LOOSENS (enable a model the
// catalog ships off, say how a model is billed, set a price, map a tier alias on
// a harness, admit discovered ids), so it is trusted only when nobody else could
// have written it. statepath.ReadTrusted makes the checks (see LoadOverlay for
// what a refusal does).
//
//	version: 1                    # optional; any other value is refused
//	models:
//	  gpt-5.6-sol:
//	    enabled: false            # or true, for an entry the catalog ships off
//	  claude-opus-5-5-high:
//	    billing: api              # subscription | api | local
//	    pricing: {input: 5, output: 25, cache_read: 0.5}   # USD per million tokens, billing api only
//	aliases:
//	  balanced:
//	    codex: gpt-5.6-terra      # codex and agy columns; "" means the harness default
//	discovery:
//	  admit: [agy]                # discovered ids on these harnesses join the registry
//
// Keys the file does not know are reported and ignored, so a file written for a
// newer yakOS still loads.

// maxOverlayBytes caps how much of the file is read. An overlay is a few lines;
// anything bigger is not one.
const maxOverlayBytes = 256 << 10

// maxOverlayEntries bounds the model and alias entries read, so a huge file
// cannot make every registry load slow.
const maxOverlayEntries = 512

// Overlay is the parsed user overlay. The zero value changes nothing.
type Overlay struct {
	// Models patches entries by id, on every harness that lists the id.
	Models map[string]ModelPatch
	// Aliases maps a tier alias to a model id per harness (codex and agy; the
	// claude column is fixed). "" means the harness default.
	Aliases map[string]map[string]string
	// Admit lists the harnesses whose discovered ids become registry entries.
	Admit []string
}

// ModelPatch is what the overlay says about one model.
type ModelPatch struct {
	// Enabled is nil when the overlay does not say.
	Enabled *bool
	// Billing is "" when the overlay does not say.
	Billing Billing
	// Pricing is nil when the overlay does not say. It applies only while the
	// model's effective billing is api.
	Pricing *Pricing
}

// OverlayPath returns the overlay file path inside stateDir.
func OverlayPath(stateDir string) string {
	return filepath.Join(stateDir, OverlayFileName)
}

// DefaultStateDir is where the overlay and the discovery cache live: the user's
// real state directory, $HOME/.yakos-state, never statepath.Dir(). A project can
// set the environment variable statepath.Dir() honours (a committed
// .claude/settings.json env block, K-129), and the overlay loosens a policy, so
// letting that variable move it would let a cloned repository plant its own. It is
// "" when there is no home directory, and the registry then reads no overlay.
func DefaultStateDir() string { return statepath.TrustedDir() }

// LoadOverlay reads the overlay from stateDir. A missing file is an empty overlay
// with no warning. A file that is not trusted (a symlink, not a regular file,
// owned by someone else, or group- or world-writable, in a directory with the
// same problems), is too large, or is not YAML is refused whole: it yields an
// empty overlay and one warning. Refusing it loosens nothing, which is the safe
// direction for a file whose job is to loosen.
func LoadOverlay(stateDir string) (Overlay, []string) {
	if stateDir == "" {
		return Overlay{}, nil
	}
	path := OverlayPath(stateDir)
	data, err := statepath.ReadTrusted(path, maxOverlayBytes+1)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Overlay{}, nil
		}
		var untrusted *statepath.UntrustedError
		if errors.As(err, &untrusted) {
			return Overlay{}, []string{overlayName + " ignored: " + untrustedSubject(untrusted, path) + " " + untrusted.Reason}
		}
		return Overlay{}, []string{overlayName + " ignored: it could not be read"}
	}
	if len(data) > maxOverlayBytes {
		return Overlay{}, []string{fmt.Sprintf("%s ignored: it is larger than %d bytes", overlayName, maxOverlayBytes)}
	}
	return ParseOverlay(data)
}

// overlayName is how warnings name the file. They carry no path: they reach
// stderr and, later, API responses.
const overlayName = "model-registry.yml (yakOS state directory)"

// untrustedSubject names what the trust check refused by role, without a path:
// the overlay file itself, or the state directory holding it.
func untrustedSubject(u *statepath.UntrustedError, file string) string {
	if u.Path == file {
		return "the file"
	}
	return "the state directory"
}

// ParseOverlay decodes overlay YAML. A document that is not valid YAML, or whose
// top level is not a mapping, yields an empty overlay and one warning. Otherwise
// every entry that decoded cleanly is kept and each one that did not is reported.
func ParseOverlay(data []byte) (Overlay, []string) {
	var doc any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return Overlay{}, []string{overlayName + " ignored: cannot parse: " + firstLine(err.Error())}
	}
	if doc == nil {
		return Overlay{}, nil // an empty file, or only comments
	}
	top, ok := asStringMap(doc)
	if !ok {
		return Overlay{}, []string{overlayName + " ignored: the top level must be a mapping"}
	}
	var (
		ov    Overlay
		warns []string
	)
	warn := func(format string, a ...any) { warns = append(warns, overlayName+": "+fmt.Sprintf(format, a...)) }

	if v, present := top["version"]; present {
		if n, ok := v.(int); !ok || n != 1 {
			return Overlay{}, []string{overlayName + " ignored: version must be 1 (this yakOS reads version 1)"}
		}
	}
	for _, k := range sortedKeys(top) {
		switch k {
		case "version", "models", "aliases", "discovery":
		default:
			warn("unknown key %s ignored", quote(k))
		}
	}
	if v, ok := top["models"]; ok && v != nil {
		ov.Models = parseOverlayModels(v, warn)
	}
	if v, ok := top["aliases"]; ok && v != nil {
		ov.Aliases = parseOverlayAliases(v, warn)
	}
	if v, ok := top["discovery"]; ok && v != nil {
		ov.Admit = parseOverlayDiscovery(v, warn)
	}
	return ov, warns
}

func parseOverlayModels(v any, warn func(string, ...any)) map[string]ModelPatch {
	m, ok := asStringMap(v)
	if !ok {
		warn("models: want a mapping of model id to settings; ignored")
		return nil
	}
	out := map[string]ModelPatch{}
	for i, id := range sortedKeys(m) {
		if i >= maxOverlayEntries {
			warn("models: only the first %d entries are read", maxOverlayEntries)
			break
		}
		if !ValidID(id) {
			warn("models: %s is not a valid model id; skipped", quote(id))
			continue
		}
		fields, ok := asStringMap(m[id])
		if !ok {
			warn("models.%s: want a mapping (enabled, billing, pricing); skipped", id)
			continue
		}
		var patch ModelPatch
		for _, k := range sortedKeys(fields) {
			switch k {
			case "enabled":
				b, ok := fields[k].(bool)
				if !ok {
					warn("models.%s.enabled: want true or false; ignored", id)
					continue
				}
				patch.Enabled = &b
			case "billing":
				s, _ := fields[k].(string)
				if !Billing(s).Valid() {
					warn("models.%s.billing: want subscription, api or local; ignored", id)
					continue
				}
				patch.Billing = Billing(s)
			case "pricing":
				p, ok := parsePricing(fields[k])
				if !ok {
					warn("models.%s.pricing: want input and output as dollars per million tokens (0..%d); ignored", id, maxPricePerMillion)
					continue
				}
				patch.Pricing = &p
			default:
				warn("models.%s: unknown key %s ignored", id, quote(k))
			}
		}
		out[id] = patch
	}
	return out
}

// parsePricing reads {input, output, cache_read, cache_write}. Input and output
// are required; the result must pass Pricing.Validate.
func parsePricing(v any) (Pricing, bool) {
	m, ok := asStringMap(v)
	if !ok {
		return Pricing{}, false
	}
	var p Pricing
	for k, dst := range map[string]*float64{"input": &p.Input, "output": &p.Output, "cache_read": &p.CacheRead, "cache_write": &p.CacheWrite} {
		raw, present := m[k]
		if !present {
			if k == "input" || k == "output" {
				return Pricing{}, false
			}
			continue
		}
		f, ok := asFloat(raw)
		if !ok {
			return Pricing{}, false
		}
		*dst = f
	}
	for k := range m {
		if k != "input" && k != "output" && k != "cache_read" && k != "cache_write" {
			return Pricing{}, false
		}
	}
	if p.Validate() != nil {
		return Pricing{}, false
	}
	return p, true
}

func parseOverlayAliases(v any, warn func(string, ...any)) map[string]map[string]string {
	m, ok := asStringMap(v)
	if !ok {
		warn("aliases: want a mapping of alias to harness to model id; ignored")
		return nil
	}
	out := map[string]map[string]string{}
	for _, alias := range sortedKeys(m) {
		if !IsAlias(alias) {
			warn("aliases: %s is not a tier alias (%s); skipped", quote(alias), joinWords(AliasNames))
			continue
		}
		cols, ok := asStringMap(m[alias])
		if !ok {
			warn("aliases.%s: want a mapping of harness to model id; skipped", alias)
			continue
		}
		for _, h := range sortedKeys(cols) {
			switch {
			case h == "claude":
				warn("aliases.%s.claude: the claude column is fixed (the claude CLI takes tier names); ignored", alias)
				continue
			case h != "codex" && h != "agy":
				warn("aliases.%s: %s is not a harness with an alias column (codex, agy); skipped", alias, quote(h))
				continue
			}
			id, ok := cols[h].(string)
			if !ok || (id != "" && !ValidID(id)) {
				warn("aliases.%s.%s: want a model id, or \"\" for the harness default; ignored", alias, h)
				continue
			}
			if out[alias] == nil {
				out[alias] = map[string]string{}
			}
			out[alias][h] = id
		}
	}
	return out
}

func parseOverlayDiscovery(v any, warn func(string, ...any)) []string {
	m, ok := asStringMap(v)
	if !ok {
		warn("discovery: want a mapping (admit: [harness, ...]); ignored")
		return nil
	}
	for _, k := range sortedKeys(m) {
		if k != "admit" {
			warn("discovery: unknown key %s ignored", quote(k))
		}
	}
	raw, ok := m["admit"]
	if !ok || raw == nil {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		warn("discovery.admit: want a list of harnesses; ignored")
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, el := range list {
		h, ok := el.(string)
		if !ok || !IsHarness(h) {
			warn("discovery.admit: skipping an entry that is not a harness (%s)", joinWords(Harnesses))
			continue
		}
		if h != agyHarness {
			warn("discovery.admit: %s has no model listing wired in; ignored", h)
			continue
		}
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}
