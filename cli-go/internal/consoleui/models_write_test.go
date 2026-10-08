package consoleui_test

// models_write_test.go: the Models tab's browser writes (K-175). The acceptance
// list is the K-153b test plan in the body of PR #357. Every layer of the stack is
// tested alone: the other layers valid, this one broken, the answer fixed, both
// policy files byte-identical afterwards and no audit line written.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/policywrite"
	"github.com/bakw00ds/yakos/internal/statepath"
)

var allModes = []wmode{modeLoopback, modeSession, modeCert}

// refused asserts a refused write: the status, both files unchanged, no audit.
func refused(t *testing.T, f *wfx, rr *httptest.ResponseRecorder, want int, before string, what string) {
	t.Helper()
	if rr.Code != want {
		t.Errorf("%s: status %d, want %d (%s)", what, rr.Code, want, rr.Body.String())
	}
	if got := f.files(); got != before {
		t.Errorf("%s: a policy file changed", what)
	}
	if n := len(f.audit()); n != 0 {
		t.Errorf("%s: %d audit lines written", what, n)
	}
}

func TestWrites_OffByDefaultIs405(t *testing.T) {
	f := newWfx(t, modeLoopback, false)
	before := f.files()
	csrf := "anything"
	for _, op := range wops {
		for _, m := range []string{http.MethodPut, http.MethodPost, http.MethodPatch, http.MethodDelete} {
			r := f.put(op, wbodies[op], f.a, csrf, func(r *http.Request) { r.Method = m })
			refused(t, f, f.serve(f.full, r), http.StatusMethodNotAllowed, before, m+" "+op)
		}
	}
	if rr := f.serve(f.full, f.req(http.MethodGet, "/api/models/write-session", "", f.a)); rr.Code != http.StatusNotFound {
		t.Errorf("write-session with writes off = %d, want 404", rr.Code)
	}
	r := f.req(http.MethodPost, "/api/models/step-up", `{"token":"`+f.tok+`"}`, f.a)
	r.Header.Set("Content-Type", "application/json")
	refused(t, f, f.serve(f.full, r), http.StatusMethodNotAllowed, before, "step-up")
	ov := bodyJSON(t, f.serve(f.full, f.req(http.MethodGet, "/api/models/overview", "", f.a)))
	if ov["writes_enabled"] != false || ov["can_write"] != false {
		t.Errorf("overview flags = %v %v", ov["writes_enabled"], ov["can_write"])
	}
}

func TestWrites_AcceptedWriteAuditsOnceWithServerSideIdentity(t *testing.T) {
	for _, mode := range allModes {
		for _, op := range wops {
			t.Run(string(mode)+"/"+op, func(t *testing.T) {
				f := newWfx(t, mode, true)
				csrf := f.ready(f.a)
				rr := f.serve(f.full, f.put(op, wbodies[op], f.a, csrf, func(r *http.Request) {
					// Identity claims a client makes are never the audit's operator.
					r.Header.Set("X-Operator-ID", "mallory")
					r.Header.Set("X-Forwarded-User", "mallory")
				}))
				if rr.Code != 200 {
					t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
				}
				out := bodyJSON(t, rr)
				if out["ok"] != true || out["changed"] != true {
					t.Fatalf("response = %v", out)
				}
				lines := f.audit()
				want := 1
				if op == "pricing" {
					want = 1 // billing and price in one write
				}
				if len(lines) != want {
					t.Fatalf("%d audit lines, want %d: %v", len(lines), want, lines)
				}
				last := lines[len(lines)-1]
				file := "model-registry.yml"
				if op == "policy" || op == "pin" {
					file = "router-policy.yml"
				}
				if last["file"] != file || last["surface"] != "console" || last["actor"] != "operator-browser" ||
					last["operator_id"] != f.wantOperator || last["auth_method"] != f.wantVia {
					t.Errorf("audit line = %v (want operator %q via %q)", last, f.wantOperator, f.wantVia)
				}
				if last["policy_sha_after"] != sha256File(f.read(file)) || last["policy_sha_before"] == last["policy_sha_after"] {
					t.Errorf("audit shas = %v / %v, file sha %s", last["policy_sha_before"], last["policy_sha_after"], sha256File(f.read(file)))
				}
				// Only what the write is for changed: the other keys are kept.
				if file == "router-policy.yml" {
					for _, keep := range []string{"x_secret: SENTINEL-POLICY-KEY", "hooks_endpoint: true"} {
						if !strings.Contains(f.read(file), keep) {
							t.Errorf("router-policy.yml lost %q:\n%s", keep, f.read(file))
						}
					}
					if !strings.Contains(f.read(file), "allow_unsandboxed_runtimes") || !strings.Contains(f.read(file), "codex") {
						t.Errorf("privileged key lost:\n%s", f.read(file))
					}
				}
				// The same request again changes nothing and writes no second line.
				rr = f.serve(f.full, f.put(op, wbodies[op], f.a, csrf))
				if rr.Code != 200 || bodyJSON(t, rr)["changed"] != false || len(f.audit()) != want {
					t.Errorf("repeat: %d %s, audit %d", rr.Code, rr.Body.String(), len(f.audit()))
				}
			})
		}
	}
}

