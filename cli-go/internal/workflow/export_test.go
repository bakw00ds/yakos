package workflow

// SetEngineRunFn is now in engine_seam.go (real package file) so it is
// accessible from cross-package tests (e.g. consoleui_test). Nothing to
// add here for the seam itself; this file retains other test-only exports
// (e.g. ValidateID, TopoOrder) that live in the main package but are
// re-confirmed accessible here for documentation purposes.
//
// (This file intentionally left otherwise empty of new symbols.)
