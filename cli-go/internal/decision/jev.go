package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Wire facts below come from the TypeSafe documentation, re-fetched for this
// implementation on 2026-09-29. Nothing is invented; anything the docs do not
// specify (e.g. the error body shape) is not parsed.
//
//	Endpoint:  POST https://api.typesafe.ai/v1/systemone
//	           https://docs.typesafe.ai/api
//	Auth:      Authorization: Bearer <TYPESAFE_API_KEY>
//	           https://docs.typesafe.ai/introduction/quickstart
//	Request:    {"state": string|object|array, "model": string,
//	             "questions": {id: Question}}
//	Question:   noul   {type, instructions, criteria?: {true?, false?}}
//	            choice {type, instructions, criteria: {key: description}} (max 255)
//	            score  {type, instructions, criteria: [level...]}          (2-10)
//	Response:   {"model": string, "answers": {id: Answer},
//	             "usage": {"input_tokens": int, "output_tokens": int}}
//	Answer:     noul   {type, noul}
//	            choice {type, choice, probabilities, confidence}
//	            score  {type, score, legend, probabilities, confidence}
//	Errors:     HTTP 401, 422, 429, 529 (body shape unspecified; not parsed)
//	Models:     pinned "jev-1.13.0"; aliases jev-latest / jev-preview move.
//	            https://docs.typesafe.ai/models
//	Retry:      docs recommend exponential backoff on 429/529; we retry at most
//	            once (hook deadlines), 300 ms apart.
const (
	DefaultBaseURL = "https://api.typesafe.ai"
	systemOnePath  = "/v1/systemone"
	// PinnedModel is the version this build was written and tested against.
	PinnedModel = "jev-1.13.0"

	// KeyEnv is the documented credential variable. It is read at call time
	// only and never stored, logged, or forwarded to dispatched runtimes.
	KeyEnv = "TYPESAFE_API_KEY"
	// BaseURLEnv is the documented optional endpoint override.
	BaseURLEnv = "TYPESAFE_BASE_URL"

	maxResponseBytes = 1 << 20
	retryBackoff     = 300 * time.Millisecond
)

// Jev is the TypeSafe client. There is no Go SDK; raw HTTP is documented as
// supported (https://docs.typesafe.ai/sdk).
type Jev struct {
	BaseURL string // empty: $TYPESAFE_BASE_URL, then DefaultBaseURL
	HTTP    *http.Client
	Getenv  func(string) string
	Breaker *Breaker // nil disables (tests only)
	Budget  *Budget  // nil disables (tests only)
	Backoff time.Duration
	// NoRetry makes Decide a single attempt, whatever the status. The routing
	// shadow (K-177) sets it: it has no deadline to spare for a second try.
	NoRetry bool
}

// Name implements Provider.
func (j *Jev) Name() string { return ProviderJev }

func (j *Jev) getenv(k string) string {
	if j.Getenv != nil {
		return j.Getenv(k)
	}
	return os.Getenv(k)
}

func (j *Jev) key() string { return j.getenv(KeyEnv) }

// Available reports whether a call could be attempted, without a network call.
func (j *Jev) Available(_ context.Context) error {
	if KillSwitch(j.getenv) {
		return newErr(ClassDisabled, "%s=1", killSwitchEnvVar)
	}
	if j.key() == "" {
		return newErr(ClassNoKey, "%s is not set", KeyEnv)
	}
	if _, err := j.endpoint(); err != nil {
		return err
	}
	if j.Breaker != nil {
		if err := j.Breaker.Allow(); err != nil {
			return err
		}
	}
	if j.Budget != nil {
		if err := j.Budget.Check(""); err != nil {
			return err
		}
	}
	return nil
}

