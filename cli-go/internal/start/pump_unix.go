//go:build !windows

// Package start — pump_unix.go
//
// runLocalPump is the POSIX implementation of the --share-terminal local pump
// (ADR-0008 Phase 1 T2).
//
// T2 architecture — start owns the PTY:
//
//  1. Generate a sessionId; dial the daemon's JSON-RPC Unix socket.
//  2. Send yakos.term.create RPC (registers the externally-owned session on
//     the daemon — no daemon fork/exec).
//  3. Send yakos.term.attach RPC; hijacks the connection to the push transport.
//  4. Open a local PTY (creack/pty) and spawn the runtime (claude / codex / …)
//     under it.  start owns the PTY master for the lifetime of the session.
//  5. Local raw-mode passthrough: stdin → PTY master, PTY master → stdout.
//     SIGWINCH → pty.Setsize (local resize, no daemon round-trip).
//  6. Tee PTY output to the daemon as length-prefixed 0x00 frames so browser
//     /v1/term subscribers mirror the session in real time.
//  7. On child exit: send 0x01 exit frame to daemon; restore TTY; propagate
//     exit code.
//
// The daemon NEVER fork/execs the child process in this path.  The only daemon
// actions are registering the session (yakos.term.create) and relaying pushed
// output to browser subscribers (/v1/term WebSocket).

package start

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/term"
)

// termCreateParams is the JSON body for yakos.term.create RPC (T2).
// start generates the sessionId; the daemon registers it without spawning.
type termCreateParams struct {
	SessionID     string   `json:"sessionId"`
	Argv          []string `json:"argv"`
	WorkspaceRoot string   `json:"workspaceRoot"`
}

// termCreateResult is the JSON response for yakos.term.create RPC.
type termCreateResult struct {
	SessionID string `json:"sessionId"`
}

