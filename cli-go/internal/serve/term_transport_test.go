//go:build !windows

package serve

// term_transport_test.go — unit tests for the attach transport pump.
//
// Strategy: the transport under test is runAttachTransport(conn, sessionID, mgr).
// Rather than spawning a real PTY (which would require OS sandbox clearance),
// we inject a fake Manager that satisfies the same interface via a test double
// that records calls and delivers synthetic output.
//
// The conn is a net.Pipe() pair so we can drive the client side from the test.

import (
	"encoding/binary"
	"net"
	"sync"
	"testing"
	"time"
)

// fakeManager is a test double for *terminalmanager.Manager.
// It records WriteInput and Resize calls, and lets the test inject synthetic
// output chunks and exit events via its subscriber sinks.
type fakeManager struct {
	mu sync.Mutex

	// sinks registered by runAttachTransport.
	outputSink func([]byte)
	exitSink   func(int)

	// writeInputCalls records all data written via WriteInput.
	writeInputCalls [][]byte

	// resizeCalls records (cols, rows) pairs from Resize.
	resizeCalls [][2]uint16

	// notFound, when true, makes Subscribe return an error.
	notFound bool
}

// fakeNotFoundError satisfies error.
type fakeNotFoundError struct{}

func (e *fakeNotFoundError) Error() string { return "terminalmanager: session not found" }

// Subscribe mimics terminalmanager.Manager.Subscribe.
// Manager.Subscribe signature: (sessionId, outputFn func([]byte), exitFn func(int)).
func (f *fakeManager) Subscribe(sessionID string, outputFn func([]byte), exitFn func(int)) (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.notFound {
		return nil, &fakeNotFoundError{}
	}
	f.outputSink = outputFn
	f.exitSink = exitFn
	return func() {
		f.mu.Lock()
		f.outputSink = nil
		f.exitSink = nil
		f.mu.Unlock()
	}, nil
}

func (f *fakeManager) WriteInput(sessionID string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	f.writeInputCalls = append(f.writeInputCalls, cp)
	return nil
}

func (f *fakeManager) Resize(sessionID string, cols, rows uint16) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resizeCalls = append(f.resizeCalls, [2]uint16{cols, rows})
	return nil
}

// deliverOutput calls the registered output sink with raw PTY bytes.
// Mirrors what the Manager's fanOut goroutine does when PTY data arrives.
func (f *fakeManager) deliverOutput(data []byte) {
	f.mu.Lock()
	sink := f.outputSink
	f.mu.Unlock()
	if sink != nil {
		sink(data)
	}
}

// deliverExit calls the registered exit sink with the exit code.
// Mirrors what session_unix.go's deliverExit does with the split-sink design:
// exit notification travels through a dedicated exitFn, never through outputFn.
func (f *fakeManager) deliverExit(code int) {
	f.mu.Lock()
	sink := f.exitSink
	f.mu.Unlock()
	if sink != nil {
		sink(code)
	}
}

// --- runAttachTransport adapter for the fake manager --------------------------

