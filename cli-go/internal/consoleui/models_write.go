package consoleui

// models_write.go: the Models tab's browser writes (K-175, the K-153b design).
//
//	PUT  /api/models/{enable|disable|alias|pin|pricing}   model registry overlay
//	PUT  /api/router/policy                               router policy rules
//	GET  /api/models/write-session                        mint the CSRF token
//	POST /api/models/step-up                              re-authenticate (5 min)
//
// These go through internal/policywrite, the same checks and trusted writers the
// CLI uses (statepath.EditYAML: trust-checked read, 0600 atomic rename, only the
// rules key of the router policy changes, no YAML anchors/aliases/merge keys), and
// each change is recorded as the same config_changed line, with actor
// operator-browser and the server-side identity (never a header or body value).
//
// Off by default. Config.ModelWrites (the operator's `yakos serve
// --console-model-writes`) turns the write paths on; a project file, a request
// header or a body cannot. While it is off every path answers 405.
//
// The stack on every write, in the order it runs. Each layer refuses on its own and
// leaves both policy files byte-identical and the audit log untouched:
//
//	role        RoleAdmin (route wrapper, not re-checked here)
//	host        DNS rebinding: the loopback Host check again on loopback, the
//	            configured external hosts on the networked bind
//	fetch/origin Sec-Fetch-Site must be absent, same-origin or none; Origin, when
//	            present, must be this server's own origin (not "null")
//	content type application/json exactly (415)
//	csrf        a per-credential token, double-submitted: header X-CSRF-Token and
//	            cookie yakos_wcsrf must both equal the token derived from the
//	            credential (session id, loopback bearer token or client-cert
//	            fingerprint). A token minted for another credential never matches.
//	step-up     the credential re-authenticated within 5 minutes: the password on a
//	            session, the bearer token typed again on loopback, the certificate on
//	            mTLS. Refusal is 401 with a structured body.
//	body        at most 64 KiB, JSON, unknown fields refused; the DTO has no
//	            privileged field to bind.
//
// Responses are Cache-Control: no-store, nosniff and JSON, with fixed error text
// that never echoes a request value, a path or a secret.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bakw00ds/yakos/internal/authsession"
	"github.com/bakw00ds/yakos/internal/dashauth"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/modelreg"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/policywrite"
	"github.com/bakw00ds/yakos/internal/statepath"
	"github.com/bakw00ds/yakos/internal/userstore"
)

const (
	maxWriteBody = 64 << 10
	maxStepBody  = 4 << 10
	// stepUpWindow is how long a re-authentication lets writes through.
	stepUpWindow = 5 * time.Minute
	// stepUpMaxFails failed re-authentications per stepUpFailWindow per credential
	// lock further attempts out (429) until the window passes.
	stepUpMaxFails   = 5
	stepUpFailWindow = time.Minute
	maxTrackedCreds  = 1024

	csrfCookieName = "yakos_wcsrf"
	actorBrowser   = "operator-browser"
)

type credKind string

const (
	credSession credKind = "session"
	credBearer  credKind = "bearer"
	credCert    credKind = "cert"
)

// credential is what the request authenticated with, as the server sees it.
type credential struct {
	kind credKind
	// id is the secret-derived, non-reversible handle of the credential: the
	// session id, the sha256 of the bearer token or the certificate fingerprint.
	id string
	// csrf is the token that credential must present.
	csrf string
	user string // session: the username the password is checked for
}

func (c credential) key() string { return string(c.kind) + ":" + c.id }

func (c credential) stepUpMethod() string {
	switch c.kind {
	case credSession:
		return "password"
	case credCert:
		return "certificate"
	}
	return "token"
}

type failCount struct {
	n     int
	since time.Time
}

