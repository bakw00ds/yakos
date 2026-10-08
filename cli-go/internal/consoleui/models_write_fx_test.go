package consoleui_test

// models_write_fx_test.go: fixtures for the Models tab's browser writes (K-175).
// A wfx is a console in one of three credential regimes (loopback bearer token,
// networked session cookie, networked client certificate) with scratch home,
// state files, dispatch log and clock. Requests go through the server's full
// handler (Host guard, token gate, resolver, CSRF, JSON gate, mux) unless a test
// asks for the bare mux with an injected identity.

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/authsession"
	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/modelreg"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/statepath"
	"github.com/bakw00ds/yakos/internal/userstore"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

type wmode string

const (
	modeLoopback wmode = "loopback"
	modeSession  wmode = "session"
	modeCert     wmode = "cert"
)

const (
	wHost     = "127.0.0.1:7890"
	wPassword = "correcthorsebattery1"
	wLoopOp   = "loop-operator"
)

const (
	overlaySeed = "models:\n  gpt-5.5:\n    enabled: false\nx_secret: SENTINEL-OVERLAY-KEY\n"
	policySeed  = "x_secret: SENTINEL-POLICY-KEY\nallow_unsandboxed_runtimes: [codex]\nhooks_endpoint: true\n" +
		"rules:\n  - {match: {class: chat}, action: {runtime: claude, model: sonnet}}\n"
)

