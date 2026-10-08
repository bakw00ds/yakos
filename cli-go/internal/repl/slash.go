package repl

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

const helpText = `commands
  /harness [claude|codex|agy|auto]   show or pin the harness (auto: the router decides)
  /model [id|default]                list models of the harness, or pin one
  /auto                              let the router choose harness and model
  /skill <slug> [text]               run a skill (also /<slug> [text])
  /compact                           compact the conversation (claude)
  /resume [conversationId]           show this conversation id, or continue another
  /new                               start a new conversation
  /cost                              cost of this session
  /attach <claude|codex|agy>         open the native TUI, mirrored in the console Terminal pane
  /detach                            leave an attached native TUI
  /help, /exit
a message starting with @claude, @codex or @agy (optionally @codex:model) routes that one message there`

// slash runs a /command; it reports whether the REPL should exit.
func (r *REPL) slash(ctx context.Context, line string) (quit bool) {
	fields := strings.Fields(line)
	name := strings.TrimPrefix(fields[0], "/")
	rest := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
	switch name {
	case "exit", "quit":
		return true
	case "help", "?":
		r.say("%s", helpText)
		if n := len(r.skills); n > 0 {
			r.say("%d skills available (/skill lists them)", n)
		}
	case "harness":
		r.cmdHarness(rest)
	case "model":
		r.cmdModel(rest)
	case "auto":
		r.harness, r.model = "", ""
		r.say("%s", r.modeText())
	case "skill":
		r.cmdSkill(ctx, rest)
	case "compact":
		r.cmdCompact(ctx)
	case "resume":
		r.cmdResume(ctx, rest)
	case "new":
		r.conv = r.cfg.NewID("conv-")
		r.say("new conversation %s", r.conv)
	case "cost":
		r.say("session: %d turn(s), $%.4f total, $%.4f last turn (conversation %s)", r.turns, r.costUSD, r.lastUSD, r.conv)
	case "attach":
		r.cmdAttach(ctx, rest)
	case "detach":
		r.cmdDetach()
	default:
		if _, ok := r.skills[name]; ok {
			r.turn(ctx, line) // the /<slug> palette
		} else {
			r.say("unknown command /%s (try /help)", sanitize(name))
		}
	}
	return false
}

func (r *REPL) listSkills() {
	if len(r.skills) == 0 {
		return
	}
	names := make([]string, 0, len(r.skills))
	for n := range r.skills {
		names = append(names, n)
	}
	sort.Strings(names)
	r.say("skills: /%s", strings.Join(names, " /"))
}

func (r *REPL) cmdHarness(arg string) {
	if arg == "" {
		r.say("%s", r.modeText())
		return
	}
	next := arg
	if arg == "auto" {
		next = ""
	} else if !contains(harnesses, arg) {
		r.say("unknown harness %q (claude, codex, agy or auto)", sanitize(arg))
		return
	}
	if next != r.harness {
		r.model = "" // a model id means something only to its own harness
	}
	r.harness = next
	r.say("%s", r.modeText())
}

func (r *REPL) cmdModel(arg string) {
	if arg == "" {
		r.listModels()
		return
	}
	if arg == "default" {
		r.model = ""
		r.say("%s", r.modeText())
		return
	}
	if !modelIDRe.MatchString(arg) {
		r.say("invalid model id")
		return
	}
	if r.harness == "" {
		r.say("pin a harness first (/harness claude|codex|agy); a model id belongs to one harness")
		return
	}
	for _, m := range r.models.Models {
		if m.ID == arg && m.Harness != r.harness {
			r.say("%s belongs to %s, not %s", arg, m.Harness, r.harness)
			return
		}
	}
	r.model = arg
	r.say("%s", r.modeText())
}

func (r *REPL) listModels() {
	if len(r.models.Models) == 0 {
		r.say("model registry unavailable; /model <id> pins an id you know")
		return
	}
	var ids []string
	for _, m := range r.models.Models {
		if m.Usable && (r.harness == "" || m.Harness == r.harness) {
			ids = append(ids, m.Harness+"/"+m.ID)
		}
	}
	sort.Strings(ids)
	r.say("models (%s): %s", r.modeText(), strings.Join(ids, " "))
}

