//go:build !windows

package telemetry

// SecureConfigFile is a no-op on non-Windows platforms.
// POSIX mode 0600 (set at write time) is sufficient.
func SecureConfigFile(_ string) error { return nil }
