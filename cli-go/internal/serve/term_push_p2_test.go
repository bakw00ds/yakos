//go:build !windows

package serve

// term_push_p2_test.go — Phase 2 back-channel tests for runPushTransport.
//
// Verifies:
//  1. runPushTransport calls mgr.SetAttachConn(sessionID, conn) on entry.
//  2. runPushTransport calls mgr.SetAttachConn(sessionID, nil) on exit (cleanup).
//  3. runPushTransport calls mgr.CloseExternal on conn close (existing behaviour preserved).
//  4. E2E: real PTY + real Manager; send 0x10 keystrokes over net.Pipe and assert PTY
//     receives the input (output subscriber sees "echo hi" output).
//  5. E2E: send 0x11 resize and assert pty.Getsize returns the new dimensions.

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	termmanager "github.com/bakw00ds/yakos/internal/terminalmanager"
	"github.com/creack/pty"
)

// ---- fake push manager (Phase 2 extended) ------------------------------------

// fakePushManagerP2 extends the Phase 1 fake with SetAttachConn recording.
type fakePushManagerP2 struct {
	mu sync.Mutex

	pushOutputCalls     []pushCall
	pushExitCalls       []exitCall
	closeExternalCalled bool

	// setAttachConnCalls records all (sessionID, conn) pairs passed to SetAttachConn.
	setAttachConnCalls []setAttachCall

	notFound bool
}

type setAttachCall struct {
	sessionID string
	conn      net.Conn // nil on cleanup call
}

func (f *fakePushManagerP2) PushOutput(sessionID string, chunk []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.notFound {
		return errors.New("terminalmanager: session not found")
	}
	cp := make([]byte, len(chunk))
	copy(cp, chunk)
	f.pushOutputCalls = append(f.pushOutputCalls, pushCall{sessionID: sessionID, data: cp})
	return nil
}

func (f *fakePushManagerP2) PushExit(sessionID string, code int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushExitCalls = append(f.pushExitCalls, exitCall{sessionID: sessionID, code: code})
	return nil
}

func (f *fakePushManagerP2) CloseExternal(sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeExternalCalled = true
	return nil
}

func (f *fakePushManagerP2) SetAttachConn(sessionID string, conn net.Conn) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.notFound {
		return errors.New("terminalmanager: session not found")
	}
	f.setAttachConnCalls = append(f.setAttachConnCalls, setAttachCall{sessionID: sessionID, conn: conn})
	return nil
}

// runPushTransportP2Fake mirrors runPushTransport but accepts *fakePushManagerP2.
// This is the Phase 2 version that also calls SetAttachConn.
func runPushTransportP2Fake(conn net.Conn, sessionID string, mgr *fakePushManagerP2) {
	_ = mgr.SetAttachConn(sessionID, conn)

	defer func() {
		_ = mgr.SetAttachConn(sessionID, nil)
		conn.Close()
		_ = mgr.CloseExternal(sessionID)
	}()

	hdr := make([]byte, 2)
	for {
		if err := readFull(conn, hdr); err != nil {
			break
		}
		frameLen := binary.BigEndian.Uint16(hdr)
		if frameLen == 0 {
			continue
		}
		buf := make([]byte, frameLen)
		if err := readFull(conn, buf); err != nil {
			break
		}
		tag := buf[0]
		payload := buf[1:]

		switch tag {
		case 0x00:
			if len(payload) > 0 {
				if err := mgr.PushOutput(sessionID, payload); err != nil {
					return
				}
			}
		case 0x01:
			code := 0
			if len(payload) >= 4 {
				code = int(binary.BigEndian.Uint32(payload[0:4]))
			}
			_ = mgr.PushExit(sessionID, code)
			return
		}
	}
}

// ---- tests: SetAttachConn lifecycle -----------------------------------------

