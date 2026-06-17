package interactive

// testing_helpers.go — exported test helpers for the interactive package.
//
// These functions are intended for use in external test packages that need to
// inject fake engines or factories without going through the real EnsureSDK /
// Ensure lifecycle (which requires real subprocesses).
//
// They are NOT compiled into production binaries because production code never
// imports testing helper files.  However, they ARE exported symbols (not in
// _test.go files) so that external test packages (e.g. consoleui_test) can
// import and call them.
//
// Convention: functions named *ForTest or *InjectForTest are test helpers.
// Do not call them from production code.

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
