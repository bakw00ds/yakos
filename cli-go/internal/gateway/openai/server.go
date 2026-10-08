// Package openai is yakOS's OpenAI-compatible endpoint (K-150): GET /v1/models
// and POST /v1/chat/completions on 127.0.0.1:7898, so Open WebUI, Continue, an
// OpenAI SDK or curl can talk to the router.
//
// Posture:
//   - Off unless the trusted user policy says `openai_endpoint: true` or
//     `yakos serve --openai-endpoint` is given. serve.Run owns that decision.
//   - Loopback only. New refuses any other bind address.
//   - Bearer = the REST write token (~/.yakos-state/rest-write-token), compared
//     in constant time. Host and Origin are checked as the console checks them
//     (DNS-rebinding defence) before the token is looked at.
//   - Every completion goes through dispatch.Service.RunStream: the router,
//     sensitive-class scan, budget, supervision and the ledger all apply, with
//     surface=openai-compat on the ledger rows. There is no second path.
//   - Request bodies are bound through a DTO (chatRequest) and never reach a
//     domain struct; the body is capped at 1 MiB.
//   - Route metadata rides the response only (the `yakos` extension object). It
//     never enters a system prompt, --append-system-prompt or the --agents JSON,
//     so the prompt-cache prefix is unchanged (rule:cache-stability).
//
// Auth: bearer write token on every route. Idempotency: a completion is not
// idempotent (it runs a model); a retry is a new turn unless the client resumes
// with X-Yakos-Conversation, which does not dedupe either. Rate limit: the
// dispatch Service's concurrency governor and the agent budget; no extra class.
package openai

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/dashauth"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/modelreg"
)

// DefaultAddr is where the endpoint listens.
const DefaultAddr = "127.0.0.1:7898"

// MaxBodyBytes caps a request body.
const MaxBodyBytes = 1 << 20

// OperatorID is the owner every conversation made through this endpoint carries.
// It is a fixed label, not a credential: the one bearer token is the whole
// identity, so a conversation made here cannot be resumed by the console's
// operator or any other, and the reverse.
const OperatorID = "openai-compat"

// DefaultAgent answers a request that names no agent (yakos/auto and
// <runtime>/<model>).
const DefaultAgent = "lead"

// Config configures a Server.
type Config struct {
	// Addr is the listen address; empty means DefaultAddr. It must be loopback.
	Addr string
	// WriteToken is the bearer token (the REST write token). Required.
	WriteToken string
	// Service is the shared dispatch Service. Required.
	Service *dispatch.Service
	// Transcripts is the conversation store, the console's own (same work dir).
	Transcripts *consoleui.Transcripts
	// YakosRoot and Workspace locate the roster and the project pin.
	YakosRoot, Workspace string
	// Agent answers requests that name none; empty means DefaultAgent.
	Agent string

	// Registry builds the model registry. Nil means modelreg.Load on the default
	// state dir. Tests replace it.
	Registry func(project string) (*modelreg.Registry, error)
	// SignedIn reports whether the harness can run now (CLI present and signed
	// in). Nil means the same probe the router uses, cached briefly.
	SignedIn func(ctx context.Context, harness string) bool
}

// Server is the endpoint.
type Server struct {
	cfg     Config
	addr    string
	port    string
	started time.Time
	mux     http.Handler
	httpSrv *http.Server

	mu       sync.Mutex
	inflight map[string]struct{} // conversation ids with a turn running
}

// New validates cfg and builds the Server. It does not listen.
func New(cfg Config) (*Server, error) {
	if len(cfg.WriteToken) < 32 {
		return nil, errors.New("openai gateway: refusing to start: no write token configured")
	}
	if cfg.Service == nil {
		return nil, errors.New("openai gateway: refusing to start: no dispatch service")
	}
	if cfg.Transcripts == nil {
		return nil, errors.New("openai gateway: refusing to start: no transcript store")
	}
	addr := cfg.Addr
	if addr == "" {
		addr = DefaultAddr
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return nil, fmt.Errorf("openai gateway: bad address %q", addr)
	}
	if !isLoopbackHost(host) {
		return nil, fmt.Errorf("openai gateway: refusing to bind %q: the endpoint is loopback-only", addr)
	}
	if cfg.Agent == "" {
		cfg.Agent = DefaultAgent
	}
	if cfg.Registry == nil {
		cfg.Registry = func(project string) (*modelreg.Registry, error) {
			return modelreg.Load(modelreg.Options{StateDir: modelreg.DefaultStateDir(), Project: project})
		}
	}
	if cfg.SignedIn == nil {
		cfg.SignedIn = newProbeCache().signedIn
	}
	s := &Server{cfg: cfg, addr: addr, port: port, started: time.Now(), inflight: map[string]struct{}{}}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", s.handleModels)
	mux.HandleFunc("/v1/chat/completions", s.handleChat)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such route")
	})
	s.mux = dashauth.RequireLocalHost(addr, rejectNonLoopbackOrigin(port, s.requireBearer(mux)))
	s.httpSrv = &http.Server{
		Addr:              addr,
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		// No read or write timeout: a completion streams for as long as the turn runs.
	}
	return s, nil
}

// Handler returns the full handler (Host, Origin and bearer checks included).
func (s *Server) Handler() http.Handler { return s.mux }

// Listen binds the address.
func (s *Server) Listen() (net.Listener, error) {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return nil, fmt.Errorf("openai gateway: listen %s: %w", s.addr, err)
	}
	return ln, nil
}

// ServeListener serves on ln until ctx ends.
func (s *Server) ServeListener(ctx context.Context, ln net.Listener) error {
	// Requests end with the server: a turn in flight is cancelled (its runtime
	// killed) when the daemon shuts down, instead of outliving Shutdown.
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
// this server's loopback origins. A missing Origin (an SDK, curl, a server-side
// proxy such as Open WebUI's backend) passes; only a browser sets one.
func rejectNonLoopbackOrigin(port string, next http.Handler) http.Handler {
	allowed := map[string]bool{
		"http://127.0.0.1:" + port: true,
		"http://localhost:" + port: true,
		"http://[::1]:" + port:     true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && !allowed[origin] {
			writeError(w, http.StatusForbidden, "forbidden_origin", "unexpected Origin")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireBearer admits a request carrying the write token.
func (s *Server) requireBearer(next http.Handler) http.Handler {
	want := []byte(s.cfg.WriteToken)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(dashauth.BearerToken(r))
		if len(got) == 0 || subtle.ConstantTimeCompare(got, want) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "invalid_api_key", "missing or invalid bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// errorBody is the OpenAI error envelope.
type errorBody struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code,omitempty"`
	} `json:"error"`
}

func errorType(status int) string {
	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return "authentication_error"
	case status == http.StatusTooManyRequests:
		return "rate_limit_error"
	case status >= 500:
		return "server_error"
	}
	return "invalid_request_error"
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	var b errorBody
	b.Error.Message, b.Error.Type, b.Error.Code = msg, errorType(status), code
	writeJSON(w, status, b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// acquire marks conversation id as having a turn running; false when one already is.
func (s *Server) acquire(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, busy := s.inflight[id]; busy {
		return false
	}
	s.inflight[id] = struct{}{}
	return true
}

func (s *Server) release(id string) {
	s.mu.Lock()
	delete(s.inflight, id)
	s.mu.Unlock()
}