// modelsWriter holds what the write paths need. All of it comes from the server's
// Config; nothing from a request.
type modelsWriter struct {
	enabled       bool
	networked     bool
	addr          string
	externalHosts []string
	token         string // loopback bearer token
	authStore     *authsession.Store
	userStore     *userstore.Store
	stateDir      func() string
	workspace     string
	now           func() time.Time
	key           [32]byte

	mu      sync.Mutex
	stepUps map[string]time.Time
	fails   map[string]failCount
}

func newModelsWriter(cfg *Config, workspace string, stateDir func() string) *modelsWriter {
	w := &modelsWriter{
		enabled: cfg.ModelWrites, networked: cfg.NetworkedMode, addr: cfg.addr(), externalHosts: cfg.externalHosts(),
		token: cfg.Token, authStore: cfg.AuthSessionStore, userStore: cfg.UserStore,
		stateDir: stateDir, workspace: workspace, now: time.Now,
		stepUps: map[string]time.Time{}, fails: map[string]failCount{},
	}
	if _, err := rand.Read(w.key[:]); err != nil {
		w.enabled = false // no randomness, no tokens: fail closed
	}
	return w
}

// modelsPathHeaders stamps Cache-Control: no-store and nosniff on every response
// for the Models tab's paths before any middleware can answer, so a refusal from
// the Host guard, the token gate or the session CSRF check carries them too.
func modelsPathHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := r.URL.Path; strings.HasPrefix(p, "/api/models") || p == "/api/router/policy" {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
		}
		next.ServeHTTP(w, r)
	})
}

// ---- credential and token binding ------------------------------------------

func (w *modelsWriter) derive(kind credKind, id string) string {
	h := hmac.New(sha256.New, w.key[:])
	h.Write([]byte("yakos-k175-csrf\x00" + string(kind) + "\x00" + id))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// credentialOf resolves what authenticated r. ok is false when no credential the
// server recognizes backs the identity (fail closed).
func (w *modelsWriter) credentialOf(r *http.Request, id netid.Identity) (credential, bool) {
	switch id.AuthMethod {
	case netid.AuthMethodCert:
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.VerifiedChains[0]) == 0 {
			return credential{}, false
		}
		sum := sha256.Sum256(r.TLS.VerifiedChains[0][0].Raw)
		fp := hex.EncodeToString(sum[:])
		return credential{kind: credCert, id: fp, csrf: w.derive(credCert, fp)}, true
	case netid.AuthMethodSession:
		if w.authStore == nil || w.userStore == nil {
			return credential{}, false
		}
		ck, err := r.Cookie(sessionCookieName)
		if err != nil || ck.Value == "" {
			return credential{}, false
		}
		sess, found := w.authStore.Lookup(ck.Value)
		if !found || sess.Username != id.OperatorID {
			return credential{}, false
		}
		// The session's own CSRF token is the one the global session check already
		// requires in X-CSRF-Token; reusing it keeps one token per session.
		return credential{kind: credSession, id: sess.ID, csrf: sess.CSRFToken, user: sess.Username}, true
	default:
		// The loopback bearer token. Not available on the networked bind.
		if w.networked || w.token == "" {
			return credential{}, false
		}
		tok := dashauth.BearerToken(r)
		if tok == "" || !dashauth.TokenEqual(tok, w.token) {
			return credential{}, false
		}
		h := sha256Hex(tok)
		return credential{kind: credBearer, id: h, csrf: w.derive(credBearer, h)}, true
	}
}

// ---- layers -----------------------------------------------------------------

// hostOK is the DNS-rebinding check. On loopback the daemon's Host check already
// ran; it runs again here so the write path does not depend on its position.
func (w *modelsWriter) hostOK(r *http.Request) bool {
	if w.networked {
		for _, h := range w.externalHosts {
			if strings.EqualFold(h, r.Host) {
				return true
			}
		}
		return false
	}
	ok := false
	dashauth.RequireLocalHost(w.addr, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { ok = true })).
		ServeHTTP(discardWriter{}, r)
	return ok
}

type discardWriter struct{}

