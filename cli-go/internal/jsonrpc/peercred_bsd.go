//go:build darwin || freebsd

package jsonrpc

import (
	"net"

	"golang.org/x/sys/unix"
)

// peerUIDOf returns the uid of the peer of c from LOCAL_PEERCRED.
func peerUIDOf(c *net.UnixConn) (uint32, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var uid uint32
	var serr error
	if err := raw.Control(func(fd uintptr) {
		cred, e := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if e != nil {
			serr = e
			return
		}
		uid = cred.Uid
	}); err != nil {
		return 0, err
	}
	return uid, serr
}
