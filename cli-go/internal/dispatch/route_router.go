package dispatch

// route_router.go joins dispatch to internal/router at the one chokepoint Run
// and RunStream share (routeDispatch). It holds nothing the router package does
// not define; it applies a selected policy rule to the P0a chain, keeps the
// process-wide cooldown and sticky tables, and builds the RouteDecision that
// Account later writes to the ledger.
//
// With no rule in ~/.yakos-state/router-policy.yml none of this changes a
// decision: policy and sticky stay nil, the cooldown can only reorder when a
// runtime has actually failed three times in a row, and the decision is R0.

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/bakw00ds/yakos/internal/agentscompose"
	"github.com/bakw00ds/yakos/internal/router"
	"github.com/bakw00ds/yakos/internal/routerpolicy"
)

// routerPolicyDir is where the policy file is read from. Tests replace it.
var routerPolicyDir = routerpolicy.StateDir

// routerCooldown and routerSticky are the process-wide, in-memory router state.
// Tests replace them.
var (
	routerCooldown = router.NewCooldown(nil)
	routerSticky   = router.NewSticky()
)

// policyAction is a selected policy rule as the chain sees it.
type policyAction struct {
	rule         router.Rule
	runtime      string
	model        string
	fallbacks    []string
	overridePins bool
}

// overridesPins reports whether the rule's runtime outranks the agent's
// frontmatter runtime: pin.
func (p *policyAction) overridesPins() bool {
	return p != nil && p.runtime != "" && p.overridePins
}

// replacesFallbacks reports whether the rule's fallbacks stand in for the
// agent's runtime-fallback and the project's default-fallback. They do when the
// rule chose the first runtime, when it overrides pins, or when the agent has no
// fallback list of its own to outrank.
func (p *policyAction) replacesFallbacks(first candidate, agent *agentscompose.ComposedAgent) bool {
	if p == nil || len(p.fallbacks) == 0 {
		return false
	}
	return first.by == RuntimeByPolicy || p.overridePins || agent == nil || len(agent.RuntimeFallback) == 0
}

// routerState is what the router decided for one routing step, besides the
// runtime the chain picked.
type routerState struct {
	policy router.Policy
	class  string
	rule   *policyAction // the matching rule, nil when none
	pin    *router.Pin   // the conversation's earlier routing, nil when none
}

// applyRouter loads the policy, classifies the dispatch, selects a rule and
// wires the router's inputs into ci. It is also what PreferredRuntime and
// ResolveRuntime use, so the runtime they report is the one Run will pick.
func applyRouter(ci *chainInput, agent *agentscompose.ComposedAgent, agentName string, class string, taskBytes int64, project, conversation string, warn io.Writer) routerState {
	st := routerState{policy: router.LoadPolicy(routerPolicyDir())}
	if warn != nil {
		for _, w := range st.policy.Warnings {
			fmt.Fprintf(warn, "yakos dispatch: %s\n", w)
		}
	}
	domain := ""
	if agent != nil {
		domain = agent.Domain
	}
	input := router.Input{Class: class, Agent: agentName, Domain: domain, TaskBytes: taskBytes}
	st.class = router.Classify(input)
	input.Class = st.class
	if st.class == router.ClassSensitive {
		ci.sensitive = true
		if ci.sensitiveWhy == "" {
			ci.sensitiveWhy = router.ReasonDeclared
		}
	}
	if st.policy.Active() {
		if r, ok := st.policy.Select(input); ok {
			st.rule = &policyAction{rule: r, runtime: r.Action.Runtime, model: r.Action.Model, fallbacks: r.Action.Fallbacks, overridePins: r.OverridePins}
			ci.policy = st.rule
		}
	}
	if !st.policy.FilePresent() {
		// No trusted policy file: the router changes nothing. No pins, no
		// cooldown; the P0a chain runs as it always did, failures included.
		return st
	}
	// Sticky: a conversation keeps the runtime and model its first turn was
	// routed to, as long as the project root and the policy file are the ones
	// it was routed under. An explicit runtime on the request is the operator
	// moving it: the chain ranks an override above the pin, and the move
	// re-pins (remember).
	if p, ok := routerSticky.Get(conversation, agentName, project, st.policy.FileSHA); ok {
		st.pin = &p
		ci.sticky = p.Runtime
	}
	// The cooldown only skips runtimes the chain may choose among. A pinned
	// conversation's runtime is an explicit candidate (RuntimeBySticky), which
	// the walk never skips, so the cooldown cannot move it.
	ci.cooling = routerCooldown.Cooling
	return st
}

