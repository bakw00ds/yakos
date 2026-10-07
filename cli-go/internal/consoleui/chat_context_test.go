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
		WorkspaceRoot: t.TempDir(), YakosRoot: t.TempDir(), LoopbackOwnerID: "host-op",
	})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role := netid.RoleRead
		if r.Header.Get("X-Test-Role") == "none" {
			role = netid.RoleNone
		}
		id := netid.Identity{OperatorID: r.Header.Get("X-Test-Op"), Role: role, Authenticated: true, Resolved: true, AuthMethod: netid.AuthMethodCert}
		if r.Header.Get("X-Test-Loopback") == "1" {
			// The loopback cooperative identity: Role admin, not authenticated.
			id = netid.Identity{OperatorID: r.Header.Get("X-Test-Op"), Role: netid.RoleAdmin, Resolved: true}
		}
		srv.HandlerForTest().ServeHTTP(w, r.WithContext(netid.WithIdentityForTest(r.Context(), id)))
	})
	ts = httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts, workDir
}

func ctxGet(t *testing.T, ts *httptest.Server, method, path, op, role string) (int, string) {
	t.Helper()
	return ctxGetAs(t, ts, method, path, op, role, false)
}

func ctxGetAs(t *testing.T, ts *httptest.Server, method, path, op, role string, loopback bool) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, nil)
	if loopback {
		req.Header.Set("X-Test-Loopback", "1")
	}
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
	writeSoul(t, "SOUL-TEXT-XYZ\n")
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
	if got.Soul != "" || strings.Contains(body, "SOUL-TEXT") {
		t.Fatalf("a cert-identity owner of a lead conversation must get no soul: %s", body)
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

func writeSoul(t *testing.T, text string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, ".yakos-state/soul"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".yakos-state/soul/global.md"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func leadConv(t *testing.T, store *consoleui.Transcripts, conv, owner string) {
	t.Helper()
	_, err := store.EnsureKnowledge(conv, owner, func() knowledge.Pack {
		text := "## rule: a\n\n"
		return knowledge.Pack{Text: text, SHA: knowledge.SHA(text), Parts: []knowledge.Part{
			{Name: "lead", Kind: knowledge.KindAgent, Bytes: 5, Included: true},
		}}
	})
	if err != nil {
		t.Fatal(err)
	}
}

// F1: the soul is the host's; only the loopback host operator reads it.
func TestChatContext_SoulOnlyForLoopbackHostOperator(t *testing.T) {
	writeSoul(t, "SOUL-TEXT-XYZ\n")
	ts, workDir := newContextServer(t)
	store := consoleui.NewTranscripts(workDir)
	leadConv(t, store, "conv-host", "host-op")
	leadConv(t, store, "conv-bob", "remote-bob")
	leadConv(t, store, "conv-spoof", "host-op")

	// Loopback host operator owns a lead conversation: soul.
	_, body := ctxGetAs(t, ts, "GET", "/api/chat/context?conv=conv-host", "host-op", "read", true)
	if !strings.Contains(body, "SOUL-TEXT-XYZ") {
		t.Fatalf("host operator must get the soul: %s", body)
	}
	// A cert identity that owns a lead conversation: nothing.
	_, body = ctxGet(t, ts, "GET", "/api/chat/context?conv=conv-bob", "remote-bob", "read")
	if strings.Contains(body, "SOUL") || strings.Contains(body, `"soul`) || !strings.Contains(body, `"parts"`) {
		t.Fatalf("cert owner: %s", body)
	}
	// A cert identity whose CN equals the host operator id: nothing.
	_, body = ctxGet(t, ts, "GET", "/api/chat/context?conv=conv-spoof", "host-op", "read")
	if strings.Contains(body, "SOUL-TEXT") {
		t.Fatalf("cert CN equal to the host id must not read the soul: %s", body)
	}
}

func TestChatContext_SoulWithSecretIsOmitted(t *testing.T) {
	key := "AKIA" + "ABCDEFGHIJKLMNOP"
	writeSoul(t, "be kind "+key+"\n")
	ts, workDir := newContextServer(t)
	leadConv(t, consoleui.NewTranscripts(workDir), "conv-host", "host-op")
	_, body := ctxGetAs(t, ts, "GET", "/api/chat/context?conv=conv-host", "host-op", "read", true)
	if strings.Contains(body, key) || strings.Contains(body, "be kind") || !strings.Contains(body, "soulNote") {
		t.Fatalf("secret-shaped soul must be omitted with a note: %s", body)
	}
	if strings.Contains(body, "global.md") || strings.Contains(body, "/") && strings.Contains(body, ".yakos-state") {
		t.Fatalf("note must be path-free: %s", body)
	}
}
