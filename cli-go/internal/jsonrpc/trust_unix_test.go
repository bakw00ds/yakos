//go:build !windows

package jsonrpc

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func trustSock(t *testing.T, dirMode, sockMode os.FileMode) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "yk154t")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	dir := filepath.Join(d, "yakos")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "a.sock")
	ln, err := net.Listen("unix", p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	if err := os.Chmod(p, sockMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, dirMode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDialTrusted(t *testing.T) {
	t.Run("private socket and a same-uid peer", func(t *testing.T) {
		c, err := DialTrusted(trustSock(t, 0o700, 0o600))
		if err != nil {
			t.Fatalf("refused: %v", err)
		}
		_ = c.Close()
	})
	for _, tc := range []struct {
		name      string
		dir, sock os.FileMode
	}{
		{"world-writable dir", 0o777, 0o600},
		{"group-readable dir", 0o750, 0o600},
		{"world-accessible socket", 0o700, 0o777},
		{"group-writable socket", 0o700, 0o660},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := DialTrusted(trustSock(t, tc.dir, tc.sock))
			if !errors.Is(err, ErrUntrustedSocket) {
				if c != nil {
					_ = c.Close()
				}
				t.Fatalf("err = %v, want ErrUntrustedSocket", err)
			}
		})
	}
	t.Run("symlinked directory", func(t *testing.T) {
		p := trustSock(t, 0o700, 0o600)
		link := filepath.Join(filepath.Dir(filepath.Dir(p)), "link")
		if err := os.Symlink(filepath.Dir(p), link); err != nil {
			t.Fatal(err)
		}
		if _, err := DialTrusted(filepath.Join(link, "a.sock")); !errors.Is(err, ErrUntrustedSocket) {
			t.Fatalf("err = %v, want ErrUntrustedSocket", err)
		}
	})
	t.Run("a regular file is not a socket", func(t *testing.T) {
		p := trustSock(t, 0o700, 0o600)
		f := filepath.Join(filepath.Dir(p), "plain")
		if err := os.WriteFile(f, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := DialTrusted(f); !errors.Is(err, ErrUntrustedSocket) {
			t.Fatalf("err = %v, want ErrUntrustedSocket", err)
		}
	})
	t.Run("missing socket stays retryable", func(t *testing.T) {
		p := trustSock(t, 0o700, 0o600)
		_, err := DialTrusted(filepath.Join(filepath.Dir(p), "absent.sock"))
		if err == nil || errors.Is(err, ErrUntrustedSocket) {
			t.Fatalf("err = %v, want a plain not-found error", err)
		}
	})
	t.Run("peer of another uid", func(t *testing.T) {
		p := trustSock(t, 0o700, 0o600)
		old := peerUID
		peerUID = func(*net.UnixConn) (uint32, error) { return uint32(os.Geteuid()) + 1, nil } //nolint:gosec
		t.Cleanup(func() { peerUID = old })
		if _, err := DialTrusted(p); !errors.Is(err, ErrUntrustedSocket) {
			t.Fatalf("err = %v, want ErrUntrustedSocket", err)
		}
	})
	t.Run("peer credential unavailable fails closed", func(t *testing.T) {
		p := trustSock(t, 0o700, 0o600)
		old := peerUID
		peerUID = func(*net.UnixConn) (uint32, error) { return 0, errors.New("no cred") }
		t.Cleanup(func() { peerUID = old })
		if _, err := DialTrusted(p); !errors.Is(err, ErrUntrustedSocket) {
			t.Fatalf("err = %v, want ErrUntrustedSocket", err)
		}
	})
}

// The kernel really reports our own uid for a peer we started.
func TestPeerUIDOfReportsOwnUID(t *testing.T) {
	p := trustSock(t, 0o700, 0o600)
	conn, err := net.Dial("unix", p)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close() //nolint:errcheck
	uid, err := peerUIDOf(conn.(*net.UnixConn))
	if err != nil {
		t.Skipf("no peer credential on this platform: %v", err)
	}
	if uid != uint32(os.Geteuid()) { //nolint:gosec
		t.Fatalf("peer uid %d, want %d", uid, os.Geteuid())
	}
}
