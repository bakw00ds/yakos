// Package anthropic is yakOS's Anthropic pass-through gateway (K-151): a
// loopback reverse proxy on 127.0.0.1:7897 that Claude Code reaches through
// ANTHROPIC_BASE_URL (`yakos start --routed`). It forwards /v1/messages,
// /v1/messages/count_tokens and /v1/models to api.anthropic.com and nothing
// else. docs/adr/ADR-0011.md is the trust-boundary statement.
//
// Posture:
//   - Off unless `yakos serve --gateway` or `anthropic_gateway: true` in the
//     trusted user policy. serve.Run owns that decision.
//   - Loopback only: New refuses any other bind address. Host and Origin are
//     checked before anything is read (DNS-rebinding and browser defence).
//   - One upstream, api.anthropic.com over TLS, pinned in code. No redirect is
//     followed, so a credential can never be replayed to another host. A
//     registry billing=local slot is represented as a type only (Upstream.Kind).
//   - The gateway token is stripped and never forwarded. The upstream gets the
//     operator's ANTHROPIC_API_KEY as x-api-key (or the x-api-key the client
//     sent). A subscription OAuth credential (sk-ant-oat anywhere in a
//     credential value) is refused with 403 unless the operator started the
//     gateway with --gateway-passthrough-subscription; then the bearer is
//     forwarded on its own request, with the gateway token in TokenHeader.
//   - Request and response headers pass verbatim (hop-by-hop headers excepted),
//     SSE is flushed as it arrives with its ping frames, and error bodies are
//     the upstream's own. Bodies are byte-identical unless a gateway_classes
//     rewrite applies (rewrite.go).
//   - Route metadata never enters a prompt: the only request change is the
//     `model` value, and only on a class match (rule:cache-stability).
//
// Auth: every request must carry the gateway token (token.go) as
// `Authorization: Bearer <token>`, which Claude Code sends from
// ANTHROPIC_AUTH_TOKEN; without it the answer is 401 and nothing reaches the
// upstream. Idempotency:
// a proxied POST is exactly as idempotent as Anthropic's own endpoint; the
// gateway never retries. Rate limit: a fixed in-flight cap (maxInflight).
package anthropic

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bakw00ds/yakos/internal/dashauth"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/routerpolicy"
)

// DefaultAddr is where the gateway listens.
const DefaultAddr = "127.0.0.1:7897"

// UpstreamHost is the only host the gateway forwards to.
const UpstreamHost = "api.anthropic.com"

// Limits.
const (
	// MaxBodyBytes caps a request body. 32 MiB is Anthropic's own request-size
	// limit, so a smaller cap would refuse sessions the API accepts.
	MaxBodyBytes = 32 << 20
	maxInflight  = 64
	// BodyBudget caps the body bytes held at once across all requests; with the
	// per-request cap it bounds the gateway's request memory: at most BodyBudget
	// of reserved bodies, each read into a buffer that is at most that size
	// (the rewrite splice copies one body once), so about 2 x BodyBudget = 256 MiB
	// in the worst case, not 64 x 32 MiB x 2.
	BodyBudget = 128 << 20
	// BodyTimeout is the absolute time a client has to deliver a request body.
	BodyTimeout = 30 * time.Second
)

// Upstream kinds. KindLocal is the registry billing=local slot: a type only,
// no transport is built for it in K-151.
const (
	KindAnthropic = "anthropic"
	KindLocal     = "local"
)

// Upstream is where a request goes.
type Upstream struct {
	Kind string
	// Primary marks the primary runtime's own API (api.anthropic.com).
	Primary bool
}

// Config configures a Server.
type Config struct {
	// Addr is the listen address; empty means DefaultAddr. It must be loopback.
	Addr string
	// PassthroughSubscription lets an sk-ant-oat* bearer through. Off by default.
	PassthroughSubscription bool
	// APIKey is the operator's ANTHROPIC_API_KEY, used only for a request that
	// carries no credential of its own. Never logged.
	APIKey string
	// Classes returns the validated gateway_classes table (K-141). Nil means none.
	Classes func() routerpolicy.GatewayClasses
	// GatewayToken is the secret a client must present (Authorization: Bearer,
	// or TokenHeader). Required: New refuses an empty one. Never logged.
	GatewayToken string
	// DeferToken allows New with no GatewayToken; SetToken must follow before
	// ServeListener.
	DeferToken bool
	// Ledger receives one event per request. Nil writes gateway_request through
	// a dispatch.Account.
	Ledger func(dispatch.GatewayEvent)

	// transport, baseURL and route are test seams; serve cannot set them, so
	// production traffic always goes to https://api.anthropic.com.
	transport http.RoundTripper
	baseURL   *url.URL
	route     func(model string) Upstream
	maxBody   int64
	// bodyTimeout and bodyBudget default to BodyTimeout and BodyBudget.
	bodyTimeout time.Duration
	bodyBudget  int64
	// badTokenGap is the minimum time between bad_token audit lines; 0 means
	// one second, negative turns the limit off (tests).
	badTokenGap time.Duration
}

