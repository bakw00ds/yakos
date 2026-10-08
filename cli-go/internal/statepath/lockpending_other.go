//go:build !windows

package statepath

// lockReleasePending is always false off Windows: a refusal other than "exists"
// is a real failure there. The test seam in edit_test.go swaps it.
var lockReleasePending = func(error) bool { return false }