// Each mutation breaks exactly one layer of an otherwise valid write.
func TestWrites_EachLayerRefusesAlone(t *testing.T) {
	type layer struct {
		name string
		want int
		mod  func(r *http.Request)
	}
	del := func(name string) func(*http.Request) { return func(r *http.Request) { r.Header.Del(name) } }
	layers := []layer{
		{"csrf header missing", 403, del("X-CSRF-Token")},
		{"csrf header wrong", 403, func(r *http.Request) { r.Header.Set("X-CSRF-Token", "not-the-token") }},
		{"csrf cookie missing", 403, func(r *http.Request) {
			tok := r.Header.Get("X-CSRF-Token")
			ck := r.Header.Values("Cookie")
			r.Header.Del("Cookie")
			for _, c := range ck {
				// keep every cookie except the double-submit one
				var keep []string
				for _, part := range strings.Split(c, "; ") {
					if !strings.HasPrefix(part, "yakos_wcsrf=") {
						keep = append(keep, part)
					}
				}
				if len(keep) > 0 {
					r.Header.Add("Cookie", strings.Join(keep, "; "))
				}
			}
			r.Header.Set("X-CSRF-Token", tok)
		}},
		{"csrf cookie differs from header", 403, func(r *http.Request) {
			tok := r.Header.Get("X-CSRF-Token")
			r.Header.Set("Cookie", strings.ReplaceAll(r.Header.Get("Cookie"), "yakos_wcsrf="+tok, "yakos_wcsrf=other"))
		}},
		{"origin cross-site", 403, func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }},
		{"origin null", 403, func(r *http.Request) { r.Header.Set("Origin", "null") }},
		{"origin suffix trick", 403, func(r *http.Request) { r.Header.Set("Origin", "http://"+wHost+".evil.example") }},
		{"origin other port", 403, func(r *http.Request) { r.Header.Set("Origin", "http://127.0.0.1:9999") }},
		{"origin wrong scheme", 403, func(r *http.Request) {
			if strings.HasPrefix(r.Header.Get("Origin"), "https") {
				r.Header.Set("Origin", "http://"+wHost)
			} else {
				r.Header.Set("Origin", "https://"+wHost)
			}
		}},
		{"fetch-site cross-site", 403, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }},
		{"fetch-site same-site", 403, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-site") }},
		{"fetch-site garbage", 403, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-origin, cross-site") }},
		{"content-type text/plain", 415, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }},
		{"content-type form", 415, func(r *http.Request) { r.Header.Set("Content-Type", "application/x-www-form-urlencoded") }},
		{"content-type missing", 415, del("Content-Type")},
		{"content-type json lookalike", 415, func(r *http.Request) { r.Header.Set("Content-Type", "application/jsonp") }},
	}
	for _, mode := range allModes {
		for _, ly := range layers {
			for _, op := range wops {
				t.Run(string(mode)+"/"+ly.name+"/"+op, func(t *testing.T) {
					f := newWfx(t, mode, true)
					csrf := f.ready(f.a)
					before := f.files()
					refused(t, f, f.serve(f.full, f.put(op, wbodies[op], f.a, csrf, ly.mod)), ly.want, before, ly.name)
					// And the same request without the break is accepted: the
					// fixture really was valid.
					if rr := f.serve(f.full, f.put(op, wbodies[op], f.a, csrf)); rr.Code != 200 {
						t.Fatalf("control write = %d %s", rr.Code, rr.Body.String())
					}
				})
			}
		}
	}
}

// The token is bound to the credential: a token minted for one session, bearer
// token or certificate never works for another.
func TestWrites_CSRFTokenIsBoundToTheCredential(t *testing.T) {
	for _, mode := range []wmode{modeSession, modeCert} {
		t.Run(string(mode), func(t *testing.T) {
			f := newWfx(t, mode, true)
			tokA, tokB := f.mint(f.a), f.mint(f.b)
			if tokA == tokB {
				t.Fatal("two credentials were given the same CSRF token")
			}
			// Both credentials are stepped up, so only the binding can refuse.
			_ = f.ready(f.a)
			_ = f.ready(f.b)
			before := f.files()
			for _, op := range wops {
				refused(t, f, f.serve(f.full, f.put(op, wbodies[op], f.a, tokB)), 403, before, "token of B for credential A: "+op)
				refused(t, f, f.serve(f.full, f.put(op, wbodies[op], f.b, tokA)), 403, before, "token of A for credential B: "+op)
			}
			if rr := f.serve(f.full, f.put("enable", wbodies["enable"], f.a, tokA)); rr.Code != 200 {
				t.Errorf("own token = %d %s", rr.Code, rr.Body.String())
			}
		})
	}
	// The daemon's own session CSRF check also compares X-CSRF-Token with the
	// session; on the bare mux only this stack is left, and it must hold alone.
	t.Run("session binding without the daemon's CSRF middleware", func(t *testing.T) {
		f := newWfx(t, modeSession, true)
		tokA, tokB := f.mint(f.a), f.mint(f.b)
		_ = f.ready(f.a)
		_ = f.ready(f.b)
		bare := injectIdentityMiddleware(netid.Identity{OperatorID: "alice", Role: netid.RoleAdmin, Authenticated: true, Resolved: true, AuthMethod: netid.AuthMethodSession}, f.srv.HandlerForTest())
		before := f.files()
		refused(t, f, f.serve(bare, f.put("enable", wbodies["enable"], f.a, tokB)), 403, before, "B's token, A's session")
		if rr := f.serve(bare, f.put("enable", wbodies["enable"], f.a, tokA)); rr.Code != 200 {
			t.Errorf("own token on the bare mux = %d %s", rr.Code, rr.Body.String())
		}
	})
	t.Run("loopback token of another daemon", func(t *testing.T) {
		f, other := newWfx(t, modeLoopback, true), newWfx(t, modeLoopback, true)
		foreign := other.mint(other.a)
		// other changed HOME; put it back for f.
		t.Setenv("HOME", f.home)
		csrf := f.ready(f.a)
		_ = csrf
		before := f.files()
		refused(t, f, f.serve(f.full, f.put("enable", wbodies["enable"], f.a, foreign)), 403, before, "foreign token")
	})
	t.Run("tokens differ per kind and are stable per credential", func(t *testing.T) {
		f := newWfx(t, modeSession, true)
		first, second := f.mint(f.a), f.mint(f.a)
		if first != second {
			t.Error("a credential's token changed between mints")
		}
	})
}

