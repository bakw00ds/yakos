package repl

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Harnesses a REPL can pin; "auto" means the router decides.
var harnesses = []string{"claude", "codex", "agy"}

var (
	modelIDRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]{0,63}$`)
	convIDRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	skillSlug  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	prefixRe   = regexp.MustCompile(`^@(claude|codex|agy)(?::([a-z0-9][a-z0-9._:-]{0,63}))?[ \t]+(\S[\s\S]*)$`)
	maxLineLen = 1 << 20
)

// Config wires a REPL to its terminal and daemon.
type Config struct {
	Client *Client
	In     io.Reader
	Out    io.Writer
	Color  bool

	Agent          string // agent name sent with every turn; default "claude"
	Harness        string // "" = auto (the router decides)
	Model          string
	ConversationID string // default: a fresh one

	// Interrupt delivers Ctrl-C. During a turn it cancels the turn; at the
	// prompt it prints a hint.
	Interrupt <-chan struct{}
	// Attach runs the native TUI of a runtime mirrored in the console Terminal
	// pane and returns when it exits. Nil: /attach says it is unavailable.
	Attach func(ctx context.Context, runtime string) error

	NewID func(prefix string) string // test seam
	Sleep func(time.Duration)        // test seam (409 backoff)
	// BusyWaits is how many times a turn waits out a 409 (default 20, 500 ms).
	BusyWaits int
}

// REPL is one interactive session.
type REPL struct {
	cfg  Config
	rd   *Renderer
	out  io.Writer
	cl   *Client
	conv string
	sess string

	harness string
	model   string

	events  <-chan Event
	lines   chan string
	models  Models
	skills  map[string]string
	attachd bool

	turns   int
	costUSD float64
	lastUSD float64
}

// New builds a REPL; Run starts it.
func New(cfg Config) *REPL {
	if cfg.Agent == "" {
		cfg.Agent = "claude"
	}
	if cfg.NewID == nil {
		cfg.NewID = randomID
	}
	if cfg.Sleep == nil {
		cfg.Sleep = time.Sleep
	}
	if cfg.BusyWaits <= 0 {
		cfg.BusyWaits = 20
	}
	if cfg.Client != nil && cfg.Client.HTTP == nil {
		cfg.Client.HTTP = newHTTP()
	}
	r := &REPL{cfg: cfg, rd: NewRenderer(cfg.Out, cfg.Color), out: cfg.Out, cl: cfg.Client,
		harness: cfg.Harness, model: cfg.Model, conv: cfg.ConversationID, skills: map[string]string{}}
	if r.conv == "" {
		r.conv = cfg.NewID("conv-")
	}
	r.sess = cfg.NewID("sess-")
	return r
}

func randomID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic("repl: no randomness")
	}
	return prefix + hex.EncodeToString(b)
}

func (r *REPL) say(format string, a ...any) { r.rd.line(fmt.Sprintf(format, a...)) }

// Run reads lines until EOF, /exit or ctx ends.
func (r *REPL) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ev, err := r.cl.Stream(ctx)
	if err != nil {
		return err
	}
	r.events = ev

	// Best effort: the registry and the skill catalog make completion-style
	// checks possible; the REPL works without them.
	if m, err := r.cl.Models(ctx); err == nil {
		r.models = m
	}
	if s, err := r.cl.Skills(ctx); err == nil {
		for _, k := range s.Skills {
			if skillSlug.MatchString(k.Name) {
				r.skills[k.Name] = k.Description
			}
		}
	}

	r.lines = make(chan string, 1)
	go r.readLines(ctx)

	r.say("yakOS REPL - conversation %s - %s - /help for commands, /exit to leave", r.conv, r.modeText())
	for {
		r.prompt()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.cfg.Interrupt:
			r.say("(Ctrl-C at the prompt does nothing; /exit or Ctrl-D leaves)")
		case line, ok := <-r.lines:
			if !ok {
				r.say("")
				return nil
			}
			if quit := r.handle(ctx, strings.TrimSpace(line)); quit {
				return nil
			}
		}
	}
}

func (r *REPL) prompt() {
	h := r.harness
	if h == "" {
		h = "auto"
	}
	_, _ = fmt.Fprintf(r.out, "yakos [%s]> ", h)
	r.rd.atBOL = false
}

func (r *REPL) readLines(ctx context.Context) {
	defer close(r.lines)
	sc := bufio.NewScanner(r.cfg.In)
	sc.Buffer(make([]byte, 64<<10), maxLineLen)
	for sc.Scan() {
		select {
		case r.lines <- sc.Text():
		case <-ctx.Done():
			return
		}
	}
}

func (r *REPL) modeText() string {
	switch {
	case r.harness == "":
		return "router chooses"
	case r.model == "":
		return r.harness + " pinned"
	}
	return r.harness + " / " + r.model + " pinned"
}

// handle runs one input line; it reports whether the REPL should exit.
func (r *REPL) handle(ctx context.Context, line string) (quit bool) {
	r.rd.atBOL = true // the user's Enter moved the cursor
	if line == "" {
		return false
	}
	if strings.HasPrefix(line, "/") {
		return r.slash(ctx, line)
	}
	r.turn(ctx, line)
	return false
}

// parseOverride reads "@codex[:model] task".
func parseOverride(text string) (runtime, model, task string, ok bool) {
	m := prefixRe.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return "", "", text, false
	}
	return m[1], m[2], m[3], true
}

// turn sends one message and renders the stream until the turn ends.
func (r *REPL) turn(ctx context.Context, text string) {
	req := DispatchRequest{
		Runtime: r.harness, Model: r.model, Agent: r.cfg.Agent, Task: text,
		SessionID: r.sess, ConversationID: r.conv,
	}
	if rt, md, task, ok := parseOverride(text); ok {
		req.OverrideRuntime, req.OverrideModel, req.Task = rt, md, task
	}
	r.rd.Reset()
	if !r.dispatch(ctx, req) {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.cfg.Interrupt:
			r.cancelTurn(ctx)
			return
		case ev, ok := <-r.events:
			if !ok {
				if !r.reconnect(ctx) {
					return
				}
				continue
			}
			if ev.SessionID != r.sess {
				continue // another pane or client of this operator
			}
			if r.onEvent(ctx, ev) {
				return
			}
		}
	}
}

// dispatch posts the turn, waiting out a 409 (a cancelled turn still unwinding
// or another client on the session). It reports whether the turn started.
func (r *REPL) dispatch(ctx context.Context, req DispatchRequest) bool {
	for i := 0; ; i++ {
		err := r.cl.Dispatch(ctx, req)
		switch {
		case err == nil:
			return true
		case errors.Is(err, ErrTurnInFlight):
			if i == 0 {
				r.say("a turn is still running on this session; waiting for it to finish (Ctrl-C gives up)")
			}
			if i >= r.cfg.BusyWaits {
				r.say("error: the earlier turn did not finish; try again, or /new for a fresh conversation")
				return false
			}
			select {
			case <-r.cfg.Interrupt:
				r.say("gave up waiting")
				return false
			case <-ctx.Done():
				return false
			default:
			}
			r.cfg.Sleep(500 * time.Millisecond)
		default:
			r.say("error: %s", sanitize(err.Error()))
			return false
		}
	}
}

// onEvent renders ev and reports whether the turn is over.
func (r *REPL) onEvent(ctx context.Context, ev Event) bool {
	switch ev.Type {
	case "ask_user_question":
		r.ask(ctx, ev)
		return false
	case "summary":
		r.rd.Event(ev)
		r.turns++
		if ev.TotalCostUSD != nil {
			r.lastUSD = *ev.TotalCostUSD
			r.costUSD += *ev.TotalCostUSD
		}
		return true
	case "error":
		r.rd.Event(ev)
		return true
	}
	r.rd.Event(ev)
	return false
}

// cancelTurn stops the running turn and waits briefly for its closing event so
// the next turn does not see stale frames.
func (r *REPL) cancelTurn(ctx context.Context) {
	r.say("(cancelling)")
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := r.cl.Cancel(cctx, r.sess); err != nil {
		r.say("error: %s", sanitize(err.Error()))
		return
	}
	for {
		select {
		case <-cctx.Done():
			return
		case ev, ok := <-r.events:
			if !ok {
				return
			}
			if ev.SessionID == r.sess && (ev.Type == "summary" || ev.Type == "error") {
				return
			}
		}
	}
}

// reconnect reopens the stream once after it dropped mid-turn.
func (r *REPL) reconnect(ctx context.Context) bool {
	r.say("event stream closed; reconnecting")
	ev, err := r.cl.Stream(ctx)
	if err != nil {
		r.say("error: the event stream is gone (%s); the turn may still finish in the console", sanitize(err.Error()))
		return false
	}
	r.events = ev
	return true
}

// ask shows an AskUserQuestion and posts the operator's answers.
func (r *REPL) ask(ctx context.Context, ev Event) {
	qs, err := ParseQuestions(ev.AskQuestions)
	if err != nil || len(qs) == 0 || ev.AskToolUseID == "" {
		r.say("error: the agent asked a question the REPL cannot read; answer it in the console")
		return
	}
	answers := map[string]string{}
	for _, q := range qs {
		r.rd.RenderQuestion(q)
		_, _ = io.WriteString(r.out, "answer (number or text)> ")
		r.rd.atBOL = false
		select {
		case <-ctx.Done():
			return
		case <-r.cfg.Interrupt:
			r.say("(question left unanswered)")
			return
		case line, ok := <-r.lines:
			if !ok {
				return
			}
			line = strings.TrimSpace(line)
			r.rd.atBOL = true
			if n, err := strconv.Atoi(line); err == nil && n >= 1 && n <= len(q.Options) {
				line = q.Options[n-1].Label
			}
			answers[q.Question] = line
		}
	}
	if err := r.cl.Answer(ctx, AnswerRequest{ConversationID: r.conv, ToolUseID: ev.AskToolUseID, Answers: answers}); err != nil {
		r.say("error: could not deliver the answer: %s", sanitize(err.Error()))
	}
}
