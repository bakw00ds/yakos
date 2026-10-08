package consoleui

// export_models_test.go: test-only hooks for the Models & Providers tab (K-153).

import (
	"context"
	"time"

	"github.com/bakw00ds/yakos/internal/auth"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/router"
)

// ModelsHooks replaces what the tab reads from its surroundings. A nil field is
// left as it was.
type ModelsHooks struct {
	Probe   func(ctx context.Context, harness string) auth.ProbeResult
	Cooling func(project, runtime string) (bool, time.Duration)
	Now     func() time.Time
	LogPath func() string
}

// SetModelsHooksForTest applies h to the server's Models page.
func SetModelsHooksForTest(s *Server, h ModelsHooks) {
	if h.Probe != nil {
		s.models.probe = h.Probe
	}
	if h.Cooling != nil {
		s.models.cooling = h.Cooling
	}
	if h.Now != nil {
		s.models.now = h.Now
	}
	if h.LogPath != nil {
		s.models.logPath = h.LogPath
	}
}

// SetModelsExplainForTest replaces the explain call and returns the restore func.
func SetModelsExplainForTest(f func(context.Context, dispatch.ExplainQuery) (router.RouteDecision, error)) func() {
	old := explainDecision
	explainDecision = f
	return func() { explainDecision = old }
}

// ModelsEvalTailBytes and ModelsMaxEvals are the bounds on the eval reader.
const (
	ModelsEvalTailBytes = evalTailBytes
	ModelsMaxEvals      = maxEvals
)
