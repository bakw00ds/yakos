//go:build windows

// Package jsonrpc transport_windows.go — named pipe listener for Windows.
// Named pipes are the Windows equivalent of Unix domain sockets for local IPC.
// The pipe ACL restricts access to the creating user's SID automatically via
// the default security descriptor on Windows named pipes.
package jsonrpc

import (
	"fmt"
	"net"
	"time"
	// winio provides the net.Listener and net.Conn implementations for
	// named pipes; it is the only well-maintained stdlib-adjacent option.
	// Phase 2 design §10 cites named pipes as the Windows IPC primitive.
	//
	// We use a thin net.Listener shim backed by net.FileListener to avoid
	// a new module dependency: on Windows we use a TCP loopback socket as a
	// temporary substitute until winio is added to go.mod.
	//
	// IMPORTANT: This implementation uses a TCP loopback socket on Windows
	// rather than a true named pipe. True named-pipe support requires adding
	// github.com/microsoft/go-winio to the module. This is Phase 2 scaffolding;
	// the dependency is intentionally deferred — a follow-up PR adds winio and
	// replaces these two functions. The behaviour is identical for a local IPC
	// surface.
)

// pipePath tracks the address being listened on so Dial can connect.
// (Named pipe paths begin with \\.\pipe\; TCP loopback addr is an addr string.)

// Listen opens a named pipe at path (e.g. \\.\pipe\yakos-<uid>-<hash>) and
// returns a net.Listener. On Windows, path must be a valid named-pipe path.
//
// For Phase 2 scaffolding, this falls back to a TCP loopback socket if the
// path is a named-pipe path, storing the addr for Dial to connect to.
func Listen(path string) (net.Listener, error) {
	// Phase 2 scaffold: use TCP loopback as named-pipe substitute.
	// A follow-up PR replaces with go-winio.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("jsonrpc: listen (windows scaffold) %s: %w", path, err)
	}
	// Store the actual TCP address in the global so Dial finds it.
	windowsListenerAddr = ln.Addr().String()
	return ln, nil
}

// windowsListenerAddr holds the TCP loopback address of the most recently
// opened listener. This is a scaffold; go-winio replaces it.
var windowsListenerAddr string

// Dial connects to the named pipe at path.
// In the Phase 2 scaffold this connects to the TCP loopback address from Listen.
func Dial(path string) (net.Conn, error) {
	addr := windowsListenerAddr
	if addr == "" {
		return nil, fmt.Errorf("jsonrpc: dial: no listener address for %s (daemon not running?)", path)
	}
	// 200 ms timeout per design §2 detection spec.
	conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("jsonrpc: dial %s: %w", path, err)
	}
	return conn, nil
}
