// Package router is the model router's policy core: the rules read from the
// user-level router policy file, the per-runtime cooldown, the per-conversation
// stickiness and the RouteDecision record.
//
// It decides nothing on its own about availability. The default rule, R0, is the
// P0a resolve chain in internal/dispatch (override > agent frontmatter >
// per-domain > default-runtime > env > state default > claude, then the
// fallbacks, filtered by the sign-in probe); dispatch.Service calls into this
// package at the one chokepoint Run and RunStream share (dispatch.routeDispatch)
// to learn which policy rule applies, which runtimes are cooling down and what a
// conversation is already pinned to. With no rules in the policy file the router
// changes nothing: every decision is R0 and equals the P0a chain byte for byte
// (TestRoute_NoPolicyMatchesFrozenP0aGolden in internal/dispatch, against a table frozen at the pre-router base).
//
// Rule ids: R0 is the default chain, R1..R6 are the policy file's rules in file
// order (the first that matches wins).
//
// A rule can only choose among runtimes and models this build knows and the
// operator's own policy file names; the project's .yakos.yml can only switch
// things off (projectcfg Config.DisableRuntimes/DisableModels). Route metadata
// goes to the ledger and to explain output, never into a system prompt
// (rule:cache-stability).
package router

import (
	"sync"
)

// ClassDefault is the route class of every dispatch in v1.
const ClassDefault = "default"

// RouteDecision is the router's record of one routing outcome. dispatch fills it
// and copies RuleID, Reason, FallbackFrom, RouteClass and PolicySHA onto the
// Request that dispatch.Account writes to the ledger; nothing else writes them.
type RouteDecision struct {
	Runtime      string
	Provider     string
	ModelID      string // "" = the harness default
	Chain        []string
	RuleID       string
	Reason       string
	FallbackFrom string
	RouteClass   string
	PolicySHA    string
}

// Classifier assigns a route class to a dispatch: "default", or "sensitive"
// (sensitive.go) when the request holds a secret-shaped string or a never-path.
type Classifier func(Input) string

// DefaultClassifier classifies everything "default".
func DefaultClassifier(Input) string { return ClassDefault }

// ActiveClassifier is the classifier Classify uses. It is a seam, not a setting:
// only code in this module can replace it.
var ActiveClassifier Classifier = SensitiveClassifier

// Classify returns the route class for in. A class the caller already set (the
// `--class` flag of `yakos router explain`) is kept when it is a valid
// identifier, except that it never hides a sensitive request: when there is
// material to scan and it is sensitive, the class is sensitive whatever was set.
func Classify(in Input) string {
	if in.Class != "" && identRe.MatchString(in.Class) {
		if in.Class != ClassSensitive && len(in.Material) > 0 && SensitiveReason(in) != "" {
			return ClassSensitive
		}
		return in.Class
	}
	if c := ActiveClassifier(in); identRe.MatchString(c) {
		return c
	}
	return ClassDefault
}

// ClassifyReason is Classify plus why a sensitive class was assigned (one of the
// Reason constants), "" for any other class. It scans a second time to find the
// reason, and only for a sensitive request.
func ClassifyReason(in Input) (class, reason string) {
	class = Classify(in)
	if class != ClassSensitive {
		return class, ""
	}
	if reason = SensitiveReason(in); reason == "" {
		reason = ReasonDeclared
	}
	return class, reason
}

// Pin is what a conversation was routed to on its first turn, under which
// project and policy file. A pin made under another project root or another
// policy_sha is not this request's pin (a conversation id is client-supplied
// and not unique across projects, and a policy edit drops the old routing).
type Pin struct {
	RuleID    string // the rule the first turn was routed under, for the ledger
	Runtime   string
	Model     string
	Project   string // project root the pin was made under
	PolicySHA string // sha of the policy file the pin was made under
}

// maxSticky bounds the table; past it the oldest half is forgotten, which only
// means those conversations are routed afresh.
const maxSticky = 4096

// Sticky remembers, per conversation and agent, the runtime and model the first
// turn was routed to, so later turns never move on their own. A switch is the
// operator's act (an explicit runtime or model on the request, which re-pins),
// never the router's. In memory only.
type Sticky struct {
	mu    sync.Mutex
	pins  map[string]Pin
	order []string
}

// NewSticky returns an empty table.
func NewSticky() *Sticky { return &Sticky{pins: map[string]Pin{}} }

func stickyKey(conversation, agent string) string { return conversation + "\x00" + agent }

// Get returns the pin for the conversation and agent made under this project
// and policy sha. An empty conversation id has none, and a pin made under a
// different project root or policy sha is ignored.
func (s *Sticky) Get(conversation, agent, project, policySHA string) (Pin, bool) {
	if conversation == "" {
		return Pin{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pins[stickyKey(conversation, agent)]
	if !ok || p.Project != project || p.PolicySHA != policySHA {
		return Pin{}, false
	}
	return p, true
}

// Put records the routing of a conversation. An existing pin made under the
// same project and policy sha stands (the first decision wins) unless replace
// is set, which is the operator's explicit runtime or model on the request. A
// pin made under another project or policy sha is always replaced.
func (s *Sticky) Put(conversation, agent string, p Pin, replace bool) {
	if conversation == "" {
		return
	}
	k := stickyKey(conversation, agent)
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.pins[k]; ok {
		if !replace && old.Project == p.Project && old.PolicySHA == p.PolicySHA {
			return
		}
		s.pins[k] = p
		return
	}
	if len(s.pins) >= maxSticky {
		half := len(s.order) / 2
		for _, old := range s.order[:half] {
			delete(s.pins, old)
		}
		s.order = append([]string(nil), s.order[half:]...)
	}
	s.pins[k] = p
	s.order = append(s.order, k)
}
