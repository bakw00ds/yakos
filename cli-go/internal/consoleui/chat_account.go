package consoleui

// chat_account.go gives every turn of an interactive chat session its entry in
// the dispatch ledger (K-136): exactly one dispatch_started / dispatch_finished
// pair per turn, written by dispatch.Account like every other dispatch, with
// surface "console-chat".
//
// Why this exists. One-shot chat turns go through Service.RunStream, which opens
// and finishes an Account itself. An interactive session instead keeps one claude
// process (the CLI engine) or one Agent SDK sidecar alive across many turns, so no
// single call spans a turn: the turn starts when the chat handler writes the user
// frame, and ends when the engine reports the turn's summary chunk (the CLI
// engine's result frame, the sidecar's summary frame). turnLedger bridges the two:
// the handler calls begin before it delivers a frame, and the session's chunk
// callback calls finish when the summary arrives.
//
// What gets written, and when. Nothing is written when a turn starts. Both events
// are written together when it ends, with the start event stamped at the begin
// time (dispatch.Account.Finish does this for an entry that was never started). A
// turn the engine refuses (no session, owner conflict, a turn already in flight, a
// failed write) is dropped before it reaches the log, so it leaves no event; a
// turn that ran always leaves a pair, even when the session dies under it (it is
// finished as failed when the session closes). A result the engine reports with no
// turn waiting (claude can start a turn on its own) is recorded as a turn of its
// own when it used tokens, and ignored when it used none.

import (
	"sync"
	"time"

	"github.com/bakw00ds/yakos/internal/cost"
	"github.com/bakw00ds/yakos/internal/dispatch"
)

// maxPendingTurns bounds the turns remembered per conversation. The engines
// admit one write at a time but let the next frame queue behind a running turn, so
// a handful is plenty; the cap keeps a stuck session from growing the map.
const maxPendingTurns = 16

// turnLedger remembers, per conversation, who an interactive session belongs to
// and which turns are in flight. The zero value is not usable; use newTurnLedger.
type turnLedger struct {
	mu    sync.Mutex
	convs map[string]*convTurns
}

type convTurns struct {
	tmpl    dispatch.Request // everything but the task; copied for each turn
	pending []*pendingTurn   // oldest first: the engine answers turns in order
}

// pendingTurn is one turn between its frame being written and its summary.
type pendingTurn struct {
	acct      *dispatch.Account
	taskBytes int64
	outBytes  int64
}

func newTurnLedger() *turnLedger {
	return &turnLedger{convs: make(map[string]*convTurns)}
}

// register records the template of an interactive session: the agent, project,
// model and identity every turn of the conversation is attributed to. Calling it
// again for a live conversation refreshes the template and keeps the turns in
// flight.
func (l *turnLedger) register(conversationID string, tmpl dispatch.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if c := l.convs[conversationID]; c != nil {
		c.tmpl = tmpl
		return
	}
	l.convs[conversationID] = &convTurns{tmpl: tmpl}
}

// begin notes that a user frame for conversationID is about to be delivered and
// returns the handle to drop if the delivery fails. task is the user's text; only
// its size and a short preview are kept. sessionID, when non-empty, replaces the
// template's console session id (a follow-up may come from another tab). It
// returns nil when the conversation was never registered, in which case nothing
// is accounted (there is no agent to attribute the turn to).
func (l *turnLedger) begin(conversationID, task, sessionID string) *pendingTurn {
	l.mu.Lock()
	defer l.mu.Unlock()
	c := l.convs[conversationID]
	if c == nil || len(c.pending) >= maxPendingTurns {
		return nil
	}
	req := c.tmpl
	req.Task = task
	if sessionID != "" {
		req.SessionID = sessionID
	}
	t := &pendingTurn{acct: dispatch.NewAccount(req), taskBytes: int64(len(task))}
	c.pending = append(c.pending, t)
	return t
}

// drop forgets a turn whose frame was not delivered. Nothing was written for it.
// A nil turn is ignored.
func (l *turnLedger) drop(conversationID string, t *pendingTurn) {
	if t == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	c := l.convs[conversationID]
	if c == nil {
		return
	}
	for i, p := range c.pending {
		if p == t {
			c.pending = append(c.pending[:i], c.pending[i+1:]...)
			return
		}
	}
}

