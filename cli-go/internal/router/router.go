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
// (TestRoute_NoPolicyEqualsP0a in internal/dispatch).
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

// Classifier assigns a route class to a dispatch. K-140 adds the sensitive class
// by replacing it; v1 has one class.
type Classifier func(Input) string

// DefaultClassifier is the v1 classifier: everything is "default".
func DefaultClassifier(Input) string { return ClassDefault }

// ActiveClassifier is the classifier Classify uses. It is a seam, not a setting:
// only code in this module can replace it.
var ActiveClassifier Classifier = DefaultClassifier

// Classify returns the route class for in. A class the caller already set (the
// `--class` flag of `yakos router explain`) is kept when it is a valid identifier.
func Classify(in Input) string {
	if in.Class != "" {
		if identRe.MatchString(in.Class) {
			return in.Class
		}
		return ClassDefault
	}
	if c := ActiveClassifier(in); identRe.MatchString(c) {
		return c
	}
	return ClassDefault
}

// Pin is what a conversation was routed to on its first turn.
type Pin struct {
	RuleID  string // the rule the first turn was routed under, for the ledger
	Runtime string
	Model   string
}

// maxSticky bounds the table; past it the oldest half is forgotten, which only
// means those conversations are routed afresh.
const maxSticky = 4096

// Sticky remembers, per conversation and agent, the runtime and model the first
// turn was routed to, so later turns never move on their own. A switch is the
// operator's act (an explicit runtime or model on the request), never the
// router's. In memory only.
type Sticky struct {
	mu    sync.Mutex
	pins  map[string]Pin
	order []string
}

// NewSticky returns an empty table.
func NewSticky() *Sticky { return &Sticky{pins: map[string]Pin{}} }

func stickyKey(conversation, agent string) string { return conversation + "\x00" + agent }

// Get returns the pin for the conversation and agent. An empty conversation id
// has none.
func (s *Sticky) Get(conversation, agent string) (Pin, bool) {
	if conversation == "" {
		return Pin{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pins[stickyKey(conversation, agent)]
	return p, ok
}

// Put records the first routing of a conversation. A later Put for the same
// conversation and agent is ignored: the first decision stands.
func (s *Sticky) Put(conversation, agent string, p Pin) {
	if conversation == "" {
		return
	}
	k := stickyKey(conversation, agent)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.pins[k]; ok {
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
