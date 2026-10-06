package modelreg

import (
	"context"
	"regexp"
	"sort"
	"time"
)

// Harnesses are the harness (runtime) names the registry covers, in display
// order. A catalog entry names the harnesses that accept its id as the value of
// their model flag. The legacy alias columns claude-sdk, gemini and
// antigravity-sdk are not harnesses here: they exist only so the catalog's
// aliases key stays equal to lib/settings/model-aliases.json.
var Harnesses = []string{"claude", "codex", "agy"}

// IsHarness reports whether name is one of Harnesses.
func IsHarness(name string) bool {
	for _, h := range Harnesses {
		if h == name {
			return true
		}
	}
	return false
}

// DefaultProvider is who serves a model on a harness when the entry does not say
// otherwise, which is also who receives the request. It is what an id admitted
// from discovery gets: the harness's own vendor.
var DefaultProvider = map[string]string{
	"claude": "anthropic",
	"codex":  "openai",
	"agy":    "google",
}

// IDPattern is the shape of a model id, the same rule dispatch enforces before an
// id reaches a harness's argv (runtime.ModelIDPattern; a test keeps the two
// equal). It starts with a letter or digit, never a dash, and is bounded.
const IDPattern = `^[a-z0-9][a-z0-9._:-]{0,63}$`

var idRe = regexp.MustCompile(IDPattern)

// ValidID reports whether id is safe to hand to a harness as a model id.
func ValidID(id string) bool { return idRe.MatchString(id) }

// Billing says how a model's use is paid for. Tokens are the accounting unit
// everywhere; only a model billed per API call carries a price.
type Billing string

// The billing modes.
const (
	// BillingSubscription: the harness runs under the operator's login and a call
	// costs nothing per token. No price is kept.
	BillingSubscription Billing = "subscription"
	// BillingAPI: billed per call. The only mode that carries a price.
	BillingAPI Billing = "api"
	// BillingLocal: runs on the operator's own hardware. No price is kept.
	BillingLocal Billing = "local"
)

// Valid reports whether b is one of the three modes.
func (b Billing) Valid() bool {
	switch b {
	case BillingSubscription, BillingAPI, BillingLocal:
		return true
	}
	return false
}

// AvailState is whether discovery has seen a model on its harness.
type AvailState string

// The availability states.
const (
	// AvailUnknown: no discovery result covers this harness (claude and codex
	// have no listing command wired in), or none has been taken yet.
	AvailUnknown AvailState = "unknown"
	// AvailYes: the harness's own listing named the id.
	AvailYes AvailState = "yes"
	// AvailNo: a listing was taken and did not name the id.
	AvailNo AvailState = "no"
)

// Availability is an entry's discovery state.
type Availability struct {
	State AvailState `json:"state"`
	// Source names where the answer came from ("agy models"); empty for unknown.
	Source string `json:"source,omitempty"`
	// At is when the listing was taken; zero for unknown.
	At time.Time `json:"at,omitzero"`
	// Stale is true when the listing is older than the freshness window. A stale
	// answer is still the best one available; a refresh is due.
	Stale bool `json:"stale,omitempty"`
}

// DiscoveredModel is one line of a harness's own model listing.
type DiscoveredModel struct {
	// ID passed ValidID. Anything that did not is never kept.
	ID string `json:"id"`
	// Name is the listing's display name with control characters removed and its
	// length bounded. It is for display only and never reaches a command line.
	Name string `json:"name,omitempty"`
}

// Snapshot is one successful listing of a harness's models.
type Snapshot struct {
	Harness string `json:"harness"`
	// Source names the command that produced it ("agy models").
	Source   string            `json:"source"`
	ProbedAt time.Time         `json:"probed_at"`
	Models   []DiscoveredModel `json:"models"`
}

// Has reports whether the snapshot lists id.
func (s Snapshot) Has(id string) bool {
	for _, m := range s.Models {
		if m.ID == id {
			return true
		}
	}
	return false
}

// IDs returns the listed ids, sorted.
func (s Snapshot) IDs() []string {
	out := make([]string, len(s.Models))
	for i, m := range s.Models {
		out[i] = m.ID
	}
	sort.Strings(out)
	return out
}

// SnapshotSource is what the registry reads discovery results from. It must not
// block on I/O beyond reading a small file: the dispatch path calls it.
// *Discoverer implements it.
type SnapshotSource interface {
	// Snapshot returns the latest listing for harness, whether it is fresh or not
	// (the Registry marks it Stale), and false when there is none.
	Snapshot(harness string) (Snapshot, bool)
}

// ProbeFunc answers "can this harness run now?" for discovery: its CLI is on PATH
// and it looks signed in. cmd/yakos injects a function over auth.ProbeRuntime (the
// P0a probe); this package does not import auth (see the package comment). reason
// is a short operator-facing sentence, set when ready is false.
type ProbeFunc func(ctx context.Context, harness string) (ready bool, reason string)

// Names of the files the registry keeps in the user state directory.
const (
	// OverlayFileName is the user overlay.
	OverlayFileName = "model-registry.yml"
	// DiscoveryFileName is the on-disk cache of discovery results.
	DiscoveryFileName = "model-discovery.json"
)