// noteRun feeds a finished run to the cooldown: a run that failed to execute or
// exited non-zero counts against its runtime, a clean one clears the count. A run
// cut short by its context says nothing about the runtime.
func noteRun(ctx context.Context, rt string, exitCode int, err error) {
	if ctx.Err() != nil {
		return
	}
	if err != nil || exitCode != 0 {
		routerCooldown.Failure(rt)
		return
	}
	routerCooldown.Success(rt)
}

// applyModel lets the router adjust the model resolveModel chose for rt:
// the conversation's earlier model, else the matching rule's model. It reports
// whether the policy rule supplied the model. An explicit --model always stands.
// A rule's model passes the same per-runtime check as any model, and is dropped
// with a notice when rt cannot take it; a Claude tier never reaches codex or agy
// and a foreign id never reaches Claude Code.
func (st routerState) applyModel(rt, modelOverride string, mc modelChoice) (modelChoice, bool) {
	if modelOverride != "" {
		return mc, false
	}
	if st.pin != nil && st.pin.Runtime == rt && st.pin.Model != "" {
		mc.model, mc.chosenBy, mc.explicit = st.pin.Model, "sticky", true
		return mc, false
	}
	p := st.rule
	if p == nil || p.model == "" || (p.runtime != "" && p.runtime != rt) {
		return mc, false
	}
	pinned := mc.explicit && mc.chosenBy == "frontmatter"
	if pinned && !p.overridePins {
		return mc, false
	}
	c := checkModel(rt, p.model)
	if !c.ok || c.resolved == "" {
		fmt.Fprintf(routeLog, "yakos dispatch: router rule %s model %q does not fit runtime %s; ignoring it\n", p.rule.ID, p.model, rt)
		return mc, false
	}
	mc.model, mc.chosenBy, mc.explicit = c.resolved, "policy", true
	return mc, true
}

// decision builds the RouteDecision for a finished routing step.
func (st routerState) decision(ci chainInput, choice RuntimeChoice, mc modelChoice, modelFromPolicy bool) router.RouteDecision {
	d := st.decide(ci, choice, mc, modelFromPolicy)
	if ci.sensitive {
		d.Reason += fmt.Sprintf("; sensitive -> primary only (%s): %s or a local runtime", ci.sensitiveWhy, primaryRuntime)
	}
	return d
}

func (st routerState) decide(ci chainInput, choice RuntimeChoice, mc modelChoice, modelFromPolicy bool) router.RouteDecision {
	chain, _ := buildChain(ci)
	names := make([]string, len(chain))
	for i, c := range chain {
		names[i] = c.name
	}
	d := router.RouteDecision{
		Runtime:      choice.Runtime,
		Provider:     providerForRuntime(choice.Runtime),
		ModelID:      mc.model,
		Chain:        names,
		RuleID:       router.RuleDefault,
		FallbackFrom: choice.FallbackFrom,
		RouteClass:   st.class,
		PolicySHA:    st.policy.SHA,
	}
	switch {
	case st.pin != nil && choice.ChosenBy == RuntimeBySticky:
		d.RuleID = st.pin.RuleID
		d.Reason = "sticky: the conversation was already routed to " + choice.Runtime
		return d
	case st.rule != nil:
		first := chain[0]
		applied := first.by == RuntimeByPolicy || modelFromPolicy || ci.policy.replacesFallbacks(first, ci.agent) && len(chain) > 1
		if applied {
			d.RuleID = st.rule.rule.ID
			d.Reason = fmt.Sprintf("rule %s matched [%s]: %s", d.RuleID, describeMatch(st.rule.rule.Match), describeAction(st.rule, first.by == RuntimeByPolicy, modelFromPolicy))
			if choice.ChosenBy == RuntimeByFallback {
				d.Reason += fmt.Sprintf("; fell back from %s", choice.FallbackFrom)
			}
			return d
		}
		d.Reason = fmt.Sprintf("default chain: runtime %s by %s; rule %s matched but a pin outranks everything it sets", choice.Runtime, choice.ChosenBy, st.rule.rule.ID)
		return d
	}
	d.Reason = fmt.Sprintf("default chain: runtime %s by %s", choice.Runtime, choice.ChosenBy)
	if choice.ChosenBy == RuntimeByFallback {
		d.Reason += fmt.Sprintf("; fell back from %s", choice.FallbackFrom)
	}
	return d
}