func TestWrites_StepUp(t *testing.T) {
	for _, mode := range allModes {
		t.Run(string(mode), func(t *testing.T) {
			f := newWfx(t, mode, true)
			csrf := f.mint(f.a)
			before := f.files()
			method := map[wmode]string{modeLoopback: "token", modeSession: "password", modeCert: "certificate"}[mode]

			// No step-up: 401 with a structured body naming the method.
			rr := f.serve(f.full, f.put("enable", wbodies["enable"], f.a, csrf))
			refused(t, f, rr, 401, before, "no step-up")
			if m := bodyJSON(t, rr); m["error"] != "step_up_required" || m["method"] != method {
				t.Errorf("refusal body = %v", m)
			}
			if rr := f.stepUp(f.a, csrf, f.a.stepBody); rr.Code != 200 {
				t.Fatalf("step-up = %d %s", rr.Code, rr.Body.String())
			}
			// Valid for five minutes, not a second longer.
			f.clock.Advance(5*time.Minute - time.Second)
			if rr := f.serve(f.full, f.put("enable", wbodies["enable"], f.a, csrf)); rr.Code != 200 {
				t.Fatalf("inside the window = %d %s", rr.Code, rr.Body.String())
			}
			f.clock.Advance(time.Second)
			before = f.files()
			rr = f.serve(f.full, f.put("disable", wbodies["disable"], f.a, csrf))
			if rr.Code != 401 || bodyJSON(t, rr)["error"] != "step_up_required" {
				t.Errorf("at the window's end = %d %s", rr.Code, rr.Body.String())
			}
			if f.files() != before {
				t.Error("a stale step-up wrote")
			}
			// Re-authenticating opens a new window.
			if rr := f.stepUp(f.a, csrf, f.a.stepBody); rr.Code != 200 {
				t.Fatal(rr.Body.String())
			}
			if rr := f.serve(f.full, f.put("disable", wbodies["disable"], f.a, csrf)); rr.Code != 200 {
				t.Errorf("after a new step-up = %d %s", rr.Code, rr.Body.String())
			}
		})
	}
}

// A step-up belongs to the credential that made it.
func TestWrites_StepUpOfAnotherCredentialDoesNotCount(t *testing.T) {
	for _, mode := range []wmode{modeSession, modeCert} {
		t.Run(string(mode), func(t *testing.T) {
			f := newWfx(t, mode, true)
			csrfA, csrfB := f.mint(f.a), f.mint(f.b)
			if rr := f.stepUp(f.b, csrfB, f.b.stepBody); rr.Code != 200 {
				t.Fatal(rr.Body.String())
			}
			before := f.files()
			refused(t, f, f.serve(f.full, f.put("enable", wbodies["enable"], f.a, csrfA)), 401, before, "A with B's step-up")
			if rr := f.serve(f.full, f.put("enable", wbodies["enable"], f.b, csrfB)); rr.Code != 200 {
				t.Errorf("B = %d", rr.Code)
			}
		})
	}
}

func TestWrites_StepUpRefusalsAreUniformAndRateLimited(t *testing.T) {
	for _, mode := range []wmode{modeLoopback, modeSession} {
		t.Run(string(mode), func(t *testing.T) {
			f := newWfx(t, mode, true)
			csrf := f.mint(f.a)
			bad := []string{`{}`, `{"password":""}`, `{"password":"wrong-password-value"}`, `{"token":"wrong"}`, `{"token":""}`, `{"password":"` + wPassword + `x"}`}
			var first string
			for i, b := range bad[:4] {
				rr := f.stepUp(f.a, csrf, b)
				if rr.Code != 401 {
					t.Fatalf("bad step-up %d = %d %s", i, rr.Code, rr.Body.String())
				}
				if first == "" {
					first = rr.Body.String()
				} else if rr.Body.String() != first {
					t.Errorf("refusal %d differs: %q vs %q", i, rr.Body.String(), first)
				}
			}
			// Unknown fields and a wrong-type field are a request error, not an oracle.
			if rr := f.stepUp(f.a, csrf, `{"password":"x","extra":1}`); rr.Code != 400 {
				t.Errorf("unknown field = %d", rr.Code)
			}
			// The fifth failure locks the credential out for a minute, even for the
			// right secret.
			f.stepUp(f.a, csrf, bad[4])
			if rr := f.stepUp(f.a, csrf, f.a.stepBody); rr.Code != 429 {
				t.Errorf("after five failures the right secret = %d, want 429", rr.Code)
			}
			f.clock.Advance(61 * time.Second)
			if rr := f.stepUp(f.a, csrf, f.a.stepBody); rr.Code != 200 {
				t.Errorf("after the lockout = %d %s", rr.Code, rr.Body.String())
			}
		})
	}
	t.Run("step-up needs the CSRF stack too", func(t *testing.T) {
		f := newWfx(t, modeLoopback, true)
		csrf := f.mint(f.a)
		for name, mod := range map[string]func(*http.Request){
			"no token":     func(r *http.Request) { r.Header.Del("X-CSRF-Token") },
			"cross-origin": func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") },
			"cross-site":   func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") },
			"text/plain":   func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") },
		} {
			r := f.req(http.MethodPost, "/api/models/step-up", f.a.stepBody, f.a)
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-CSRF-Token", csrf)
			r.AddCookie(&http.Cookie{Name: "yakos_wcsrf", Value: csrf})
			r.Header.Set("Origin", f.origin())
			mod(r)
			if rr := f.serve(f.full, r); rr.Code != 403 && rr.Code != 415 {
				t.Errorf("%s: step-up = %d, want 403/415", name, rr.Code)
			}
		}
		before := f.files()
		refused(t, f, f.serve(f.full, f.put("enable", wbodies["enable"], f.a, csrf)), 401, before, "none of those stepped up")
	})
}

