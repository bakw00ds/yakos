package workflow

// SetEngineRunFn sets the per-node dispatch function on an Engine.
//
// This is a test-only seam: it allows tests in this package (engine_test.go,
// package workflow_test) to inject a deterministic fake runFn so they can
// exercise the DAG executor without making live LLM calls.
//
// This file is compiled ONLY when running tests for the workflow package
// itself (go test ./internal/workflow/...). It is NOT included in the
// production binary and NOT visible to external packages importing workflow.
//
// External packages that need a test seam (e.g. consoleui) must inject fakes
// at their own layer (e.g. flowsHandlers.nodeRunFn) rather than reaching into
// this package's internals.
func SetEngineRunFn(e *Engine, fn EngineRunFn) {
	e.runFn = fn
}

// NodeDispatchLogName exposes the per-run node dispatch log filename for tests.
const NodeDispatchLogName = nodeDispatchLogName

// NodeDispatchEvent is the exported alias of the internal nodeDispatchEvent type
// so that tests in package workflow_test can construct and pass event values to
// AppendNodeDispatchEvent without importing an internal type directly.
type NodeDispatchEvent = nodeDispatchEvent

// AppendNodeDispatchEvent is the exported test shim for appendNodeDispatchEvent.
// Tests use this to write events concurrently and assert no line corruption.
func AppendNodeDispatchEvent(logPath string, ev NodeDispatchEvent) {
	appendNodeDispatchEvent(logPath, ev)
}