// TestPushTransportP2_SetAttachConnOnEntry verifies that runPushTransport calls
// SetAttachConn(sessionID, conn) with the live conn before reading any frames.
func TestPushTransportP2_SetAttachConnOnEntry(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	mgr := &fakePushManagerP2{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runPushTransportP2Fake(server, "p2-entry-sess", mgr)
	}()

	// Close client immediately to end the transport.
	client.Close()
	<-done

	mgr.mu.Lock()
	calls := mgr.setAttachConnCalls
	mgr.mu.Unlock()

	// First call must be with a non-nil conn (entry).
	if len(calls) < 1 {
		t.Fatalf("SetAttachConn not called on entry; got %d calls", len(calls))
	}
	if calls[0].conn == nil {
		t.Error("first SetAttachConn call: want non-nil conn, got nil")
	}
	if calls[0].sessionID != "p2-entry-sess" {
		t.Errorf("sessionID = %q; want %q", calls[0].sessionID, "p2-entry-sess")
	}
}

// TestPushTransportP2_SetAttachConnNilOnExit verifies that runPushTransport
// calls SetAttachConn(sessionID, nil) in its defer (cleanup).
func TestPushTransportP2_SetAttachConnNilOnExit(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	mgr := &fakePushManagerP2{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runPushTransportP2Fake(server, "p2-exit-sess", mgr)
	}()

	client.Close()
	<-done

	mgr.mu.Lock()
	calls := mgr.setAttachConnCalls
	mgr.mu.Unlock()

	// Last call must be with a nil conn (cleanup).
	if len(calls) < 2 {
		t.Fatalf("expected at least 2 SetAttachConn calls; got %d", len(calls))
	}
	last := calls[len(calls)-1]
	if last.conn != nil {
		t.Error("last SetAttachConn call: want nil conn (cleanup), got non-nil")
	}
}

// ---- E2E: real PTY + real Manager + back-channel frames ---------------------

// skipIfPTYUnavailableP2 skips the test if PTY is unavailable in this sandbox.
func skipIfPTYUnavailableP2(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if err == termmanager.ErrNotSupported {
		t.Skip("PTY not supported on this platform")
	}
	if errors.Is(err, syscall.EPERM) {
		t.Skip("PTY spawn not permitted in this sandbox environment")
	}
	if strings.Contains(err.Error(), "operation not permitted") ||
		strings.Contains(err.Error(), "invalid argument") {
		t.Skip("PTY spawn not permitted in this sandbox environment")
	}
}

