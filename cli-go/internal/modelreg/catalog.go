package modelreg

import (
	"bytes"
	_ "embed" // model-catalog.json
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// embeddedCatalogJSON is a byte-for-byte copy of lib/settings/model-catalog.json.
// go:embed cannot reach outside the package directory, and the staged framework
// copy (internal/framework/embedded) is empty in a source checkout, so the
// catalog the Go programs read lives here too, the way the alias table lives in
// internal/runtime. TestEmbeddedCatalogMatchesLib fails when the two drift;
// refresh with
//
//	cp lib/settings/model-catalog.json cli-go/internal/modelreg/model-catalog.json
//
//go:embed model-catalog.json
var embeddedCatalogJSON []byte

// AliasNames are the semantic tier aliases in documentation order. An alias names
// a quality class; each harness maps it to its own model id. They are the same
// five words runtime.AliasNames carries (a test keeps the two equal).
var AliasNames = []string{"cheap", "balanced", "best", "reasoning", "frontier"}

// IsAlias reports whether name is one of AliasNames.
func IsAlias(name string) bool {
	for _, a := range AliasNames {
		if a == name {
			return true
		}
	}
	return false
}

// aliasRank orders the tier classes by cost. best and reasoning share a rank:
// both resolve to the same Claude tier (opus), which is the order
// budget.ClampModel has always applied (haiku < sonnet < opus < fable).
var aliasRank = map[string]int{"cheap": 1, "balanced": 2, "best": 3, "reasoning": 3, "frontier": 4}

// ClassRank returns the cost rank of a tier alias (cheap 1, balanced 2, best and
// reasoning 3, frontier 4) and false for any other word.
func ClassRank(alias string) (int, bool) {
	r, ok := aliasRank[alias]
	return r, ok
}

// legacyColumns are the runtime columns of the alias table. The registry reads
// claude, codex and agy; the other three exist so the catalog's aliases key stays
// equal to lib/settings/model-aliases.json, which the bash CLI reads.
var legacyColumns = []string{"claude", "claude-sdk", "codex", "gemini", "agy", "antigravity-sdk"}

// EffortLevels are the reasoning-effort words a catalog entry may list.
var EffortLevels = []string{"low", "medium", "high", "xhigh", "max", "ultra"}

// Pricing is what a model billed per API call costs, in US dollars per million
// tokens (the unit models.dev uses). Only an entry whose billing is api carries
// one; for every other mode tokens are the unit and no price is kept.
type Pricing struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
}

// Limits are a model's token limits. Zero means not recorded.
type Limits struct {
	// Context is the standard context window in tokens.
	Context int `json:"context,omitempty"`
	// ContextMax is the extended window some models offer (codex's
	// max_context_window); zero when there is none.
	ContextMax int `json:"context_max,omitempty"`
	// Output is the output-token limit.
	Output int `json:"output,omitempty"`
}

// Modalities lists what a model accepts as input.
type Modalities struct {
	Input []string `json:"input,omitempty"`
}

// Model is one catalog entry, in the models.dev shape plus the yakOS keys.
type Model struct {
	// ID is the value of the harness's model flag. For claude it is a tier name.
	ID   string `json:"id"`
	Name string `json:"name"`
	// Provider serves the model and so receives the request: anthropic for the
	// claude harness, openai for codex, google for everything agy lists
	// (Antigravity fronts Anthropic and open-weight models too).
	Provider string `json:"provider"`
	// Family groups related models (claude-opus, gemini-flash).
	Family      string `json:"family,omitempty"`
	Description string `json:"description,omitempty"`
	// Harnesses are the runtimes whose model flag takes this id.
	Harnesses []string `json:"harnesses"`
	Billing   Billing  `json:"billing"`
	// DefaultEnabled is false for an entry that ships switched off (a model the
	// harness hides from its own picker). nil means enabled.
	DefaultEnabled *bool `json:"default_enabled,omitempty"`
	// Visibility is the harness's own word for whether it offers the model in its
	// picker: "list" or "hide". Informational.
	Visibility    string   `json:"visibility,omitempty"`
	EffortLevels  []string `json:"effort_levels,omitempty"`
	DefaultEffort string   `json:"default_effort,omitempty"`
	// EffortInID is true when the id already carries the effort (agy's -low,
	// -medium and -high suffix) and the harness rejects --effort next to it.
	EffortInID  bool        `json:"effort_in_id,omitempty"`
	Modalities  *Modalities `json:"modalities,omitempty"`
	OpenWeights bool        `json:"open_weights,omitempty"`
	Limit       *Limits     `json:"limit,omitempty"`
	// Cost is set only when Billing is api.
	Cost *Pricing `json:"cost,omitempty"`
}

