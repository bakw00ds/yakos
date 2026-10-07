package interactive

// resume_engine.go: ResumeEngine, the Engine for codex and agy panes.
//
// Neither harness keeps a long-lived child the way claude's stream-json
// session does. A ResumeEngine therefore holds no process between turns: each
// SendUserTurn runs one Service.RunStream (routing, sandbox, budget
// pre-flight, supervision and accounting all unchanged), threads the
// harness's own session id from the previous turn into it through
// dispatch.Params.NativeSessions, and saves the id the summary chunk returns.
//
// Accounting: RunStream opens and finishes the Account event pair for the
// turn itself, and ResumeEngine calls it exactly once per turn. The console's
// turnLedger must not wrap these turns (AccountsOwnTurns reports that).

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/bakw00ds/yakos/internal/dispatch"
)

// TurnRunner is the part of *dispatch.Service a ResumeEngine uses.
type TurnRunner interface {
	RunStream(ctx context.Context, p dispatch.Params, onChunk func(dispatch.StreamChunk)) (dispatch.Result, error)
}

// ErrEngineClosed is returned by SendUserTurn after Close.
var ErrEngineClosed = errors.New("interactive: resume engine closed")

// resumeCloseWait bounds how long Close waits for a cancelled turn to unwind.
const resumeCloseWait = 10 * time.Second

// ResumeEngineParams holds construction parameters for NewResumeEngine.
type ResumeEngineParams struct {
	ConversationID  string
	OwnerOperatorID string

	// Runner runs one turn. Required.
	Runner TurnRunner

	// Base is the dispatch request every turn starts from (agent, pinned
	// runtime, model, effort, operator, identity, surface). Task and
	// NativeSessions are set per turn. Base.Runtime pins the pane: the engine
	// never changes it.
	Base dispatch.Params

	// OnChunk receives every chunk of every turn. Required.
	OnChunk func(dispatch.StreamChunk)

	// Sessions returns the stored native session ids by runtime (read at the
	// start of each turn, so a restarted manager resumes where it left off).
	Sessions func() map[string]string

	// SaveSession stores the id a turn's summary returned.
	SaveSession func(rt, id string)

	// ForgetSession is called when a turn that resumed a stored id for rt
	// failed, so the caller can drop a dead id.
	ForgetSession func(rt string, res dispatch.Result)

	// PrepareTurn, when set, is called at the start of every turn with the
	// user's text and returns the task to dispatch (a skill tail may be appended
	// to it) and the conversation's stored knowledge block for
	// dispatch.Params.Knowledge (K-149). The block must be the same stored bytes
	// every turn.
	PrepareTurn func(text string) (task, knowledge string)

	// OnTurnError receives a RunStream error that is not a cancellation (a
	// refused budget, a bad id, a launch failure). The engine stays usable.
	OnTurnError func(err error)
}

// ResumeEngine implements Engine on top of per-turn RunStream calls.
type ResumeEngine struct {
	p ResumeEngineParams

	ctx    context.Context
	cancel context.CancelFunc

	mu           sync.Mutex
	started      bool
	closing      bool
	inFlight     bool
	turnSeq      uint64
	lastActivity time.Time

	wg        sync.WaitGroup
	closed    chan struct{}
	closeOnce sync.Once
}

var _ Engine = (*ResumeEngine)(nil)

// NewResumeEngine allocates a ResumeEngine; nothing runs until a turn is sent.
func NewResumeEngine(p ResumeEngineParams) (*ResumeEngine, error) {
	if p.Runner == nil || p.OnChunk == nil {
		return nil, errors.New("interactive: resume engine needs a runner and an OnChunk")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &ResumeEngine{
		p:            p,
		ctx:          ctx,
		cancel:       cancel,
		lastActivity: time.Now(),
		closed:       make(chan struct{}),
	}, nil
}

// AccountsOwnTurns reports that RunStream writes the Account pair for each
// turn, so a caller must not account these turns a second time.
func (e *ResumeEngine) AccountsOwnTurns() bool { return true }

// Start marks the engine live. ctx cancellation closes the engine.
func (e *ResumeEngine) Start(ctx context.Context) error {
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		return errors.New("interactive: resume engine already started")
	}
	e.started = true
	e.mu.Unlock()
	go func() {
		select {
		case <-ctx.Done():
			_ = e.Close()
		case <-e.closed:
		}
	}()
	return nil
}

