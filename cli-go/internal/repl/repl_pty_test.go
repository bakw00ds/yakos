//go:build !windows

package repl

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func openPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	m, s, err := pty.Open()
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	t.Cleanup(func() { _ = s.Close(); _ = m.Close() })
	go func() { _, _ = io.Copy(io.Discard, m) }() // the terminal's echo and output
	return m, s
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	end := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestAttachOwnsTheTerminalExclusively drives a fake native child through a
// pty. While it runs, the REPL must not read the terminal (every byte typed
// reaches the child, in order); when it exits leaving the tty raw and echo off,
// the REPL restores the mode and drops whatever the child did not consume, so
// nothing typed during the attach is sent as a chat message.
func TestAttachOwnsTheTerminalExclusively(t *testing.T) {
	master, slave := openPTY(t)
	st0, err := term.GetState(int(slave.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ready, got := filepath.Join(dir, "ready"), filepath.Join(dir, "got")

	d := newFakeDaemon(t)
	out := &syncBuf{}
	cfg := Config{Client: d.client(), In: slave, Out: out, Terminal: slave, NewID: seqIDs(),
		Sleep: func(time.Duration) {}}
	cfg.Attach = func(ctx context.Context, rt string) error {
		// Raw, no echo, reads exactly 12 bytes, lingers, exits without restoring.
		script := `stty raw -echo; : > "$READY"; dd bs=1 count=12 of="$GOT" 2>/dev/null; sleep 0.4`
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
		cmd.Env = append(os.Environ(), "READY="+ready, "GOT="+got)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
		return cmd.Run()
	}
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go func() { done <- New(cfg).Run(ctx) }()

	if _, err := master.WriteString("/attach claude\n"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the child to be ready", func() bool { _, err := os.Stat(ready); return err == nil })
	// 12 bytes for the child, then a line it never reads.
	if _, err := master.WriteString("sk-FAKE-KEY1" + "leftover-line\r"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the REPL to resume", func() bool { return strings.Contains(out.String(), "back in the yakOS REPL") })

	if b, _ := os.ReadFile(got); string(b) != "sk-FAKE-KEY1" {
		t.Errorf("child received %q, want every byte typed during the attach (sk-FAKE-KEY1)", b)
	}
	if st1, err := term.GetState(int(slave.Fd())); err != nil || !reflect.DeepEqual(st0, st1) {
		t.Errorf("terminal mode not restored after the child left it raw: %v", err)
	}
	if _, err := master.WriteString("/exit\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("REPL did not exit on /exit")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.dispatches) != 0 {
		t.Fatalf("input typed during the attach was sent as chat: %+v", d.dispatches)
	}
}

func TestGuardTerminalRestoresOnPanicAndError(t *testing.T) {
	_, slave := openPTY(t)
	fd := int(slave.Fd())
	st0, err := term.GetState(fd)
	if err != nil {
		t.Fatal(err)
	}
	same := func() bool { st, err := term.GetState(fd); return err == nil && reflect.DeepEqual(st0, st) }

	if _, err := term.MakeRaw(fd); err != nil {
		t.Fatal(err)
	}
	if same() {
		t.Fatal("MakeRaw changed nothing; the test cannot tell")
	}
	_ = term.Restore(fd, st0)

	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic must propagate after the restore")
			}
		}()
		_ = GuardTerminal(fd, func() error { _, _ = term.MakeRaw(fd); panic("child supervisor crashed") })
	}()
	if !same() {
		t.Error("terminal left raw after a panic")
	}

	werr := GuardTerminal(fd, func() error { _, _ = term.MakeRaw(fd); return io.ErrUnexpectedEOF })
	if werr != io.ErrUnexpectedEOF || !same() {
		t.Errorf("err=%v restored=%v", werr, same())
	}
}