func (discardWriter) Header() http.Header         { return http.Header{} }
func (discardWriter) Write(b []byte) (int, error) { return len(b), nil }
func (discardWriter) WriteHeader(int)             {}

// fetchOK is the cross-site refusal: Sec-Fetch-Site and Origin.
func (w *modelsWriter) fetchOK(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
	default: // cross-site, same-site, anything else
		return false
	}
	o := r.Header.Get("Origin")
	if o == "" {
		return true // not a browser fetch (curl, a script); the CSRF layer still applies
	}
	u, err := url.Parse(o)
	if err != nil || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return false // includes "null"
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return u.Scheme == scheme && strings.EqualFold(u.Host, r.Host)
}

func jsonContentType(r *http.Request) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mt == "application/json"
}

// csrfOK is the double-submit check: the header and the cookie must both carry
// the token derived from the request's own credential.
func csrfOK(r *http.Request, cred credential) bool {
	hdr := r.Header.Get("X-CSRF-Token")
	ck, err := r.Cookie(csrfCookieName)
	if hdr == "" || err != nil || ck.Value == "" || cred.csrf == "" {
		return false
	}
	a := subtle.ConstantTimeCompare([]byte(hdr), []byte(cred.csrf))
	b := subtle.ConstantTimeCompare([]byte(ck.Value), []byte(cred.csrf))
	return a&b == 1
}

func (w *modelsWriter) steppedUp(cred credential) (time.Time, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	at, ok := w.stepUps[cred.key()]
	if !ok {
		return time.Time{}, false
	}
	now := w.now()
	if now.Before(at) || now.Sub(at) >= stepUpWindow {
		delete(w.stepUps, cred.key())
		return time.Time{}, false
	}
	return at.Add(stepUpWindow), true
}

func (w *modelsWriter) recordStepUp(cred credential) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	if len(w.stepUps) >= maxTrackedCreds {
		for k, at := range w.stepUps {
			if now.Sub(at) >= stepUpWindow {
				delete(w.stepUps, k)
			}
		}
		if len(w.stepUps) >= maxTrackedCreds {
			w.stepUps = map[string]time.Time{}
		}
	}
	w.stepUps[cred.key()] = now
	delete(w.fails, cred.key())
}

// locked reports whether cred has used up its failed re-authentications.
func (w *modelsWriter) locked(cred credential) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	f := w.fails[cred.key()]
	return f.n >= stepUpMaxFails && w.now().Sub(f.since) < stepUpFailWindow
}

func (w *modelsWriter) recordFail(cred credential) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	if len(w.fails) >= maxTrackedCreds {
		w.fails = map[string]failCount{}
	}
	f := w.fails[cred.key()]
	if f.n == 0 || now.Sub(f.since) >= stepUpFailWindow {
		f = failCount{since: now}
	}
	f.n++
	w.fails[cred.key()] = f
}

// ---- guard ------------------------------------------------------------------

// guard runs the layers shared by every mutating path (step-up itself skips the
// step-up layer). It writes the refusal and returns false when one fails.
func (w *modelsWriter) guard(rw http.ResponseWriter, r *http.Request, wantMethod string, needStepUp bool) (credential, netid.Identity, bool) {
	if !w.enabled || r.Method != wantMethod {
		rw.Header().Set("Allow", wantMethod)
		modelsError(rw, http.StatusMethodNotAllowed, "method not allowed")
		return credential{}, netid.Identity{}, false
	}
	if !w.hostOK(r) {
		modelsError(rw, http.StatusForbidden, "unexpected host")
		return credential{}, netid.Identity{}, false
	}
	if !w.fetchOK(r) {
		modelsError(rw, http.StatusForbidden, "cross-site request refused")
		return credential{}, netid.Identity{}, false
	}
	if !jsonContentType(r) {
		modelsError(rw, http.StatusUnsupportedMediaType, "content type must be application/json")
		return credential{}, netid.Identity{}, false
	}
	id := netid.IdentityFrom(r.Context())
	cred, ok := w.credentialOf(r, id)
	if !ok || !csrfOK(r, cred) {
		modelsError(rw, http.StatusForbidden, "invalid CSRF token")
		return credential{}, netid.Identity{}, false
	}
	if needStepUp {
		if _, ok := w.steppedUp(cred); !ok {
			writeModelsJSON(rw, http.StatusUnauthorized, map[string]string{"error": "step_up_required", "method": cred.stepUpMethod()})
			return credential{}, netid.Identity{}, false
		}
	}
	return cred, id, true
}