// SendUserTurn decodes the user-turn frame and runs one turn in the
// background. ErrTurnInFlight while a turn is running.
func (e *ResumeEngine) SendUserTurn(frame []byte) error {
	text, err := decodeUserTurn(frame)
	if err != nil {
		return err
	}
	e.mu.Lock()
	if e.closing {
		e.mu.Unlock()
		return ErrEngineClosed
	}
	if e.inFlight {
		e.mu.Unlock()
		return ErrTurnInFlight
	}
	e.inFlight = true
	e.turnSeq++
	seq := e.turnSeq
	e.lastActivity = time.Now()
	e.wg.Add(1)
	e.mu.Unlock()

	go e.runTurn(seq, text)
	return nil
}

// release clears the in-flight flag for turn seq (idempotent; a late finish
// of an older turn never clears a newer one).
func (e *ResumeEngine) release(seq uint64) {
	e.mu.Lock()
	if e.turnSeq == seq {
		e.inFlight = false
	}
	e.mu.Unlock()
}

func (e *ResumeEngine) runTurn(seq uint64, text string) {
	defer e.wg.Done()
	defer e.release(seq)

	params := e.p.Base
	params.Task = text
	if e.p.PrepareTurn != nil {
		params.Task, params.Knowledge = e.p.PrepareTurn(text)
	}
	params.ResumeSessionID = ""
	sessions := map[string]string{}
	if e.p.Sessions != nil {
		for k, v := range e.p.Sessions() {
			sessions[k] = v
		}
	}
	params.NativeSessions = sessions
	resumed := ""
	if params.Runtime != "" && sessions[params.Runtime] != "" {
		resumed = params.Runtime
	}

	onChunk := func(c dispatch.StreamChunk) {
		if c.Type == "summary" {
			if c.NativeSessionID != "" && c.RuntimeResolved != "" && e.p.SaveSession != nil {
				e.p.SaveSession(c.RuntimeResolved, c.NativeSessionID)
			}
			// The client sends its next turn as soon as it sees the summary,
			// so the pane must be free before the summary is forwarded.
			e.release(seq)
		}
		e.p.OnChunk(c)
	}

	res, err := e.p.Runner.RunStream(e.ctx, params, onChunk)
	if resumed != "" && e.ctx.Err() == nil && e.p.ForgetSession != nil {
		e.p.ForgetSession(resumed, res)
	}
	if err != nil && !errors.Is(err, context.Canceled) && e.ctx.Err() == nil && e.p.OnTurnError != nil {
		e.p.OnTurnError(err)
	}
}

// AnswerQuestion is not supported: codex and agy have no structured questions.
func (e *ResumeEngine) AnswerQuestion(string, QuestionAnswer) error { return ErrAnswerUnsupported }

// Close cancels an in-flight turn (RunStream kills the process group on
// cancellation) and waits, bounded, for it to unwind. Idempotent.
func (e *ResumeEngine) Close() error {
	e.closeOnce.Do(func() {
		e.mu.Lock()
		e.closing = true
		e.mu.Unlock()
		e.cancel()
		done := make(chan struct{})
		go func() { e.wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(resumeCloseWait):
		}
		close(e.closed)
	})
	return nil
}

// IsClosed reports whether Close has finished.
func (e *ResumeEngine) IsClosed() bool {
	select {
	case <-e.closed:
		return true
	default:
		return false
	}
}

// Closed returns a channel closed when the engine has exited.
func (e *ResumeEngine) Closed() <-chan struct{} { return e.closed }

// OwnerOperatorID returns the owning operator.
func (e *ResumeEngine) OwnerOperatorID() string { return e.p.OwnerOperatorID }

// ConversationID returns the conversation identifier.
func (e *ResumeEngine) ConversationID() string { return e.p.ConversationID }

// LastActivity returns the time of the last accepted turn.
func (e *ResumeEngine) LastActivity() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastActivity
}

// decodeUserTurn extracts the text of a runtime.EncodeUserTurn frame.
func decodeUserTurn(frame []byte) (string, error) {
	var f struct {
		Message struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(frame, &f); err != nil {
		return "", errors.New("interactive: malformed user-turn frame")
	}
	text := ""
	for _, c := range f.Message.Content {
		if c.Type == "text" {
			text += c.Text
		}
	}
	if text == "" {
		return "", errors.New("interactive: empty user-turn frame")
	}
	return text, nil
}
