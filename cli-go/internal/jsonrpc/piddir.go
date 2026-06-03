// Package jsonrpc piddir.go — helper exported for serve package.
package jsonrpc

import "path/filepath"

// PIDDir returns the directory component of a PID file path.
// Exported so that the serve package can create it before writing the file.
func PIDDir(pidPath string) string {
	return filepath.Dir(pidPath)
}