// decode reads the bounded JSON body into dst (unknown fields refused).
func decodeBody(rw http.ResponseWriter, r *http.Request, limit int64, dst any) bool {
	if r.ContentLength > limit {
		modelsError(rw, http.StatusRequestEntityTooLarge, "request body too large")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(rw, r.Body, limit))
	dec.DisallowUnknownFields()
	err := dec.Decode(dst)
	if err == nil {
		if _, e2 := dec.Token(); !errors.Is(e2, io.EOF) {
			err = errors.New("trailing data")
		}
	}
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			modelsError(rw, http.StatusRequestEntityTooLarge, "request body too large")
		} else {
			modelsError(rw, http.StatusBadRequest, "invalid request body")
		}
		return false
	}
	return true
}

// ---- mint and step-up -------------------------------------------------------

// handleWriteSession mints the CSRF token for the caller's credential, sets the
// double-submit cookie and says whether a step-up is current.
func (m *modelsPage) handleWriteSession(rw http.ResponseWriter, r *http.Request) {
	w := m.w
	if !w.enabled {
		modelsError(rw, http.StatusNotFound, "browser writes are off")
		return
	}
	if !getOnly(rw, r) {
		return
	}
	if !w.hostOK(r) || !w.fetchOK(r) {
		modelsError(rw, http.StatusForbidden, "request refused")
		return
	}
	cred, ok := w.credentialOf(r, netid.IdentityFrom(r.Context()))
	if !ok {
		modelsError(rw, http.StatusForbidden, "no credential for browser writes")
		return
	}
	http.SetCookie(rw, &http.Cookie{Name: csrfCookieName, Value: cred.csrf, Path: "/api/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil})
	out := map[string]any{"csrf_token": cred.csrf, "step_up_method": cred.stepUpMethod(), "step_up_current": false}
	if until, ok := w.steppedUp(cred); ok {
		out["step_up_current"] = true
		out["step_up_expires"] = until.UTC().Format(time.RFC3339)
	}
	writeModelsJSON(rw, http.StatusOK, out)
}

type stepUpDTO struct {
	Password string `json:"password"`
	Token    string `json:"token"`
}

// handleStepUp re-authenticates the caller. Every failure is the same 401 body.
func (m *modelsPage) handleStepUp(rw http.ResponseWriter, r *http.Request) {
	w := m.w
	cred, _, ok := w.guard(rw, r, http.MethodPost, false)
	if !ok {
		return
	}
	if w.locked(cred) {
		writeModelsJSON(rw, http.StatusTooManyRequests, map[string]string{"error": "step_up_failed"})
		return
	}
	var dto stepUpDTO
	if !decodeBody(rw, r, maxStepBody, &dto) {
		return
	}
	good := false
	switch cred.kind {
	case credSession:
		// Verify does the full password derivation for an unknown or disabled user
		// too, so the time does not tell which case it was.
		_, err := w.userStore.Verify(cred.user, dto.Password)
		good = err == nil
	case credBearer:
		// Compare fixed-length digests: the time depends on neither value.
		a, b := sha256.Sum256([]byte(dto.Token)), sha256.Sum256([]byte(w.token))
		good = subtle.ConstantTimeCompare(a[:], b[:]) == 1
	case credCert:
		// The certificate was verified by the TLS stack and bound as the
		// credential; presenting it on this request is the step-up.
		good = true
	}
	if !good {
		w.recordFail(cred)
		writeModelsJSON(rw, http.StatusUnauthorized, map[string]string{"error": "step_up_failed"})
		return
	}
	w.recordStepUp(cred)
	writeModelsJSON(rw, http.StatusOK, map[string]any{"ok": true, "expires_in": int(stepUpWindow.Seconds())})
}

// ---- the writes --------------------------------------------------------------

type enableDTO struct {
	ID string `json:"id"`
}

type aliasDTO struct {
	Alias   string `json:"alias"`
	Harness string `json:"harness"`
	ID      string `json:"id"` // "" or "default" is the harness default
}

type pinDTO struct {
	Agent   string `json:"agent"`
	ID      string `json:"id"`
	Runtime string `json:"runtime"`
	Clear   bool   `json:"clear"`
}

type pricingDTO struct {
	ID         string   `json:"id"`
	Input      *float64 `json:"input"`
	Output     *float64 `json:"output"`
	CacheRead  *float64 `json:"cache_read"`
	CacheWrite *float64 `json:"cache_write"`
	Billing    string   `json:"billing"`
	Clear      bool     `json:"clear"`
}

type policyDTO struct {
	RulesYAML string `json:"rules_yaml"`
}

func fnum(p *float64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p, 'g', -1, 64)
}

type changeView struct {
	File    string `json:"file"`
	Action  string `json:"action"`
	Changed bool   `json:"changed"`
	SHA     string `json:"sha"`
}

// handleWrite serves one PUT. op is the path's last element ("policy" for the
// router policy).
func (m *modelsPage) handleWrite(op string) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		w := m.w
		_, id, ok := w.guard(rw, r, http.MethodPut, true)
		if !ok {
			return
		}
		var run func(reg *modelreg.Registry, state string, rec policywrite.Recorder) error
		switch op {
		case "enable", "disable":
			var d enableDTO
			if !decodeBody(rw, r, maxWriteBody, &d) {
				return
			}
			run = func(reg *modelreg.Registry, state string, rec policywrite.Recorder) error {
				return policywrite.SetEnabled(state, reg, d.ID, op == "enable", rec)
			}
		case "alias":
			var d aliasDTO
			if !decodeBody(rw, r, maxWriteBody, &d) {
				return
			}
			if d.ID == "default" {
				d.ID = ""
			}
			run = func(reg *modelreg.Registry, state string, rec policywrite.Recorder) error {
				return policywrite.SetAlias(state, reg, d.Alias, d.Harness, d.ID, rec)
			}
		case "pin":
			var d pinDTO
			if !decodeBody(rw, r, maxWriteBody, &d) {
				return
			}
			run = func(reg *modelreg.Registry, state string, rec policywrite.Recorder) error {
				return policywrite.SetPin(state, reg, d.Agent, d.ID, d.Runtime, d.Clear, rec)
			}
		case "pricing":
			var d pricingDTO
			if !decodeBody(rw, r, maxWriteBody, &d) {
				return
			}
			run = func(reg *modelreg.Registry, state string, rec policywrite.Recorder) error {
				return policywrite.SetPricing(state, reg, d.ID, policywrite.PricingArgs{
					Input: fnum(d.Input), Output: fnum(d.Output), CacheRead: fnum(d.CacheRead), CacheWrite: fnum(d.CacheWrite),
					Billing: d.Billing, Clear: d.Clear,
				}, rec)
			}
		default: // policy
			var d policyDTO
			if !decodeBody(rw, r, maxWriteBody, &d) {
				return
			}
			run = func(_ *modelreg.Registry, state string, rec policywrite.Recorder) error {
				return policywrite.SetRules(state, []byte(d.RulesYAML), rec)
			}
		}
		w.perform(rw, id, run)
	}
}

