package interactive_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/interactive"
	"github.com/bakw00ds/yakos/internal/runtime"
)

// fakeRunner is a TurnRunner: it records each turn's params and runs fn.
type fakeRunner struct {
	mu     sync.Mutex
	params []dispatch.Params
	fn     func(ctx context.Context, n int, p dispatch.Params, on func(dispatch.StreamChunk)) (dispatch.Result, error)
}

func (r *fakeRunner) RunStream(ctx context.Context, p dispatch.Params, on func(dispatch.StreamChunk)) (dispatch.Result, error) {
	r.mu.Lock()
	r.params = append(r.params, p)
	n := len(r.params)
	r.mu.Unlock()
	return r.fn(ctx, n, p, on)
}

func (r *fakeRunner) seen() []dispatch.Params {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]dispatch.Params(nil), r.params...)
}

type resumeRig struct {
	eng      *interactive.ResumeEngine
	run      *fakeRunner
	mu       sync.Mutex
	store    map[string]string
	chunks   []dispatch.StreamChunk
	errs     []error
	forgot   []string
	summaryC chan struct{}
}

func newRig(t *testing.T, fn func(ctx context.Context, n int, p dispatch.Params, on func(dispatch.StreamChunk)) (dispatch.Result, error)) *resumeRig {
	t.Helper()
	r := &resumeRig{run: &fakeRunner{fn: fn}, store: map[string]string{}, summaryC: make(chan struct{}, 16)}
	eng, err := interactive.NewResumeEngine(interactive.ResumeEngineParams{
		ConversationID: "c1", OwnerOperatorID: "alice", Runner: r.run,
		Base: dispatch.Params{Agent: "codex", Runtime: "codex"},
		OnChunk: func(c dispatch.StreamChunk) {
			r.mu.Lock()
			r.chunks = append(r.chunks, c)
			r.mu.Unlock()
			if c.Type == "summary" {
				r.summaryC <- struct{}{}
			}
		},
		Sessions: func() map[string]string {
			r.mu.Lock()
			defer r.mu.Unlock()
			out := map[string]string{}
			for k, v := range r.store {
				out[k] = v
			}
			return out
		},
		SaveSession: func(rt, id string) { r.mu.Lock(); r.store[rt] = id; r.mu.Unlock() },
		ForgetSession: func(rt string, _ dispatch.Result) {
			r.mu.Lock()
			r.forgot = append(r.forgot, rt)
			r.mu.Unlock()
		},
		OnTurnError: func(err error) { r.mu.Lock(); r.errs = append(r.errs, err); r.mu.Unlock() },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	r.eng = eng
	return r
}

func summary(rt, id string) dispatch.StreamChunk {
	return dispatch.StreamChunk{Type: "summary", RuntimeResolved: rt, NativeSessionID: id}
}

func (r *resumeRig) waitSummary(t *testing.T) {
	t.Helper()
	select {
	case <-r.summaryC:
	case <-time.After(5 * time.Second):
		t.Fatal("no summary")
	}
}

func okTurn(_ context.Context, n int, _ dispatch.Params, on func(dispatch.StreamChunk)) (dispatch.Result, error) {
	on(dispatch.StreamChunk{Type: "token", Text: "hi"})
	on(summary("codex", "thread-1"))
	return dispatch.Result{}, nil
}

func TestResumeEngine_ThreadsSessionIDIntoNextTurn(t *testing.T) {
	r := newRig(t, okTurn)
	for _, text := range []string{"one", "two"} {
		if err := r.eng.SendUserTurn(runtime.EncodeUserTurn(text)); err != nil {
			t.Fatal(err)
		}
		r.waitSummary(t)
	}
	p := r.run.seen()
	if len(p) != 2 || p[0].Task != "one" || p[1].Task != "two" {
		t.Fatalf("turns: %+v", p)
	}
	if len(p[0].NativeSessions) != 0 {
		t.Errorf("turn 1 carries %v, want none", p[0].NativeSessions)
	}
	if p[1].NativeSessions["codex"] != "thread-1" {
		t.Errorf("turn 2 carries %v, want codex thread-1", p[1].NativeSessions)
	}
	if p[1].Runtime != "codex" {
		t.Errorf("runtime drifted to %q", p[1].Runtime)
	}
}

func TestResumeEngine_SecondSendDuringTurnIsRefused(t *testing.T) {
	release := make(chan struct{})
	r := newRig(t, func(ctx context.Context, n int, p dispatch.Params, on func(dispatch.StreamChunk)) (dispatch.Result, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return dispatch.Result{}, nil
	})
	if err := r.eng.SendUserTurn(runtime.EncodeUserTurn("a")); err != nil {
		t.Fatal(err)
	}
	if err := r.eng.SendUserTurn(runtime.EncodeUserTurn("b")); !errors.Is(err, interactive.ErrTurnInFlight) {
		t.Fatalf("second send: %v, want ErrTurnInFlight", err)
	}
	close(release)
}

// The client sends its next turn the moment it sees the summary, while the
// previous RunStream is still unwinding (accounting): that send must be accepted.
func TestResumeEngine_SummaryFreesThePane(t *testing.T) {
	hold := make(chan struct{})
	r := newRig(t, func(ctx context.Context, n int, p dispatch.Params, on func(dispatch.StreamChunk)) (dispatch.Result, error) {
		on(summary("codex", "thread-1"))
		if n == 1 {
			<-hold
		}
		return dispatch.Result{}, nil
	})
	if err := r.eng.SendUserTurn(runtime.EncodeUserTurn("a")); err != nil {
		t.Fatal(err)
	}
	r.waitSummary(t)
	if err := r.eng.SendUserTurn(runtime.EncodeUserTurn("b")); err != nil {
		t.Fatalf("send after summary: %v", err)
	}
	close(hold)
	r.waitSummary(t)
}

func TestResumeEngine_CloseCancelsTurnAndIsIdempotent(t *testing.T) {
	started := make(chan struct{})
	var cancelled sync.WaitGroup
	cancelled.Add(1)
	r := newRig(t, func(ctx context.Context, n int, p dispatch.Params, on func(dispatch.StreamChunk)) (dispatch.Result, error) {
		close(started)
		<-ctx.Done()
		cancelled.Done()
		return dispatch.Result{}, context.Canceled
	})
	if err := r.eng.SendUserTurn(runtime.EncodeUserTurn("a")); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := r.eng.Close(); err != nil {
		t.Fatal(err)
	}
	cancelled.Wait()
	if !r.eng.IsClosed() {
		t.Error("IsClosed false after Close")
	}
	select {
	case <-r.eng.Closed():
	default:
		t.Error("Closed() not closed")
	}
	_ = r.eng.Close()
	if err := r.eng.SendUserTurn(runtime.EncodeUserTurn("b")); !errors.Is(err, interactive.ErrEngineClosed) {
		t.Errorf("send after close: %v", err)
	}
	if len(r.errs) != 0 || len(r.forgot) != 0 {
		t.Errorf("a cancelled turn is not an error: errs=%v forgot=%v", r.errs, r.forgot)
	}
}

func TestResumeEngine_TurnErrorKeepsPaneAlive(t *testing.T) {
	r := newRig(t, func(ctx context.Context, n int, p dispatch.Params, on func(dispatch.StreamChunk)) (dispatch.Result, error) {
		if n == 1 {
			return dispatch.Result{}, errors.New("budget refused")
		}
		return okTurn(ctx, n, p, on)
	})
	if err := r.eng.SendUserTurn(runtime.EncodeUserTurn("a")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		r.mu.Lock()
		n := len(r.errs)
		r.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("OnTurnError not called")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if r.eng.IsClosed() {
		t.Fatal("a refused turn must not close the pane")
	}
	// the in-flight flag was released: a retry is accepted.
	for {
		if err := r.eng.SendUserTurn(runtime.EncodeUserTurn("b")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pane stuck in flight after an error")
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.waitSummary(t)
}

func TestResumeEngine_ForgetsOnlyAResumedFailedTurn(t *testing.T) {
	r := newRig(t, func(ctx context.Context, n int, p dispatch.Params, on func(dispatch.StreamChunk)) (dispatch.Result, error) {
		on(summary("codex", "thread-1"))
		return dispatch.Result{ExitCode: 1}, nil
	})
	for i := 0; i < 2; i++ {
		if err := r.eng.SendUserTurn(runtime.EncodeUserTurn("x")); err != nil {
			t.Fatal(err)
		}
		r.waitSummary(t)
		time.Sleep(50 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.forgot) != 1 || r.forgot[0] != "codex" {
		t.Errorf("forget calls = %v, want exactly one (turn 2 resumed; turn 1 did not)", r.forgot)
	}
}

func TestResumeEngine_MalformedFrames(t *testing.T) {
	r := newRig(t, okTurn)
	for _, f := range [][]byte{nil, []byte("not json"), []byte(`{"message":{"content":[]}}`)} {
		if err := r.eng.SendUserTurn(f); err == nil {
			t.Errorf("frame %q accepted", f)
		}
	}
	if n := len(r.run.seen()); n != 0 {
		t.Errorf("%d turns ran for bad frames", n)
	}
}

// ---- Manager.EnsureEngine ------------------------------------------------------

func engineFactory(t *testing.T) interactive.EngineFactory {
	return func(conv, owner string) (interactive.Engine, error) {
		return interactive.NewResumeEngine(interactive.ResumeEngineParams{
			ConversationID: conv, OwnerOperatorID: owner,
			Runner:  &fakeRunner{fn: okTurn},
			OnChunk: func(dispatch.StreamChunk) {},
		})
	}
}

func TestManager_EnsureEngine_CapOwnerReuseAndReap(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := interactive.NewManager(ctx, interactive.ManagerConfig{Cap: 2, IdleTimeout: 150 * time.Millisecond, ReaperInterval: 30 * time.Millisecond})
	defer m.Stop()
	f := engineFactory(t)

	e1, err := m.EnsureEngine("a", "alice", f)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := m.EnsureEngine("a", "alice", f); again != e1 {
		t.Error("same owner must get the live engine back")
	}
	if _, err := m.EnsureEngine("a", "mallory", f); !errors.Is(err, interactive.ErrOwnerConflict) {
		t.Errorf("owner conflict: %v", err)
	}
	if _, err := m.EnsureEngine("b", "alice", f); err != nil {
		t.Fatal(err)
	}
	if _, err := m.EnsureEngine("c", "alice", f); !errors.Is(err, interactive.ErrCapExceeded) {
		t.Errorf("cap: %v", err)
	}
	if !m.AccountsOwnTurns("a", "alice") || m.AccountsOwnTurns("a", "mallory") || m.AccountsOwnTurns("zz", "alice") {
		t.Error("AccountsOwnTurns must be true only for the owner of a live resume engine")
	}
	// Idle reaper closes them; the slot frees up.
	deadline := time.Now().Add(5 * time.Second)
	for m.ActiveCount() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("idle engines not reaped")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !e1.IsClosed() {
		t.Error("reaped engine not closed")
	}
	if _, err := m.EnsureEngine("c", "alice", f); err != nil {
		t.Errorf("after reap: %v", err)
	}
}

func TestManager_EnsureEngine_SendRoutesToEngine(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := interactive.NewManager(ctx, interactive.ManagerConfig{})
	defer m.Stop()
	run := &fakeRunner{fn: okTurn}
	_, err := m.EnsureEngine("a", "alice", func(conv, owner string) (interactive.Engine, error) {
		return interactive.NewResumeEngine(interactive.ResumeEngineParams{ConversationID: conv, OwnerOperatorID: owner, Runner: run, OnChunk: func(dispatch.StreamChunk) {}})
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close("a")
	if err := m.Send("a", "alice", runtime.EncodeUserTurn("hello")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(run.seen()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("turn never ran")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := run.seen()[0].Task; got != "hello" {
		t.Errorf("task %q", got)
	}
}
