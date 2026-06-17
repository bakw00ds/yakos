package interactive

// testing_helpers.go — exported test seams for the interactive package.
//
// WARNING: These functions ARE compiled into production binaries.  They exist
// in a non-_test.go file because they must access unexported Manager fields
// (mu, entries, cap) to inject fake engines, and Go does not allow external
// _test.go files to access unexported symbols.  Moving them to _test.go files
// within this package would make them inaccessible to external test packages
// such as consoleui_test.
//
// The exported symbols here (ManagerInjectForTest, FakeSDKEngineFactory) are
// test-only seams by convention and naming.  Production code must not call
// them.  A future refactor may replace them with a functional-options injection
// API on Manager so they can be removed from the production binary.
//
// Do not call these functions from production code.

import "errors"

// ManagerInjectForTest inserts eng as the live engine for conversationID in mgr.
// Used by external test packages (e.g. consoleui_test) to pre-populate the
// manager with a fake engine without needing a real subprocess.
//
// The injected engine's OwnerOperatorID() is used directly — it must match
// the ownerOperatorID that tests supply to AnswerQuestion and Send.
//
// Returns ErrCapExceeded when the manager is at capacity.
func ManagerInjectForTest(mgr *Manager, conversationID string, eng Engine) error {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if len(mgr.entries) >= mgr.cap {
		return ErrCapExceeded
	}
	mgr.entries[conversationID] = &managerEntry{session: eng}
	return nil
}

// errFakeSDKUnavailable is a sentinel for the fake factory.
var errFakeSDKUnavailable = errors.New("fake: SDK engine not available in tests")

// FakeSDKEngineFactory returns an SDKEngineFactory that always returns an error
// (no real node process is started).  Suitable for wiring-guard tests that only
// need a non-nil factory pointer, not a working sidecar.
func FakeSDKEngineFactory() SDKEngineFactory {
	return func(params SDKEngineParams) (*SDKEngine, error) {
		return nil, errFakeSDKUnavailable
	}
}