// endpoint validates the base URL. The bearer key and the redacted state are
// only ever sent to *.typesafe.ai over TLS, or to a loopback address (tests and
// local gateways). A cloned project's environment must not be able to point the
// key at another host, so any other host is refused.
func (j *Jev) endpoint() (string, error) {
	base := j.BaseURL
	if base == "" {
		base = j.getenv(BaseURLEnv)
	}
	if base == "" {
		base = DefaultBaseURL
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return "", newErr(ClassBadRequest, "invalid %s", BaseURLEnv)
	}
	if u.User != nil {
		return "", newErr(ClassBadRequest, "%s must not embed credentials", BaseURLEnv)
	}
	host := strings.ToLower(u.Hostname())
	switch {
	case isLoopbackHost(host) && (u.Scheme == "http" || u.Scheme == "https"):
	case u.Scheme == "https" && (host == "typesafe.ai" || strings.HasSuffix(host, ".typesafe.ai")):
	default:
		return "", newErr(ClassBadRequest, "%s host %q is not allowed: only https://*.typesafe.ai (or loopback for tests) may receive the API key", BaseURLEnv, host)
	}
	u.Path = u.Path + systemOnePath
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

type wireRequest struct {
	State     any                 `json:"state"`
	Model     string              `json:"model"`
	Questions map[string]Question `json:"questions"`
}

type wireResponse struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   Usage             `json:"usage"`
}

// Decide sends one request. It always returns either a validated Result or an
// *Error; the caller falls back to its deterministic path on any error.
func (j *Jev) Decide(ctx context.Context, req Request) (*Result, error) {
	if KillSwitch(j.getenv) {
		return nil, newErr(ClassDisabled, "%s=1", killSwitchEnvVar)
	}
	if !IsPinnedModel(req.Model) {
		return nil, newErr(ClassBadRequest, "model %q is not a pinned version", req.Model)
	}
	if len(req.Questions) == 0 {
		return nil, newErr(ClassBadRequest, "no questions")
	}
	key := j.key()
	if key == "" {
		return nil, newErr(ClassNoKey, "%s is not set", KeyEnv)
	}
	endpoint, err := j.endpoint()
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(wireRequest{State: req.State, Model: req.Model, Questions: req.Questions})
	if err != nil {
		return nil, newErr(ClassBadRequest, "request not serialisable")
	}
	// Belt and braces behind Sanitize: no caller can push an oversize state.
	if len(body) > HardMaxStateBytes+questionsAllowance {
		return nil, newErr(ClassOversize, "request is %d bytes", len(body))
	}

	if j.Breaker != nil {
		if err := j.Breaker.Allow(); err != nil {
			return nil, err
		}
	}
	if j.Budget != nil {
		if err := j.Budget.Reserve(req.Session); err != nil {
			return nil, err
		}
	}

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = PrefilterTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt == 1 {
			backoff := j.Backoff
			if backoff <= 0 {
				backoff = retryBackoff
			}
			if dl, ok := ctx.Deadline(); ok && time.Until(dl) <= backoff {
				break // not enough deadline left for a second try
			}
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				last = classifyTransport(ctx.Err())
				attempt = 2
				continue
			}
		}
		res, retry, err := j.once(ctx, endpoint, key, body, req)
		if err == nil {
			res.LatencyMS = time.Since(start).Milliseconds()
			if j.Breaker != nil {
				j.Breaker.Success()
			}
			if j.Budget != nil {
				j.Budget.AddUSD(res.CostUSD)
			}
			return res, nil
		}
		last = err
		if !retry || j.NoRetry {
			break
		}
	}
	if last == nil {
		last = newErr(ClassTimeout, "deadline exceeded")
	}
	if j.Breaker != nil {
		switch ErrorClass(last) {
		case ClassBadRequest, ClassOversize, ClassNoKey, ClassDisabled, ClassBudget:
		default:
			j.Breaker.Failure(ErrorClass(last))
		}
	}
	return nil, last
}

// questionsAllowance is headroom for the question set inside the wire body;
// the vendor limit covers state plus the longest single question.
const questionsAllowance = 32 * 1024

func (j *Jev) client() *http.Client {
	c := &http.Client{}
	if j.HTTP != nil {
		*c = *j.HTTP
	}
	// Never follow redirects: the Authorization header must not travel to a
	// host the operator did not configure.
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}

