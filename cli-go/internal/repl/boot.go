package repl

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
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
	ErrNotLoopback   = errors.New("the REPL talks to a local daemon only; use a loopback --console-addr (the console token is never sent to another host)")
	ErrDaemonStart   = errors.New("the yakOS daemon is not running and could not be started; run `yakos serve` in another terminal, then retry")
	ErrDaemonSlow    = errors.New("the yakOS daemon did not come up in time; run `yakos serve` in another terminal, then retry")
	ErrTokenMissing  = errors.New("no console token found: the daemon creates it on first start; run `yakos serve` once, then retry")
	ErrTokenBad      = errors.New("the console token file is unreadable or empty; restart the daemon (`yakos serve stop`, then `yakos serve`) to recreate it")
	ErrDaemonForeign = errors.New("the process on the console address is not this project's yakOS daemon, so the console token was not sent; run `yakos serve stop` in the project that started the daemon (or free the port), then retry")
	ErrDaemonStale   = errors.New("the running yakOS daemon is from another build; restart it (`yakos serve stop`, then `yakos serve`) and retry")
	ErrDaemonAuth    = errors.New("the daemon rejected the console token; restart it (`yakos serve stop`, then `yakos serve`) and retry")
)

// ErrConsoleUnbound is the verdict for a daemon of this project that is running
// but holds no console: it could not bind Port because another process has it.
// It matches ErrDaemonForeign (errors.Is) since the token is not sent either.
type ErrConsoleUnbound struct{ Port string }

func (e *ErrConsoleUnbound) Error() string {
	return fmt.Sprintf("this project's yakOS daemon is running but could not bind the console port %s (another process holds it), so the console token was not sent; free the port or choose another with --console-addr, run `yakos serve stop`, then retry", e.Port)
}

// Is makes the error match ErrDaemonForeign.
func (e *ErrConsoleUnbound) Is(target error) bool { return target == ErrDaemonForeign }

// InstanceNonceMax bounds the /api/instance response a client will read.
const InstanceNonceMax = 512

// FetchInstance reads the per-boot instance nonce the console serves, without
// a token, at GET http://addr/api/instance. It sends nothing secret: no
// credentials, no cookies, no proxy, no redirects. A hostile listener learns
// only that a client probed it.
func FetchInstance(ctx context.Context, addr string) (string, error) {
	if !isLoopbackAddr(addr) {
		return "", ErrNotLoopback
	}
	hc := &http.Client{
		Timeout:       3 * time.Second,
		Transport:     &http.Transport{Proxy: nil, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/api/instance", nil)
	if err != nil {
		return "", err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("instance: status %d", resp.StatusCode)
	}
	var out struct {
		Instance string `json:"instance"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, InstanceNonceMax)).Decode(&out); err != nil {
		return "", fmt.Errorf("instance: %w", err)
	}
	if out.Instance == "" {
		return "", errors.New("instance: empty")
	}
	return out.Instance, nil
}

// SameInstance compares two nonces in constant time.
func SameInstance(a, b string) bool {
	return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

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

	// Verify proves, over the owner-only unix socket and before the token is
	// read, that the daemon on addr is this workspace's daemon, built from this
	// binary. On success it returns the daemon-reported bound address (an
	// ip:port loopback literal, never a hostname): the only address the nonce
	// and the token are then sent to. Failures are ErrDaemonForeign or
	// ErrDaemonStale. Nil Verify fails closed (ErrDaemonForeign): the token is
	// never sent unverified.
	Verify func(ctx context.Context, addr string) (string, error)

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
	if b.Verify == nil {
		return nil, ErrDaemonForeign
	}
	vctx, vcancel := context.WithTimeout(ctx, 5*time.Second)
	bound, verr := b.Verify(vctx, addr)
	vcancel()
	if verr != nil {
		if errors.Is(verr, ErrDaemonStale) {
			return nil, ErrDaemonStale
		}
		var cu *ErrConsoleUnbound
		if errors.As(verr, &cu) {
			return nil, cu
		}
		return nil, ErrDaemonForeign
	}
	if !isLoopbackIPAddr(bound) {
		return nil, ErrDaemonForeign
	}
	tok, err := readToken(b.StateDir)
	if err != nil {
		return nil, err
	}
	c := &Client{
		Base:       "http://" + bound,
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

// isLoopbackIPAddr reports whether addr is host:port with host a loopback IP
// literal (no hostname, no wildcard).
func isLoopbackIPAddr(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return false
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
