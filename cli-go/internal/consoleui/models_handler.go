package consoleui

// models_handler.go: GET /api/models (K-148).
//
// The model registry (internal/modelreg) as the chat panes need it: which models
// each harness offers, so the runtime and model selects depend on each other
// without a list baked into app.js.
//
// Auth: RoleRead (read-only; the registry holds no secret). Idempotent GET.
// Rate-limit class: inherits the project default. The response carries no path:
// registry warnings (which can name a file role) are reduced to a count.

import (
	"encoding/json"
	"net/http"

	"github.com/bakw00ds/yakos/internal/modelreg"
)

// modelsEntry is one selectable model.
type modelsEntry struct {
	ID            string   `json:"id"`
	Harness       string   `json:"harness"`
	Name          string   `json:"name,omitempty"`
	Provider      string   `json:"provider"`
	Billing       string   `json:"billing"`
	Usable        bool     `json:"usable"`
	EffortLevels  []string `json:"effort_levels"`
	DefaultEffort string   `json:"default_effort,omitempty"`
	Aliases       []string `json:"aliases"`
}

type modelsResponse struct {
	Harnesses []string      `json:"harnesses"`
	Models    []modelsEntry `json:"models"`
	Warnings  int           `json:"warnings"`
}

// modelsRegistry builds the registry a request sees: the embedded catalog, the
// operator's overlay and the workspace's disables. Replaced by tests.
var modelsRegistry = func(project string) (*modelreg.Registry, error) {
	return modelreg.Load(modelreg.Options{StateDir: modelreg.DefaultStateDir(), Project: project})
}

func (ch *chatHandlers) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	reg, err := modelsRegistry(ch.workspaceRoot)
	if err != nil {
		http.Error(w, "model registry unavailable", http.StatusServiceUnavailable)
		return
	}
	resp := modelsResponse{Harnesses: append([]string{}, modelreg.Harnesses...), Models: []modelsEntry{}, Warnings: len(reg.Warnings())}
	for _, e := range reg.Entries() {
		if !modelreg.ValidID(e.ID) {
			continue // never offer an id dispatch would refuse
		}
		efforts := e.EffortLevels
		if efforts == nil {
			efforts = []string{}
		}
		resp.Models = append(resp.Models, modelsEntry{
			ID: e.ID, Harness: e.Harness, Name: e.Name, Provider: e.Provider,
			Billing: string(e.Billing), Usable: e.Usable(), EffortLevels: efforts,
			DefaultEffort: e.DefaultEffort, Aliases: append([]string{}, e.Aliases...),
		})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}
