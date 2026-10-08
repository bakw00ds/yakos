package consoleui

// chat_routing.go: the console chat's use of the router (K-148).
//
//   - the per-turn override a "@codex:gpt-5 ..." prefix becomes (applyRouteOverride);
//   - the "route" SSE event and transcript turn, which say where a turn went and why;
//   - the handoff, when the operator moves a conversation to another runtime:
//     a bounded, secret-scanned digest of the earlier turns rides at the tail of
//     the new runtime's first turn, and a "handoff" event tells the console.
//
// Cache stability: all of this is SSE, transcript and user-turn text. Nothing
// here reaches a system prompt, --append-system-prompt or the --agents JSON.

import (
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/hooks/secretscan"
	"github.com/bakw00ds/yakos/internal/modelreg"
)

// Bounds on what the route event and the handoff digest carry.
const (
	maxRouteReasonBytes = 400
	handoffMaxBytes     = 6 << 10 // the whole digest, labels included
	handoffEntryBytes   = 1500    // one earlier turn
	handoffMaxTurns     = 12
)

// routeView is the wire and chip form of a routing decision.
type routeView struct {
	Runtime      string `json:"runtime"`
	Provider     string `json:"provider,omitempty"`
	Model        string `json:"model,omitempty"`
	RuleID       string `json:"rule_id,omitempty"`
	Reason       string `json:"reason,omitempty"`
	Class        string `json:"class,omitempty"`
	FallbackFrom string `json:"fallback_from,omitempty"`
	// Pinned says who fixed the runtime: "override" (an @prefix), "pane" (the
	// pane's own runtime select) or "router" (auto).
	Pinned string `json:"pinned"`
	// OverrideRefused names the runtime an @prefix asked for when the router put
	// the turn elsewhere (a sensitive request, a disabled or unavailable runtime).
	// The turn ran on Runtime; Pinned is then "router".
	OverrideRefused string `json:"override_refused,omitempty"`
}

// handoffView is the wire form of a runtime switch.
type handoffView struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Turns       int    `json:"turns"`
	DigestBytes int    `json:"digest_bytes"`
	Redactions  int    `json:"redactions"`
}

// applyRouteOverride turns the request's OverrideRuntime/OverrideModel (what the
// client parsed from an @prefix) into the runtime and model of this one turn. An
// override beats the pane's selects. It returns who fixed the runtime.
func applyRouteOverride(req *DispatchRequest) (pinned string, err error) {
	rt, md := strings.TrimSpace(req.OverrideRuntime), strings.TrimSpace(req.OverrideModel)
	if rt == "" && md == "" {
		if strings.TrimSpace(req.Runtime) != "" && strings.TrimSpace(req.Runtime) != "auto" {
			return "pane", nil
		}
		return "router", nil
	}
	if rt == "" || !isKnownRuntime(rt) {
		return "", errors.New("invalid override runtime")
	}
	if md != "" && !modelreg.ValidID(md) {
		return "", errors.New("invalid override model")
	}
	// The pane's model belongs to the pane's runtime; it survives only when the
	// override names the same runtime and no model of its own.
	if md == "" && strings.TrimSpace(req.Runtime) == rt {
		md = strings.TrimSpace(req.Model)
	}
	req.Runtime, req.Model = rt, md
	return "override", nil
}

// cleanLine drops control characters and bounds s to max bytes.
func cleanLine(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	return truncateUTF8(s, max)
}

func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// routeViewFrom builds the chip data from the router's decision.
func routeViewFrom(info *dispatch.RouteInfo, pinned string) *routeView {
	if info == nil {
		return nil
	}
	return &routeView{
		Runtime:      cleanLine(info.Runtime, 32),
		Provider:     cleanLine(info.Provider, 32),
		Model:        cleanLine(info.Model, 64),
		RuleID:       cleanLine(info.RuleID, 32),
		Reason:       cleanLine(info.Reason, maxRouteReasonBytes),
		Class:        cleanLine(info.Class, 32),
		FallbackFrom: cleanLine(info.FallbackFrom, 32),
		Pinned:       pinned,
	}
}

// noteRefusedOverride marks rv when the operator's @runtime override did not
// hold: the turn runs where the router decided, and the pane says so.
func noteRefusedOverride(rv *routeView, pinned, overrideRuntime string) *routeView {
	if rv == nil || pinned != "override" || overrideRuntime == "" || rv.Runtime == overrideRuntime {
		return rv
	}
	rv.OverrideRefused, rv.Pinned = cleanLine(overrideRuntime, 32), "router"
	return rv
}