// Source records where a harness's entries came from and when.
type Source struct {
	Harness  string `json:"harness"`
	Captured string `json:"captured"`
	Note     string `json:"note"`
}

// Catalog is the parsed model-catalog.json.
type Catalog struct {
	Doc     string   `json:"_doc,omitempty"`
	Schema  int      `json:"schema"`
	Sources []Source `json:"sources,omitempty"`
	Models  []Model  `json:"models"`
	// Aliases is alias -> runtime column -> model id; an empty id means the
	// harness default (no model flag). Equal to lib/settings/model-aliases.json.
	Aliases map[string]map[string]string `json:"aliases"`
}

// CatalogSchema is the schema version this code reads.
const CatalogSchema = 1

// maxCatalogModels bounds a catalog; the framework ships a few dozen.
const maxCatalogModels = 1024

// EmbeddedCatalog parses the catalog embedded in the binary. The embedded file is
// validated by a test, so an error here means a broken build, not bad user input.
func EmbeddedCatalog() (*Catalog, error) { return ParseCatalog(embeddedCatalogJSON) }

// ParseCatalog decodes and validates a catalog. It is strict: an unknown key, a
// trailing document, a duplicate entry or any value outside its domain is an
// error, because the file ships with the binary and a typo should fail a test,
// not silently drop a field.
func ParseCatalog(data []byte) (*Catalog, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var c Catalog
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("modelreg: parse catalog: %w", err)
	}
	// A second Decode must find the end of the input: More() would miss a stray
	// closing brace.
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("modelreg: parse catalog: trailing data after the document")
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Catalog) validate() error {
	if c.Schema != CatalogSchema {
		return fmt.Errorf("modelreg: catalog schema %d, this build reads %d", c.Schema, CatalogSchema)
	}
	if len(c.Models) == 0 {
		return fmt.Errorf("modelreg: catalog has no models")
	}
	if len(c.Models) > maxCatalogModels {
		return fmt.Errorf("modelreg: catalog has %d models (limit %d)", len(c.Models), maxCatalogModels)
	}
	seen := map[string]bool{}
	for i := range c.Models {
		m := &c.Models[i]
		if err := m.validate(); err != nil {
			return fmt.Errorf("modelreg: catalog model %d (%q): %w", i, m.ID, err)
		}
		for _, h := range m.Harnesses {
			k := h + "\x00" + m.ID
			if seen[k] {
				return fmt.Errorf("modelreg: catalog lists %s on %s twice", m.ID, h)
			}
			seen[k] = true
		}
	}
	for _, s := range c.Sources {
		if !IsHarness(s.Harness) || s.Captured == "" || !printable(s.Note, 400) {
			return fmt.Errorf("modelreg: catalog source for %q is malformed", s.Harness)
		}
	}
	return c.validateAliases(seen)
}

// validateAliases checks the alias table: exactly the five tier words, only the
// known columns, and every claude, codex and agy id naming a catalog model on
// that harness (or empty, meaning the harness default).
func (c *Catalog) validateAliases(have map[string]bool) error {
	if len(c.Aliases) != len(AliasNames) {
		return fmt.Errorf("modelreg: catalog aliases must be exactly %s", strings.Join(AliasNames, ", "))
	}
	for _, a := range AliasNames {
		cols, ok := c.Aliases[a]
		if !ok {
			return fmt.Errorf("modelreg: catalog aliases: %q is missing", a)
		}
		for col, id := range cols {
			if !contains(legacyColumns, col) {
				return fmt.Errorf("modelreg: catalog aliases.%s: unknown runtime column %q", a, col)
			}
			if id == "" {
				continue
			}
			if !ValidID(id) {
				return fmt.Errorf("modelreg: catalog aliases.%s.%s: %q is not a valid model id", a, col, id)
			}
			if IsHarness(col) && !have[col+"\x00"+id] {
				return fmt.Errorf("modelreg: catalog aliases.%s.%s: %q is not a %s model in this catalog", a, col, id, col)
			}
		}
		if cols["claude"] == "" {
			return fmt.Errorf("modelreg: catalog aliases.%s.claude must name a tier", a)
		}
	}
	return nil
}

