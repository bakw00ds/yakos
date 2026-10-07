package routerpolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// gateway_classes (K-141) maps Claude Code request classes to Claude model ids.
//
//	gateway_classes:
//	  subagent: haiku
//	  haiku: claude-haiku-4-5-20251001
//
// Each class is realised only through one documented Claude Code environment
// variable (EnvName); the table below is the whole vocabulary. Nothing else
// (compaction, workflow, main-by-class) is routable without the gateway hint
// headers of the P4 phase. The key lives in the user-level policy only: a
// project .yakos.yml cannot set it.

// Class names.
const (
	ClassSubagent = "subagent"
	ClassOpus     = "opus"
	ClassSonnet   = "sonnet"
	ClassHaiku    = "haiku"
	ClassFable    = "fable"
)

// maxGatewayClasses bounds the entries read; the vocabulary has five.
const maxGatewayClasses = 16

// classEnv is the verified class -> Claude Code variable table (docs/routing.md).
var classEnv = map[string]string{
	ClassSubagent: "CLAUDE_CODE_SUBAGENT_MODEL",
	ClassOpus:     "ANTHROPIC_DEFAULT_OPUS_MODEL",
	ClassSonnet:   "ANTHROPIC_DEFAULT_SONNET_MODEL",
	ClassHaiku:    "ANTHROPIC_DEFAULT_HAIKU_MODEL",
	ClassFable:    "ANTHROPIC_DEFAULT_FABLE_MODEL",
}

// ClassNames lists the known classes in the order they are reported.
func ClassNames() []string {
	return []string{ClassSubagent, ClassOpus, ClassSonnet, ClassHaiku, ClassFable}
}

// EnvNameFor returns the variable a class sets, or "" for an unknown class.
func EnvNameFor(class string) string { return classEnv[class] }

// modelIDRe is the id shape shared with runtime.ModelIDPattern (the argv-safety
// rule); it is repeated here because runtime imports this package.
var modelIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]{0,63}$`)

// claudeIDRe accepts a first-party id (claude-...) and the Bedrock and Vertex
// spellings (anthropic.claude-..., us.anthropic.claude-...).
var claudeIDRe = regexp.MustCompile(`^(?:(?:(?:us|eu|apac|global|jp|au|ca)\.)?anthropic\.)?claude-`)

var claudeTiers = map[string]bool{"haiku": true, "sonnet": true, "opus": true, "fable": true}

// validClassModel reports whether model may be set for class. Only Claude
// models: a tier name or a Claude id for subagent (the CLI resolves a tier
// name itself); a concrete Claude id for the alias classes, where a bare tier
// name would point an alias at itself.
func validClassModel(class, model string) bool {
	if !modelIDRe.MatchString(model) {
		return false
	}
	if claudeIDRe.MatchString(model) {
		return true
	}
	return class == ClassSubagent && claudeTiers[model]
}

// GatewayClass is one validated class assignment.
type GatewayClass struct {
	Class   string
	EnvName string
	Model   string
}

// GatewayClasses is the validated table, sorted by class name.
type GatewayClasses []GatewayClass

// SHA returns a hex digest of the table: sha256 over the sorted
// "ENV=model\n" lines. Empty for an empty table.
func (g GatewayClasses) SHA() string {
	if len(g) == 0 {
		return ""
	}
	h := sha256.New()
	for _, c := range g {
		h.Write([]byte(c.EnvName + "=" + c.Model + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Classes validates the gateway_classes key. Any problem (a non-map
// shape, an unknown class, a duplicate class, a non-Claude or malformed model
// id, too many entries) ignores the WHOLE key, so a half-valid table never
// applies, and returns path-free warnings. A missing key is an empty table and
// no warning. Warnings name a class only when it is a known one and never echo
// a value.
func (f File) Classes() (GatewayClasses, []string) {
	n := f.RawGatewayClasses
	if n.Kind == 0 || (n.Kind == yaml.ScalarNode && n.Tag == "!!null") {
		return nil, nil
	}
	refuse := func(why string) (GatewayClasses, []string) {
		return nil, []string{"router policy: gateway_classes ignored (" + why + "); no class aliasing is applied"}
	}
	if n.Kind != yaml.MappingNode {
		return refuse("it is not a mapping of class to model id")
	}
	if len(n.Content)/2 > maxGatewayClasses {
		return refuse("too many entries")
	}
	seen := map[string]bool{}
	var out GatewayClasses
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.Kind != yaml.ScalarNode || v.Kind != yaml.ScalarNode || v.Tag != "!!str" {
			return refuse("every entry must be a class name and a model id string")
		}
		class := strings.ToLower(strings.TrimSpace(k.Value))
		env := classEnv[class]
		if env == "" {
			return refuse("unknown class; known: " + strings.Join(ClassNames(), ", "))
		}
		if seen[class] {
			return refuse("class " + class + " is listed twice")
		}
		seen[class] = true
		model := strings.TrimSpace(v.Value)
		if !validClassModel(class, model) {
			return refuse("class " + class + " is not set to a Claude model id")
		}
		out = append(out, GatewayClass{Class: class, EnvName: env, Model: model})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Class < out[b].Class })
	return out, nil
}

// LoadGatewayClasses reads the policy in stateDir (same trust checks as Load)
// and returns its validated gateway_classes. A load error returns an empty
// table plus the error: a bad file fails closed to no aliasing.
func LoadGatewayClasses(stateDir string) (GatewayClasses, []string, error) {
	p, err := Load(stateDir)
	if err != nil {
		return nil, nil, err
	}
	g, warn := p.Classes()
	return g, warn, nil
}