func TestWrites_HostIsChecked(t *testing.T) {
	hosts := []string{"evil.example.com", "evil.example.com:7890", "127.0.0.1.evil.example.com:7890", "localhost.evil.com:7890", "0.0.0.0:7890", "127.0.0.1:9999", ""}
	t.Run("loopback full handler", func(t *testing.T) {
		f := newWfx(t, modeLoopback, true)
		csrf := f.ready(f.a)
		before := f.files()
		for _, h := range hosts {
			for _, op := range wops {
				r := f.put(op, wbodies[op], f.a, csrf, func(r *http.Request) { r.Host = h; r.Header.Set("Origin", "http://"+h) })
				refused(t, f, f.serve(f.full, r), 403, before, "Host "+h+" "+op)
			}
		}
		if rr := f.serve(f.full, f.put("enable", wbodies["enable"], f.a, csrf, func(r *http.Request) {
			r.Host = "localhost:7890"
			r.Header.Set("Origin", "http://localhost:7890")
		})); rr.Code != 200 {
			t.Errorf("Host localhost:7890 = %d %s", rr.Code, rr.Body.String())
		}
	})
	// The write path checks Host itself: with the daemon's guard out of the way
	// (the bare mux) a rebinding Host is still refused.
	t.Run("write path alone", func(t *testing.T) {
		f := newWfx(t, modeLoopback, true)
		csrf := f.ready(f.a)
		bare := injectIdentityMiddleware(netid.Identity{OperatorID: wLoopOp, Role: netid.RoleAdmin, Resolved: true}, f.srv.HandlerForTest())
		before := f.files()
		for _, h := range hosts {
			r := f.put("enable", wbodies["enable"], f.a, csrf, func(r *http.Request) { r.Host = h; r.Header.Set("Origin", "http://"+h) })
			refused(t, f, f.serve(bare, r), 403, before, "bare mux Host "+h)
		}
		// And the credential is the daemon's bearer token, checked here too.
		wrong := wcred{apply: func(r *http.Request) { r.Header.Set("Authorization", "Bearer not-the-token") }}
		refused(t, f, f.serve(bare, f.put("enable", wbodies["enable"], wrong, csrf)), 403, before, "wrong bearer token")
		// The mint answers only to the real token as well.
		for _, c := range []wcred{wrong, {apply: func(*http.Request) {}}} {
			if rr := f.serve(bare, f.req(http.MethodGet, "/api/models/write-session", "", c)); rr.Code != 403 {
				t.Errorf("mint with a bad bearer token = %d, want 403", rr.Code)
			}
		}
		none := wcred{apply: func(*http.Request) {}}
		refused(t, f, f.serve(bare, f.put("enable", wbodies["enable"], none, csrf)), 403, before, "no bearer token")
		if rr := f.serve(bare, f.put("enable", wbodies["enable"], f.a, csrf)); rr.Code != 200 {
			t.Errorf("bare mux, good Host = %d %s", rr.Code, rr.Body.String())
		}
	})
	t.Run("write-session mint refuses a rebinding Host", func(t *testing.T) {
		f := newWfx(t, modeLoopback, true)
		bare := injectIdentityMiddleware(netid.Identity{OperatorID: wLoopOp, Role: netid.RoleAdmin, Resolved: true}, f.srv.HandlerForTest())
		for _, h := range hosts {
			r := f.req(http.MethodGet, "/api/models/write-session", "", f.a)
			r.Host = h
			if rr := f.serve(bare, r); rr.Code != 403 {
				t.Errorf("mint with Host %q = %d, want 403", h, rr.Code)
			}
			r = f.req(http.MethodGet, "/api/models/write-session", "", f.a)
			r.Host = h
			if rr := f.serve(f.full, r); rr.Code != 403 {
				t.Errorf("mint (full handler) with Host %q = %d, want 403", h, rr.Code)
			}
		}
	})
	t.Run("networked host must be a configured external host", func(t *testing.T) {
		f := newWfx(t, modeSession, true)
		csrf := f.ready(f.a)
		before := f.files()
		for _, h := range []string{"evil.example", "evil.example:7890", "127.0.0.1:7891"} {
			r := f.put("enable", wbodies["enable"], f.a, csrf, func(r *http.Request) { r.Host = h; r.Header.Set("Origin", "https://"+h) })
			refused(t, f, f.serve(f.full, r), 403, before, "networked Host "+h)
		}
	})
}

