package consoleui_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/netid"
)

type hooksEPFixture struct {
	h         http.Handler
	nonce     string
	nonceFile string
	calls     *int
}

func newHooksEP(t *testing.T, enabled bool) hooksEPFixture {
	t.Helper()
	dir := t.TempDir()
	calls := new(int)
	cfg := consoleui.Config{
		Addr:            "127.0.0.1:7899",
		Token:           "tok",
		KanbanBoardPath: filepath.Join(dir, "kanban.md"),
		KanbanProject:   "t",
	}
	nf := filepath.Join(dir, "state", "hooks-endpoint-nonce")
	if enabled {
		cfg.HooksEndpoint = &consoleui.HooksEndpoint{
			NonceFile: nf,
			Known:     func(n string) bool { return n == "secret-scan" },
			Run: func(_ context.Context, shape, name string, body []byte) hookio.Response {
				*calls++
				if bytes.Contains(body, []byte("DENY")) {
					return hookio.Respond(shape, "PreToolUse", true, "nope")
				}
				return hookio.Respond(shape, "PreToolUse", false, "")
			},
		}
	}
	srv := consoleui.MustNew(t, cfg)
	f := hooksEPFixture{h: srv.HandlerForTest(), nonceFile: nf, calls: calls}
	if enabled {
		b, err := os.ReadFile(nf)
		if err != nil {
			t.Fatalf("nonce file: %v", err)
		}
		f.nonce = strings.TrimSpace(string(b))
	}
	return f
}

func (f hooksEPFixture) do(t *testing.T, mut func(*http.Request), path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Host = "127.0.0.1:7899"
	r.RemoteAddr = "127.0.0.1:50000"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Yakos-Hook-Nonce", f.nonce)
	r = r.WithContext(netid.WithIdentityForTest(r.Context(), netid.Identity{OperatorID: "op", Role: netid.RoleDispatch, Resolved: true}))
	if mut != nil {
		mut(r)
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w
}

const epPath = "/api/hooks/run/secret-scan?shape=agy"

func TestHooksEndpointOffByDefault(t *testing.T) {
	f := newHooksEP(t, false)
	if w := f.do(t, nil, epPath, `{}`); w.Code != http.StatusNotFound {
		t.Fatalf("off: status %d, want 404", w.Code)
	}
	if _, err := os.Stat(f.nonceFile); err == nil {
		t.Fatal("nonce file written while the endpoint is off")
	}
}

func TestHooksEndpointAllowAndDeny(t *testing.T) {
	f := newHooksEP(t, true)
	w := f.do(t, nil, epPath, `{"ok":1}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"exit_code":0`) || !strings.Contains(w.Body.String(), "allow") {
		t.Fatalf("allow: %d %s", w.Code, w.Body)
	}
	w = f.do(t, nil, "/api/hooks/run/secret-scan?shape=codex", `DENY`)
	var d struct {
		ExitCode int    `json:"exit_code"`
		Stderr   string `json:"stderr"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil || w.Code != 200 || d.ExitCode != 2 || d.Stderr == "" {
		t.Fatalf("deny: %d %s", w.Code, w.Body)
	}
}

func TestHooksEndpointNonce(t *testing.T) {
	f := newHooksEP(t, true)
	for name, mut := range map[string]func(*http.Request){
		"missing": func(r *http.Request) { r.Header.Del("X-Yakos-Hook-Nonce") },
		"wrong":   func(r *http.Request) { r.Header.Set("X-Yakos-Hook-Nonce", strings.Repeat("0", 64)) },
		"prefix":  func(r *http.Request) { r.Header.Set("X-Yakos-Hook-Nonce", f.nonce[:10]) },
	} {
		if w := f.do(t, mut, epPath, `{}`); w.Code != http.StatusUnauthorized {
			t.Errorf("%s nonce: status %d, want 401", name, w.Code)
		}
	}
	if *f.calls != 0 {
		t.Fatal("hook ran without a valid nonce")
	}
	if len(f.nonce) != 64 {
		t.Fatalf("nonce length %d", len(f.nonce))
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(f.nonceFile)
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("nonce file mode %v", fi.Mode().Perm())
		}
	}
	// A second daemon gets a different nonce.
	if g := newHooksEP(t, true); g.nonce == f.nonce {
		t.Fatal("nonce reused across daemons")
	}
}

func TestHooksEndpointGates(t *testing.T) {
	f := newHooksEP(t, true)
	cases := []struct {
		name string
		mut  func(*http.Request)
		path string
		body string
		want int
	}{
		{"read role", func(r *http.Request) {
			*r = *r.WithContext(netid.WithIdentityForTest(context.Background(), netid.Identity{Role: netid.RoleRead, Resolved: true}))
		}, epPath, `{}`, 403},
		{"non-loopback peer", func(r *http.Request) { r.RemoteAddr = "10.1.2.3:5000" }, epPath, `{}`, 403},
		{"rebinding host", func(r *http.Request) { r.Host = "evil.example:7899" }, epPath, `{}`, 403},
		{"foreign origin", func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, epPath, `{}`, 403},
		{"wrong-port origin", func(r *http.Request) { r.Header.Set("Origin", "http://127.0.0.1:1") }, epPath, `{}`, 403},
		{"oversize", nil, epPath, strings.Repeat("a", 64<<10+1), 413},
		{"unknown hook", nil, "/api/hooks/run/nope?shape=agy", `{}`, 404},
		{"traversal name", nil, "/api/hooks/run/a/b?shape=agy", `{}`, 404},
		{"bad shape", nil, "/api/hooks/run/secret-scan?shape=claude", `{}`, 400},
		{"get", func(r *http.Request) { r.Method = http.MethodGet }, epPath, ``, 405},
	}
	for _, c := range cases {
		if w := f.do(t, c.mut, c.path, c.body); w.Code != c.want {
			t.Errorf("%s: status %d, want %d (%s)", c.name, w.Code, c.want, w.Body)
		}
	}
	if *f.calls != 0 {
		t.Fatalf("hook ran %d times for refused requests", *f.calls)
	}
	// Exactly at the cap is accepted.
	if w := f.do(t, nil, epPath, strings.Repeat("a", 64<<10)); w.Code != 200 {
		t.Errorf("64 KiB body: status %d, want 200", w.Code)
	}
	if w := f.do(t, func(r *http.Request) { r.Header.Set("Origin", "http://localhost:7899") }, epPath, `{}`); w.Code != 200 {
		t.Errorf("loopback origin: status %d, want 200", w.Code)
	}
}
