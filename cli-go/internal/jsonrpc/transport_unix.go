//go:build !windows

// Package jsonrpc transport_unix.go — Unix domain socket listener for
// Linux and macOS. The socket is created with mode 0600 so only the
// owning user can connect.
package jsonrpc

import (
	"fmt"
	"net"
	"os"
)

// Listen opens a Unix domain socket at path and returns a net.Listener.
// The socket file is created with mode 0600. If a stale socket file exists
// from a previous run it is removed first (cleanup on abnormal exit).
//
// Callers must call Close() on the returned listener to remove the socket file.
func Listen(path string) (net.Listener, error) {
	// Remove stale socket from a previous unclean shutdown.
	if _, err := os.Stat(path); err == nil {
		if removeErr := os.Remove(path); removeErr != nil {
			return nil, fmt.Errorf("jsonrpc: remove stale socket %s: %w", path, removeErr)
		}
	}

	// Ensure parent directory exists.
	dir := socketDir(path)
	if err := os.MkdirAll(dir, 0700); err != nil { //nolint:gosec
		return nil, fmt.Errorf("jsonrpc: mkdir %s: %w", dir, err)
	}

	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("jsonrpc: listen unix %s: %w", path, err)
	}

	// Restrict socket to owner only (defence-in-depth; umask may already do this).
	if err := os.Chmod(path, 0600); err != nil { //nolint:gosec
		_ = ln.Close()
		return nil, fmt.Errorf("jsonrpc: chmod %s: %w", path, err)
	}

	return ln, nil
}

// Dial connects to a Unix domain socket at path and returns a net.Conn.
func Dial(path string) (net.Conn, error) {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return nil, fmt.Errorf("jsonrpc: dial unix %s: %w", path, err)
	}
	return conn, nil
}
