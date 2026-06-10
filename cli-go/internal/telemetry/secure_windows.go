//go:build windows

package telemetry

import "github.com/bakw00ds/yakos/internal/winsec"

// SecureConfigFile restricts the NTFS DACL on the telemetry config/log file
// so that only the current user has access. Delegates to the shared winsec
// package (PR #108 convention).
func SecureConfigFile(path string) error {
	return winsec.SecureFile(path)
}