// once performs a single HTTP attempt. retry reports whether the failure is in
// the documented retryable set (408/429/529/5xx).
func (j *Jev) once(ctx context.Context, endpoint, key string, body []byte, req Request) (*Result, bool, error) {
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, false, newErr(ClassBadRequest, "cannot build request")
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "application/json")
	hreq.Header.Set("Authorization", "Bearer "+key)

	resp, err := j.client().Do(hreq)
	if err != nil {
		return nil, false, classifyTransport(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096)) // body shape is unspecified; never surfaced
		class, retry := classifyStatus(resp.StatusCode)
		return nil, retry, newErr(class, "HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, false, classifyTransport(err)
	}
	if len(data) > maxResponseBytes {
		return nil, false, newErr(ClassMalformed, "response exceeds %d bytes", maxResponseBytes)
	}
	var wr wireResponse
	if err := json.Unmarshal(data, &wr); err != nil {
		return nil, false, newErr(ClassMalformed, "response is not valid JSON")
	}
	if err := validateResponse(&wr, req.Questions); err != nil {
		return nil, false, err
	}
	answers := make(map[string]Answer, len(req.Questions))
	for id := range req.Questions {
		answers[id] = wr.Answers[id]
	}
	return &Result{
		Provider: ProviderJev, Model: wr.Model, Answers: answers,
		Usage: wr.Usage, CostUSD: CostUSD(wr.Usage),
	}, false, nil
}

func classifyStatus(code int) (class string, retry bool) {
	switch {
	case code == 401:
		return ClassHTTP401, false
	case code == 422:
		return ClassHTTP422, false
	case code == 429:
		return ClassHTTP429, true
	case code == 529:
		return ClassHTTP529, true
	case code == 408:
		return ClassHTTPOther, true
	case code >= 500:
		return ClassHTTP5xx, true
	default:
		return ClassHTTPOther, false
	}
}

func classifyTransport(err error) *Error {
	var ne net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return &Error{Class: ClassTimeout, Err: err}
	case errors.As(err, &ne) && ne.Timeout():
		return &Error{Class: ClassTimeout, Err: err}
	case errors.Is(err, context.Canceled):
		return &Error{Class: ClassTimeout, Err: err}
	default:
		return &Error{Class: ClassNetwork, Err: err}
	}
}

// validateResponse treats the vendor's answer as untrusted input: every asked
// question must be answered with the right shape and in-range numbers.
func validateResponse(wr *wireResponse, qs map[string]Question) *Error {
	if wr.Model == "" {
		return newErr(ClassMalformed, "response has no model")
	}
	if wr.Usage.InputTokens < 0 || wr.Usage.OutputTokens < 0 {
		return newErr(ClassMalformed, "negative usage")
	}
	for _, id := range sortedKeys(qs) {
		q := qs[id]
		a, ok := wr.Answers[id]
		if !ok {
			return newErr(ClassMalformed, "answer %q missing", id)
		}
		if a.Type != q.Type {
			return newErr(ClassMalformed, "answer %q has type %q, want %q", id, a.Type, q.Type)
		}
		switch q.Type {
		case "noul":
			if a.Noul == nil || !unit(*a.Noul) {
				return newErr(ClassMalformed, "answer %q: noul missing or out of [0,1]", id)
			}
		case "choice":
			opts, _ := asStringMap(q.Criteria)
			if _, ok := opts[a.Choice]; !ok {
				return newErr(ClassMalformed, "answer %q: choice is not one of the asked options", id)
			}
			if err := checkDist(id, a); err != nil {
				return err
			}
		case "score":
			if a.Score == nil || math.IsNaN(*a.Score) || math.IsInf(*a.Score, 0) || *a.Score < 0 {
				return newErr(ClassMalformed, "answer %q: score missing or invalid", id)
			}
			if err := checkDist(id, a); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkDist(id string, a Answer) *Error {
	if a.Confidence == nil || !unit(*a.Confidence) {
		return newErr(ClassMalformed, "answer %q: confidence missing or out of [0,1]", id)
	}
	for k, p := range a.Probabilities {
		if !unit(p) {
			return newErr(ClassMalformed, "answer %q: probability %q out of [0,1]", id, k)
		}
	}
	return nil
}

func unit(f float64) bool { return !math.IsNaN(f) && f >= 0 && f <= 1 }
