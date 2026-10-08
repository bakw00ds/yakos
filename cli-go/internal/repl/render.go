package repl

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Display bounds: a tool card is a glance, not a log.
const (
	maxToolInput  = 240
	maxToolOutput = 600
	maxToolLines  = 8
)

// sanitize drops terminal control characters from text that came from the
// daemon or a model: ESC and the other C0 controls (newline and tab stay), the
// C1 controls, DEL, bidi overrides and the BOM. A model must not be able to
// move the cursor, retitle the window or reorder what the operator reads.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			return -1
		case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069, r == 0xfeff:
			return -1
		}
		return r
	}, s)
}

// clip bounds s to n runes.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

// Renderer prints chat events to a terminal. It keeps one bit of state, whether
// the cursor is at the start of a line, so streamed tokens and block lines never
// run together.
type Renderer struct {
	W     io.Writer
	Color bool

	atBOL    bool
	started  bool
	thinking bool
}

// NewRenderer returns a renderer for w.
func NewRenderer(w io.Writer, color bool) *Renderer {
	return &Renderer{W: w, Color: color, atBOL: true}
}

func (r *Renderer) paint(code, s string) string {
	if !r.Color || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (r *Renderer) write(s string) {
	if s == "" {
		return
	}
	_, _ = io.WriteString(r.W, s)
	r.atBOL = strings.HasSuffix(s, "\n")
}

// line prints s on its own line, closing any open token line first.
func (r *Renderer) line(s string) {
	r.closeLine()
	r.write(s + "\n")
}

func (r *Renderer) closeLine() {
	if !r.atBOL {
		r.write("\n")
	}
	r.thinking = false
}

// Reset clears per-turn state (call before a turn starts).
func (r *Renderer) Reset() { r.started, r.thinking = false, false }

// Event renders one chat event. It reports nothing for types it does not show.
func (r *Renderer) Event(ev Event) {
	switch ev.Type {
	case "route":
		if ev.Route != nil {
			r.line(r.Chip(*ev.Route))
		}
	case "handoff":
		if ev.Handoff != nil {
			r.line(r.paint("33", HandoffText(*ev.Handoff)))
		}
	case "token":
		if r.thinking {
			r.closeLine()
		}
		r.started = true
		r.write(sanitize(ev.Text))
	case "thinking":
		txt := ev.Thinking
		if ev.ThinkingRedact {
			txt = "[redacted thinking]"
		}
		if !r.thinking {
			r.closeLine()
			r.thinking = true
			r.write(r.paint("2", "thinking: "))
		}
		r.write(r.paint("2", sanitize(txt)))
	case "tool_use":
		r.line(r.paint("36", "[tool] "+sanitize(ev.ToolName)+"  "+clip(oneLine(sanitize(ev.ToolInput)), maxToolInput)))
	case "tool_result":
		tag := "[result]"
		if ev.IsError {
			tag = "[result: error]"
		}
		r.line(r.paint("36", tag+" "+sanitize(ev.ToolName)))
		for _, l := range headLines(sanitize(ev.ToolOutput), maxToolLines, maxToolOutput) {
			r.write("    " + l + "\n")
		}
	case "summary":
		r.line(r.paint("2", SummaryText(ev)))
	case "error":
		r.line(r.paint("31", "error: "+sanitize(ev.Text)))
	}
}

// Chip is the "why this model" line of a route.
func (r *Renderer) Chip(rt Route) string {
	where := sanitize(rt.Runtime)
	if where == "" {
		where = "?"
	}
	if rt.Model != "" {
		where += " / " + sanitize(rt.Model)
	}
	how := map[string]string{"override": "set by @prefix", "pane": "set by /harness", "router": "chosen by the router"}[rt.Pinned]
	if how == "" {
		how = "chosen by the router"
	}
	s := "[route] " + where + " - " + how
	if rt.FallbackFrom != "" {
		s += " (fell back from " + sanitize(rt.FallbackFrom) + ")"
	}
	if rt.Reason != "" {
		s += " - " + sanitize(rt.Reason)
	}
	if rt.OverrideRefused != "" {
		s += " Override @" + sanitize(rt.OverrideRefused) + " refused (sensitive request, or that runtime is disabled or unavailable)."
	}
	if rt.RuleID != "" {
		s += " [" + sanitize(rt.RuleID) + "]"
	}
	return r.paint("35", s)
}

// HandoffText is the banner for a runtime switch (same words as the console).
func HandoffText(h Handoff) string {
	plural := func(n int, w string) string {
		if n == 1 {
			return fmt.Sprintf("%d %s", n, w)
		}
		return fmt.Sprintf("%d %ss", n, w)
	}
	red := ""
	if h.Redactions > 0 {
		red = ", " + plural(h.Redactions, "secret-like value") + " redacted"
	}
	return "Context reset (cache): moved from " + sanitize(h.From) + " to " + sanitize(h.To) +
		". The new runtime starts without the earlier prompt cache; a digest of " + plural(h.Turns, "earlier turn") +
		" (" + fmt.Sprintf("%d bytes", h.DigestBytes) + red + ") was added to this message."
}

// SummaryText is the footer of a finished turn.
func SummaryText(ev Event) string {
	parts := []string{}
	if ev.ExitCode != nil {
		parts = append(parts, fmt.Sprintf("exit %d", *ev.ExitCode))
	}
	if ev.DurationS != nil {
		parts = append(parts, fmt.Sprintf("%.1fs", *ev.DurationS))
	}
	if ev.TotalCostUSD != nil {
		parts = append(parts, fmt.Sprintf("$%.4f", *ev.TotalCostUSD))
	}
	if ev.ModelResolved != "" {
		m := sanitize(ev.ModelResolved)
		if ev.RuntimeResolved != "" {
			m += " (" + sanitize(ev.RuntimeResolved) + ")"
		}
		parts = append(parts, m)
	}
	return "-- " + strings.Join(parts, " | ")
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// headLines returns at most n lines and maxBytes bytes of s, with a marker when
// something was cut.
func headLines(s string, n, maxBytes int) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	cut := false
	if len(s) > maxBytes {
		s, cut = clip(s[:maxBytes], maxBytes), true
	}
	ls := strings.Split(s, "\n")
	if len(ls) > n {
		ls, cut = ls[:n], true
	}
	if cut {
		ls = append(ls, "...")
	}
	return ls
}

// Question is one entry of an AskUserQuestion payload.
type Question struct {
	Question    string `json:"question"`
	Header      string `json:"header"`
	MultiSelect bool   `json:"multiSelect"`
	Options     []struct {
		Label       string `json:"label"`
		Description string `json:"description"`
	} `json:"options"`
}

// ParseQuestions decodes the ask_questions_json of an ask_user_question event.
func ParseQuestions(raw string) ([]Question, error) {
	var qs []Question
	if err := json.Unmarshal([]byte(raw), &qs); err != nil {
		return nil, err
	}
	return qs, nil
}

// RenderQuestion prints one question with numbered options.
func (r *Renderer) RenderQuestion(q Question) {
	h := ""
	if q.Header != "" {
		h = "[" + sanitize(q.Header) + "] "
	}
	r.line(r.paint("1", h+sanitize(q.Question)))
	for i, o := range q.Options {
		d := ""
		if o.Description != "" {
			d = " - " + sanitize(o.Description)
		}
		r.write(fmt.Sprintf("  %d) %s%s\n", i+1, sanitize(o.Label), d))
	}
}