func describeMatch(m router.Match) string {
	var parts []string
	if m.Class != "" {
		parts = append(parts, "class="+m.Class)
	}
	if m.Agent != "" {
		parts = append(parts, "agent="+m.Agent)
	}
	if m.Domain != "" {
		parts = append(parts, "domain="+m.Domain)
	}
	if m.TaskBytesGT > 0 {
		parts = append(parts, fmt.Sprintf("task_bytes_gt=%d", m.TaskBytesGT))
	}
	if len(m.Tags) > 0 {
		parts = append(parts, "tags="+strings.Join(m.Tags, "+"))
	}
	sort.Strings(parts)
	if len(parts) == 0 {
		return "any"
	}
	return strings.Join(parts, ",")
}

func describeAction(p *policyAction, runtimeApplied, modelApplied bool) string {
	var parts []string
	if runtimeApplied {
		parts = append(parts, "runtime="+p.runtime)
	}
	if modelApplied {
		parts = append(parts, "model="+p.model)
	}
	if len(p.fallbacks) > 0 {
		parts = append(parts, "fallbacks=["+strings.Join(p.fallbacks, " ")+"]")
	}
	return strings.Join(parts, " ")
}

// remember records the routing of a conversation, so later turns stay put. It
// does nothing without a trusted policy file (the router then changes nothing,
// and a conversation keeps resolving by the P0a chain every turn, as before).
// A rule-derived or default decision never overwrites an in-scope pin; replace
// is set for the operator's explicit runtime or model on the request, which
// moves the conversation and re-pins it.
func (st routerState) remember(conversation, agent, project string, d router.RouteDecision, replace bool) {
	if !st.policy.FilePresent() || conversation == "" {
		return
	}
	routerSticky.Put(conversation, agent, router.Pin{
		RuleID: d.RuleID, Runtime: d.Runtime, Model: d.ModelID,
		Project: project, PolicySHA: st.policy.FileSHA,
	}, replace)
}

// ExplainQuery asks what the router would decide for a dispatch, without making
// it: nothing is started, no ledger event is written, and the conversation is not
// pinned. It is the seam `yakos router explain` and `yakos dispatch --explain`
// use.
type ExplainQuery struct {
	YakosRoot, Project, Agent string
	Runtime                   string // explicit runtime, "" or "auto" for none
	Model                     string // explicit model
	Class                     string // route class, "" = classify
	TaskBytes                 int64
	ConversationID            string
	// Task and Extra are the request text the classifier scans (K-140), as Run
	// would: the task, then upstream outputs, a knowledge block, digests.
	Task  string
	Extra []string
}

// Explain runs the routing step Run and RunStream run, probes included, and
// returns its RouteDecision.
func Explain(ctx context.Context, q ExplainQuery) (router.RouteDecision, error) {
	rr, err := routeDispatchAt(ctx, routeInput{
		YakosRoot: q.YakosRoot, Project: q.Project, Agent: q.Agent,
		RuntimeOverride: q.Runtime, ModelOverride: q.Model,
		Class: q.Class, TaskBytes: q.TaskBytes, ConversationID: q.ConversationID,
		Task: q.Task, Extra: q.Extra,
	}, true)
	if err != nil {
		return router.RouteDecision{}, err
	}
	return rr.Decision, nil
}