func (r *REPL) cmdSkill(ctx context.Context, arg string) {
	if arg == "" {
		if len(r.skills) == 0 {
			r.say("no skills available")
		}
		r.listSkills()
		return
	}
	f := strings.Fields(arg)
	slug := strings.TrimPrefix(f[0], "/")
	if !skillSlug.MatchString(slug) {
		r.say("invalid skill name")
		return
	}
	if _, ok := r.skills[slug]; !ok && len(r.skills) > 0 {
		r.say("unknown skill %q (/skill lists them)", slug)
		return
	}
	text := "/" + slug
	if tail := strings.TrimSpace(strings.TrimPrefix(arg, f[0])); tail != "" {
		text += " " + tail
	}
	r.turn(ctx, text)
}

// cmdCompact forwards /compact to the harness: claude compacts its own context.
// Other harnesses have no such command, so it is refused rather than sent as
// ordinary text.
func (r *REPL) cmdCompact(ctx context.Context) {
	if r.harness != "" && r.harness != "claude" {
		r.say("/compact is a claude command; %s has none (use /new for a fresh conversation)", r.harness)
		return
	}
	r.turn(ctx, "/compact")
}

func (r *REPL) cmdResume(ctx context.Context, arg string) {
	if arg == "" {
		r.say("conversation %s (/resume <id> continues another; the console Chat pane shows the same one)", r.conv)
		return
	}
	if !convIDRe.MatchString(arg) {
		r.say("invalid conversation id")
		return
	}
	entries, err := r.cl.Transcript(ctx, arg)
	if err != nil {
		r.say("error: %s", sanitize(err.Error()))
		return
	}
	if len(entries) == 0 {
		r.say("no transcript for that conversation")
		return
	}
	r.conv = arg
	r.say("resumed %s (%d stored entries); last turns:", r.conv, len(entries))
	r.showTail(entries, 6)
}

// showTail prints the last n blocks of a transcript; assistant chunks are joined.
func (r *REPL) showTail(entries []TranscriptEntry, n int) {
	type block struct{ role, text string }
	var bl []block
	for _, e := range entries {
		switch e.Role {
		case "assistant":
			if len(bl) > 0 && bl[len(bl)-1].role == "assistant" {
				bl[len(bl)-1].text += e.Text
			} else {
				bl = append(bl, block{"assistant", e.Text})
			}
		case "user", "error":
			bl = append(bl, block{e.Role, e.Text})
		case "route":
			bl = append(bl, block{"route", fmt.Sprintf("%s / %s - %s", e.Runtime, e.Model, e.Text)})
		}
	}
	if len(bl) > n {
		bl = bl[len(bl)-n:]
	}
	for _, b := range bl {
		switch b.role {
		case "user":
			r.say("> %s", clip(sanitize(b.text), 400))
		case "route":
			r.say("[route] %s", clip(sanitize(b.text), 200))
		default:
			r.say("%s", clip(sanitize(b.text), 800))
		}
	}
}

func (r *REPL) cmdAttach(ctx context.Context, arg string) {
	if r.cfg.Attach == nil {
		r.say("/attach is not available in this build")
		return
	}
	if !contains(harnesses, arg) {
		r.say("usage: /attach <claude|codex|agy>")
		return
	}
	r.say("attaching the native %s TUI (mirrored in the console Terminal pane); leave it the usual way to return here", arg)
	r.attachd = true
	err := r.cfg.Attach(ctx, arg)
	r.attachd = false
	select { // a Ctrl-C meant for the native TUI must not cancel the next turn
	case <-r.cfg.Interrupt:
	default:
	}
	if err != nil {
		r.say("attach ended: %s", sanitize(err.Error()))
		return
	}
	r.say("back in the yakOS REPL")
}

func (r *REPL) cmdDetach() {
	if !r.attachd {
		r.say("not attached; /attach <runtime> opens a native TUI, and leaving it returns here")
		return
	}
	r.say("detach by leaving the native TUI")
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
