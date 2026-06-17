// Package interactivetest provides test helpers for the interactive package.
//
// Import this package ONLY from _test.go files.  It is NOT compiled into
// production binaries because no production code imports it.
//
// This package re-exports ManagerInjectForTest from the interactive package
// (which must live there because it accesses unexported fields) and provides
// FakeSDKEngineFactory as a self-contained implementation that needs no
// private access.
package interactivetest

import (
	"errors"

	"github.com/bakw00ds/yakos/internal/interactive"
)

// ManagerInjectForTest inserts eng as the live engine for conversationID in mgr.
// Used by external test packages (e.g. consoleui_test) to pre-populate the
// manager with a fake engine without needing a real subprocess.
//
// The injected engine's OwnerOperatorID() is used directly — it must match
// the ownerOperatorID that tests supply to AnswerQuestion and Send.
//
// Returns interactive.ErrCapExceeded when the manager is at capacity.
func ManagerInjectForTest(mgr *interactive.Manager, conversationID string, eng interactive.Engine) error {
	return interactive.ManagerInjectForTest(mgr, conversationID, eng)
}

// errFakeSDKUnavailable is the sentinel returned by the fake factory.
var errFakeSDKUnavailable = errors.New("fake: SDK engine not available in tests")

// FakeSDKEngineFactory returns an interactive.SDKEngineFactory that always
// returns an error (no real node process is started).  Suitable for
// wiring-guard tests that only need a non-nil factory pointer, not a working
// sidecar.
func FakeSDKEngineFactory() interactive.SDKEngineFactory {
	return func(_ interactive.SDKEngineParams) (*interactive.SDKEngine, error) {
		return nil, errFakeSDKUnavailable
	}
}
