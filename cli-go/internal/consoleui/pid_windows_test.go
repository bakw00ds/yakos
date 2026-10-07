//go:build windows

package consoleui_test

// pidAlive: the resume tests that use it run shell stubs and skip on Windows.
func pidAlive(int) bool { return false }