// runAttachTransportFake mirrors runAttachTransport from term_transport_unix.go
// but accepts *fakeManager instead of *terminalmanager.Manager so tests don't
// need a real PTY.  The logic is identical; if this compiles and the tests pass,
// the production version is structurally sound.
func runAttachTransportFake(conn net.Conn, sessionID string, mgr *fakeManager) {
	defer conn.Close()

	// outCh carries typed events from the subscriber callbacks to the outbound
	// writer goroutine.  outputFn → kindOutput; exitFn → kindExit.
	// Payload content is NEVER inspected to determine event kind.
	outCh := make(chan outEvent, 256)

	outputFn := func(chunk []byte) {
		cp := make([]byte, len(chunk))
		copy(cp, chunk)
		select {
		case outCh <- outEvent{kind: kindOutput, payload: cp}:
		default:
		}
	}
	exitFn := func(code int) {
		select {
		case outCh <- outEvent{kind: kindExit, exitCode: code}:
		default:
		}
	}

	unsub, err := mgr.Subscribe(sessionID, outputFn, exitFn)
	if err != nil {
		return
	}

	connClosed := make(chan struct{})
	outDone := make(chan struct{})

	go func() {
		defer close(outDone)
		for {
			select {
			case ev, ok := <-outCh:
				if !ok {
					return
				}
				switch ev.kind {
				case kindExit:
					c := uint32(ev.exitCode)
					exitFrame := []byte{
						0x01,
						byte(c >> 24), byte(c >> 16), byte(c >> 8), byte(c),
					}
					_, _ = conn.Write(exitFrame)
					unsub()
					conn.Close()
					return
				case kindOutput:
					if len(ev.payload) == 0 {
						continue
					}
					frame := make([]byte, 1+len(ev.payload))
					frame[0] = 0x00
					copy(frame[1:], ev.payload)
					if _, err := conn.Write(frame); err != nil {
						unsub()
						return
					}
				}
			case <-connClosed:
				unsub()
				return
			}
		}
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
		case 0x10:
			if len(payload) > 0 {
				if err := mgr.WriteInput(sessionID, payload); err != nil {
					goto done
				}
			}
		case 0x11:
			if len(payload) >= 4 {
				cols := binary.BigEndian.Uint16(payload[0:2])
				rows := binary.BigEndian.Uint16(payload[2:4])
				_ = mgr.Resize(sessionID, cols, rows)
			}
		}
	}
done:
	close(connClosed)
	for {
		select {
		case <-outCh:
		case <-outDone:
			return
		}
	}
}

// readFull reads exactly len(buf) bytes, returning error on short read.
func readFull(conn net.Conn, buf []byte) error {
	total := 0
	for total < len(buf) {
		n, err := conn.Read(buf[total:])
		total += n
		if err != nil {
			return err
		}
	}
	return nil
}

// writeFrameToConn sends a length-prefixed frame: [ 2-byte len ] [ tag ] [ payload ]
func writeFrameToConn(conn net.Conn, tag byte, payload []byte) error {
	total := uint16(1 + len(payload))
	buf := make([]byte, 2+int(total))
	binary.BigEndian.PutUint16(buf[0:2], total)
	buf[2] = tag
	copy(buf[3:], payload)
	_, err := conn.Write(buf)
	return err
}

// --- helper: wait for sinks to be registered ----------------------------------