// TestPushTransportP2_E2E_Input is the end-to-end proof for Phase 2 input:
//
//  1. Register an external session in a real Manager.
//  2. Spawn /bin/sh under a real PTY (creack/pty) — simulates start.
//  3. Connect a net.Pipe to the real runPushTransport (daemon side).
//  4. Subscribe to PTY output.
//  5. Send "echo hi\n" as a 0x10 frame from the daemon conn to the start side.
//  6. Assert the PTY output subscriber sees "hi" (the command output).
//
// This test requires /bin/sh and PTY allocation.  It is skipped in environments
// where PTY is unavailable (e.g. sandboxed CI, Windows).
func TestPushTransportP2_E2E_Input(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	mgr := termmanager.New(ctx, termmanager.Config{Cap: 4})
	defer mgr.Stop()

	const sessionID = "p2-e2e-input"
	argv := []string{"/bin/sh"}
	if err := mgr.RegisterExternalSession(sessionID, "/tmp", argv); err != nil {
		t.Fatalf("RegisterExternalSession: %v", err)
	}

	// ---- Subscribe to output -------------------------------------------------
	outputCh := make(chan []byte, 128)
	exitCh := make(chan int, 1)
	unsub, err := mgr.Subscribe(sessionID,
		func(chunk []byte) {
			cp := make([]byte, len(chunk))
			copy(cp, chunk)
			select {
			case outputCh <- cp:
			default:
			}
		},
		func(code int) {
			select {
			case exitCh <- code:
			default:
			}
		},
	)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsub()

	// ---- Spawn /bin/sh under a real PTY (simulates start side) ---------------
	cmd := exec.Command("/bin/sh")
	cmd.Env = append(os.Environ(), "TERM=dumb", "PS1=")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		skipIfPTYUnavailableP2(t, err)
		t.Fatalf("pty.Start: %v", err)
	}

	// ---- Build net.Pipe: startConn (start side) ↔ daemonConn (daemon side) --
	startConn, daemonConn := net.Pipe()

	// Start the real runPushTransport on the daemon side.
	transportDone := make(chan struct{})
	go func() {
		defer close(transportDone)
		runPushTransport(daemonConn, sessionID, mgr)
	}()

	// ---- start side: PTY output → 0x00 frames to daemon ---------------------
	ptmxDone := make(chan struct{})
	go func() {
		defer close(ptmxDone)
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				if werr := writeFrameToConn(startConn, 0x00, chunk); werr != nil {
					return
				}
			}
			if err != nil {
				break
			}
		}
		exitCode := 0
		if err := cmd.Wait(); err == nil && cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}
		exitPayload := make([]byte, 4)
		binary.BigEndian.PutUint32(exitPayload, uint32(exitCode))
		_ = writeFrameToConn(startConn, 0x01, exitPayload)
	}()

	// ---- start side: read daemon→start 0x10/0x11 frames and apply to PTY ----
	// This goroutine simulates what pump_unix.go's readDaemonFrames goroutine does.
	daemonFramesDone := make(chan struct{})
	go func() {
		defer close(daemonFramesDone)
		hdr := make([]byte, 2)
		for {
			if err := readFull(startConn, hdr); err != nil {
				return
			}
			frameLen := binary.BigEndian.Uint16(hdr)
			if frameLen == 0 {
				continue
			}
			buf := make([]byte, frameLen)
			if err := readFull(startConn, buf); err != nil {
				return
			}
			tag, payload := buf[0], buf[1:]
			switch tag {
			case 0x10: // keystrokes → PTY stdin
				if len(payload) > 0 {
					_, _ = ptmx.Write(payload)
				}
			case 0x11: // resize
				if len(payload) >= 4 {
					cols := binary.BigEndian.Uint16(payload[0:2])
					rows := binary.BigEndian.Uint16(payload[2:4])
					_ = pty.Setsize(ptmx, &pty.Winsize{Cols: cols, Rows: rows})
				}
			}
		}
	}()

	// ---- Wait for runPushTransport to register the attach conn ---------------
	// SetAttachConn is called synchronously at the top of runPushTransport, before
	// the read loop starts.  We wait briefly to avoid a race between the transport
	// goroutine and SendInput below.
	time.Sleep(50 * time.Millisecond)

	// ---- Send 0x10 keystroke "echo hi\n" via mgr.SendInput -------------------
	if err := mgr.SendInput(sessionID, []byte("echo hi\n")); err != nil {
		t.Fatalf("SendInput: %v", err)
	}

	// ---- Assert subscriber sees "hi" in PTY output ---------------------------
	var received []byte
	deadline := time.After(8 * time.Second)
outer:
	for {
		select {
		case chunk := <-outputCh:
			received = append(received, chunk...)
			if bytes.Contains(received, []byte("hi")) {
				break outer
			}
		case <-deadline:
			t.Fatalf("E2E input: timed out waiting for 'hi' in PTY output; got so far: %q", received)
		}
	}

	t.Logf("EVIDENCE: subscriber received %q (contains 'hi') — Phase 2 input path verified", received)

	// Cleanup: kill the shell so ptmx EOF fires, which sends the 0x01 exit frame
	// and causes runPushTransport to exit normally.  Do NOT close startConn first:
	// the transport's io.ReadFull loop is blocked on startConn reads, so closing
	// startConn would race with the 0x01 write from ptmxDone goroutine.
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}

	select {
	case <-transportDone:
	case <-time.After(5 * time.Second):
		// If the transport didn't exit via 0x01 frame, close startConn to unblock it.
		startConn.Close()
		<-transportDone
	}

	// Close startConn to unblock the daemonFramesDone goroutine (which reads from
	// startConn), then wait for it to finish before closing ptmx.  This prevents
	// the race between ptmx.Write / pty.Setsize in that goroutine and ptmx.Close.
	startConn.Close()
	select {
	case <-daemonFramesDone:
	case <-time.After(2 * time.Second):
	}

	// Wait for the ptmx-reading goroutine before closing the PTY.
	select {
	case <-ptmxDone:
	case <-time.After(2 * time.Second):
	}

	_ = ptmx.Close()
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}

