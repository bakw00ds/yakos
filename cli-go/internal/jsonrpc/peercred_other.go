//go:build !windows && !linux && !darwin && !freebsd

package jsonrpc

import (
	"errors"
	"net"
)

// peerUIDOf fails closed where no peer-credential call is wired up.
func peerUIDOf(*net.UnixConn) (uint32, error) {
	return 0, errors.New("jsonrpc: no peer credential on this platform")
}