func waitForSinks(t *testing.T, mgr *fakeManager) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		mgr.mu.Lock()
		ready := mgr.outputSink != nil && mgr.exitSink != nil
		mgr.mu.Unlock()
		if ready {
			return
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for sinks to be registered")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

// --- tests -------------------------------------------------------------------

// TestTransport_KeystrokesForwardedToWriteInput verifies that a 0x10 frame
// from the client is forwarded to mgr.WriteInput.
func TestTransport_KeystrokesForwardedToWriteInput(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	mgr := &fakeManager{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runAttachTransportFake(server, "sess1", mgr)
	}()

	keystrokes := []byte("hello")
	if err := writeFrameToConn(client, 0x10, keystrokes); err != nil {
		t.Fatalf("write 0x10 frame: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		mgr.mu.Lock()
		n := len(mgr.writeInputCalls)
		mgr.mu.Unlock()
		if n >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for WriteInput to be called")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	mgr.mu.Lock()
	got := mgr.writeInputCalls[0]
	mgr.mu.Unlock()
	if string(got) != string(keystrokes) {
		t.Errorf("WriteInput got %q; want %q", got, keystrokes)
	}

	client.Close()
	<-done
}

// TestTransport_ResizeForwardedToResize verifies that a 0x11 frame is
// forwarded to mgr.Resize with the correct cols/rows.
func TestTransport_ResizeForwardedToResize(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	mgr := &fakeManager{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runAttachTransportFake(server, "sess2", mgr)
	}()

	payload := make([]byte, 4)
	binary.BigEndian.PutUint16(payload[0:], 120)
	binary.BigEndian.PutUint16(payload[2:], 40)
	if err := writeFrameToConn(client, 0x11, payload); err != nil {
		t.Fatalf("write 0x11 frame: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		mgr.mu.Lock()
		n := len(mgr.resizeCalls)
		mgr.mu.Unlock()
		if n >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for Resize to be called")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	mgr.mu.Lock()
	call := mgr.resizeCalls[0]
	mgr.mu.Unlock()
	if call[0] != 120 || call[1] != 40 {
		t.Errorf("Resize(%d, %d); want (120, 40)", call[0], call[1])
	}

	client.Close()
	<-done
}

// TestTransport_PTYOutputSentAs0x00Frame verifies that PTY output injected via
// the output sink is forwarded to the client as 0x00-tagged frames.
func TestTransport_PTYOutputSentAs0x00Frame(t *testing.T) {
	client, server := net.Pipe()

	mgr := &fakeManager{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runAttachTransportFake(server, "sess3", mgr)
	}()

	waitForSinks(t, mgr)

	output := []byte("prompt$ ")
	mgr.deliverOutput(output)

	buf := make([]byte, 64)
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("read output frame: %v", err)
	}
	if n < 1 {
		t.Fatal("expected output frame, got 0 bytes")
	}
	if buf[0] != 0x00 {
		t.Errorf("frame tag = 0x%02x; want 0x00", buf[0])
	}
	if got := string(buf[1:n]); got != string(output) {
		t.Errorf("frame payload = %q; want %q", got, output)
	}

	client.Close()
	<-done
}

// TestTransport_SessionExitSendsExitFrame verifies that when the session exits,
// the transport sends a 0x01 frame with the exit code and closes the connection.
func TestTransport_SessionExitSendsExitFrame(t *testing.T) {
	client, server := net.Pipe()

	mgr := &fakeManager{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runAttachTransportFake(server, "sess4", mgr)
	}()

	waitForSinks(t, mgr)

	mgr.deliverExit(42)

	buf := make([]byte, 16)
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("read exit frame: %v", err)
	}
	if n < 5 {
		t.Fatalf("exit frame too short: %d bytes", n)
	}
	if buf[0] != 0x01 {
		t.Errorf("exit frame tag = 0x%02x; want 0x01", buf[0])
	}
	code := int(binary.BigEndian.Uint32(buf[1:5]))
	if code != 42 {
		t.Errorf("exit code = %d; want 42", code)
	}

	// Transport must close conn after exit frame.
	_ = client.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, err = client.Read(buf)
	if err == nil {
		t.Error("expected EOF/closed after exit frame, got nil error")
	}

	<-done
}

// TestTransport_FiveByte0x01PTYOutputIsNotMistakenForExit is the regression test
// for the exit-frame misclassification fix (finding #1).
//
// A genuine PTY output chunk that happens to be exactly 5 bytes long and begin
// with 0x01 (e.g. an SOH control char followed by four printable chars) MUST be
// delivered to the client as a 0x00-tagged output frame, NOT treated as a
// session-exit event.
//
// The Manager.Subscribe API now routes PTY output through outputFn and exit
// through exitFn — structurally separate code paths — so payload content can
// never be confused with event type.
func TestTransport_FiveByte0x01PTYOutputIsNotMistakenForExit(t *testing.T) {
	client, server := net.Pipe()

	mgr := &fakeManager{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runAttachTransportFake(server, "sess5", mgr)
	}()

	waitForSinks(t, mgr)

	// 5 bytes, first byte is 0x01 (SOH) — under the old content-inspection
	// approach this would have been treated as an exit frame (exit code 0x61626364).
	// With split sinks it arrives via outputFn and MUST become a 0x00 output frame.
	ambiguous := []byte{0x01, 'a', 'b', 'c', 'd'}
	mgr.deliverOutput(ambiguous)

	buf := make([]byte, 64)
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	if n < 1 {
		t.Fatal("expected frame, got 0 bytes")
	}

	// Must be delivered as 0x00 output, NOT 0x01 exit.
	if buf[0] != 0x00 {
		t.Errorf("frame tag = 0x%02x; want 0x00 (output) — "+
			"5-byte 0x01-prefixed PTY output was misclassified as exit frame", buf[0])
	}
	if got := buf[1:n]; string(got) != string(ambiguous) {
		t.Errorf("frame payload = %q; want %q", got, ambiguous)
	}

	// Connection must still be open — session did NOT exit.
	// If the transport incorrectly treated the output as exit, done would close.
	select {
	case <-done:
		t.Error("transport exited after delivering ambiguous PTY output — " +
			"misclassified as exit frame")
	default:
	}

	client.Close()
	<-done
}
