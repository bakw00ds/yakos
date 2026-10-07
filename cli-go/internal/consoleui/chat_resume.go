package consoleui

// chat_resume.go: the interactive branch for codex and agy panes (K-147).
//
// The pane is a interactive.ResumeEngine: one Service.RunStream per turn, the
// harness's own session id read from and written to the conversation's meta
// store (native_sessions, keyed by runtime), so a pane keeps its context across
// turns and across a console restart. RunStream writes the Account event pair
// for each turn, which is why the turnLedger is not used for these panes.

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/interactive"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/runtime"
)

type resumePaneArgs struct {
	dispReq          DispatchRequest
	conversationID   string
	operatorID       string
	runtimeName      string // the resolved runtime the pane is pinned to
	model            string // the request's own model (resolved again per runtime)
	identity         netid.Identity
	worktreeOverride string
	onChunk          func(dispatch.StreamChunk)
	fail             func()
}

// runResumePane starts (or reuses) the pane's ResumeEngine, delivers the first
// turn and blocks until the engine closes (idle reap, cancel, shutdown), like
// the claude branches, so the hub entry stays live for the pane's lifetime.
func (ch *chatHandlers) runResumePane(ctx context.Context, a resumePaneArgs) {
	d := a.dispReq
	routeError := func(text string) {
		_ = ch.transcripts.Append(TranscriptEntry{
			SessionID: d.SessionID, ConversationID: a.conversationID,
			OperatorID: a.operatorID, Role: RoleError, Text: text,
		})
		ch.hub.Route(SSEEvent{
			SessionID: d.SessionID, ConversationID: a.conversationID, Type: "error",
			Text: text, TS: time.Now().UTC().Format(time.RFC3339Nano),
		})
	}

	// A per-session worktree is a different directory than the one the harness
	// filed its session under, so ids are neither read nor stored there.
	useStore := a.worktreeOverride == ""
	factory := func(conversationID, operatorID string) (interactive.Engine, error) {
		return interactive.NewResumeEngine(interactive.ResumeEngineParams{
			ConversationID:  conversationID,
			OwnerOperatorID: operatorID,
			Runner:          ch.svc,
			OnChunk:         a.onChunk,
			Base: dispatch.Params{
				Agent:          d.Agent,
				Runtime:        a.runtimeName,
				Model:          a.model,
				Effort:         d.Effort,
				OperatorID:     operatorID,
				ConversationID: conversationID,
				SessionID:      d.SessionID,
				ResolvedIdentity: dispatch.IdentityCarrier{
					Populated: a.identity.Resolved,
					Identity:  a.identity,
				},
				WorkDirOverride: a.worktreeOverride,
				Surface:         dispatch.SurfaceConsoleChat,
			},
			Sessions: func() map[string]string {
				if !useStore {
					return nil
				}
				// Every runtime's id: RunStream resumes the one it routes to.
				ids := map[string]string{}
				for _, rt := range []string{"claude", "codex", "agy"} {
					if id := ch.transcripts.NativeSession(conversationID, rt, operatorID); id != "" {
						ids[rt] = id
					}
				}
				return ids
			},
			SaveSession: func(rt, id string) {
				if !useStore {
					return
				}
				if err := ch.transcripts.SetNativeSession(conversationID, rt, id, operatorID); err != nil {
					slog.Warn("consoleui: store native session id", "conversation", conversationID, "err", err)
				}
			},
			ForgetSession: func(rt string, res dispatch.Result) {
				ch.forgetDeadResume(conversationID, operatorID, rt, res)
			},
			OnTurnError: func(err error) {
				slog.Error("consoleui: resume pane turn error", "session", d.SessionID, "agent", d.Agent, "err", err)
				a.fail()
				routeError(fmt.Sprintf("dispatch failed: %s", err.Error()))
			},
		})
	}

	sess, err := ch.interactiveMgr.EnsureEngine(a.conversationID, a.operatorID, factory)
	if err != nil {
		a.fail()
		routeError(fmt.Sprintf("interactive: start failed: %s", err.Error()))
		return
	}
	if err := ch.sendFrame(a.conversationID, a.operatorID, runtime.EncodeUserTurn(d.Task)); err != nil {
		a.fail()
		routeError(fmt.Sprintf("interactive: send failed: %s", err.Error()))
		// The pane is alive; let it run until closed, as the claude branches do.
	}

	select {
	case <-sess.Closed():
	case <-ctx.Done():
		ch.interactiveMgr.Close(a.conversationID)
	}
}
