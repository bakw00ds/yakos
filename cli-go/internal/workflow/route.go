package workflow

// route.go — K-142: Flows nodes may leave runtime and model to the router
// ("auto"). The engine does no resolution of its own: it hands the dispatch
// Service an empty runtime/model for an auto node, the Service routes at its
// chokepoint, and the decision it made comes back on dispatch.Result to be
// recorded in run.json. A concrete runtime or model on a node is an explicit
// pin for that node only (dispatch treats it as an override that policy rules
// never beat).
//
// Stickiness: a node is dispatched exactly once per run and its recorded
// decision is never recomputed, so the router cannot switch a node mid-run.

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bakw00ds/yakos/internal/dispatch"
)

var (
	routeModelRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]{0,63}$`)
	routeIdentRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)
)

const maxRouteReasonRunes = 256

// pinOf maps a node's runtime or model value to what dispatch receives: "auto"
// means no pin.
func pinOf(v string) string {
	if v == Auto {
		return ""
	}
	return v
}

func routeIdent(s string) string {
	if routeIdentRe.MatchString(s) {
		return s
	}
	return ""
}

// routeText keeps printable text only and caps its length, so a reason (router
// output, which can quote policy text) cannot carry control characters, a
// path-like secret blob or an unbounded string into run.json.
func routeText(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return ' '
	}, s)
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max])
	}
	return s
}

// newNodeRoute builds the run.json record from the node's YAML and the result
// of its dispatch. It returns nil when the dispatch never reached routing.
func newNodeRoute(n Node, res dispatch.Result) *NodeRoute {
	model := res.ModelResolved
	if model == "" {
		model = res.ModelID
	}
	if !routeModelRe.MatchString(model) {
		model = ""
	}
	r := &NodeRoute{
		Runtime:          routeIdent(res.Runtime),
		Model:            model,
		Rule:             routeIdent(res.RouteRule),
		Reason:           routeText(res.RouteReason, maxRouteReasonRunes),
		Class:            routeIdent(res.RouteClass),
		PolicySHA:        routeIdent(res.PolicySHA),
		RuntimeRequested: routeIdent(n.Runtime),
		ModelRequested:   routeIdent(n.Model),
	}
	if r.Runtime == "" && r.Model == "" && r.Rule == "" && r.Reason == "" {
		return nil
	}
	return r
}

// PlannedRoute is one node's routing as `workflow run --dry-run` reports it.
type PlannedRoute struct {
	Node    string
	Agent   string
	Runtime string
	Model   string
	Rule    string
	Reason  string
	Pinned  string // "runtime", "model", "runtime+model" or ""
	Err     error
}

// explainFn is the routing seam PlanRoutes uses; tests replace it.
var explainFn = dispatch.Explain

// PlanRoutes asks the router, through dispatch.Explain (the same routing step
// the real dispatch runs; nothing is started, logged or pinned), where each
// node would run. The task size is the unsubstituted prompt's, so a rule keyed
// on task size may decide differently once upstream output is spliced in. Nodes
// come back in workflow order.
func PlanRoutes(ctx context.Context, wf *Workflow, yakosRoot, project string) []PlannedRoute {
	out := make([]PlannedRoute, 0, len(wf.Nodes))
	for _, n := range wf.Nodes {
		pr := PlannedRoute{Node: n.ID, Agent: n.Agent}
		var pins []string
		if pinOf(n.Runtime) != "" {
			pins = append(pins, "runtime")
		}
		if pinOf(n.Model) != "" {
			pins = append(pins, "model")
		}
		pr.Pinned = strings.Join(pins, "+")
		d, err := explainFn(ctx, dispatch.ExplainQuery{
			YakosRoot: yakosRoot, Project: project, Agent: n.Agent,
			Runtime: pinOf(n.Runtime), Model: pinOf(n.Model),
			TaskBytes: int64(len(n.Prompt)),
		})
		if err != nil {
			pr.Err = fmt.Errorf("node %q: %w", n.ID, err)
		} else {
			pr.Runtime, pr.Model = d.Runtime, d.ModelID
			pr.Rule, pr.Reason = d.RuleID, routeText(d.Reason, maxRouteReasonRunes)
		}
		out = append(out, pr)
	}
	return out
}
