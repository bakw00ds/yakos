//go:build windows

// Package serve — term_transport_windows.go
//
// Windows stubs for the push and attach transports.  PTY sessions are not
// supported on Windows; yakos.term.attach returns ErrNotSupported on this
// platform before the connection is ever hijacked, so these functions are
// never reached in practice.

package serve

import (
	"net"

	termmanager "github.com/bakw00ds/yakos/internal/terminalmanager"
)

// runPushTransport is a no-op on Windows.
func runPushTransport(conn net.Conn, sessionID string, mgr *termmanager.Manager) {
	_ = conn.Close()
	_ = mgr
	_ = sessionID
}

// runAttachTransport is a no-op on Windows (T1 path, preserved for tests).
func runAttachTransport(conn net.Conn, sessionID string, mgr *termmanager.Manager) {
	_ = conn.Close()
	_ = mgr
	_ = sessionID
}