func (m *Model) validate() error {
	if !ValidID(m.ID) {
		return fmt.Errorf("id is not a valid model id")
	}
	if !printable(m.Name, 80) || m.Name == "" {
		return fmt.Errorf("name must be 1-80 printable characters")
	}
	if !labelRe.MatchString(m.Provider) {
		return fmt.Errorf("provider %q is not a label", m.Provider)
	}
	if m.Family != "" && !labelRe.MatchString(m.Family) {
		return fmt.Errorf("family %q is not a label", m.Family)
	}
	if !printable(m.Description, 400) {
		return fmt.Errorf("description must be at most 400 printable characters")
	}
	if len(m.Harnesses) == 0 {
		return fmt.Errorf("harnesses must name at least one harness")
	}
	dupe := map[string]bool{}
	for _, h := range m.Harnesses {
		if !IsHarness(h) || dupe[h] {
			return fmt.Errorf("harnesses: %q is unknown or repeated", h)
		}
		dupe[h] = true
	}
	if !m.Billing.Valid() {
		return fmt.Errorf("billing %q is not subscription, api or local", m.Billing)
	}
	switch m.Visibility {
	case "", "list", "hide":
	default:
		return fmt.Errorf("visibility %q is not list or hide", m.Visibility)
	}
	if err := validateEfforts(m.EffortLevels, m.DefaultEffort); err != nil {
		return err
	}
	if m.EffortInID && len(m.EffortLevels) != 1 {
		return fmt.Errorf("effort_in_id means exactly one effort level (the id's own)")
	}
	if m.Modalities != nil {
		for _, in := range m.Modalities.Input {
			if !contains([]string{"text", "image", "audio", "video", "pdf"}, in) {
				return fmt.Errorf("modalities.input: unknown %q", in)
			}
		}
	}
	if l := m.Limit; l != nil {
		if l.Context < 0 || l.ContextMax < 0 || l.Output < 0 || (l.ContextMax != 0 && l.ContextMax < l.Context) {
			return fmt.Errorf("limit is out of range")
		}
	}
	if m.Cost != nil {
		if m.Billing != BillingAPI {
			return fmt.Errorf("cost is only for billing=api (billing is %s: tokens are the unit)", m.Billing)
		}
		if err := m.Cost.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func validateEfforts(levels []string, def string) error {
	seen := map[string]bool{}
	for _, e := range levels {
		if !contains(EffortLevels, e) || seen[e] {
			return fmt.Errorf("effort_levels: %q is unknown or repeated", e)
		}
		seen[e] = true
	}
	if def != "" && !seen[def] {
		return fmt.Errorf("default_effort %q is not one of effort_levels", def)
	}
	return nil
}

// maxPricePerMillion is a sanity bound, not a real price: it keeps a typo
// (a price in cents, or in dollars per token) from becoming a budget input.
const maxPricePerMillion = 100000

// Validate reports a price that is not a finite, non-negative number of dollars
// per million tokens within the sanity bound. Input and output are required.
func (p Pricing) Validate() error {
	// A fixed order, so two bad fields always give the same error text.
	for _, f := range []struct {
		name string
		v    float64
	}{{"input", p.Input}, {"output", p.Output}, {"cache_read", p.CacheRead}, {"cache_write", p.CacheWrite}} {
		if math.IsNaN(f.v) || math.IsInf(f.v, 0) || f.v < 0 || f.v > maxPricePerMillion {
			return fmt.Errorf("cost.%s must be between 0 and %d dollars per million tokens", f.name, maxPricePerMillion)
		}
	}
	if p.Input == 0 && p.Output == 0 {
		return fmt.Errorf("cost needs a non-zero input or output price (an api model that is free belongs under another billing mode)")
	}
	return nil
}

// labelRe is the shape of a provider or family label: short, lower case, no spaces.
var labelRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// printable reports whether s is at most max runes of text with no control or
// format characters, so a catalog or overlay string can never carry a terminal
// escape or a bidi override into the CLI's output.
func printable(s string, max int) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > max {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}
