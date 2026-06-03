//go:build windows

package install

// ResetDevModeProbe resets the package-level DevMode sync.Once cache and
// installs probeFn as the replacement probe for the duration of a test.
// Call it at the start of any test that needs a specific DevMode outcome.
// Tests must restore the real probe (or call ResetDevModeProbe(nil)) in
// t.Cleanup to prevent cross-test pollution.
func ResetDevModeProbe(probeFn func() bool) {
	if probeFn == nil {
		probeFn = probeDevMode
	}
	resetDevModeProbe(probeFn)
}