// noteOutput adds n bytes of assistant text to the turn now running (the oldest
// in flight).
func (l *turnLedger) noteOutput(conversationID string, n int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if c := l.convs[conversationID]; c != nil && len(c.pending) > 0 {
		c.pending[0].outBytes += int64(n)
	}
}

// finish ends the oldest turn in flight with the engine's summary chunk and
// writes its event pair. A summary with no turn waiting is a turn of its own if it
// used tokens or cost, and is ignored otherwise.
func (l *turnLedger) finish(conversationID string, chunk dispatch.StreamChunk) {
	l.mu.Lock()
	c := l.convs[conversationID]
	if c == nil {
		l.mu.Unlock()
		return
	}
	var t *pendingTurn
	if len(c.pending) > 0 {
		t = c.pending[0]
		c.pending = c.pending[1:]
	}
	tmpl := c.tmpl
	l.mu.Unlock()

	usage := chunkUsage(chunk)
	if t == nil && usageEmpty(usage) {
		return // a handshake or an empty result: no turn, nothing to account
	}
	res := dispatch.Result{
		ExitCode:      chunk.ExitCode,
		Usage:         usage,
		Runtime:       tmpl.Runtime,
		ModelResolved: tmpl.ModelResolved,
		ModelChosenBy: tmpl.ModelChosenBy,
		ModelID:       chunk.ModelResolved, // the concrete id the engine saw, "" when unknown
		SessionID:     chunk.NativeSessionID,
	}
	if t == nil {
		res.DurationS = chunk.DurationS
		req := tmpl
		end := time.Now()
		dispatch.NewAccountAt(req, end.Add(-time.Duration(chunk.DurationS*float64(time.Second)))).FinishAt(res, end)
		return
	}
	res.TaskBytes = t.taskBytes
	res.OutputBytes = t.outBytes
	t.acct.Finish(res)
}

// closeConversation ends a conversation's session: every turn still in flight is
// finished as failed (the session died or was closed under it) and the template
// is forgotten.
func (l *turnLedger) closeConversation(conversationID string) {
	l.mu.Lock()
	c := l.convs[conversationID]
	delete(l.convs, conversationID)
	l.mu.Unlock()
	if c == nil {
		return
	}
	for _, t := range c.pending {
		t.acct.Finish(dispatch.Result{
			ExitCode:      -1,
			TaskBytes:     t.taskBytes,
			OutputBytes:   t.outBytes,
			Runtime:       c.tmpl.Runtime,
			ModelResolved: c.tmpl.ModelResolved,
			ModelChosenBy: c.tmpl.ModelChosenBy,
			StderrTail:    "interactive session closed before the turn finished",
		})
	}
}

// usageEmpty reports whether u says nothing: no tokens and no dollars.
func usageEmpty(u *cost.Usage) bool {
	return u == nil || (u.InputTokens == 0 && u.OutputTokens == 0 && u.CacheRead == 0 && u.CacheCreation == 0 && u.TotalCostUSD == 0)
}

// chunkUsage is the token usage a summary chunk reports, or nil when it reports
// none. The CLI engine carries the result frame's whole usage object; the Agent
// SDK sidecar reports only the turn's dollar cost (it is API-key billed), so that
// is kept as a usage object with no tokens rather than lost.
func chunkUsage(chunk dispatch.StreamChunk) *cost.Usage {
	if chunk.Usage != nil {
		u := *chunk.Usage
		if u.TotalCostUSD == 0 {
			u.TotalCostUSD = chunk.TotalCostUSD
		}
		return &u
	}
	if chunk.TotalCostUSD > 0 {
		return &cost.Usage{TotalCostUSD: chunk.TotalCostUSD}
	}
	return nil
}

// interactiveTurnTemplate is the request every turn of an interactive session is
// accounted under: the agent, the claude runtime (the persistent engines are
// claude-only), the pane's model, the project the console pins, the owner and
// the console session, with surface console-chat.
func (ch *chatHandlers) interactiveTurnTemplate(agent, model, conversationID, operatorID, sessionID string) dispatch.Request {
	return dispatch.Request{
		AgentName:      agent,
		Project:        ch.workspaceRoot,
		Runtime:        "claude",
		ModelResolved:  model,
		OperatorID:     operatorID,
		ConversationID: conversationID,
		SessionID:      sessionID,
		Surface:        dispatch.SurfaceConsoleChat,
	}
}
