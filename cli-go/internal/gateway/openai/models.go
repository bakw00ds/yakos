package openai

// models.go: GET /v1/models and the mapping from a model id to a dispatch target.
//
//	yakos/auto              the router decides (runtime and model)
//	yakos/agent/<id>        a roster agent; the router still picks where it runs
//	<runtime>/<model>       a registry entry whose harness can run now
//
// Auth: bearer write token. Read-only, idempotent.

import (
	"context"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bakw00ds/yakos/internal/agentscompose"
	"github.com/bakw00ds/yakos/internal/auth"
	"github.com/bakw00ds/yakos/internal/modelreg"
	yruntime "github.com/bakw00ds/yakos/internal/runtime"
)

const (
	autoModel   = "yakos/auto"
	agentPrefix = "yakos/agent/"
)

// agentIDRe is the shape of a roster id the endpoint offers as a model id.
var agentIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// target is what a model id resolves to.
type target struct {
	agent   string
	runtime string // "" = the router decides
	model   string // "" = the harness or rule decides
}

type modelObject struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type modelList struct {
	Object string        `json:"object"`
	Data   []modelObject `json:"data"`
}

// catalog is every model id the endpoint offers right now, in a stable order,
// and the target of each.
func (s *Server) catalog(ctx context.Context) (ids []string, targets map[string]target) {
	targets = map[string]target{autoModel: {agent: s.cfg.Agent}}
	ids = []string{autoModel}

	if roster, err := agentscompose.Compose(s.cfg.YakosRoot, s.cfg.Workspace); err == nil {
		var agents []string
		for _, a := range roster {
			if agentIDRe.MatchString(a.ID) {
				agents = append(agents, a.ID)
			}
		}
		sort.Strings(agents)
		for _, id := range agents {
			targets[agentPrefix+id] = target{agent: id}
			ids = append(ids, agentPrefix+id)
		}
	}

	reg, err := s.cfg.Registry(s.cfg.Workspace)
	if err != nil {
		return ids, targets
	}
	signed := map[string]bool{}
	var entries []modelreg.Entry
	for _, e := range reg.Entries() {
		if !e.Usable() || !modelreg.ValidID(e.ID) || !modelreg.ValidID(e.Harness) {
			continue
		}
		if !slices.Contains(yruntime.Known, e.Harness) {
			continue // dispatch cannot run it
		}
		if _, seen := signed[e.Harness]; !seen {
			signed[e.Harness] = s.cfg.SignedIn(ctx, e.Harness)
		}
		if signed[e.Harness] {
			entries = append(entries, e)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Harness != entries[j].Harness {
			return entries[i].Harness < entries[j].Harness
		}
		return entries[i].ID < entries[j].ID
	})
	for _, e := range entries {
		id := e.Harness + "/" + e.ID
		if _, dup := targets[id]; dup {
			continue
		}
		targets[id] = target{agent: s.cfg.Agent, runtime: e.Harness, model: e.ID}
		ids = append(ids, id)
	}
	return ids, targets
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}
	ids, _ := s.catalog(r.Context())
	out := modelList{Object: "list", Data: make([]modelObject, 0, len(ids))}
	for _, id := range ids {
		out.Data = append(out.Data, modelObject{ID: id, Object: "model", Created: s.started.Unix(), OwnedBy: "yakos"})
	}
	writeJSON(w, http.StatusOK, out)
}

// resolve maps a requested model id to its target; false for an id the endpoint
// does not offer (unknown, a harness not signed in, a disabled model).
func (s *Server) resolve(ctx context.Context, model string) (target, bool) {
	model = strings.TrimSpace(model)
	if model == "" || len(model) > 160 {
		return target{}, false
	}
	// The catalog is rebuilt per request: it is a roster read and a cached probe,
	// and a stale one would offer a model whose harness has since signed out.
	_, targets := s.catalog(ctx)
	t, ok := targets[model]
	return t, ok
}

// probeCache remembers sign-in answers briefly, so a client that polls
// /v1/models does not run the keyring probe each time.
type probeCache struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[string]probeEntry
}

type probeEntry struct {
	ok bool
	at time.Time
}

func newProbeCache() *probeCache {
	return &probeCache{ttl: 15 * time.Second, m: map[string]probeEntry{}}
}

func (c *probeCache) signedIn(ctx context.Context, harness string) bool {
	c.mu.Lock()
	if e, ok := c.m[harness]; ok && time.Since(e.at) < c.ttl {
		c.mu.Unlock()
		return e.ok
	}
	c.mu.Unlock()
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	p := auth.ProbeRuntime(pctx, harness)
	ok := p.CLIPresent && p.Authed
	if pctx.Err() == nil {
		c.mu.Lock()
		c.m[harness] = probeEntry{ok: ok, at: time.Now()}
		c.mu.Unlock()
	}
	return ok
}
