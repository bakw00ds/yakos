package consoleui_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
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
	project   string
}

func newHooksEP(t *testing.T, enabled bool) hooksEPFixture {
	t.Helper()
	dir := t.TempDir()
	calls := new(int)
	project, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := consoleui.Config{
		Addr:            "127.0.0.1:7899",
		Token:           "tok",
		KanbanBoardPath: filepath.Join(dir, "kanban.md"),
		KanbanProject:   "t",
	}
	nf := filepath.Join(dir, "state", "hooks-endpoint-nonce")
	if enabled {
		cfg.HooksEndpoint = &consoleui.HooksEndpoint{
			NonceFile:  nf,
			ProjectDir: project,
			Known:      func(n string) bool { return n == "secret-scan" },
			Run: func(ctx context.Context, shape, name string, body []byte) hookio.Response {
				*calls++
				if bytes.Contains(body, []byte("PROJECT")) {
					// Never echo the path: only whether it is the bound project.
					got := "OTHER"
					if hookio.ProjectFrom(ctx) == project {
						got = "BOUND"
					}
					return hookio.Respond(shape, "PreToolUse", true, "project="+got)
				}
				if bytes.Contains(body, []byte("WHOAMI")) {
					return hookio.Respond(shape, "PreToolUse", true, "agent="+hookio.AgentFrom(ctx))
				}
				if bytes.Contains(body, []byte("DENY")) {
					return hookio.Respond(shape, "PreToolUse", true, "nope")
				}
				return hookio.Respond(shape, "PreToolUse", false, "")
			},
		}
	}
	srv := consoleui.MustNew(t, cfg)
	f := hooksEPFixture{h: srv.HandlerForTest(), nonceFile: nf, calls: calls, project: project}
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

func TestHooksEndpointPassesDispatchedAgent(t *testing.T) {
	f := newHooksEP(t, true)
	got := func(q string) string {
		w := f.do(t, nil, epPath+q, "WHOAMI")
		if w.Code != http.StatusOK {
			t.Fatalf("status %d", w.Code)
		}
		return w.Body.String()
	}
	if b := got("&agent=reviewer"); !strings.Contains(b, "agent=reviewer") {
		t.Errorf("agent not passed: %s", b)
	}
	for _, bad := range []string{"&agent=a%20b", "&agent=..%2Fx", "&agent=a;b", ""} {
		if b := got(bad); strings.Contains(b, "agent=a") || strings.Contains(b, "agent=..") {
			t.Errorf("invalid agent %q accepted: %s", bad, b)
		}
	}
}

// K-170 (e): the nonce is bound to one project at issue time. The envelope
// cannot move the hooks to another directory.
func TestHooksEndpointBindsProjectToNonce(t *testing.T) {
	f := newHooksEP(t, true)
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(f.project, "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(f.project, "link")
	if runtime.GOOS != "windows" {
		if err := os.Symlink(outside, link); err != nil {
			t.Fatal(err)
		}
	}
	codex := func(cwd string) string {
		b, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "cwd": cwd, "note": "PROJECT"})
		return string(b)
	}
	agy := func(ws ...string) string {
		b, _ := json.Marshal(map[string]any{"workspacePaths": ws, "toolCall": map[string]any{"name": "run_command"}, "note": "PROJECT"})
		return string(b)
	}
	do := func(shape, body string) *httptest.ResponseRecorder {
		return f.do(t, nil, "/api/hooks/run/secret-scan?shape="+shape, body)
	}

	// Refused: another directory, a sibling that shares the prefix, a relative
	// path, a traversal, a symlink out of the project, any agy workspace entry.
	refused := map[string]*httptest.ResponseRecorder{}
	refused["other dir"] = do("codex", codex(outside))
	refused["prefix sibling"] = do("codex", codex(f.project+"-evil"))
	refused["relative"] = do("codex", codex("proj"))
	refused["traversal"] = do("codex", codex(f.project+"/../"+filepath.Base(outside)))
	refused["agy second workspace"] = do("agy", agy(f.project, outside))
	refused["agy relative"] = do("agy", agy("proj"))
	if runtime.GOOS != "windows" {
		refused["symlink out"] = do("codex", codex(link))
		deep := filepath.Join(outside, "a", "b")
		if err := os.MkdirAll(deep, 0o755); err != nil {
			t.Fatal(err)
		}
		deepLink := filepath.Join(f.project, "deeplink")
		if err := os.Symlink(deep, deepLink); err != nil {
			t.Fatal(err)
		}
		dangle := filepath.Join(f.project, "dangle")
		if err := os.Symlink(filepath.Join(outside, "not-created"), dangle); err != nil {
			t.Fatal(err)
		}
		// Lexically "deeplink/.." is the project; the OS applies ".." to the target.
		refused["dot-dot after a link"] = do("codex", codex(deepLink+"/.."))
		refused["dangling link out"] = do("codex", codex(dangle))
		refused["below a dangling link"] = do("codex", codex(filepath.Join(dangle, "x")))
		refused["symlink out, missing child"] = do("codex", codex(filepath.Join(link, "missing", "deeper")))
	}
	for name, w := range refused {
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403 (%s)", name, w.Code, w.Body)
		}
		for _, p := range []string{outside, f.project, "-evil", strings.ReplaceAll(outside, `\`, `\\`), strings.ReplaceAll(f.project, `\`, `\\`)} {
			if strings.Contains(w.Body.String(), p) {
				t.Errorf("%s: response leaks a path (%q): %s", name, p, w.Body)
			}
		}
	}
	if *f.calls != 0 {
		t.Fatalf("hook ran %d times for refused envelopes", *f.calls)
	}

	// Accepted: the project, a subdirectory, an agy workspace inside it, and an
	// envelope that names none. The hook always sees the BOUND project.
	for name, w := range map[string]*httptest.ResponseRecorder{
		"project":                      do("codex", codex(f.project)),
		"subdir":                       do("codex", codex(sub)),
		"agy":                          do("agy", agy(f.project)),
		"no cwd":                       do("codex", codex("")),
		"missing child of the project": do("codex", codex(filepath.Join(f.project, "not-yet", "there"))),
		"unparsable":                   do("codex", "PROJECT"),
	} {
		if w.Code != 200 || !strings.Contains(w.Body.String(), "project=BOUND") {
			t.Errorf("%s: %d %s, want the bound project", name, w.Code, w.Body)
		}
	}
}

// The refusal leaves an audit line that names the hook and shape but no path.
func TestHooksEndpointProjectRefusalIsAudited(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	f := newHooksEP(t, true)
	outside := t.TempDir()
	b, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "cwd": outside})
	if w := f.do(t, nil, "/api/hooks/run/secret-scan?shape=codex", string(b)); w.Code != 403 {
		t.Fatalf("status %d", w.Code)
	}
	log := buf.String()
	if !strings.Contains(log, "hooks endpoint refused") || !strings.Contains(log, "secret-scan") {
		t.Errorf("no audit line: %q", log)
	}
	if strings.Contains(log, outside) || strings.Contains(log, f.project) {
		t.Errorf("audit line carries a path: %q", log)
	}
}

func TestHooksEndpointNeedsABoundProject(t *testing.T) {
	for _, dir := range []string{"", "relative/dir"} {
		cfg := consoleui.Config{Addr: "127.0.0.1:7899", Token: "tok", KanbanBoardPath: filepath.Join(t.TempDir(), "k.md"), KanbanProject: "t"}
		nf := filepath.Join(t.TempDir(), "nonce")
		cfg.HooksEndpoint = &consoleui.HooksEndpoint{NonceFile: nf, ProjectDir: dir,
			Known: func(string) bool { return true },
			Run:   func(context.Context, string, string, []byte) hookio.Response { return hookio.Response{} }}
		srv := consoleui.MustNew(t, cfg)
		_ = srv
		if _, err := os.Stat(nf); err == nil {
			t.Errorf("ProjectDir %q: nonce issued without a bound project", dir)
		}
	}
}
