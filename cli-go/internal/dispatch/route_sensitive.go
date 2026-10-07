package dispatch

// route_sensitive.go is the dispatch side of the sensitive route class (K-140,
// internal/router/sensitive.go): the class decision from the material a request
// carries, the restriction of the candidate chain, and the refusal when even the
// primary runtime cannot take the request.
//
// A sensitive request may go to the primary runtime (claude) or to a runtime
// whose every registry entry is billing=local. Nothing else is a candidate, not
// an explicit --runtime, an agent pin, a policy rule or a fallback list. If the
// chain empties it falls closed to claude, and if claude is unavailable the
// dispatch is refused (RouteRefusedError) and a route_refused event is written.
// This is a routing class, not an egress guarantee (docs/routing.md).

import (
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/bakw00ds/yakos/internal/agentscompose"
	"github.com/bakw00ds/yakos/internal/modelreg"
	"github.com/bakw00ds/yakos/internal/router"
)

// primaryRuntime is the runtime a sensitive request falls closed to.
const primaryRuntime = "claude"

// RouteRefusedError is a sensitive dispatch that no permitted runtime can take.
// It carries only the class and a fixed-vocabulary reason, never request text.
type RouteRefusedError struct {
	Class  string
	Reason string
	Cause  error
}

func (e *RouteRefusedError) Error() string {
	return fmt.Sprintf("dispatch: route refused: the request is %s (%s) and may only run on %s or a local runtime: %v",
		e.Class, e.Reason, primaryRuntime, e.Cause)
}

func (e *RouteRefusedError) Unwrap() error { return e.Cause }

// AsRouteRefused reports whether err is (or wraps) a *RouteRefusedError.
func AsRouteRefused(err error) (*RouteRefusedError, bool) {
	var e *RouteRefusedError
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// localRuntimes returns the runtimes whose every embedded-catalog model is
// billing=local. Computed once. The embedded catalog only: a user overlay cannot
// widen the set a secret may go to. An unreadable catalog yields none.
var localRuntimes = sync.OnceValue(func() map[string]bool {
	out := map[string]bool{}
	cat, err := modelreg.EmbeddedCatalog()
	if err != nil {
		return out
	}
	allLocal := map[string]bool{}
	for _, m := range cat.Models {
		for _, h := range m.Harnesses {
			v, seen := allLocal[h]
			allLocal[h] = (!seen || v) && m.Billing == modelreg.BillingLocal
		}
	}
	for h, ok := range allLocal {
		if ok {
			out[h] = true
		}
	}
	return out
})

// sensitiveEligible reports whether a sensitive request may run on rt.
func sensitiveEligible(rt string) bool { return rt == primaryRuntime || localRuntimes()[rt] }

// restrictSensitive narrows a chain to the runtimes a sensitive request may use;
// an empty result becomes the primary alone.
func restrictSensitive(chain []candidate) []candidate {
	out := chain[:0:0]
	for _, c := range chain {
		if sensitiveEligible(c.name) {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		out = []candidate{{primaryRuntime, RuntimeByFallback}}
	}
	return out
}

// classifyRequest returns the route class of a request, and why when sensitive.
// class is the caller's class ("" = classify). Material is the task, the agent's
// prompt and the extra texts the caller names (upstream flow outputs, a knowledge
// block, transcript digests). Classification ends in the safe direction: the
// router's scan fails closed. warn gets one line per sensitive request.
func classifyRequest(class string, ci chainInput, agent *agentscompose.ComposedAgent, task string, extra []string, warn io.Writer) (string, string) {
	in := router.Input{Class: class, NeverPaths: ci.project.NeverPaths}
	if task != "" {
		in.Material = append(in.Material, task)
	}
	if agent != nil && agent.Prompt != "" {
		in.Material = append(in.Material, agent.Prompt)
	}
	for _, x := range extra {
		if x != "" {
			in.Material = append(in.Material, x)
		}
	}
	c, why := router.ClassifyReason(in)
	if c == router.ClassSensitive && warn != nil {
		fmt.Fprintf(warn, "yakos dispatch: WARN: request classified sensitive (%s); restricted to %s or a local runtime\n", why, primaryRuntime)
	}
	return c, why
}

// ClassifyFlowOutput is what the workflow engine calls on an upstream node's
// output (or any text bound for a downstream node) before dispatching the node:
// it reports whether the text makes the downstream request sensitive. Put the
// text in Params.ScanExtra and dispatch does the rest; this function is for an
// engine that wants to know first (to record it, or to fail a node early).
func ClassifyFlowOutput(project string, output ...string) bool {
	ci := chainInputForClass(project)
	c, _ := classifyRequest("", ci, nil, "", output, nil)
	return c == router.ClassSensitive
}

// chainInputForClass loads only what classification needs from the project.
func chainInputForClass(project string) chainInput {
	ci := loadChainInput(nil, "", project, "", "", nil, false)
	return ci
}
