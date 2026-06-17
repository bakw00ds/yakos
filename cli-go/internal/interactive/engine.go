package interactive

// engine.go — Engine interface and related types for the interactive package.
//
// Engine is the abstraction that Manager operates against.  The P1 CLI
// transport (*Session) and the forthcoming P2b SDK-sidecar engine both satisfy
// this interface, allowing Manager to be engine-agnostic while preserving all
// existing lifecycle semantics (cap, idle-reaper, owner-conflict,
// crash-detection/OnError, process-group-kill via Engine.Close).
//
// # Adding a new engine (P2b)
//
// Implement Engine in a new file (e.g. sdk_engine.go).  Wire it into Manager
// via Ensure by supplying an EngineFactory instead of SessionParams (that
// migration is done in P2b).  The Manager, chat_handler, and all tests remain
// unchanged — they only see the Engine interface.
//
// # AnswerQuestion (P2c placeholder)
//
// The CLI engine (*Session) cannot answer AskUserQuestion tool calls —
// AnswerQuestion returns ErrAnswerUnsupported.  The SDK engine (P2b) will
// implement it for real.  The interface declares it now so P2c callers can use
// Engine without a type assertion.

import (
	"context"
	"errors"
	"time"
)

// QuestionAnswer carries the caller's response to an AskUserQuestion tool call.
//
// This is a placeholder for P2c; the SDK engine will use it to route answers
// back to the model.  The CLI engine ignores the contents and always returns
// ErrAnswerUnsupported.
//
// The map key is the field name declared by the AskUserQuestion schema;
// values are free-form strings supplied by the operator.
type QuestionAnswer struct {
	// Answers maps AskUserQuestion field names to operator-supplied values.
	Answers map[string]string
}

// ErrAnswerUnsupported is returned by Engine.AnswerQuestion on engines that do
// not support interactive question answering (e.g. the CLI transport engine).
// Callers should surface this as a 501 Not Implemented or equivalent.
var ErrAnswerUnsupported = errors.New("interactive: AnswerQuestion not supported by this engine")

// Engine is the interface that Manager operates against for a single persistent
// multi-turn session.
//
// All exported methods must be safe for concurrent use.
//
// # Lifecycle
//
//  1. Engine is obtained from an engine factory (e.g. NewSession + CmdProvider).
//  2. Start(ctx) begins the underlying process or event loop.
//  3. SendUserTurn delivers user-turn frames.
//  4. Close() performs a clean teardown; idempotent.
//  5. Closed() / IsClosed() allow callers to observe termination.
//
// # Implementation contract
//
// Implementations must ensure that Closed() is closed (and IsClosed() returns
// true) after Close() is called, and also after any internal error that causes
// the engine to terminate on its own (e.g. process crash).
type Engine interface {
	// Start begins the engine's process or event loop.
	// Must be called exactly once after construction.
	// ctx is the engine-lifetime context; cancellation should trigger Close.
	Start(ctx context.Context) error

	// SendUserTurn delivers a user-turn frame to the engine.
	// Returns ErrTurnInFlight if a turn is already in progress.
	// Returns an error if the engine is closed or the write times out.
	SendUserTurn(frame []byte) error

	// AnswerQuestion delivers the operator's answer to an AskUserQuestion tool
	// call identified by toolUseID.
	//
	// CLI engine (*Session) always returns ErrAnswerUnsupported.
	// SDK engine (P2b) implements this for real.
	AnswerQuestion(toolUseID string, answer QuestionAnswer) error

	// Close shuts down the engine cleanly.  Idempotent — safe to call multiple
	// times.  After Close returns (or concurrently once Closed() is closed),
	// IsClosed() must return true.
	Close() error

	// IsClosed reports whether the engine has been closed or has exited.
	IsClosed() bool

	// Closed returns a channel that is closed when the engine exits.
	// Callers may select on this channel to wait for termination.
	Closed() <-chan struct{}

	// OwnerOperatorID returns the operator that owns this engine/session.
	OwnerOperatorID() string

	// ConversationID returns the conversation identifier for this engine.
	ConversationID() string

	// LastActivity returns the time of the last SendUserTurn call.
	// Used by the idle reaper.
	LastActivity() time.Time
}