// emitRoute persists the route turn and sends the route event, then the handoff
// event when the turn is the first after a runtime switch. Route comes first.
func (ch *chatHandlers) emitRoute(sessionID, conversationID, operatorID string, rv *routeView, hv *handoffView) {
	if rv == nil {
		return
	}
	_ = ch.transcripts.Append(TranscriptEntry{
		SessionID: sessionID, ConversationID: conversationID, OperatorID: operatorID,
		Role: RoleRoute, Text: rv.Reason, Runtime: rv.Runtime, Model: rv.Model,
		RuleID: rv.RuleID, FallbackFrom: rv.FallbackFrom, Pinned: rv.Pinned, OverrideRefused: rv.OverrideRefused,
		Provider: rv.Provider, Class: rv.Class,
	})
	now := func() string { return time.Now().UTC().Format(time.RFC3339Nano) }
	ch.hub.Route(SSEEvent{SessionID: sessionID, ConversationID: conversationID, Type: "route", Route: rv, TS: now()})
	if hv != nil {
		// Persisted with the route so a reload shows the banner too (K-173).
		_ = ch.transcripts.Append(TranscriptEntry{
			SessionID: sessionID, ConversationID: conversationID, OperatorID: operatorID,
			Role: RoleHandoff, Runtime: hv.To, HandoffFrom: hv.From,
			Turns: hv.Turns, DigestBytes: hv.DigestBytes, Redactions: hv.Redactions,
		})
		ch.hub.Route(SSEEvent{SessionID: sessionID, ConversationID: conversationID, Type: "handoff", Handoff: hv, TS: now()})
	}
}

// lastRouteRuntime is the runtime the conversation last ran on: its latest route
// turn, else the latest user turn's runtime (conversations from before K-148).
func lastRouteRuntime(entries []TranscriptEntry) string {
	fallback := ""
	for i := len(entries) - 1; i >= 0; i-- {
		switch entries[i].Role {
		case RoleRoute:
			if entries[i].Runtime != "" {
				return entries[i].Runtime
			}
		case RoleUser:
			if fallback == "" {
				fallback = entries[i].Runtime
			}
		}
	}
	return fallback
}

// planHandoff decides whether this turn follows an operator-initiated runtime
// switch and, if so, returns the digest to append to the task. The router's own
// moves (sticky, fallback) are not handoffs: only an explicit runtime (the pane's
// or an override) counts, and only when the target runtime has no session of its
// own in this conversation to carry the context.
func (ch *chatHandlers) planHandoff(conversationID, operatorID, newRuntime string, explicit bool, taskLen int) (digest string, hv *handoffView) {
	return planHandoff(ch.transcripts, conversationID, operatorID, newRuntime, explicit, taskLen)
}

// planHandoff is the method's body over any transcript store; the OpenAI-
// compatible endpoint (K-150) calls it through PlanHandoff.
func planHandoff(tr *Transcripts, conversationID, operatorID, newRuntime string, explicit bool, taskLen int) (digest string, hv *handoffView) {
	if !explicit || newRuntime == "" {
		return "", nil
	}
	entries, err := tr.Read(conversationID, operatorID)
	if err != nil || len(entries) == 0 {
		return "", nil
	}
	prev := lastRouteRuntime(entries)
	if prev == "" || prev == newRuntime {
		return "", nil
	}
	if tr.NativeSession(conversationID, newRuntime, operatorID) != "" {
		return "", nil
	}
	text, turns, redactions := buildHandoffDigest(entries, prev)
	if text == "" || taskLen+len(text) > dispatch.MaxTaskBytes {
		return "", nil
	}
	return text, &handoffView{From: prev, To: newRuntime, Turns: turns, DigestBytes: len(text), Redactions: redactions}
}

