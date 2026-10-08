package anthropic

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/router"
)

const surface = dispatch.SurfaceAnthropicGateway

// hopByHop are the headers a proxy must not forward (RFC 9110 section 7.6.1).
var hopByHop = []string{
	"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
	"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

// copyHeaders copies src to dst without the hop-by-hop headers (and without any
// header the Connection header names).
func copyHeaders(dst, src http.Header) {
	drop := map[string]bool{}
	for _, h := range hopByHop {
		drop[http.CanonicalHeaderKey(h)] = true
	}
	for _, v := range src.Values("Connection") {
		for _, tok := range strings.Split(v, ",") {
			if t := strings.TrimSpace(tok); t != "" {
				drop[http.CanonicalHeaderKey(t)] = true
			}
		}
	}
	for k, vs := range src {
		if drop[http.CanonicalHeaderKey(k)] {
			continue
		}
		dst[k] = append([]string(nil), vs...)
	}
}

// credential inspects the headers a request authenticates with. It returns
// whether any is present and whether any is a subscription OAuth credential.
// The values are looked at, never stored or logged.
func credential(h http.Header) (present, oauth bool) {
	check := func(v string) {
		v = strings.TrimSpace(v)
		if len(v) > 7 && strings.EqualFold(v[:7], "bearer ") {
			v = strings.TrimSpace(v[7:])
		}
		if strings.HasPrefix(strings.ToLower(v), "sk-ant-oat") {
			oauth = true
		}
	}
	for _, v := range h.Values("X-Api-Key") {
		present = true
		check(v)
	}
	for _, v := range h.Values("Authorization") {
		present = true
		check(v)
	}
	// The OAuth flow announces itself with a beta flag too; refuse that shape
	// even when the token itself does not carry the prefix.
	for _, v := range h.Values("Anthropic-Beta") {
		if strings.Contains(strings.ToLower(v), "oauth-") {
			oauth = true
		}
	}
	return present, oauth
}

// request is one in-flight proxied request.
type request struct {
	s        *Server
	w        http.ResponseWriter
	r        *http.Request
	ev       dispatch.GatewayEvent
	finished bool
}

func (q *request) finish() {
	if q.finished {
		return
	}
	q.finished = true
	q.s.cfg.Ledger(q.ev)
}

func (q *request) refuse(status int, typ, reason, msg string) {
	q.ev.Status, q.ev.Refused = status, reason
	writeError(q.w, status, typ, msg)
	q.finish()
}

// admit runs the checks common to every route: in-flight cap and credential.
// It returns the credential kind for the ledger, or false after answering.
func (s *Server) admit(w http.ResponseWriter, r *http.Request, endpoint, method string) (*request, func(), bool) {
	if r.Method != method {
		w.Header().Set("Allow", method)
		writeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return nil, nil, false
	}
	q := &request{s: s, w: w, r: r, ev: dispatch.GatewayEvent{Surface: surface, Endpoint: endpoint, Class: requestClass(r.Header), Billing: "api"}}
	select {
	case s.sem <- struct{}{}:
	default:
		w.Header().Set("Retry-After", "1")
		q.refuse(http.StatusTooManyRequests, "rate_limit_error", "gateway_busy", "gateway is busy")
		return nil, nil, false
	}
	release := func() { <-s.sem }
	present, oauth := credential(r.Header)
	switch {
	case oauth && !s.cfg.PassthroughSubscription:
		release()
		q.refuse(http.StatusForbidden, "permission_error", "subscription_token",
			"subscription OAuth tokens are not accepted by this gateway; start it with --gateway-passthrough-subscription to allow them")
		return nil, nil, false
	case oauth:
		q.ev.Billing = "subscription"
	case !present && s.cfg.APIKey == "":
		release()
		q.refuse(http.StatusUnauthorized, "authentication_error", "no_credential",
			"no x-api-key on the request and no ANTHROPIC_API_KEY in the gateway environment")
		return nil, nil, false
	}
	return q, release, true
}

// upstreamRequest builds the outbound request: the client's headers minus
// hop-by-hop, the operator key when the client sent none.
func (q *request) upstreamRequest(path string, body []byte) (*http.Request, error) {
	s := q.s
	u := &url.URL{Scheme: s.base.Scheme, Host: s.base.Host, Path: path, RawQuery: q.r.URL.RawQuery}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(q.r.Context(), q.r.Method, u.String(), rd)
	if err != nil {
		return nil, err
	}
	req.Header = make(http.Header)
	copyHeaders(req.Header, q.r.Header)
	req.Header.Del("Content-Length")
	req.Header.Del("Host")
	if present, _ := credential(q.r.Header); !present {
		req.Header.Set("X-Api-Key", s.cfg.APIKey)
	}
	return req, nil
}

func (s *Server) handleMessages(endpoint string) http.HandlerFunc {
	path := "/v1/messages"
	if endpoint == "count_tokens" {
		path += "/count_tokens"
	}
	return func(w http.ResponseWriter, r *http.Request) {
		q, release, ok := s.admit(w, r, endpoint, http.MethodPost)
		if !ok {
			return
		}
		defer release()
		defer q.finish()

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.cfg.maxBody))
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				q.refuse(http.StatusRequestEntityTooLarge, "request_too_large", "too_large", "request body too large")
			} else {
				q.refuse(http.StatusBadRequest, "invalid_request_error", "bad_body", "could not read the request body")
			}
			return
		}
		info := scanBody(body)
		q.ev.Stream = info.stream
		q.ev.ModelIn, q.ev.ModelOut = info.model, info.model
		if info.ok && info.modelKeys == 1 {
			if out := s.planModel(q.ev.Class, info.model); out != info.model {
				body = splice(body, info, out)
				q.ev.ModelOut, q.ev.Rewritten = out, true
			}
		}
		// The sensitive class (K-140) is decided before a non-primary upstream is
		// chosen. Today every route is the primary one, so this is a guard.
		up := s.cfg.route(q.ev.ModelOut)
		if !up.Primary && up.Kind != KindLocal {
			if reason := router.SensitiveReason(router.Input{Material: []string{string(body)}}); reason != "" {
				q.refuse(http.StatusForbidden, "permission_error", "sensitive_non_primary",
					"this request is classed sensitive and may only go to the primary upstream or a local slot")
				return
			}
		}
		req, err := q.upstreamRequest(path, body)
		if err != nil {
			q.refuse(http.StatusInternalServerError, "api_error", "bad_request", "could not build the upstream request")
			return
		}
		q.relay(req)
	}
}