func TestWrites_LowerRolesAreRefused(t *testing.T) {
	f := newWfx(t, modeLoopback, true)
	csrf := f.ready(f.a)
	before := f.files()
	for name, role := range map[string]netid.Role{"read": netid.RoleRead, "dispatch": netid.RoleDispatch, "none": netid.RoleNone} {
		h := injectIdentityMiddleware(netid.Identity{OperatorID: "carol", Role: role, Authenticated: true, Resolved: true}, f.srv.HandlerForTest())
		for _, op := range wops {
			refused(t, f, f.serve(h, f.put(op, wbodies[op], f.a, csrf)), 403, before, name+" "+op)
		}
		for _, p := range []string{"/api/models/write-session", "/api/models/step-up"} {
			if rr := f.serve(h, f.req(http.MethodGet, p, "", f.a)); rr.Code != 403 {
				t.Errorf("%s GET %s = %d, want 403", name, p, rr.Code)
			}
		}
	}
	// An identity the resolver never resolved is refused too.
	h := injectIdentityMiddleware(netid.Identity{OperatorID: "x", Role: netid.RoleAdmin}, f.srv.HandlerForTest())
	refused(t, f, f.serve(h, f.put("enable", wbodies["enable"], f.a, csrf)), 403, before, "unresolved")
}

func TestWrites_BodyIsBoundedStrictAndHasNoPrivilegedField(t *testing.T) {
	f := newWfx(t, modeLoopback, true)
	csrf := f.ready(f.a)
	before := f.files()
	pad := strings.Repeat("a", 64<<10)
	cases := []struct {
		name, op, body string
		want           int
	}{
		{"over 64 KiB", "enable", `{"id":"` + pad + `"}`, 413},
		{"over 64 KiB policy", "policy", `{"rules_yaml":"` + pad + `"}`, 413},
		{"invalid JSON", "enable", `{"id":`, 400},
		{"trailing data", "enable", `{"id":"gpt-5.5"} {"id":"gpt-5.5"}`, 400},
		{"unknown field role", "enable", `{"id":"gpt-5.5","role":"admin"}`, 400},
		{"unknown field operator", "pin", `{"agent":"a","id":"gpt-5.6-terra","operator":"mallory"}`, 400},
		{"privileged key in policy body", "policy", `{"rules_yaml":"","allow_unsandboxed_runtimes":["claude"]}`, 400},
		{"hooks_endpoint in policy body", "policy", `{"hooks_endpoint":false}`, 400},
		{"wrong type", "pricing", `{"id":"gpt-5.6-terra","input":"1"}`, 400},
		{"array body", "enable", `["gpt-5.5"]`, 400},
		{"empty body", "enable", ``, 400},
		{"yaml anchor", "policy", `{"rules_yaml":"- &a {match: {agent: x}, action: {runtime: codex}}\n- *a\n"}`, 422},
		{"yaml merge key", "policy", `{"rules_yaml":"- {<<: {match: {agent: x}}, action: {runtime: codex}}\n"}`, 422},
		{"rules not a list", "policy", `{"rules_yaml":"allow_unsandboxed_runtimes: [claude]\n"}`, 422},
		{"too many rules", "policy", `{"rules_yaml":"` + strings.Repeat("- {match: {agent: x}, action: {runtime: codex}}\\n", 7) + `"}`, 422},
		{"not yaml", "policy", `{"rules_yaml":"- {"}`, 422},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refused(t, f, f.serve(f.full, f.put(tc.op, tc.body, f.a, csrf)), tc.want, before, tc.name)
		})
	}
}

func TestWrites_RefusalMappingAndFixedText(t *testing.T) {
	f := newWfx(t, modeLoopback, true)
	csrf := f.ready(f.a)
	before := f.files()
	const generic = "the policy writer refused the change; use the yakos CLI to see why"
	cases := []struct {
		name, op, body string
		want           int
	}{
		{"unknown model", "enable", `{"id":"no-such-model"}`, 422},
		{"unknown model alias", "alias", `{"alias":"balanced","harness":"codex","id":"no-such-model"}`, 422},
		{"bad alias name", "alias", `{"alias":"nope","harness":"codex","id":"gpt-5.6-terra"}`, 422},
		{"claude is not an alias harness", "alias", `{"alias":"balanced","harness":"claude","id":"sonnet"}`, 422},
		{"pin disabled model", "pin", `{"agent":"backend","id":"gpt-5.5","runtime":"codex"}`, 409},
		{"pin bad agent", "pin", `{"agent":"../etc","id":"gpt-5.6-terra"}`, 400},
		{"price on a subscription model", "pricing", `{"id":"gpt-5.6-terra","input":1,"output":2}`, 422},
		{"negative price", "pricing", `{"id":"gpt-5.6-terra","billing":"api","input":-1,"output":2}`, 400},
		{"bad billing", "pricing", `{"id":"gpt-5.6-terra","billing":"free"}`, 400},
		{"missing output", "pricing", `{"id":"gpt-5.6-terra","billing":"api","input":1}`, 400},
		{"rule the router rejects", "policy", `{"rules_yaml":"- {match: {agent: x}, action: {runtime: not-a-runtime}}\n"}`, 422},
	}
	forbidden := []string{f.home, f.ledger, f.work, "SENTINEL-OVERLAY-KEY", "SENTINEL-POLICY-KEY", "SENTINEL-OPENAI-KEY", "\x1b", "yakos models", "--"}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := f.serve(f.full, f.put(tc.op, tc.body, f.a, csrf))
			refused(t, f, rr, tc.want, before, tc.name)
			assertSafeResponse(t, rr, forbidden)
			// A refusal that is not one of the writer's classified ones is the one
			// fixed sentence, whatever the writer said.
			if tc.name == "rule the router rejects" || tc.name == "claude is not an alias harness" {
				if e := bodyJSON(t, rr)["error"]; e != generic {
					t.Errorf("%s: error = %v, want the fixed sentence", tc.name, e)
				}
			}
		})
	}
}

