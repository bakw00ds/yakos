package workflow

// SetEngineRunFn sets the per-node dispatch function on an Engine.
// Only used in tests to inject a deterministic fake that avoids live LLM calls.
// Production code must never call this function.
func SetEngineRunFn(e *Engine, fn EngineRunFn) {
	e.runFn = fn
}
