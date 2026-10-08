//go:build !windows

package jsonrpc

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

// ErrUntrustedSocket reports that the daemon socket, its directory or the
// process behind it cannot be shown to belong to the current user.
var ErrUntrustedSocket = errors.New("jsonrpc: the daemon socket is not private to this user")

// peerUID returns the uid of the process on the other end of c. It is a seam
// so a test can stand in a foreign peer without a second user on the host.
var peerUID = peerUIDOf

// DialTrusted dials the daemon socket at path only after proving it is private
// to this user, for a client that is about to act on what the daemon says
// (the REPL sends a bearer token on the strength of yakos.version).
//
// It checks, in order: the socket directory is a real directory (not a
// symlink) owned by the effective uid with no group or world access; the
// socket is a socket owned by the effective uid with no group or world
// access; and, after connecting, the kernel-reported peer credential of the
// server is the effective uid. The server side already creates the directory
// 0700 and the socket 0600 (Listen); a shared, predictable directory such as
// /tmp/yakos-<uid> or /tmp/yakos could have been pre-created by someone else,
// which these checks refuse.
//
// Windows has no peer credential here (see trust_windows.go).
func DialTrusted(path string) (net.Conn, error) {
	if err := checkPrivate(filepath.Dir(path), true); err != nil {
		return nil, err
	}
	if err := checkPrivate(path, false); err != nil {
		return nil, err
	}
	conn, err := Dial(path)
	if err != nil {
		return nil, err
	}
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		_ = conn.Close()
		return nil, ErrUntrustedSocket
	}
	uid, err := peerUID(uc)
	if err != nil || uid != uint32(os.Geteuid()) { //nolint:gosec // uid fits
		_ = conn.Close()
		return nil, ErrUntrustedSocket
	}
	return conn, nil
}

// checkPrivate Lstats p and requires the expected kind, the effective uid as
// owner and no group/other permission bits. A missing path is reported as the
// underlying error so callers can retry a daemon that is still starting.
func checkPrivate(p string, wantDir bool) error {
	fi, err := os.Lstat(p)
	if err != nil {
		return fmt.Errorf("jsonrpc: stat socket path: %w", err)
	}
	m := fi.Mode()
	if wantDir && (!m.IsDir() || m&os.ModeSymlink != 0) {
		return ErrUntrustedSocket
	}
	if !wantDir && m&os.ModeSocket == 0 {
		return ErrUntrustedSocket
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Geteuid()) { //nolint:gosec // uid fits
		return ErrUntrustedSocket
	}
	if m.Perm()&0o077 != 0 {
		return ErrUntrustedSocket
	}
	return nil
}
