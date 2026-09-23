// streamhttp.go — Streamable HTTP MCP transport.
//
// Per the MCP spec "streamable HTTP" transport:
//   - Client POSTs NDJSON request frames to a single endpoint.
//   - Server responds with a streaming body of NDJSON response frames, one per line.
//   - Transfer-Encoding: chunked; the server flushes after each response frame.
//   - Content-Type: application/x-ndjson.
//
// Authentication uses the same "Authorization: Bearer <token>" model as the REST API
// (write token required).  The endpoint is wired by the daemon at
// --mcp-http-addr (default 127.0.0.1:7894).
//
// # Stability: experimental
package mcpserver

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/dashauth"
)

// HTTPConfig holds configuration for the streamable HTTP MCP server.
type HTTPConfig struct {
	// Addr is the TCP listen address (e.g. "127.0.0.1:7894").
	Addr string

	// WriteToken is the bearer token required for all requests.
	//
	// SECURITY: an empty WriteToken does NOT mean "no auth required". This
	// transport listens on a TCP loopback socket reachable by any local
	// process (or, absent Origin/Host checks, any web page running in the
	// operator's browser), and serves yakos.dispatch — which is arbitrary
	// local code execution on the operator's credentials (see C2 in
	// security-review-2026-09-14.md). An empty token instead means the
	// transport refuses to start at all (Serve returns an error) and, as a
	// defense-in-depth backstop, every request is rejected with 401 even if
	// something manages to invoke the handler directly (e.g. via Handler()).
	WriteToken string

	// MCPConfig is the MCP session configuration forwarded to each request handler.
	MCPConfig Config
}

// HTTPServer is the streamable HTTP MCP server.
type HTTPServer struct {
	cfg     HTTPConfig
	mux     *http.ServeMux
	httpSrv *http.Server
}