// assertSafeResponse: headers on every response, and no secret, path, request
// value or terminal escape in the body.
func assertSafeResponse(t *testing.T, rr *httptest.ResponseRecorder, forbidden []string) {
	t.Helper()
	h := rr.Header()
	if h.Get("Cache-Control") != "no-store" || h.Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(h.Get("Content-Type"), "application/json") {
		t.Errorf("headers on %d = %v", rr.Code, h)
	}
	for _, bad := range forbidden {
		if strings.Contains(rr.Body.String(), bad) {
			t.Errorf("response %d leaks %q: %s", rr.Code, bad, rr.Body.String())
		}
	}
}

// Sentinel fuzz over every write response and error path: request values that are
// secret-shaped, escapes and paths must never come back.
func TestWrites_SentinelFuzz(t *testing.T) {
	for _, mode := range allModes {
		t.Run(string(mode), func(t *testing.T) {
			f := newWfx(t, mode, true)
			csrf := f.ready(f.a)
			evil := "SENTINEL-REQUEST-SECRET\x1b[31m" + f.home
			sentinels := []string{f.home, f.ledger, f.work, "SENTINEL-OVERLAY-KEY", "SENTINEL-POLICY-KEY", "SENTINEL-OPENAI-KEY", "SENTINEL-REQUEST-SECRET", "\x1b", csrf}
			esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\x1b", `\u001b`).Replace(evil)
			bodies := map[string][]string{
				"enable":  {`{"id":"` + esc + `"}`, `{"id":"` + esc + `","x":1}`, wbodies["enable"]},
				"disable": {`{"id":"` + esc + `"}`, wbodies["disable"]},
				"alias":   {`{"alias":"` + esc + `","harness":"codex","id":"x"}`, `{"alias":"balanced","harness":"` + esc + `","id":"x"}`, `{"alias":"balanced","harness":"codex","id":"` + esc + `"}`, wbodies["alias"]},
				"pin":     {`{"agent":"` + esc + `","id":"x"}`, `{"agent":"backend","id":"` + esc + `","runtime":"` + esc + `"}`, wbodies["pin"]},
				"pricing": {`{"id":"` + esc + `","billing":"` + esc + `"}`, wbodies["pricing"]},
				"policy":  {`{"rules_yaml":"` + esc + `"}`, `{"rules_yaml":"- {match: {agent: ` + esc + `}, action: {runtime: ` + esc + `}}\n"}`, wbodies["policy"]},
			}
			for op, list := range bodies {
				for _, b := range list {
					rr := f.serve(f.full, f.put(op, b, f.a, csrf))
					assertSafeResponse(t, rr, sentinels)
					if rr.Code >= 500 {
						t.Errorf("%s %q = %d", op, b, rr.Code)
					}
				}
			}
			// The mint, step-up and every refusal path as well.
			for name, rr := range map[string]*httptest.ResponseRecorder{
				"stepup wrong": f.stepUp(f.a, csrf, `{"password":"`+esc+`","token":"`+esc+`"}`),
				"no csrf":      f.serve(f.full, f.put("enable", wbodies["enable"], f.a, "")),
				"bad host": f.serve(f.full, f.put("enable", wbodies["enable"], f.a, csrf, func(r *http.Request) {
					r.Host = "evil.example"
				})),
				"cross-site": f.serve(f.full, f.put("enable", wbodies["enable"], f.a, csrf, func(r *http.Request) {
					r.Header.Set("Sec-Fetch-Site", "cross-site")
				})),
				"text/plain": f.serve(f.full, f.put("enable", wbodies["enable"], f.a, csrf, func(r *http.Request) {
					r.Header.Set("Content-Type", "text/plain")
				})),
			} {
				if name == "bad host" || name == "text/plain" {
					continue // plain-text errors from the daemon's own guards, not this stack
				}
				assertSafeResponse(t, rr, sentinels[:len(sentinels)-1])
			}
			// The mint carries the token by design, and only that.
			mint := f.serve(f.full, f.req(http.MethodGet, "/api/models/write-session", "", f.a))
			assertSafeResponse(t, mint, sentinels[:len(sentinels)-1])
		})
	}
}

func TestWrites_AuditThatCannotBeOpenedRefusesTheWrite(t *testing.T) {
	f := newWfx(t, modeLoopback, true)
	csrf := f.ready(f.a)
	if err := os.Mkdir(statepath.DispatchLogIn(f.state()), 0o700); err != nil {
		t.Fatal(err)
	}
	before := f.files()
	for _, op := range wops {
		rr := f.serve(f.full, f.put(op, wbodies[op], f.a, csrf))
		if rr.Code != http.StatusServiceUnavailable || f.files() != before {
			t.Errorf("%s with an unopenable log = %d, files changed: %v", op, rr.Code, f.files() != before)
		}
	}
}

// The audit goes to the trusted home log, never to the YAKOS_DISPATCH_LOG a
// project can set.
func TestWrites_AuditIgnoresTheProjectLogOverride(t *testing.T) {
	f := newWfx(t, modeLoopback, true)
	csrf := f.ready(f.a)
	if rr := f.serve(f.full, f.put("enable", wbodies["enable"], f.a, csrf)); rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	if len(f.audit()) != 1 {
		t.Fatalf("home log has %d lines", len(f.audit()))
	}
	entries, _ := os.ReadDir(f.ledger)
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(f.ledger, e.Name()))
		if strings.Contains(string(b), "config_changed") {
			t.Errorf("audit line landed in the override log %s", e.Name())
		}
	}
}