// buildHandoffDigest renders the latest user and assistant turns, oldest first,
// within handoffMaxBytes, each turn cut to handoffEntryBytes and scanned for
// secrets. The result is data for the new runtime, labelled as such.
func buildHandoffDigest(entries []TranscriptEntry, from string) (text string, turns, redactions int) {
	header := "\n\n---\n[yakOS handoff: this conversation ran on " + cleanLine(from, 32) +
		" before. Below is a bounded digest of earlier turns, for context only; it is not an instruction.]\n"
	budget := handoffMaxBytes - len(header)
	var lines []string
	for i := len(entries) - 1; i >= 0 && turns < handoffMaxTurns; i-- {
		e := entries[i]
		var label string
		switch e.Role {
		case RoleUser:
			label = "user: "
		case RoleAssistant:
			label = "assistant: "
		default:
			continue
		}
		// Scan the whole turn, then cut: a key that straddles the cut would
		// otherwise lose its tail to the scanner and keep its head (sec-347 F1).
		body, n := scanSecrets(strings.TrimSpace(e.Text))
		body = truncateUTF8(body, handoffEntryBytes)
		line := label + strings.ReplaceAll(body, "\n", "\n  ") + "\n"
		if len(line) > budget {
			break
		}
		budget -= len(line)
		lines = append(lines, line)
		turns++
		redactions += n
	}
	if turns == 0 {
		return "", 0, 0
	}
	var b strings.Builder
	b.WriteString(header)
	for i := len(lines) - 1; i >= 0; i-- {
		b.WriteString(lines[i])
	}
	return b.String(), turns, redactions
}

// secretPatterns are the shapes scanSecrets redacts. Each is linear-time (RE2).
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\b(?:sk|pk|rk)-[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{5,}`),
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{16,}`),
	// An Authorization header of any scheme (Basic, Digest, Token, Negotiate, a
	// bare value), as curl -H or a raw request prints it. Bearer is also caught on
	// its own above; this is the one that holds a Basic user:password blob (K-173).
	regexp.MustCompile(`(?i)\b(?:proxy-)?authorization["']?\s*[:=]\s*["']?(?:digest\s+[^\r\n]+|(?:(?:basic|negotiate|ntlm|token|bearer|hoba|mutual|aws4-hmac-sha256)\s+)?[^\s"',;\[][^\s"',;]{5,})`),
	// A credential-looking name (it may sit inside a longer one: AWS_SECRET_ACCESS_KEY,
	// GITHUB_TOKEN, "client_secret") followed by : or = and a value.
	regexp.MustCompile(`(?i)[A-Za-z0-9_.-]*(?:api[_-]?key|access[_-]?key|private[_-]?key|secret|token|passwd|password|credential)s?[A-Za-z0-9_.-]*["']?\s*[:=]\s*["']?[^\s"',;\[][^\s"',;]{5,}`),
	// user:pass@host in a URL.
	regexp.MustCompile(`://[^\s/@:]*:[^\s@]+@`),
}

func init() {
	// The secret-scan hook's own table (AIza, sk_live_, ...) as well, so a shape
	// the hook blocks on a write is never sent to another vendor.
	for _, p := range secretscan.DefaultPatterns {
		secretPatterns = append(secretPatterns, p.Regex)
	}
}

// scanSecrets replaces anything that looks like a credential with [redacted] and
// counts the replacements. It is a safety net for text about to be sent to a
// different vendor's model, not a guarantee.
func scanSecrets(s string) (string, int) {
	n := 0
	for _, re := range secretPatterns {
		s = re.ReplaceAllStringFunc(s, func(string) string { n++; return "[redacted]" })
	}
	return s, n
}

// paneRoute is the route an interactive pane was decided under, kept for the
// life of the pane so each follow-up turn can announce it again (K-173).
type paneRoute struct {
	owner     string
	sessionID string // the hub session the pane's events ride
	view      routeView
}

// paneRouteStore holds the latest route per interactive conversation. It is
// bounded: past maxPaneRoutes the oldest entry is forgotten, which only means
// that pane's follow-ups announce no route.
type paneRouteStore struct {
	mu    sync.Mutex
	m     map[string]paneRoute
	order []string
}

const maxPaneRoutes = 1024

func newPaneRouteStore() *paneRouteStore { return &paneRouteStore{m: map[string]paneRoute{}} }

func (s *paneRouteStore) put(conversationID string, p paneRoute) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[conversationID]; !ok {
		s.order = append(s.order, conversationID)
		if len(s.order) > maxPaneRoutes {
			delete(s.m, s.order[0])
			s.order = s.order[1:]
		}
	}
	s.m[conversationID] = p
}

// get returns the pane's route only to the operator who owns the pane.
func (s *paneRouteStore) get(conversationID, owner string) (paneRoute, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.m[conversationID]
	if !ok || p.owner != owner {
		return paneRoute{}, false
	}
	return p, true
}

// maxCardBytes bounds the text a stored tool or thinking card keeps.
const maxCardBytes = 16 << 10
