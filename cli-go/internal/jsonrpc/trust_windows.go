//go:build windows

package jsonrpc

import (
	"errors"
	"net"
)

// ErrUntrustedSocket is returned where a socket cannot be trusted.
var ErrUntrustedSocket = errors.New("jsonrpc: the daemon socket is not private to this user")

// DialTrusted is a plain Dial on Windows. KNOWN GAP: the Windows transport is a
// loopback TCP scaffold (transport_windows.go), which has no peer credential
// and no owner/mode to check, so nothing here proves who the server is. A
// caller must therefore not rely on this for identity: the REPL refuses to
// reuse a daemon on Windows unless the build id AND the per-boot instance
// nonce both match (the nonce is mandatory on every platform), and the REPL
// cannot reach the daemon at all until the scaffold is replaced by go-winio.
func DialTrusted(path string) (net.Conn, error) { return Dial(path) }