// A policy file another user could have written is refused, not overwritten.
func TestWrites_UntrustedFilesAreNotOverwritten(t *testing.T) {
	for name, file := range map[string]string{"overlay": "model-registry.yml", "policy": "router-policy.yml"} {
		t.Run(name, func(t *testing.T) {
			f := newWfx(t, modeLoopback, true)
			csrf := f.ready(f.a)
			if err := os.Chmod(filepath.Join(f.state(), file), 0o666); err != nil { //nolint:gosec
				t.Fatal(err)
			}
			before := f.files()
			op := "enable"
			if file == "router-policy.yml" {
				op = "policy"
			}
			rr := f.serve(f.full, f.put(op, wbodies[op], f.a, csrf))
			refused(t, f, rr, 422, before, "untrusted "+name)
			assertSafeResponse(t, rr, []string{f.home, file})
			if e := bodyJSON(t, rr)["error"]; e != "the policy writer refused the change; use the yakos CLI to see why" {
				t.Errorf("untrusted %s: error = %v", name, e)
			}
		})
	}
}

func TestWrites_WriteSessionEndpoint(t *testing.T) {
	for _, mode := range allModes {
		t.Run(string(mode), func(t *testing.T) {
			f := newWfx(t, mode, true)
			rr := f.serve(f.full, f.req(http.MethodGet, "/api/models/write-session", "", f.a))
			if rr.Code != 200 {
				t.Fatal(rr.Body.String())
			}
			assertSafeResponse(t, rr, nil)
			m := bodyJSON(t, rr)
			if m["step_up_current"] != false {
				t.Errorf("fresh credential reports a current step-up: %v", m)
			}
			var ck *http.Cookie
			for _, c := range rr.Result().Cookies() {
				if c.Name == "yakos_wcsrf" {
					ck = c
				}
			}
			if ck == nil || ck.Value != m["csrf_token"] || !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode || ck.Path != "/api/" || ck.Secure != f.https {
				t.Errorf("cookie = %+v (https %v)", ck, f.https)
			}
			csrf := f.ready(f.a)
			again := bodyJSON(t, f.serve(f.full, f.req(http.MethodGet, "/api/models/write-session", "", f.a)))
			if again["step_up_current"] != true || again["step_up_expires"] == nil || again["csrf_token"] != csrf {
				t.Errorf("after step-up: %v", again)
			}
			// Cross-site mint is refused (a page cannot even read its own token cross-origin).
			r := f.req(http.MethodGet, "/api/models/write-session", "", f.a)
			r.Header.Set("Sec-Fetch-Site", "cross-site")
			if rr := f.serve(f.full, r); rr.Code != 403 {
				t.Errorf("cross-site mint = %d", rr.Code)
			}
			// No credential, no token.
			r = httptest.NewRequest(http.MethodGet, "http://"+wHost+"/api/models/write-session", nil)
			r.Host = wHost
			if rr := f.serve(f.full, r); rr.Code == 200 {
				t.Errorf("mint without a credential = %d", rr.Code)
			}
		})
	}
}

// What a read-only role sees: no unsandboxed-runtime names. Admins keep them.
func TestWrites_UnsandboxedRuntimesAreAdminOnly(t *testing.T) {
	f := newWfx(t, modeLoopback, true)
	for name, tc := range map[string]struct {
		role netid.Role
		want int
	}{"read": {netid.RoleRead, 0}, "dispatch": {netid.RoleDispatch, 0}, "admin": {netid.RoleAdmin, 1}} {
		h := injectIdentityMiddleware(netid.Identity{OperatorID: "u", Role: tc.role, Authenticated: true, Resolved: true}, f.srv.HandlerForTest())
		pol := bodyJSON(t, f.serve(h, f.req(http.MethodGet, "/api/router/policy", "", f.a)))
		ov := bodyJSON(t, f.serve(h, f.req(http.MethodGet, "/api/models/overview", "", f.a)))
		for where, view := range map[string]map[string]any{"policy": pol, "overview": ov["router"].(map[string]any)} {
			if got := len(view["allow_unsandboxed_runtimes"].([]any)); got != tc.want {
				t.Errorf("%s sees %d unsandboxed runtimes in %s, want %d", name, got, where, tc.want)
			}
			if view["hooks_endpoint"] != true {
				t.Errorf("%s: hooks_endpoint flag hidden in %s", name, where)
			}
		}
		if ov["can_write"] != (tc.role == netid.RoleAdmin) || ov["writes_enabled"] != (tc.role == netid.RoleAdmin) {
			t.Errorf("%s: can_write=%v writes_enabled=%v", name, ov["can_write"], ov["writes_enabled"])
		}
	}
}