// relay sends req and streams the reply to the client unchanged, flushing as
// bytes arrive.
func (q *request) relay(req *http.Request) {
	resp, err := q.s.client.Do(req)
	if err != nil {
		q.upstreamFailed(err)
		return
	}
	defer resp.Body.Close()
	h := q.w.Header()
	copyHeaders(h, resp.Header)
	q.w.WriteHeader(resp.StatusCode)
	q.ev.Status = resp.StatusCode

	t := newTap(resp.Header.Get("Content-Type"), resp.Header.Get("Content-Encoding"))
	rc := http.NewResponseController(q.w)
	buf := make([]byte, 16<<10)
	aborted := false
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := q.w.Write(buf[:n]); werr != nil {
				break
			}
			_, _ = t.Write(buf[:n])
			_ = rc.Flush()
		}
		if rerr != nil {
			if !errors.Is(rerr, io.EOF) {
				aborted = true
			}
			break
		}
	}
	_ = t.Close()
	if tp, ok := t.(*tap); ok {
		u := tp.usage()
		q.ev.InputTokens, q.ev.OutputTokens, q.ev.CacheRead, q.ev.CacheCreate = u.in, u.out, u.cacheRead, u.cacheCreate
	}
	if aborted {
		// A reply cut short upstream must not look complete to the client: drop
		// the connection instead of ending the chunked body cleanly.
		q.finish()
		panic(http.ErrAbortHandler)
	}
}

func (q *request) upstreamFailed(err error) {
	status, typ := http.StatusBadGateway, "api_error"
	reason := "upstream_unreachable"
	if errors.Is(err, context.Canceled) {
		status, typ, reason = 499, "api_error", "client_gone"
	}
	// err can carry the request URL; the client and the ledger get fixed text.
	q.ev.Status, q.ev.Refused = status, reason
	if status != 499 {
		writeError(q.w, status, typ, "could not reach api.anthropic.com")
	}
}