// perform opens the audit log first (a change that cannot be recorded is not
// made), loads the registry the validation reads, runs the writer and records each
// change it makes.
func (w *modelsWriter) perform(rw http.ResponseWriter, id netid.Identity, run func(*modelreg.Registry, string, policywrite.Recorder) error) {
	state := w.stateDir()
	if state == "" {
		modelsError(rw, http.StatusServiceUnavailable, "no state directory")
		return
	}
	au, err := dispatch.OpenConfigAudit(dispatch.Request{OperatorID: auditOperator(id.OperatorID), Surface: dispatch.SurfaceConsole}, state)
	if err != nil {
		modelsError(rw, http.StatusServiceUnavailable, "the dispatch log cannot be opened, so the change was not made")
		return
	}
	defer au.Close()
	reg, err := modelreg.Load(modelreg.Options{StateDir: state, Project: w.workspace,
		Snapshots: modelreg.NewDiscoverer(modelreg.DiscovererConfig{StateDir: state})})
	if err != nil {
		modelsError(rw, http.StatusServiceUnavailable, "model registry unavailable")
		return
	}
	var views []changeView
	unrecorded := false
	rec := func(c policywrite.Change) error {
		views = append(views, changeView{File: c.File, Action: c.Action, Changed: c.Res.Changed, SHA: c.Res.SHAAfter})
		if !c.Res.Changed {
			return nil
		}
		if err := au.Record(dispatch.ConfigChange{File: c.File, Action: c.Action, SHABefore: c.Res.SHABefore, SHAAfter: c.Res.SHAAfter,
			Surface: dispatch.SurfaceConsole, Actor: actorBrowser, AuthMethod: id.AuthMethod.String()}); err != nil {
			unrecorded = true
			return err
		}
		return nil
	}
	err = run(reg, state, rec)
	switch {
	case unrecorded:
		modelsError(rw, http.StatusInternalServerError, "the change was written but could not be recorded in the dispatch log")
	case err == nil:
		changed := false
		for _, v := range views {
			changed = changed || v.Changed
		}
		if views == nil {
			views = []changeView{}
		}
		writeModelsJSON(rw, http.StatusOK, map[string]any{"ok": true, "changed": changed, "changes": views})
	default:
		status, msg := refusalOf(err)
		modelsError(rw, status, msg)
	}
}

