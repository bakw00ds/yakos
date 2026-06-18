//go:build windows

// Package serve — term_transport_windows.go
//
// Windows stub for the attach transport. PTY sessions are not supported on
// Windows; yakos.term.attach returns ErrNotSupported on this platform.

package serve

import (
	"net"

	termmanager "github.com/bakw00ds/yakos/internal/terminalmanager"
)

// runAttachTransport is a no-op on Windows.  The yakos.term.attach RPC handler
// returns an error before hijacking on Windows, so this function is never
// reached in practice.
func runAttachTransport(conn net.Conn, sessionID string, mgr *termmanager.Manager) {
	_ = conn.Close()
	_ = mgr // suppress unused import
	_ = sessionID
}
