package modelreg

import (
	"fmt"
	"strings"
	"time"
)

// Where a setting on an Entry came from.
const (
	// FromCatalog: the embedded catalog.
	FromCatalog = "catalog"
	// FromOverlay: the user overlay, ~/.yakos-state/model-registry.yml.
	FromOverlay = "overlay"
	// FromProject: the project's .yakos.yml (a disable, the only thing it can do).
	FromProject = "project"
	// FromDiscovery: an id a harness's own listing named and the overlay admitted.
	FromDiscovery = "discovered"
)

// Entry is one model on one harness as the registry sees it, after the overlay,
// the project and discovery have been applied. A catalog model that lists two
// harnesses is two entries.
type Entry struct {
	ID          string `json:"id"`
	Harness     string `json:"harness"`
	Name        string `json:"name"`
	Provider    string `json:"provider"`
	Family      string `json:"family,omitempty"`
	Description string `json:"description,omitempty"`

	// Billing is how the model is paid for, and BillingBy where that was decided
	// (catalog, overlay, or discovered for an id the overlay admitted: the harness
	// default, assumed and not stated by the catalog). The catalog states the mode
	// of the harness's own login; a dispatch authenticated by an API key is billed
	// per call whatever the entry says, which dispatch accounting decides (K-136).
	// An operator on a pay-per-token login says so with `billing: api` in the
	// overlay.
	Billing   Billing `json:"billing"`
	BillingBy string  `json:"billing_by"`
	// Cost is the price in dollars per million tokens. It is set only when Billing
	// is api and a price is known; CostBy says who set it.
	Cost   *Pricing `json:"cost,omitempty"`
	CostBy string   `json:"cost_by,omitempty"`

	// Enabled is false for a model switched off, and EnabledBy says by whom
	// (catalog default, overlay, or project). A disabled model is not a routing
	// candidate; naming it explicitly is a decision for the router, not the
	// registry.
	Enabled   bool   `json:"enabled"`
	EnabledBy string `json:"enabled_by"`
	// Source is catalog, or discovered for an id the overlay admitted.
	Source string `json:"source"`

	Visibility    string   `json:"visibility,omitempty"`
	EffortLevels  []string `json:"effort_levels,omitempty"`
	DefaultEffort string   `json:"default_effort,omitempty"`
	EffortInID    bool     `json:"effort_in_id,omitempty"`
	Modalities    []string `json:"modalities,omitempty"`
	OpenWeights   bool     `json:"open_weights,omitempty"`
	Limit         *Limits  `json:"limit,omitempty"`

	// Aliases are the tier aliases that resolve to this entry on its harness, in
	// AliasNames order.
	Aliases []string `json:"aliases"`
	// Availability is what discovery knows.
	Availability Availability `json:"availability"`
}

func (e Entry) clone() Entry {
	e.EffortLevels = append([]string(nil), e.EffortLevels...)
	e.Modalities = append([]string(nil), e.Modalities...)
	e.Aliases = append([]string{}, e.Aliases...) // never nil: the JSON says [] for no alias
	if e.Cost != nil {
		c := *e.Cost
		e.Cost = &c
	}
	if e.Limit != nil {
		l := *e.Limit
		e.Limit = &l
	}
	return e
}

// Usable reports whether the entry may be offered as a routing candidate: it is
// enabled and discovery has not seen it missing.
func (e Entry) Usable() bool { return e.Enabled && e.Availability.State != AvailNo }

// Options says where a Registry reads from. The zero value is the embedded
// catalog alone.
type Options struct {
	// StateDir is where the overlay is read from. Pass DefaultStateDir(). Empty
	// means no overlay.
	StateDir string
	// Project is the project root whose .yakos.yml `models:` may disable models.
	// Empty means none.
	Project string
	// Catalog replaces the embedded catalog (tests). Nil means the embedded one.
	Catalog *Catalog
	// Snapshots supplies discovery results. Nil means none: every entry is
	// unknown. A *Discoverer qualifies.
	Snapshots SnapshotSource
	// Now is the clock used to mark a snapshot stale; default time.Now.
	Now func() time.Time
	// FreshFor is how old a snapshot may be before it is marked stale; default
	// DefaultFreshFor.
	FreshFor time.Duration
}

