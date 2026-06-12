package workflow

// SetEngineRunFn sets the per-node dispatch function on an Engine.
// This is a test-seam: it allows cross-package tests (e.g. consoleui_test) to
// inject a deterministic fake runFn so tests can exercise the handler code path
// that reaches the engine without making live LLM calls.
//
// IMPORTANT: Production code must never call this function. Setting runFn
// bypasses the governed dispatch.Service (rate-limiting, audit log, concurrency
// governor). It exists solely to satisfy the test-seam requirement without
// duplicating the export_test.go pattern (which is only compiled for the
// workflow package's own tests, not for cross-package callers).
func SetEngineRunFn(e *Engine, fn EngineRunFn) {
	e.runFn = fn
}
