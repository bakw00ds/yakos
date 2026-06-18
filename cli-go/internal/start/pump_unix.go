//go:build !windows

// Package start — pump_unix.go
//
// runLocalPump is the POSIX implementation of the --share-terminal local pump
// (ADR-0008 Phase 1).
//
// Behaviour:
//  1. Dial the daemon's JSON-RPC Unix socket (socketPath).
//  2. Send yakos.term.create RPC with the spawn spec.
//  3. Receive sessionId and a streaming connection for PTY I/O.
//  4. Put local stdin into raw mode (golang.org/x/term).
//  5. Bridge:  stdin → daemon PTY (write frames via jsonrpc stream)
//     daemon PTY output → stdout
//     SIGWINCH → resize frames → daemon pty.Setsize
//  6. On child exit, restore terminal and propagate the exit code.
//
// Phase 1 note: the daemon only wires output (0x00 frames) + close (0x01 frames)
// from the PTY.  The pump sends input keystrokes (0x10 frames) and resize frames
// (0x11 frames) in Phase 1 for the local pump path only.  Browser keystroke
// ingestion is Phase 2.

package start

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"
)

// termCreateParams is the JSON body for yakos.term.create RPC.
type termCreateParams struct {
	Argv          []string `json:"argv"`
	Cwd           string   `json:"cwd"`
	Env           []string `json:"env"`
	WorkspaceRoot string   `json:"workspaceRoot"`
	Cols          uint16   `json:"cols"`
	Rows          uint16   `json:"rows"`
}

// termCreateResult is the JSON response for yakos.term.create RPC.
type termCreateResult struct {
	SessionID string `json:"sessionId"`
}

// runLocalPump dials socketPath, creates a terminal session from spawnSpec, and
// pumps stdin/stdout until the child process exits.  Returns the child's exit code.
func runLocalPump(socketPath string, spawnSpec map[string]interface{}, ew io.Writer) (int, error) {
	// ---- 1. Dial the daemon JSON-RPC socket ------------------------------------
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return 1, fmt.Errorf("start: dial daemon socket %s: %w\n  (Is the daemon running? Start it with 'yakos serve')", socketPath, err)
	}
	defer conn.Close()

	// ---- 2. Detect terminal dimensions -----------------------------------------
	cols, rows := uint16(80), uint16(24)
	if fdInt := int(os.Stdin.Fd()); term.IsTerminal(fdInt) {
		if w, h, err := term.GetSize(fdInt); err == nil {
			cols, rows = uint16(w), uint16(h)
		}
	}

	// ---- 3. Build and send yakos.term.create RPC --------------------------------
	params := termCreateParams{
		WorkspaceRoot: strFromSpec(spawnSpec, "workspaceRoot"),
		Cwd:           strFromSpec(spawnSpec, "cwd"),
		Cols:          cols,
		Rows:          rows,
	}
	if argv, ok := spawnSpec["argv"].([]string); ok {
		params.Argv = argv
	}
	if envSlice, ok := spawnSpec["env"].([]string); ok {
		params.Env = envSlice
	}

	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return 1, fmt.Errorf("start: marshal term.create params: %w", err)
	}

	type rpcRequest struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int             `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "yakos.term.create",
		Params:  paramsJSON,
	}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		return 1, fmt.Errorf("start: marshal term.create request: %w", err)
	}
	reqBytes = append(reqBytes, '\n')
	if _, err := conn.Write(reqBytes); err != nil {
		return 1, fmt.Errorf("start: write term.create: %w", err)
	}

	// ---- 4. Read the RPC response -----------------------------------------------
	type rpcResponse struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      interface{}     `json:"id"`
		Result  json.RawMessage `json:"result,omitempty"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	dec := json.NewDecoder(conn)
	var resp rpcResponse
	if err := dec.Decode(&resp); err != nil {
		return 1, fmt.Errorf("start: decode term.create response: %w", err)
	}
	if resp.Error != nil {
		return 1, fmt.Errorf("start: term.create RPC error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	var result termCreateResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return 1, fmt.Errorf("start: unmarshal term.create result: %w", err)
	}
	sessionID := result.SessionID
	slog.Debug("start: term session created", "sessionId", sessionID)

	// ---- 5. Send yakos.term.attach — switch connection to streaming PTY I/O ----
	type termAttachParams struct {
		SessionID string `json:"sessionId"`
	}
	attachParams, _ := json.Marshal(termAttachParams{SessionID: sessionID})
	attachReq := rpcRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "yakos.term.attach",
		Params:  attachParams,
	}
	attachBytes, _ := json.Marshal(attachReq)
	attachBytes = append(attachBytes, '\n')
	if _, err := conn.Write(attachBytes); err != nil {
		return 1, fmt.Errorf("start: write term.attach: %w", err)
	}
	// Read the attach ack.
	var attachResp rpcResponse
	if err := dec.Decode(&attachResp); err != nil {
		return 1, fmt.Errorf("start: decode term.attach response: %w", err)
	}
	if attachResp.Error != nil {
		return 1, fmt.Errorf("start: term.attach RPC error %d: %s", attachResp.Error.Code, attachResp.Error.Message)
	}

	// ---- 6. Put stdin into raw mode ---------------------------------------------
	stdinFd := int(os.Stdin.Fd())
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

	// writeFrame sends a length-prefixed binary frame to the daemon transport.
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

	// ---- 8. SIGWINCH → 0x11 resize frames ---------------------------------------
	winchCh := make(chan os.Signal, 4)
	signal.Notify(winchCh, syscall.SIGWINCH)
	go func() {
		for range winchCh {
			w, h, err := term.GetSize(stdinFd)
			if err != nil {
				continue
			}
			payload := make([]byte, 4)
			binary.BigEndian.PutUint16(payload[0:], uint16(w))
			binary.BigEndian.PutUint16(payload[2:], uint16(h))
			_ = writeFrame(0x11, payload)
		}
	}()

	// ---- 9. stdin → daemon (0x10 keystroke frames) ------------------------------
	exitCh := make(chan int, 1)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				if werr := writeFrame(0x10, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// ---- 10. daemon PTY output → stdout (0x00 / 0x01 frames) -------------------
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				data := buf[:n]
				for len(data) > 0 {
					if len(data) < 1 {
						break
					}
					tag := data[0]
					switch tag {
					case 0x00: // PTY output
						payload := data[1:]
						_, _ = os.Stdout.Write(payload)
						data = nil // consumed
					case 0x01: // exit frame (5 bytes: tag + 4-byte exit code)
						if len(data) >= 5 {
							code := int(binary.BigEndian.Uint32(data[1:5]))
							exitCh <- code
							return
						}
						data = nil
					default:
						data = nil
					}
				}
			}
			if err != nil {
				exitCh <- 0
				return
			}
		}
	}()

	// Wait for child exit.
	code := <-exitCh
	return code, nil
}

func strFromSpec(spec map[string]interface{}, key string) string {
	if v, ok := spec[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