// Registry is the merged, immutable view of the catalog, the user overlay, the
// project's disables and discovery. It is built once by Load and is safe for
// concurrent use; build a new one to see a changed file or a new snapshot.
// Every listing is in a fixed order (harness order, then catalog order, then
// discovered ids by name), so the same inputs always give the same bytes.
type Registry struct {
	catalog  *Catalog
	entries  []Entry
	index    map[string]int
	aliases  map[string]map[string]string // alias -> harness -> id, effective
	aliasBy  map[string]map[string]string // alias -> harness -> catalog|overlay
	warnings []string
	// mergeWarnings counts the problems found while merging; past
	// maxMergeWarnings they are counted, not kept.
	mergeWarnings int
}

// maxMergeWarnings bounds the warnings the merge itself adds (an overlay naming
// hundreds of unknown models, say).
const maxMergeWarnings = 40

// Load builds a Registry. The error is for a catalog that does not validate (a
// broken build); every problem with the overlay, the project file or discovery
// is a warning, available from Warnings, and degrades to "that source says
// nothing".
func Load(o Options) (*Registry, error) {
	cat := o.Catalog
	if cat == nil {
		c, err := EmbeddedCatalog()
		if err != nil {
			return nil, err
		}
		cat = c
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	fresh := o.FreshFor
	if fresh <= 0 {
		fresh = DefaultFreshFor
	}

	r := &Registry{catalog: cat, index: map[string]int{}}
	ov, w := LoadOverlay(o.StateDir)
	r.warnings = append(r.warnings, w...)
	pol, w := LoadProject(o.Project)
	r.warnings = append(r.warnings, w...)

	r.expandCatalog()
	snaps := map[string]Snapshot{}
	if o.Snapshots != nil {
		for _, h := range Harnesses {
			if s, ok := o.Snapshots.Snapshot(h); ok {
				snaps[h] = s
			}
		}
	}
	r.admit(ov.Admit, snaps)
	r.applyOverlayModels(ov.Models)
	r.buildAliases(ov.Aliases)
	r.warnAmbiguousAliases()
	r.applyProject(pol.Disable)
	r.applyAvailability(snaps, now(), fresh)
	r.labelAliases()
	return r, nil
}

func key(harness, id string) string { return harness + "\x00" + id }

// expandCatalog turns each catalog model into one entry per harness, in harness
// order and then catalog order.
func (r *Registry) expandCatalog() {
	for _, h := range Harnesses {
		for i := range r.catalog.Models {
			m := &r.catalog.Models[i]
			if !contains(m.Harnesses, h) {
				continue
			}
			e := Entry{
				ID: m.ID, Harness: h, Name: m.Name, Provider: m.Provider, Family: m.Family,
				Description: m.Description, Billing: m.Billing, BillingBy: FromCatalog,
				Enabled: m.DefaultEnabled == nil || *m.DefaultEnabled, EnabledBy: FromCatalog,
				Source: FromCatalog, Visibility: m.Visibility,
				EffortLevels:  append([]string(nil), m.EffortLevels...),
				DefaultEffort: m.DefaultEffort, EffortInID: m.EffortInID, OpenWeights: m.OpenWeights,
				Availability: Availability{State: AvailUnknown},
			}
			if m.Modalities != nil {
				e.Modalities = append([]string(nil), m.Modalities.Input...)
			}
			if m.Limit != nil {
				l := *m.Limit
				e.Limit = &l
			}
			if m.Cost != nil {
				c := *m.Cost
				e.Cost, e.CostBy = &c, FromCatalog
			}
			r.add(e)
		}
	}
}

func (r *Registry) add(e Entry) {
	r.index[key(e.Harness, e.ID)] = len(r.entries)
	r.entries = append(r.entries, e)
}

// admit adds the ids a harness's own listing named, for each harness the overlay
// admits. It is the only way a model the catalog does not list enters the
// registry, and only the operator's overlay can open it.
func (r *Registry) admit(harnesses []string, snaps map[string]Snapshot) {
	for _, h := range harnesses {
		snap, ok := snaps[h]
		if !ok {
			continue
		}
		var added []Entry
		for _, d := range snap.Models {
			if !ValidID(d.ID) {
				continue // a Snapshot is already validated; this is the last line
			}
			if _, have := r.index[key(h, d.ID)]; have {
				continue
			}
			if r.reserved(d.ID) {
				r.warn("discovery: %s on %s is not registered: dispatch reads that word as a tier alias or a Claude tier, not as this model", d.ID, h)
				continue
			}
			e := Entry{
				// The name is display text from another program: the Discoverer
				// sanitizes it, and this is the last line for any other SnapshotSource.
				ID: d.ID, Harness: h, Name: sanitizeText(d.Name, maxNameRunes), Provider: DefaultProvider[h],
				Billing: BillingSubscription, BillingBy: FromDiscovery,
				Enabled: true, EnabledBy: FromOverlay, Source: FromDiscovery,
				Availability: Availability{State: AvailUnknown},
			}
			if e.Name == "" {
				e.Name = d.ID
			}
			if h == agyHarness {
				if eff := agyEffortSuffix(d.ID); eff != "" {
					e.EffortLevels, e.DefaultEffort, e.EffortInID = []string{eff}, eff, true
				}
			}
			added = append(added, e)
		}
		sortEntriesByID(added)
		for _, e := range added {
			r.add(e)
		}
	}
}

// reserved reports whether id is a word dispatch reads as something other than the
// id of a model on a harness that is not claude: a tier alias (expanded through the
// alias table, so the model would be unreachable by that name) or a Claude tier name
// (dropped, because it names no model on another harness). A discovered id with such
// a name is not registered.
func (r *Registry) reserved(id string) bool {
	if IsAlias(id) {
		return true
	}
	_, tier := r.index[key("claude", id)]
	return tier
}

// agyEffortSuffix returns the reasoning effort an agy model id carries, or "".
// Every id `agy models` lists ends in -low, -medium or -high.
func agyEffortSuffix(id string) string {
	for _, e := range []string{"low", "medium", "high"} {
		if strings.HasSuffix(id, "-"+e) {
			return e
		}
	}
	return ""
}

func sortEntriesByID(es []Entry) {
	for i := 1; i < len(es); i++ {
		for j := i; j > 0 && es[j-1].ID > es[j].ID; j-- {
			es[j-1], es[j] = es[j], es[j-1]
		}
	}
}

// applyOverlayModels applies the operator's per-model settings. The overlay names
// a model by id and the setting applies on every harness that lists the id.
func (r *Registry) applyOverlayModels(patches map[string]ModelPatch) {
	for _, id := range sortedKeys(patches) {
		p := patches[id]
		hit := false
		for i := range r.entries {
			e := &r.entries[i]
			if e.ID != id {
				continue
			}
			hit = true
			if p.Enabled != nil {
				e.Enabled, e.EnabledBy = *p.Enabled, FromOverlay
			}
			if p.Billing != "" {
				e.Billing, e.BillingBy = p.Billing, FromOverlay
				if e.Billing != BillingAPI {
					e.Cost, e.CostBy = nil, "" // tokens are the unit outside api billing
				}
			}
			if p.Pricing != nil {
				if e.Billing != BillingAPI {
					r.warn("models.%s.pricing ignored on %s: billing is %s, and only an api model carries a price (set billing: api to price it)", id, e.Harness, e.Billing)
					continue
				}
				c := *p.Pricing
				e.Cost, e.CostBy = &c, FromOverlay
			}
		}
		if !hit {
			r.warn("models.%s: not a model the catalog lists (and not an admitted discovered id); ignored", id)
		}
	}
}

// buildAliases merges the catalog's alias columns with the overlay's. The claude
// column is the catalog's alone (the overlay cannot reach it: the claude CLI takes
// tier names). A mapping to an id the registry does not know is kept, because the
// overlay is the operator's own file and a model may be newer than the catalog,
// and reported, because a typo looks the same.
func (r *Registry) buildAliases(overlay map[string]map[string]string) {
	r.aliases = map[string]map[string]string{}
	r.aliasBy = map[string]map[string]string{}
	for _, a := range AliasNames {
		r.aliases[a] = map[string]string{}
		r.aliasBy[a] = map[string]string{}
		for _, h := range Harnesses {
			r.aliases[a][h] = r.catalog.Aliases[a][h]
			r.aliasBy[a][h] = FromCatalog
		}
	}
	for _, a := range sortedKeys(overlay) {
		for _, h := range sortedKeys(overlay[a]) {
			id := overlay[a][h]
			if h == "claude" || !IsAlias(a) || !IsHarness(h) {
				continue // the overlay parser already refused these
			}
			if _, known := r.index[key(h, id)]; id != "" && !known {
				r.warn("aliases.%s.%s: %s is not a %s model the registry knows; accepted because the overlay is yours", a, h, id, h)
			}
			r.aliases[a][h], r.aliasBy[a][h] = id, FromOverlay
		}
	}
}

// warnAmbiguousAliases reports an id that two aliases of different cost classes
// map to on one harness. The catalog has none (best and reasoning share a rank);
// an overlay can create one, and the dearer class counts for the model (see
// ClassOf), which is rarely what a mapping meant.
func (r *Registry) warnAmbiguousAliases() {
	for _, h := range Harnesses {
		byID := map[string][]string{}
		var order []string
		for _, a := range AliasNames {
			id := r.aliases[a][h]
			if id == "" {
				continue
			}
			if _, seen := byID[id]; !seen {
				order = append(order, id)
			}
			byID[id] = append(byID[id], a)
		}
		for _, id := range order {
			aliases := byID[id]
			lo, hi := aliasRank[aliases[0]], aliasRank[aliases[0]]
			for _, a := range aliases[1:] {
				if rk := aliasRank[a]; rk < lo {
					lo = rk
				} else if rk > hi {
					hi = rk
				}
			}
			if lo != hi {
				r.warn("aliases %s all map to %s on %s but are different cost classes; the dearest counts", joinWords(aliases), id, h)
			}
		}
	}
}

// applyProject switches off the ids a project's .yakos.yml names. It can only set
// Enabled to false, whatever the overlay or the catalog said.
func (r *Registry) applyProject(disable []string) {
	for _, id := range disable {
		hit := false
		for i := range r.entries {
			if r.entries[i].ID == id {
				r.entries[i].Enabled, r.entries[i].EnabledBy = false, FromProject
				hit = true
			}
		}
		if !hit {
			r.warn(".yakos.yml models.disable: %s is not a model the registry knows; ignored", id)
		}
	}
}

func (r *Registry) applyAvailability(snaps map[string]Snapshot, now time.Time, fresh time.Duration) {
	for i := range r.entries {
		e := &r.entries[i]
		snap, ok := snaps[e.Harness]
		if !ok {
			continue
		}
		st := AvailNo
		if snap.Has(e.ID) {
			st = AvailYes
		}
		e.Availability = Availability{State: st, Source: sanitizeText(snap.Source, 64), At: snap.ProbedAt, Stale: now.Sub(snap.ProbedAt) > fresh}
	}
}

func (r *Registry) labelAliases() {
	for i := range r.entries {
		e := &r.entries[i]
		e.Aliases = []string{}
		for _, a := range AliasNames {
			if r.aliases[a][e.Harness] == e.ID {
				e.Aliases = append(e.Aliases, a)
			}
		}
	}
}

func (r *Registry) warn(format string, a ...any) {
	r.mergeWarnings++
	if r.mergeWarnings > maxMergeWarnings {
		return
	}
	r.warnings = append(r.warnings, "model registry: "+fmt.Sprintf(format, a...))
}

// Warnings are the problems found while loading the overlay, the project file and
// the merge, in a fixed order, each source capped. They carry no secret, and the
// ones about the overlay carry no path.
func (r *Registry) Warnings() []string {
	out := append([]string(nil), r.warnings...)
	if r.mergeWarnings > maxMergeWarnings {
		out = append(out, fmt.Sprintf("model registry: and %d more problems not shown", r.mergeWarnings-maxMergeWarnings))
	}
	return out
}

// Sources are the dated provenance notes of the catalog.
func (r *Registry) Sources() []Source { return append([]Source(nil), r.catalog.Sources...) }

// Entries returns every entry in a fixed order (harness order, catalog order,
// discovered ids by name). The slice and what it points at are the caller's.
func (r *Registry) Entries() []Entry {
	out := make([]Entry, len(r.entries))
	for i := range r.entries {
		out[i] = r.entries[i].clone()
	}
	return out
}

// Lookup returns the entry for id on harness.
func (r *Registry) Lookup(harness, id string) (Entry, bool) {
	i, ok := r.index[key(harness, id)]
	if !ok {
		return Entry{}, false
	}
	return r.entries[i].clone(), true
}

// Find returns every entry with the given id, one per harness that lists it.
func (r *Registry) Find(id string) []Entry {
	var out []Entry
	for i := range r.entries {
		if r.entries[i].ID == id {
			out = append(out, r.entries[i].clone())
		}
	}
	return out
}

// ResolveAlias returns the model id a tier alias maps to on harness. found is
// false for a word that is not an alias, a harness with no column, and a mapping
// that is empty: an empty mapping is the harness default (no model flag), which
// is how the codex column ships. It reads the mapping only; whether the target is
// enabled is Lookup's to say.
func (r *Registry) ResolveAlias(harness, alias string) (id string, found bool) {
	id = r.aliases[alias][harness]
	return id, id != ""
}

// AliasSource says where the mapping of alias on harness came from: catalog or
// overlay. It is "" for an unknown alias or harness.
func (r *Registry) AliasSource(harness, alias string) string { return r.aliasBy[alias][harness] }
