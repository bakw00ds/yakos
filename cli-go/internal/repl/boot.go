package repl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/loopbackowner"
)

// DefaultAddr is the console daemon's default loopback bind.
const DefaultAddr = "127.0.0.1:7890"

// Errors of Connect. Their text is fixed: it never names a path, a token or the
// underlying system error.
var (
	ErrNotLoopback  = errors.New("the REPL talks to a local daemon only; use a loopback --console-addr (the console token is never sent to another host)")
	ErrDaemonStart  = errors.New("the yakOS daemon is not running and could not be started; run `yakos serve` in another terminal, then retry")
	ErrDaemonSlow   = errors.New("the yakOS daemon did not come up in time; run `yakos serve` in another terminal, then retry")
	ErrTokenMissing = errors.New("no console token found: the daemon creates it on first start; run `yakos serve` once, then retry")
	ErrTokenBad     = errors.New("the console token file is unreadable or empty; restart the daemon (`yakos serve stop`, then `yakos serve`) to recreate it")
	ErrDaemonAuth   = errors.New("the daemon rejected the console token; restart it (`yakos serve stop`, then `yakos serve`) and retry")
)

// Boot describes how to reach (and if needed start) the daemon.
type Boot struct {
	Addr     string // host:port of the console; default DefaultAddr
	StateDir string // holds console-token and loopback-operator-id

	// Probe reports whether something listens on addr. Default: a TCP dial.
	Probe func(addr string) bool
	// StartDaemon launches the daemon detached. Nil: the REPL cannot auto-start.
	StartDaemon func() error
	// WaitUp blocks until addr accepts connections or the wait ends.
	WaitUp func(addr string) bool

	Out io.Writer // progress line ("starting the yakOS daemon...")
}

// Connect finds the daemon (starting it when absent), reads the bearer token
// and returns a client. The error is always one of the fixed errors above.
func Connect(ctx context.Context, b Boot) (*Client, error) {
	addr := b.Addr
	if addr == "" {
		addr = DefaultAddr
	}
	if !isLoopbackAddr(addr) {
		return nil, ErrNotLoopback
	}
	probe := b.Probe
	if probe == nil {
		probe = tcpProbe
	}
	if !probe(addr) {
		if b.StartDaemon == nil {
			return nil, ErrDaemonStart
		}
		if b.Out != nil {
			_, _ = fmt.Fprintln(b.Out, "starting the yakOS daemon...")
		}
		if err := b.StartDaemon(); err != nil {
			return nil, ErrDaemonStart
		}
		wait := b.WaitUp
		if wait == nil {
			wait = func(a string) bool { return pollTCP(a, 10*time.Second) }
		}
		if !wait(addr) {
			return nil, ErrDaemonSlow
		}
	}
	tok, err := readToken(b.StateDir)
	if err != nil {
		return nil, err
	}
	c := &Client{
		Base:       "http://" + addr,
		Token:      tok,
		OperatorID: loopbackowner.LoadOrCreate(b.StateDir),
		HTTP:       newHTTP(),
	}
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := c.Ping(pctx); err != nil {
		var ae *APIError
		if errors.As(err, &ae) && (ae.Status == 401 || ae.Status == 403) {
			return nil, ErrDaemonAuth
		}
		return nil, ErrDaemonSlow
	}
	return c, nil
}

// readToken reads <stateDir>/console-token. Unlike consoleui.LoadOrCreateToken
// it never creates one: a missing token means the daemon never ran.
func readToken(stateDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(stateDir, "console-token"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrTokenMissing
		}
		return "", ErrTokenBad
	}
	tok := strings.TrimSpace(string(data))
	if tok == "" || len(tok) > 256 || strings.ContainsAny(tok, " \t\r\n") {
		return "", ErrTokenBad
	}
	return tok, nil
}

func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func tcpProbe(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func pollTCP(addr string, d time.Duration) bool {
	end := time.Now().Add(d)
	for {
		if tcpProbe(addr) {
			return true
		}
		if time.Now().After(end) {
			return false
		}
		time.Sleep(200 * time.Millisecond)
	}
}