// Server is the gateway.
type Server struct {
	cfg     Config
	addr    string
	port    string
	client  *http.Client
	base    *url.URL
	mux     http.Handler
	httpSrv *http.Server
	sem     chan struct{}
	budget  atomic.Int64

	badMu      sync.Mutex
	badLast    time.Time
	badSkipped int64
}

// New validates cfg and builds the Server. It does not listen.
func New(cfg Config) (*Server, error) {
	addr := cfg.Addr
	if addr == "" {
		addr = DefaultAddr
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return nil, fmt.Errorf("anthropic gateway: bad address %q", addr)
	}
	if !isLoopbackHost(host) {
		return nil, fmt.Errorf("anthropic gateway: refusing to bind %q: the gateway is loopback-only", addr)
	}
	base := cfg.baseURL
	if base == nil {
		base = &url.URL{Scheme: "https", Host: UpstreamHost}
	}
	rt := cfg.transport
	if rt == nil {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: UpstreamHost}
		// The body is forwarded as the client sent it; never decompress for it.
		t.DisableCompression = true
		rt = t
	}
	if cfg.route == nil {
		cfg.route = func(string) Upstream { return Upstream{Kind: KindAnthropic, Primary: true} }
	}
	if cfg.maxBody <= 0 {
		cfg.maxBody = MaxBodyBytes
	}
	if cfg.bodyTimeout <= 0 {
		cfg.bodyTimeout = BodyTimeout
	}
	if cfg.badTokenGap == 0 {
		cfg.badTokenGap = time.Second
	}
	if cfg.bodyBudget <= 0 {
		cfg.bodyBudget = BodyBudget
	}
	// DeferToken lets the daemon bind first and mint the token after (SetToken);
	// ServeListener refuses to serve without one.
	if !(cfg.DeferToken && cfg.GatewayToken == "") && len(cfg.GatewayToken) < 32 {
		return nil, errors.New("anthropic gateway: a gateway token of at least 32 characters is required")
	}
	if cfg.Ledger == nil {
		cfg.Ledger = writeLedger
	}
	s := &Server{
		cfg: cfg, addr: addr, port: port, base: base,
		client: &http.Client{
			Transport: rt,
			// A redirect would replay the credential to another host.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		sem: make(chan struct{}, maxInflight),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/messages", s.handleMessages("messages"))
	mux.HandleFunc("/v1/messages/count_tokens", s.handleMessages("count_tokens"))
	mux.HandleFunc("/v1/models", s.handleModels)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found_error", "no such route")
	})
	s.mux = dashauth.RequireLocalHost(addr, rejectNonLoopbackOrigin(port, mux))
	s.httpSrv = &http.Server{
		Addr:              addr,
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		// No read or write timeout: a completion streams for as long as it runs.
	}
	return s, nil
}

func writeLedger(ev dispatch.GatewayEvent) {
	req := dispatch.Request{AgentName: "anthropic-gateway", Surface: dispatch.SurfaceAnthropicGateway}
	if ev.Started.IsZero() {
		dispatch.NewAccount(req).Gateway(ev)
		return
	}
	// The Account is built at write time, so it must be told when the request began.
	dispatch.NewAccountAt(req, ev.Started).Gateway(ev)
}

// Handler returns the full handler (Host and Origin checks included).
func (s *Server) Handler() http.Handler { return s.mux }

// Listen binds the address.
func (s *Server) Listen() (net.Listener, error) {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return nil, fmt.Errorf("anthropic gateway: listen %s: %w", s.addr, err)
	}
	return ln, nil
}

// SetToken sets the gateway token. Call it after Listen and before
// ServeListener; it is not safe once the server is serving.
func (s *Server) SetToken(tok string) { s.cfg.GatewayToken = tok }

// ServeListener serves on ln until ctx ends.
func (s *Server) ServeListener(ctx context.Context, ln net.Listener) error {
	if len(s.cfg.GatewayToken) < 32 {
		_ = ln.Close()
		return errors.New("anthropic gateway: a gateway token of at least 32 characters is required")
	}
	s.httpSrv.BaseContext = func(net.Listener) context.Context { return ctx }
	errCh := make(chan error, 1)
	go func() {
		if err := s.httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	select {
	case <-ctx.Done():
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.httpSrv.Shutdown(shut)
		return <-errCh
	case err := <-errCh:
		return err
	}
}

func isLoopbackHost(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(h, "[]"))
	return ip != nil && ip.IsLoopback()
}

// rejectNonLoopbackOrigin rejects a request whose Origin is set and is not one of
// this server's loopback origins. Claude Code sets no Origin; a browser always does.
func rejectNonLoopbackOrigin(port string, next http.Handler) http.Handler {
	allowed := map[string]bool{
		"http://127.0.0.1:" + port: true,
		"http://localhost:" + port: true,
		"http://[::1]:" + port:     true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && !allowed[origin] {
			writeError(w, http.StatusForbidden, "permission_error", "unexpected Origin")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// writeError writes the Anthropic error envelope. The message is fixed text.
func writeError(w http.ResponseWriter, status int, typ, msg string) {
	body := map[string]any{"type": "error", "error": map[string]string{"type": typ, "message": msg}}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
