//go:build !windows

// Package jsonrpc transport_unix.go — Unix domain socket listener for
// Linux and macOS. The socket is created with mode 0600 so only the
// owning user can connect.
package jsonrpc

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/bakw00ds/yakos/internal/statepath"
)

// Listen opens a Unix domain socket at path and returns a net.Listener.
// The socket file is created with mode 0600. If a stale socket file exists
// from a previous run it is removed first (cleanup on abnormal exit).
//
// Callers must call Close() on the returned listener to remove the socket file.
func Listen(path string) (net.Listener, error) {
	// Ensure parent directory exists and is private BEFORE touching anything
	// inside it (including the stale-socket unlink below).
	//
	// S-2 N6 (s2-daemon-security-review-r2-2026-09-21.md): on Linux without
	// XDG_RUNTIME_DIR the directory is /tmp/yakos-<uid>. MkdirAll(0700) is a
	// no-op on an existing directory, so an attacker who pre-created it 0777
	// owned it: they could unlink the daemon's socket and bind their own at
	// the same path, and `yakos start` would dial the attacker and push the
	// whole PTY stream to them. For the yakOS-managed directory, reject a
	// symlink or a directory owned by someone else and tighten group/other
	// bits. A caller-chosen directory is left as MkdirAll leaves it.
	dir := socketDir(path)
	if isYakosManagedSocketDir(dir) {
		if err := statepath.SecureDir(dir); err != nil {
			return nil, fmt.Errorf("jsonrpc: socket dir: %w", err)
		}
	} else if err := os.MkdirAll(dir, 0700); err != nil { //nolint:gosec
		return nil, fmt.Errorf("jsonrpc: mkdir %s: %w", dir, err)
	}

	// Remove stale socket from a previous unclean shutdown.
	if _, err := os.Stat(path); err == nil {
		if removeErr := os.Remove(path); removeErr != nil {
			return nil, fmt.Errorf("jsonrpc: remove stale socket %s: %w", path, removeErr)
		}
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

// isYakosManagedSocketDir reports whether dir is one of the per-user
// directories SocketPath/PIDPath choose ("yakos" under XDG_RUNTIME_DIR or
// TMPDIR, or "yakos-<uid>" under /tmp), as opposed to a directory a caller
// picked explicitly.
func isYakosManagedSocketDir(dir string) bool {
	b := filepath.Base(dir)
	return b == "yakos" || (strings.HasPrefix(b, "yakos-") && len(b) > len("yakos-"))
}