// wcred is one credential of a fixture: how to attach it to a request.
type wcred struct {
	apply func(*http.Request)
	// stepBody is the JSON body that re-authenticates it.
	stepBody string
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type wfx struct {
	t                     *testing.T
	mode                  wmode
	home, ledger, work    string
	srv                   *consoleui.Server
	full                  http.Handler
	tok                   string
	clock                 *fakeClock
	a, b                  wcred // two credentials of the same regime (loopback: b == a)
	https                 bool
	aStore                *authsession.Store
	uStore                *userstore.Store
	wantOperator, wantVia string
	noAutoBase            bool // put leaves a policy body without base_sha as written
}

func (f *wfx) state() string { return filepath.Join(f.home, ".yakos-state") }

func (f *wfx) writeState(name, body string) {
	f.t.Helper()
	if err := os.MkdirAll(f.state(), 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.state(), name), []byte(body), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *wfx) read(name string) string {
	b, _ := os.ReadFile(filepath.Join(f.state(), name))
	return string(b)
}

// files is both policy files, for byte-identity checks.
func (f *wfx) files() string {
	return f.read("model-registry.yml") + "\x00" + f.read("router-policy.yml")
}

// audit returns the config_changed lines of the home dispatch log.
func (f *wfx) audit() []map[string]any {
	b, _ := os.ReadFile(statepath.DispatchLogIn(f.state()))
	var out []map[string]any
	for _, ln := range strings.Split(string(b), "\n") {
		if !strings.Contains(ln, `"config_changed"`) {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			f.t.Fatalf("audit line %q: %v", ln, err)
		}
		out = append(out, m)
	}
	return out
}

func newWfx(t *testing.T, mode wmode, writes bool) *wfx {
	t.Helper()
	if goruntime.GOOS == "windows" {
		t.Skip("POSIX paths and modes")
	}
	f := &wfx{t: t, mode: mode, home: t.TempDir(), ledger: t.TempDir(), work: t.TempDir(),
		clock: &fakeClock{t: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}}
	t.Setenv("HOME", f.home)
	t.Setenv("YAKOS_DISPATCH_LOG", f.ledger) // a project can set this; the audit must ignore it
	t.Setenv("YAKOS_ROOT", "")
	t.Setenv("YAKOS_LIB", "")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "SENTINEL-OPENAI-KEY")
	f.writeState("model-registry.yml", overlaySeed)
	f.writeState("router-policy.yml", policySeed)

	tokState := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(tokState)
	if err != nil {
		t.Fatal(err)
	}
	f.tok = tok
	bus := wsbus.New()
	t.Cleanup(bus.Stop)
	cfg := consoleui.Config{
		Token: tok, KanbanBoardPath: t.TempDir() + "/kanban.md", KanbanProject: "test",
		MetricsProjectDir: t.TempDir(), PerfWorkDir: t.TempDir(), Bus: bus, WorkDir: t.TempDir(),
		WorkspaceRoot: t.TempDir(), YakosRoot: t.TempDir(), ModelWrites: writes, LoopbackOwnerID: wLoopOp,
	}
	var certA, certB *x509.Certificate
	switch mode {
	case modeLoopback:
		f.wantOperator, f.wantVia = wLoopOp, "none"
	case modeSession:
		f.https = true
		f.wantOperator, f.wantVia = "alice", "session"
		uStore, err := userstore.Open(t.TempDir() + "/users.json")
		if err != nil {
			t.Fatal(err)
		}
		if err := uStore.Create("alice", wPassword, netid.RoleAdmin); err != nil {
			t.Fatal(err)
		}
		if err := uStore.Create("bob", "correcthorsebattery2", netid.RoleRead); err != nil {
			t.Fatal(err)
		}
		f.uStore, f.aStore = uStore, authsession.NewStore(authsession.Config{})
		cfg.NetworkedMode, cfg.AuthSessionStore, cfg.UserStore = true, f.aStore, uStore
	case modeCert:
		f.https = true
		f.wantOperator, f.wantVia = "ops-cert", "cert"
		sd := t.TempDir()
		if err := os.MkdirAll(filepath.Join(sd, "mtls"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sd, "mtls", "roles.json"), []byte(`{"ops-cert":"admin","reader-cert":"read"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg.NetworkedMode, cfg.StateDir = true, sd
		cfg.AuthSessionStore = authsession.NewStore(authsession.Config{})
		uStore, err := userstore.Open(t.TempDir() + "/users.json")
		if err != nil {
			t.Fatal(err)
		}
		cfg.UserStore = uStore
		certA = &x509.Certificate{Raw: []byte("cert-A-bytes"), Subject: pkix.Name{CommonName: "ops-cert"}}
		certB = &x509.Certificate{Raw: []byte("cert-B-bytes"), Subject: pkix.Name{CommonName: "ops-cert"}}
	}
	f.srv = consoleui.MustNew(t, cfg)
	consoleui.SetModelsWriterNowForTest(f.srv, f.clock.Now)
	f.full = f.srv.FullHandler()

	switch mode {
	case modeLoopback:
		f.a = wcred{apply: func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }, stepBody: `{"token":"` + tok + `"}`}
		f.b = f.a
	case modeSession:
		mk := func() wcred {
			sess, err := f.aStore.Create("alice", netid.RoleAdmin, 0)
			if err != nil {
				t.Fatal(err)
			}
			return wcred{apply: func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "yakos_session", Value: sess.ID}) }, stepBody: `{"password":"` + wPassword + `"}`}
		}
		f.a, f.b = mk(), mk()
	case modeCert:
		mk := func(c *x509.Certificate) wcred {
			return wcred{apply: func(r *http.Request) {
				r.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{c}}}
			}, stepBody: `{}`}
		}
		f.a, f.b = mk(certA), mk(certB)
	}
	return f
}

// req builds a request carrying credential c. Nothing else is set.
func (f *wfx) req(method, path, body string, c wcred) *http.Request {
	r := httptest.NewRequest(method, "http://"+wHost+path, strings.NewReader(body))
	r.Host = wHost
	if f.https {
		r.TLS = &tls.ConnectionState{}
	}
	c.apply(r)
	return r
}

func (f *wfx) serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	return rr
}

// mint fetches the CSRF token for c through the write-session endpoint.
func (f *wfx) mint(c wcred) string {
	f.t.Helper()
	rr := f.serve(f.full, f.req(http.MethodGet, "/api/models/write-session", "", c))
	if rr.Code != 200 {
		f.t.Fatalf("write-session: %d %s", rr.Code, rr.Body.String())
	}
	var m map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		f.t.Fatal(err)
	}
	tok, _ := m["csrf_token"].(string)
	if tok == "" {
		f.t.Fatalf("no csrf_token: %s", rr.Body.String())
	}
	return tok
}

func (f *wfx) origin() string {
	if f.https {
		return "https://" + wHost
	}
	return "http://" + wHost
}

// wpath is the route of an operation.
func wpath(op string) string {
	if op == "policy" {
		return "/api/router/policy"
	}
	return "/api/models/" + op
}

// wbodies are bodies that change something, per operation, against the seed.
var wbodies = map[string]string{
	"enable":  `{"id":"gpt-5.5"}`,
	"disable": `{"id":"gpt-5.6-terra"}`,
	"alias":   `{"alias":"balanced","harness":"codex","id":"gpt-5.6-terra"}`,
	"pin":     `{"agent":"backend","id":"gpt-5.6-terra","runtime":"codex"}`,
	"pricing": `{"id":"gpt-5.6-terra","billing":"api","input":1.5,"output":6}`,
	"policy":  `{"rules_yaml":"- {match: {agent: backend}, action: {runtime: codex, model: gpt-5.6-terra}}\n"}`,
}

var wops = []string{"enable", "disable", "alias", "pin", "pricing", "policy"}

// put builds a fully valid write for op as credential c with a current step-up
// NOT assumed; mods then break it.
func (f *wfx) put(op, body string, c wcred, csrf string, mods ...func(*http.Request)) *http.Request {
	if op == "policy" && !f.noAutoBase && !strings.Contains(body, "base_sha") && strings.HasSuffix(body, "}") {
		body = strings.TrimSuffix(body, "}") + `,"base_sha":"@BASE@"}`
	}
	// The current sha of the policy file, as write-session hands it to the editor.
	body = strings.ReplaceAll(body, "@BASE@", sha256File(f.read("router-policy.yml")))
	r := f.req(http.MethodPut, wpath(op), body, c)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", csrf)
	r.AddCookie(&http.Cookie{Name: "yakos_wcsrf", Value: csrf})
	r.Header.Set("Origin", f.origin())
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	for _, m := range mods {
		m(r)
	}
	return r
}

// stepUp re-authenticates c and returns the response.
func (f *wfx) stepUp(c wcred, csrf string, body string) *httptest.ResponseRecorder {
	r := f.req(http.MethodPost, "/api/models/step-up", body, c)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", csrf)
	r.AddCookie(&http.Cookie{Name: "yakos_wcsrf", Value: csrf})
	r.Header.Set("Origin", f.origin())
	return f.serve(f.full, r)
}

// ready returns a CSRF token and completes a step-up for c.
func (f *wfx) ready(c wcred) string {
	f.t.Helper()
	csrf := f.mint(c)
	if rr := f.stepUp(c, csrf, c.stepBody); rr.Code != 200 {
		f.t.Fatalf("step-up: %d %s", rr.Code, rr.Body.String())
	}
	return csrf
}

func sha256File(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func bodyJSON(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(bytes.NewReader(rr.Body.Bytes())).Decode(&m); err != nil {
		t.Fatalf("not JSON: %q", rr.Body.String())
	}
	return m
}

// policyRegistry loads the registry the writers validate against, for a test that
// calls a writer directly (as the CLI does).
func policyRegistry(t *testing.T, f *wfx) *modelreg.Registry {
	t.Helper()
	reg, err := modelreg.Load(modelreg.Options{StateDir: f.state()})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}
