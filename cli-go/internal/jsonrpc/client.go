// Package jsonrpc client.go — JSON-RPC 2.0 client over the daemon socket/pipe.
//
// The client is used by the CLI when YAKOS_DAEMON=on|auto and a daemon socket
// is reachable. It sends newline-delimited JSON requests and reads NDJSON
// responses from the same connection.
//
// # Concurrency
//
// Client is safe for concurrent use. Each Call is matched to its response by
// the JSON-RPC id field.
package jsonrpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
)

// Client is a JSON-RPC 2.0 client connected to a single socket/pipe.
type Client struct {
	conn   net.Conn
	enc    *json.Encoder
	mu     sync.Mutex // serializes writes
	nextID atomic.Int64

	// pending maps request ID to response channel.
	pending   map[int64]chan *Response
	pendingMu sync.Mutex

	// readErr stores the first read-loop error.
	readErr   error
	readErrMu sync.Mutex
	done      chan struct{}
}

// NewClient wraps an existing net.Conn as a JSON-RPC client and starts the
// background reader goroutine.
func NewClient(conn net.Conn) *Client {
	c := &Client{
		conn:    conn,
		enc:     json.NewEncoder(conn),
		pending: make(map[int64]chan *Response),
		done:    make(chan struct{}),
	}
	go c.readLoop()
	return c
}

// DialClient dials the socket at path and returns a ready-to-use Client.
func DialClient(path string) (*Client, error) {
	conn, err := Dial(path)
	if err != nil {
		return nil, err
	}
	return NewClient(conn), nil
}

// Close closes the underlying connection and waits for the read loop to finish.
func (c *Client) Close() error {
	err := c.conn.Close()
	<-c.done
	return err
}

// Call sends a JSON-RPC request and waits for the response. It returns the
// raw result JSON or an *RPCError from the server.
//
// ctx cancellation aborts the wait but does NOT close the connection (the
// in-flight request is still outstanding on the server side).
func (c *Client) Call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	id := c.nextID.Add(1)

	req := struct {
		JSONRPC string      `json:"jsonrpc"`
		ID      int64       `json:"id"`
		Method  string      `json:"method"`
		Params  interface{} `json:"params,omitempty"`
	}{
		JSONRPC: Version,
		ID:      id,
		Method:  method,
		Params:  params,
	}

	ch := make(chan *Response, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()

	// Write the request (serialized).
	c.mu.Lock()
	err := c.enc.Encode(req)
	c.mu.Unlock()
	if err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, fmt.Errorf("jsonrpc: encode request: %w", err)
	}

	select {
	case <-ctx.Done():
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, ctx.Err()
	case resp, ok := <-ch:
		if !ok {
			// Channel was closed by readLoop on connection error.
			c.readErrMu.Lock()
			rerr := c.readErr
			c.readErrMu.Unlock()
			if rerr != nil {
				return nil, fmt.Errorf("jsonrpc: connection lost: %w", rerr)
			}
			return nil, fmt.Errorf("jsonrpc: connection closed")
		}
		if resp.Error != nil {
			return nil, resp.Error
		}
		if resp.Result == nil {
			return json.RawMessage("null"), nil
		}
		data, err := json.Marshal(resp.Result)
		if err != nil {
			return nil, fmt.Errorf("jsonrpc: re-encode result: %w", err)
		}
		return data, nil
	}
}

// readLoop reads NDJSON responses from the connection and routes each to the
// matching pending Call. It exits when the connection closes.
func (c *Client) readLoop() {
	defer close(c.done)

	scanner := bufio.NewScanner(c.conn)
	scanner.Buffer(make([]byte, maxTokenSize), maxTokenSize)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var resp Response
		if err := json.Unmarshal(line, &resp); err != nil {
			// Malformed frame; skip it.
			continue
		}

		// Match by numeric ID (server always echoes back the caller's int64 id).
		id, ok := numericID(resp.ID)
		if !ok {
			continue
		}

		c.pendingMu.Lock()
		ch, found := c.pending[id]
		if found {
			delete(c.pending, id)
		}
		c.pendingMu.Unlock()

		if found {
			ch <- &resp
		}
	}

	// Connection closed. Cancel all pending calls.
	scanErr := scanner.Err()
	c.readErrMu.Lock()
	c.readErr = scanErr
	c.readErrMu.Unlock()

	c.pendingMu.Lock()
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.pendingMu.Unlock()
}

// numericID extracts the int64 from an interface{} JSON id field.
// JSON unmarshals numbers as float64 by default.
func numericID(v interface{}) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	}
	return 0, false
}
