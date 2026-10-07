package consoleui_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/knowledge"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// newContextServer stamps each request with the operator in X-Test-Op and the
// role in X-Test-Role (read|none).
func newContextServer(t *testing.T) (ts *httptest.Server, workDir string) {
	t.Helper()
	workDir = t.TempDir()
	bus := wsbus.New()
	t.Cleanup(bus.Stop)
	srv := consoleui.MustNew(t, consoleui.Config{
		Token: "tok", KanbanBoardPath: filepath.Join(t.TempDir(), "kanban.md"), KanbanProject: "test",
		MetricsProjectDir: t.TempDir(), PerfWorkDir: t.TempDir(), Bus: bus, WorkDir: workDir,
		WorkspaceRoot: t.TempDir(), YakosRoot: t.TempDir(),
	})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role := netid.RoleRead
		if r.Header.Get("X-Test-Role") == "none" {
			role = netid.RoleNone
		}
		id := netid.Identity{OperatorID: r.Header.Get("X-Test-Op"), Role: role, Authenticated: true, Resolved: true, AuthMethod: netid.AuthMethodCert}
		srv.HandlerForTest().ServeHTTP(w, r.WithContext(netid.WithIdentityForTest(r.Context(), id)))
	})
	ts = httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts, workDir
}

func ctxGet(t *testing.T, ts *httptest.Server, method, path, op, role string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, nil)
	req.Header.Set("X-Test-Op", op)
	req.Header.Set("X-Test-Role", role)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp.StatusCode, sb.String()
}

func TestChatContext_RolesOwnerAndSoul(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".yakos-state/soul"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".yakos-state/soul/global.md"), []byte("SOUL-TEXT-XYZ\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ts, workDir := newContextServer(t)
	store := consoleui.NewTranscripts(workDir)
	mk := func(agent string) func() knowledge.Pack {
		return func() knowledge.Pack {
			text := "## rule: secretrulebody-RULETEXT\n\n"
			return knowledge.Pack{Text: text, SHA: knowledge.SHA(text), Parts: []knowledge.Part{
				{Name: "alpha", Kind: knowledge.KindRule, Bytes: 12, Included: true},
				{Name: agent, Kind: knowledge.KindAgent, Bytes: 20, Included: true},
			}}
		}
	}
	if _, err := store.EnsureKnowledge("conv-lead", "alice", mk("lead")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsureKnowledge("conv-back", "alice", mk("backend")); err != nil {
		t.Fatal(err)
	}

	if code, _ := ctxGet(t, ts, "GET", "/api/chat/context?conv=conv-lead", "alice", "none"); code != http.StatusForbidden {
		t.Fatalf("role none: %d", code)
	}
	if code, _ := ctxGet(t, ts, "POST", "/api/chat/context?conv=conv-lead", "alice", "read"); code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d", code)
	}
	if code, _ := ctxGet(t, ts, "GET", "/api/chat/context", "alice", "read"); code != http.StatusBadRequest {
		t.Fatalf("missing conv: %d", code)
	}
	if code, _ := ctxGet(t, ts, "GET", "/api/chat/context?conv=../x", "alice", "read"); code != http.StatusBadRequest {
		t.Fatalf("bad conv: %d", code)
	}

	var got struct {
		Knowledge *struct {
			SHA   string           `json:"sha"`
			Bytes int              `json:"bytes"`
			Cap   int              `json:"cap"`
			Parts []knowledge.Part `json:"parts"`
		} `json:"knowledge"`
		Soul string `json:"soul"`
	}
	code, body := ctxGet(t, ts, "GET", "/api/chat/context?conv=conv-lead", "alice", "read")
	if code != 200 || json.Unmarshal([]byte(body), &got) != nil || got.Knowledge == nil {
		t.Fatalf("owner/lead: %d %s", code, body)
	}
	if got.Knowledge.Cap != knowledge.MaxBytes || len(got.Knowledge.Parts) != 2 || len(got.Knowledge.SHA) != 64 {
		t.Fatalf("shape: %s", body)
	}
	if strings.Contains(body, "RULETEXT") {
		t.Fatal("rule text leaked through the API")
	}
	if got.Soul != "SOUL-TEXT-XYZ\n" {
		t.Fatalf("owner of a lead conversation must get the soul: %q", got.Soul)
	}

	// Not lead: no soul. Not the owner: nothing at all.
	_, body = ctxGet(t, ts, "GET", "/api/chat/context?conv=conv-back", "alice", "read")
	if strings.Contains(body, "SOUL-TEXT") || !strings.Contains(body, `"parts"`) {
		t.Fatalf("non-lead: %s", body)
	}
	_, body = ctxGet(t, ts, "GET", "/api/chat/context?conv=conv-lead", "mallory", "read")
	if strings.Contains(body, "SOUL-TEXT") || strings.Contains(body, `"sha"`) || !strings.Contains(body, `"knowledge":null`) {
		t.Fatalf("other operator: %s", body)
	}
}

func TestContextDrawerAssetServed(t *testing.T) {
	ts, _ := newContextServer(t)
	code, body := ctxGet(t, ts, "GET", "/context-drawer.js", "", "none")
	if code != 200 || !strings.Contains(body, "YakosContextDrawer") {
		t.Fatalf("asset: %d", code)
	}
}
