package serve

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/mcpserver"
)

// S-2 R15 (s2-daemon-security-review-2026-09-21.md): a failed MCP transport
// start was written to a channel drained only at shutdown, so a hostile
// process squatting 127.0.0.1:7894 made the daemon lose its MCP surface
// silently while the squatter received MCP clients' bearer tokens.

type warnRecorder struct {
	mu   sync.Mutex
	msgs []string
}

func (w *warnRecorder) warn(format string, args ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.msgs = append(w.msgs, fmt.Sprintf(format, args...))
}

func newMCPHTTP(addr, token string) *mcpserver.HTTPServer {
	return mcpserver.NewHTTPServer(mcpserver.HTTPConfig{Addr: addr, WriteToken: token})
}

func TestStartMCPHTTP_PortSquatted_WarnsLoudlyAtStartup(t *testing.T) {
	squatter, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer squatter.Close()
	addr := squatter.Addr().String()

	rec := &warnRecorder{}
	errCh := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := startMCPHTTP(ctx, newMCPHTTP(addr, "tok"), addr, errCh, rec.warn); err != nil {
		t.Fatalf("a bind failure is a loud warning, not a fatal error; got %v", err)
	}
	if len(rec.msgs) != 1 {
		t.Fatalf("got %d warnings %v; want exactly 1 at startup (not at shutdown)", len(rec.msgs), rec.msgs)
	}
	if !strings.Contains(rec.msgs[0], addr) || !strings.Contains(rec.msgs[0], "another process") {
		t.Errorf("warning %q must name the address and the squatting risk", rec.msgs[0])
	}
	// The result channel must not be left dangling for the shutdown drain.
	select {
	case _, open := <-errCh:
		if open {
			t.Error("errCh delivered a value; a failed start must close it")
		}
	case <-time.After(time.Second):
		t.Error("errCh neither closed nor written after a failed start")
	}
}

func TestStartMCPHTTP_NoWriteToken_IsFatal(t *testing.T) {
	rec := &warnRecorder{}
	errCh := make(chan error, 1)
	err := startMCPHTTP(context.Background(), newMCPHTTP("127.0.0.1:0", ""), "127.0.0.1:0", errCh, rec.warn)
	if !errors.Is(err, mcpserver.ErrNoWriteToken) {
		t.Fatalf("err=%v; want mcpserver.ErrNoWriteToken (an unauthenticated dispatch endpoint must abort startup)", err)
	}
}

func TestStartMCPHTTP_Success_ServesUntilCancelled(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	probe.Close()

	rec := &warnRecorder{}
	errCh := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	if err := startMCPHTTP(ctx, newMCPHTTP(addr, "tok"), addr, errCh, rec.warn); err != nil {
		t.Fatalf("startMCPHTTP: %v", err)
	}
	if len(rec.msgs) != 0 {
		t.Fatalf("unexpected warnings on a clean start: %v", rec.msgs)
	}
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("MCP listener not accepting on %s: %v", addr, err)
	}
	c.Close()
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("clean shutdown returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve goroutine did not exit after cancel")
	}
}