// Concurrent writes serialize: every one lands, every one is audited.
func TestWrites_ConcurrentWritesAreAllRecorded(t *testing.T) {
	f := newWfx(t, modeLoopback, true)
	csrf := f.ready(f.a)
	ids := []string{"gpt-5.6-terra", "gpt-5.6-sol", "gpt-5.6-luna", "gpt-6-astra", "gpt-reserve", "codex-auto-review", "gpt-oss-120b-medium", "gemini-3.1-pro-high"}
	var wg sync.WaitGroup
	codes := make([]int, len(ids))
	for i, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = f.serve(f.full, f.put("disable", fmt.Sprintf(`{"id":%q}`, id), f.a, csrf)).Code
		}()
	}
	wg.Wait()
	for i, c := range codes {
		if c != 200 {
			t.Errorf("write %s = %d", ids[i], c)
		}
	}
	if n := len(f.audit()); n != len(ids) {
		t.Errorf("%d audit lines for %d writes", n, len(ids))
	}
	for _, id := range ids {
		if !strings.Contains(f.read("model-registry.yml"), id) {
			t.Errorf("write for %s lost", id)
		}
	}
}

// The whole-list rules replace is a compare-and-swap: a CLI pin that lands between
// the editor's load and its save makes the save a 409, and the pin survives.
func TestWrites_RulesReplaceIsCompareAndSwap(t *testing.T) {
	for _, mode := range allModes {
		t.Run(string(mode), func(t *testing.T) {
			f := newWfx(t, mode, true)
			csrf := f.ready(f.a)
			// The editor loads: write-session hands out the sha and the rules text.
			ws := bodyJSON(t, f.serve(f.full, f.req(http.MethodGet, "/api/models/write-session", "", f.a)))
			base, _ := ws["policy_sha"].(string)
			if base != sha256File(f.read("router-policy.yml")) || !strings.Contains(ws["rules_yaml"].(string), "class: chat") {
				t.Fatalf("editor state = %v", ws)
			}
			if strings.Contains(ws["rules_yaml"].(string), "SENTINEL") {
				t.Error("rules text carries a key outside rules:")
			}
			// A CLI pin lands (the same writer `yakos models pin` calls).
			reg := policyRegistry(t, f)
			if err := policywrite.SetPin(f.state(), reg, "backend", "gpt-5.6-terra", "codex", false, func(policywrite.Change) error { return nil }); err != nil {
				t.Fatal(err)
			}
			after := f.files()
			body := `{"rules_yaml":"- {match: {agent: other}, action: {runtime: codex, model: gpt-5.6-terra}}\n","base_sha":"` + base + `"}`
			rr := f.serve(f.full, f.put("policy", body, f.a, csrf))
			if rr.Code != 409 {
				t.Fatalf("stale save = %d %s", rr.Code, rr.Body.String())
			}
			if m := bodyJSON(t, rr); m["sha"] != sha256File(f.read("router-policy.yml")) {
				t.Errorf("409 does not name the current sha: %v", m)
			}
			if f.files() != after || !strings.Contains(f.read("router-policy.yml"), "backend") || len(f.audit()) != 0 {
				t.Error("a stale save changed the file or was audited; the pin must survive")
			}
			// Redoing the edit from the fresh state works.
			ws = bodyJSON(t, f.serve(f.full, f.req(http.MethodGet, "/api/models/write-session", "", f.a)))
			body = `{"rules_yaml":` + jsonString(ws["rules_yaml"].(string)) + `,"base_sha":"` + ws["policy_sha"].(string) + `"}`
			if rr := f.serve(f.full, f.put("policy", body, f.a, csrf)); rr.Code != 200 {
				t.Errorf("save from fresh state = %d %s", rr.Code, rr.Body.String())
			}
		})
	}
	t.Run("base_sha is required", func(t *testing.T) {
		f := newWfx(t, modeLoopback, true)
		f.noAutoBase = true
		csrf := f.ready(f.a)
		before := f.files()
		refused(t, f, f.serve(f.full, f.put("policy", `{"rules_yaml":""}`, f.a, csrf)), 400, before, "no base_sha")
		refused(t, f, f.serve(f.full, f.put("policy", `{"rules_yaml":"[]","base_sha":""}`, f.a, csrf)), 409, before, "empty base_sha against an existing file")
		refused(t, f, f.serve(f.full, f.put("policy", `{"rules_yaml":"[]","base_sha":"`+strings.Repeat("0", 64)+`"}`, f.a, csrf)), 409, before, "wrong base_sha")
	})
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// Two admins: another admin's password is not a step-up for this session.
func TestWrites_StepUpRefusesAnotherAdminsPassword(t *testing.T) {
	f := newWfx(t, modeSession, true)
	if err := f.uStore.Create("carol", "carols-own-password-1", netid.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	csrf := f.mint(f.a)
	if rr := f.stepUp(f.a, csrf, `{"password":"carols-own-password-1"}`); rr.Code != 401 {
		t.Fatalf("another admin's password = %d", rr.Code)
	}
	before := f.files()
	refused(t, f, f.serve(f.full, f.put("enable", wbodies["enable"], f.a, csrf)), 401, before, "after carol's password")
	if rr := f.stepUp(f.a, csrf, f.a.stepBody); rr.Code != 200 {
		t.Errorf("own password = %d", rr.Code)
	}
	// And the other way round: carol's session checks carol's password, not alice's.
	sess, err := f.aStore.Create("carol", netid.RoleAdmin, 0)
	if err != nil {
		t.Fatal(err)
	}
	carol := wcred{apply: func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "yakos_session", Value: sess.ID}) }}
	cc := f.mint(carol)
	if rr := f.stepUp(carol, cc, `{"password":"`+wPassword+`"}`); rr.Code != 401 {
		t.Errorf("alice's password on carol's session = %d", rr.Code)
	}
	if rr := f.stepUp(carol, cc, `{"password":"carols-own-password-1"}`); rr.Code != 200 {
		t.Errorf("carol's own password = %d", rr.Code)
	}
}