// refusalOf maps a writer error to a status and fixed text. The CLI's text can
// echo the request's own values; the browser gets the code's meaning only.
func refusalOf(err error) (int, string) {
	var pe *policywrite.Error
	if errors.As(err, &pe) {
		switch pe.Code {
		case policywrite.CodeUnknownModel:
			return http.StatusUnprocessableEntity, "unknown model"
		case policywrite.CodeDisabled:
			return http.StatusConflict, "that model is disabled; enable it first"
		case policywrite.CodeAmbiguous:
			return http.StatusConflict, "that model is offered by more than one runtime; name one"
		case policywrite.CodeBilling:
			return http.StatusUnprocessableEntity, "a price counts only for api billing"
		}
		return http.StatusBadRequest, "invalid value"
	}
	if errors.Is(err, statepath.ErrBusy) {
		return http.StatusServiceUnavailable, "the policy file is being edited; retry"
	}
	return http.StatusUnprocessableEntity, "the policy writer refused the change; use the yakos CLI to see why"
}

// auditOperator makes the server-side identity safe for the log's identifier
// field: characters it would not accept become underscores.
func auditOperator(op string) string {
	if op == "" {
		return "unknown"
	}
	b := []byte(op)
	if len(b) > 100 {
		b = b[:100]
	}
	for i, c := range b {
		alnum := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if !alnum && (i == 0 || strings.IndexByte("._:/@+-", c) < 0) {
			b[i] = '_'
			if i == 0 {
				b[i] = 'u' // the log's identifier field wants an alphanumeric first character
			}
		}
	}
	return string(b)
}