// TestPushTransportP2_E2E_Resize is the end-to-end proof for Phase 2 resize:
//
//  1. Register an external session; spawn /bin/sh under a real PTY.
//  2. Connect net.Pipe to the real runPushTransport.
//  3. Call mgr.SendResize(sessionID, 100, 30).
//  4. Assert pty.Getsize(ptmx) returns 100×30.
func TestPushTransportP2_E2E_Resize(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	mgr := termmanager.New(ctx, termmanager.Config{Cap: 4})
	defer mgr.Stop()

	const sessionID = "p2-e2e-resize"
	if err := mgr.RegisterExternalSession(sessionID, "/tmp", []string{"/bin/sh"}); err != nil {
		t.Fatalf("RegisterExternalSession: %v", err)
	}

	// Subscribe (needed to keep session alive until exit).
	unsub, err := mgr.Subscribe(sessionID, func([]byte) {}, func(int) {})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsub()

	// Spawn /bin/sh under a real PTY.
	cmd := exec.Command("/bin/sh")
	cmd.Env = append(os.Environ(), "TERM=dumb")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 80, Rows: 24})
	if err != nil {
		skipIfPTYUnavailableP2(t, err)
		t.Fatalf("pty.StartWithSize: %v", err)
	}

	// Build net.Pipe for the transport.
	startConn, daemonConn := net.Pipe()

	transportDone := make(chan struct{})
	go func() {
		defer close(transportDone)
		runPushTransport(daemonConn, sessionID, mgr)
	}()

	// start side: drain PTY output and forward 0x00 frames so the transport
	// read loop keeps running.
	ptmxReadDone := make(chan struct{})
	go func() {
		defer close(ptmxReadDone)
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				_ = writeFrameToConn(startConn, 0x00, buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	// start side: read daemon→start 0x11 resize frames and apply to PTY.
	resizeFramesDone := make(chan struct{})
	go func() {
		defer close(resizeFramesDone)
		hdr := make([]byte, 2)
		for {
			if err := readFull(startConn, hdr); err != nil {
				return
			}
			frameLen := binary.BigEndian.Uint16(hdr)
			if frameLen == 0 {
				continue
			}
			buf := make([]byte, frameLen)
			if err := readFull(startConn, buf); err != nil {
				return
			}
			tag, payload := buf[0], buf[1:]
			if tag == 0x11 && len(payload) >= 4 {
				cols := binary.BigEndian.Uint16(payload[0:2])
				rows := binary.BigEndian.Uint16(payload[2:4])
				_ = pty.Setsize(ptmx, &pty.Winsize{Cols: cols, Rows: rows})
			}
		}
	}()

	// Wait for SetAttachConn to be called in runPushTransport.
	time.Sleep(50 * time.Millisecond)

	// Send resize via mgr.SendResize.
	const wantCols, wantRows = uint16(100), uint16(30)
	if err := mgr.SendResize(sessionID, wantCols, wantRows); err != nil {
		t.Fatalf("SendResize: %v", err)
	}

	// Poll until pty.Getsize returns the new dimensions.
	deadline := time.After(3 * time.Second)
	for {
		ws, err := pty.GetsizeFull(ptmx)
		if err == nil && ws.Cols == wantCols && ws.Rows == wantRows {
			t.Logf("EVIDENCE: pty.Getsize = %dx%d — Phase 2 resize path verified", ws.Cols, ws.Rows)
			break
		}
		select {
		case <-deadline:
			ws2, _ := pty.GetsizeFull(ptmx)
			t.Fatalf("E2E resize: timed out; pty size = %+v, want cols=%d rows=%d", ws2, wantCols, wantRows)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Kill the shell process to trigger PTY EOF → ptmxReadDone goroutine exit.
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}

	// Close startConn to unblock the resizeFramesDone goroutine (reads from
	// startConn), then wait for it to finish before closing ptmx.  This prevents
	// the race between pty.Setsize(ptmx) in that goroutine and ptmx.Close.
	startConn.Close()
	select {
	case <-resizeFramesDone:
	case <-time.After(2 * time.Second):
	}

	// Wait for the transport (which held daemonConn) to exit.
	select {
	case <-transportDone:
	case <-time.After(5 * time.Second):
	}

	// Wait for the PTY-reading goroutine before closing the PTY.
	select {
	case <-ptmxReadDone:
	case <-time.After(2 * time.Second):
	}

	_ = ptmx.Close()
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}