// runLocalPump dials socketPath, registers a terminal session on the daemon,
// spawns the child process under a local PTY, and pumps I/O until the child
// exits.  Returns the child's exit code.
func runLocalPump(socketPath string, spawnSpec map[string]interface{}, ew io.Writer) (int, error) {
	// ---- 1. Generate sessionId and dial the daemon socket ----------------------
	sessionID, err := newLocalSessionID()
	if err != nil {
		return 1, fmt.Errorf("start: generate sessionId: %w", err)
	}

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return 1, fmt.Errorf("start: dial daemon socket %s: %w\n  (Is the daemon running? Start it with 'yakos serve')", socketPath, err)
	}
	defer conn.Close()

	// Shared RPC types.
	type rpcRequest struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int             `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	type rpcResponse struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      interface{}     `json:"id"`
		Result  json.RawMessage `json:"result,omitempty"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}

	sendRPC := func(id int, method string, params interface{}) error {
		p, err := json.Marshal(params)
		if err != nil {
			return err
		}
		req := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: p}
		b, err := json.Marshal(req)
		if err != nil {
			return err
		}
		b = append(b, '\n')
		_, err = conn.Write(b)
		return err
	}

	dec := json.NewDecoder(conn)
	readResp := func() (rpcResponse, error) {
		var r rpcResponse
		return r, dec.Decode(&r)
	}

	// ---- 2. Send yakos.term.create RPC -----------------------------------------
	argv, _ := spawnSpec["argv"].([]string)
	workspaceRoot := strFromSpec(spawnSpec, "workspaceRoot")

	if err := sendRPC(1, "yakos.term.create", termCreateParams{
		SessionID:     sessionID,
		Argv:          argv,
		WorkspaceRoot: workspaceRoot,
	}); err != nil {
		return 1, fmt.Errorf("start: write term.create: %w", err)
	}
	resp, err := readResp()
	if err != nil {
		return 1, fmt.Errorf("start: decode term.create response: %w", err)
	}
	if resp.Error != nil {
		return 1, fmt.Errorf("start: term.create RPC error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	slog.Debug("start: external term session registered", "sessionId", sessionID)

	// ---- 3. Send yakos.term.attach — switch to push transport ------------------
	if err := sendRPC(2, "yakos.term.attach", struct {
		SessionID string `json:"sessionId"`
	}{SessionID: sessionID}); err != nil {
		return 1, fmt.Errorf("start: write term.attach: %w", err)
	}
	attachResp, err := readResp()
	if err != nil {
		return 1, fmt.Errorf("start: decode term.attach response: %w", err)
	}
	if attachResp.Error != nil {
		return 1, fmt.Errorf("start: term.attach RPC error %d: %s", attachResp.Error.Code, attachResp.Error.Message)
	}
	// From this point conn is in push-transport mode (binary frames, no NDJSON).

	// writeFrame sends a length-prefixed binary frame over conn to the daemon.
	// Layout: [ 2-byte big-endian total length ] [ tag byte ] [ payload ]
	writeFrame := func(tag byte, payload []byte) error {
		total := uint16(1 + len(payload))
		buf := make([]byte, 2+int(total))
		binary.BigEndian.PutUint16(buf[0:2], total)
		buf[2] = tag
		copy(buf[3:], payload)
		_, err := conn.Write(buf)
		return err
	}

	// ---- 4. Detect terminal dimensions -----------------------------------------
	cols, rows := uint16(80), uint16(24)
	stdinFd := int(os.Stdin.Fd())
	if term.IsTerminal(stdinFd) {
		if w, h, err := term.GetSize(stdinFd); err == nil {
			cols, rows = uint16(w), uint16(h)
		}
	}

	// ---- 5. Spawn child under a local PTY --------------------------------------
	if len(argv) == 0 {
		return 1, fmt.Errorf("start: share-terminal: argv is empty")
	}
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec
	cmd.Dir = strFromSpec(spawnSpec, "cwd")
	if envSlice, ok := spawnSpec["env"].([]string); ok {
		cmd.Env = envSlice
	}
	// Run in its own process group so signals don't propagate directly.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 1}

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return 1, fmt.Errorf("start: pty.StartWithSize %v: %w", argv[0], err)
	}
	defer func() { _ = ptmx.Close() }()

	// ---- 6. Put stdin into raw mode --------------------------------------------
	var oldState *term.State
	if term.IsTerminal(stdinFd) {
		oldState, err = term.MakeRaw(stdinFd)
		if err != nil {
			_, _ = fmt.Fprintf(ew, "WARN: start: could not set raw mode: %v\n", err)
		}
	}
	restoreTTY := func() {
		if oldState != nil {
			_ = term.Restore(stdinFd, oldState)
			oldState = nil
		}
	}
	defer restoreTTY()

	// ---- 7. Install cleanup handler for abnormal exit ---------------------------
	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		<-sigCh
		restoreTTY()
		os.Exit(1) //nolint:gocritic
	}()

	// ---- 8. SIGWINCH → local PTY resize ----------------------------------------
	// T2: resize applies locally to the PTY we own; no daemon round-trip needed.
	winchCh := make(chan os.Signal, 4)
	signal.Notify(winchCh, syscall.SIGWINCH)
	go func() {
		for range winchCh {
			w, h, err := term.GetSize(stdinFd)
			if err != nil {
				continue
			}
			_ = pty.Setsize(ptmx, &pty.Winsize{Cols: uint16(w), Rows: uint16(h)})
		}
	}()

	// ---- 9. stdin → PTY master (local passthrough) ----------------------------
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				if _, werr := ptmx.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// ---- 10. PTY master → stdout + daemon push (0x00 output frames) ----------
	exitCh := make(chan int, 1)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				// Write to local stdout for the operator's own terminal.
				_, _ = os.Stdout.Write(chunk)
				// Push to daemon for browser fan-out.
				if ferr := writeFrame(0x00, chunk); ferr != nil {
					// Daemon disconnected — continue local passthrough anyway.
					slog.Debug("start: push frame write error (daemon disconnected?)", "err", ferr)
				}
			}
			if err != nil {
				// PTY EOF: child exited.
				break
			}
		}
		// Reap the child.
		exitCode := 0
		if state := cmd.ProcessState; state != nil {
			exitCode = state.ExitCode()
		} else {
			// ProcessState is nil until Wait is called; it may have already been
			// set by the time we get here if cmd.Wait was called elsewhere.
			// Call Wait to ensure the process is reaped (may return err if
			// already waited, which is safe to ignore).
			_ = cmd.Wait()
			if state := cmd.ProcessState; state != nil {
				exitCode = state.ExitCode()
			}
		}
		exitCh <- exitCode
	}()

	// Wait for child exit.
	code := <-exitCh

	// ---- 11. Send 0x01 exit frame to daemon ------------------------------------
	exitPayload := make([]byte, 4)
	binary.BigEndian.PutUint32(exitPayload, uint32(code))
	_ = writeFrame(0x01, exitPayload)

	return code, nil
}

// newLocalSessionID generates a random 16-hex-char session identifier.
func newLocalSessionID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func strFromSpec(spec map[string]interface{}, key string) string {
	if v, ok := spec[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