// NewHTTPServer creates a streamable HTTP MCP server but does not start it.
func NewHTTPServer(cfg HTTPConfig) *HTTPServer {
	s := &HTTPServer{cfg: cfg}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp", s.handleMCP)
	s.mux = mux

	// R14 (round-1 security review): this transport had no Origin/Host
	// check, despite its own doc comment (above) acknowledging the gap and
	// an in-repo helper (dashauth.RequireLocalHost) already implementing
	// exactly this for perfdash/metricsdash. The token is bearer-only over
	// plaintext loopback HTTP, so under DNS rebinding a malicious page can
	// become same-origin with this listener and both set Authorization and
	// read the response — Host/Origin checks are the only defense that
	// survives that. reject a present, non-loopback Origin in addition to
	// the Host check, since most MCP clients are not browsers and never
	// send Origin at all — only a browser-issued fetch is affected.
	_, port, _ := net.SplitHostPort(cfg.Addr)
	protected := dashauth.RequireLocalHost(cfg.Addr, rejectNonLoopbackOrigin(port, mux))

	s.httpSrv = &http.Server{
		Addr:         cfg.Addr,
		Handler:      protected,
		ReadTimeout:  0, // streaming; no read timeout
		WriteTimeout: 0, // streaming; no write timeout
		IdleTimeout:  60 * time.Second,
		// R24 (round-1 security review): ReadHeaderTimeout was left at its
		// zero value, which inherits ReadTimeout (also 0 here, deliberately,
		// for streaming request bodies). That left the header-read phase —
		// before auth ever runs — open to a pre-auth slowloris (gosec
		// G112): a connection that trickles a partial request line is held
		// open indefinitely. Bounding only the header phase does not affect
		// legitimate streaming bodies, which are read after headers.
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s
}

// Handler returns the raw handler (no Host/Origin check) for use with
// httptest.NewServer in tests, which binds an ephemeral port that would
// never match cfg.Addr's configured port. This mirrors
// perfdash.Server.Handler's identical caveat and reasoning. Tests that
// specifically exercise Host/Origin rejection build a request with req.Host
// / the Origin header set and call dashauth.RequireLocalHost /
// rejectNonLoopbackOrigin directly, or exercise Serve() against a real
// loopback listener.
func (s *HTTPServer) Handler() http.Handler {
	return s.mux
}

// rejectNonLoopbackOrigin rejects any request carrying a non-empty Origin
// header that isn't one of the loopback origins for port (R14, round-1
// security review). A missing Origin (the common case: MCP clients are
// typically not browsers) is allowed through unchanged; only a
// browser-issued fetch sets Origin, which is exactly the DNS-rebinding
// threat model dashauth.RequireLocalHost covers for the Host header. An
// unparseable port (should not happen in production, where cfg.Addr is
// always host:port) fails closed: every non-empty Origin is rejected rather
// than silently allowed.
func rejectNonLoopbackOrigin(port string, next http.Handler) http.Handler {
	allowed := map[string]bool{
		"http://127.0.0.1:" + port: true,
		"http://localhost:" + port: true,
		"http://[::1]:" + port:     true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && !allowed[origin] {
			http.Error(w, `{"error":"DNS-rebinding defense: unexpected Origin"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ErrNoWriteToken is returned by Serve when no WriteToken is configured.
// The streamable HTTP transport must never bind without one — see the
// WriteToken doc comment on HTTPConfig.
var ErrNoWriteToken = errors.New("mcpserver/http: refusing to start: no write token configured (unauthenticated dispatch endpoint)")

// Serve listens on cfg.Addr and blocks until ctx is cancelled.
func (s *HTTPServer) Serve(ctx context.Context) error {
	if s.cfg.WriteToken == "" {
		return ErrNoWriteToken
	}

	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("mcpserver/http: listen %s: %w", s.cfg.Addr, err)
	}

	errCh := make(chan error, 1)
	go func() {
		if err := s.httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			errCh <- err
		} else {
			errCh <- nil
		}
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.httpSrv.Shutdown(shutCtx)
		return <-errCh
	case err := <-errCh:
		return err
	}
}

// ServeHTTP implements http.Handler so the server can be used with httptest.
func (s *HTTPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.httpSrv.Handler.ServeHTTP(w, r)
}

// handleMCP is the HTTP handler for POST /mcp.
//
// It reads NDJSON request frames from the body, dispatches each to the MCP tool
// registry, and writes NDJSON response frames to the response body with chunked
// encoding + flush after each frame.
func (s *HTTPServer) handleMCP(w http.ResponseWriter, r *http.Request) {
	// --- Auth ---
	// SECURITY (C2/L4): fail closed. An empty configured token must never be
	// treated as "auth disabled" — Serve() already refuses to start in that
	// case, but this handler is reachable directly in tests via Handler(),
	// so it re-asserts the same fail-closed behavior. The token comparison
	// uses subtle.ConstantTimeCompare to avoid a timing side-channel.
	tok := bearerTokenHTTP(r)
	if s.cfg.WriteToken == "" || tok == "" ||
		subtle.ConstantTimeCompare([]byte(tok), []byte(s.cfg.WriteToken)) != 1 {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	// --- Response setup ---
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // hint to nginx: disable buffering
	w.WriteHeader(http.StatusOK)

	flusher, hasFlusher := w.(http.Flusher)

	// --- Dispatch ---
	srv := &server{
		cfg:   s.cfg.MCPConfig,
		out:   &ndjsonResponseWriter{w: w, flusher: flusher, hasFlusher: hasFlusher},
		tools: registry(),
	}

	ctx := r.Context()

	const maxLine = 4 * 1024 * 1024
	scanner := bufio.NewScanner(r.Body)
	scanner.Buffer(make([]byte, maxLine), maxLine)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				// Write a parse-error frame before closing.
				srv.write(srv.errResp(nil, codeParseError, "read error: "+err.Error()))
			}
			return
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		frame := make([]byte, len(line))
		copy(frame, line)

		resp, isNotification := srv.dispatch(ctx, frame)
		if isNotification {
			continue
		}
		srv.write(resp)
	}
}

// bearerTokenHTTP extracts the Bearer token from an HTTP Authorization header.
func bearerTokenHTTP(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	const prefix = "bearer "
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return h[len(prefix):]
	}
	return ""
}

// ndjsonResponseWriter implements io.Writer by writing NDJSON frames to w
// and flushing after each write.
type ndjsonResponseWriter struct {
	w          http.ResponseWriter
	flusher    http.Flusher
	hasFlusher bool
}

// Write serializes b as one NDJSON line and flushes.
func (n *ndjsonResponseWriter) Write(b []byte) (int, error) {
	// Ensure the frame ends with exactly one newline.
	line := b
	if len(line) > 0 && line[len(line)-1] != '\n' {
		line = append(line, '\n')
	}
	written, err := n.w.Write(line)
	if n.hasFlusher {
		n.flusher.Flush()
	}
	// Return the original length so callers (json.Marshal → Write) aren't confused.
	if written > len(b) {
		return len(b), err
	}
	return written, err
}

// ---- ClientConn for streamable HTTP -----------------------------------------

// StreamHTTPClient is a minimal client for the streamable HTTP MCP transport.
// It is used in tests and as a reference implementation.
type StreamHTTPClient struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// Call sends one NDJSON request frame and returns all response frames.
func (c *StreamHTTPClient) Call(ctx context.Context, method string, id interface{}, params interface{}) ([]map[string]interface{}, error) {
	frame := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	body, err := json.Marshal(frame)
	if err != nil {
		return nil, fmt.Errorf("streamhttp: marshal: %w", err)
	}
	body = append(body, '\n')

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/mcp", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	cl := c.HTTPClient
	if cl == nil {
		cl = http.DefaultClient
	}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, fmt.Errorf("streamhttp: do: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("streamhttp: server returned %d", resp.StatusCode)
	}

	var results []map[string]interface{}
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			return nil, fmt.Errorf("streamhttp: unmarshal response: %w", err)
		}
		results = append(results, m)
	}
	return results, scanner.Err()
}
