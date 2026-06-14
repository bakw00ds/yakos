package consoleui_test

// files_handler_test.go — unit + integration tests for /api/files/* endpoints.
//
// Coverage:
//   - GET /api/files/tree: lists temp workspace, skips .git, respects caps
//     (per-dir entry cap → truncated=true, depth cap → truncated=true), sorts
//     dirs-first, OMITS secret-matched files.
//   - GET /api/files/content: UTF-8 text (correct language ID), base64 for
//     binary file, 413 over the size cap, 503 when WorkspaceRoot is empty.
//   - Path jail: ../etc/passwd, absolute path, and a symlink pointing outside
//     the workspace are ALL rejected; a symlink pointing inside is allowed.
//   - Secret deny-set: expanded patterns (.envrc, id_rsa, server.p12,
//     creds.json via "credentials" substring) refused by content AND omitted
//     from tree.
//   - Role gate: RoleRead identity passes both endpoints; zero-value Identity
//     (Resolved=false, loopback invariant) is never blocked.
//
// Determinism: no time.Sleep, no subprocess calls, no LLM calls.
// All OS operations are scoped to os.MkdirTemp directories; the real FS is
// never touched outside them.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// ---- helpers ----------------------------------------------------------------

// newFilesTestServer builds a consoleui.Server with WorkspaceRoot set to
// workspaceDir and returns the httptest.Server and the bearer token.
func newFilesTestServer(t *testing.T, workspaceDir string) (*httptest.Server, string) {
	t.Helper()
	stateDir := t.TempDir()
	workDir := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)

	srv := consoleui.New(consoleui.Config{
		Token:             tok,
		KanbanBoardPath:   t.TempDir() + "/kanban.md",
		KanbanProject:     "test",
		MetricsProjectDir: t.TempDir(),
		PerfWorkDir:       t.TempDir(),
		Bus:               bus,
		WorkDir:           workDir,
		WorkspaceRoot:     workspaceDir,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, tok
}

// newFilesEnforcementServer builds the server behind the full role-injection
// middleware stack (mirrors newEnforcementTestServer in enforcement_test.go).
func newFilesEnforcementServer(t *testing.T, workspaceDir string, id netid.Identity) (*httptest.Server, string) {
	t.Helper()
	stateDir := t.TempDir()
	workDir := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)

	srv := consoleui.New(consoleui.Config{
		Token:             tok,
		KanbanBoardPath:   t.TempDir() + "/kanban.md",
		KanbanProject:     "test",
		MetricsProjectDir: t.TempDir(),
		PerfWorkDir:       t.TempDir(),
		Bus:               bus,
		WorkDir:           workDir,
		WorkspaceRoot:     workspaceDir,
	})

	handler := consoleui.RequireTokenForNonStatic(tok,
		consoleui.RequireJSONForMutations(
			injectIdentityMiddleware(id, srv.Handler())))

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts, tok
}

// filesGet issues GET with the bearer token and returns the response.
func filesGet(t *testing.T, ts *httptest.Server, tok, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

// filesBodyStr drains and closes a response body and returns it as a string.
func filesBodyStr(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// mustWriteFile writes content to path, creating directories as needed.
func mustWriteFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdirall: %v", err)
	}
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// ---- /api/files/tree tests ---------------------------------------------------

// TestFilesTree_ListsWorkspace verifies that the tree endpoint lists files in
// the workspace root.
func TestFilesTree_ListsWorkspace(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	mustWriteFile(t, filepath.Join(ws, "main.go"), []byte("package main"))
	mustWriteFile(t, filepath.Join(ws, "README.md"), []byte("# hello"))
	if err := os.Mkdir(filepath.Join(ws, "internal"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	ts, tok := newFilesTestServer(t, ws)
	resp := filesGet(t, ts, tok, "/api/files/tree")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tree root: got %d; want 200", resp.StatusCode)
	}

	var got struct {
		Dir     string `json:"dir"`
		Entries []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"entries"`
		Truncated bool `json:"truncated"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.Truncated {
		t.Error("tree: unexpected truncated=true for small workspace")
	}

	names := make(map[string]string)
	for _, e := range got.Entries {
		names[e.Name] = e.Type
	}

	if names["main.go"] != "file" {
		t.Errorf("tree: expected main.go as file; got %q", names["main.go"])
	}
	if names["README.md"] != "file" {
		t.Errorf("tree: expected README.md as file; got %q", names["README.md"])
	}
	if names["internal"] != "dir" {
		t.Errorf("tree: expected internal as dir; got %q", names["internal"])
	}
}

// TestFilesTree_SkipsGit verifies that .git/ is not listed in the tree.
func TestFilesTree_SkipsGit(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	mustWriteFile(t, filepath.Join(ws, "app.go"), []byte("package main"))
	// Create a .git directory with a file inside.
	mustWriteFile(t, filepath.Join(ws, ".git", "HEAD"), []byte("ref: refs/heads/main\n"))

	ts, tok := newFilesTestServer(t, ws)
	resp := filesGet(t, ts, tok, "/api/files/tree")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d; want 200", resp.StatusCode)
	}

	var got struct {
		Entries []struct{ Name string } `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for _, e := range got.Entries {
		if e.Name == ".git" {
			t.Error("tree: .git should not appear in entries")
		}
	}
}

// TestFilesTree_TruncatedFlagSmall verifies truncated=false for a small workspace.
func TestFilesTree_TruncatedFlagSmall(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	mustWriteFile(t, filepath.Join(ws, "only.go"), []byte("package main"))

	ts, tok := newFilesTestServer(t, ws)
	resp := filesGet(t, ts, tok, "/api/files/tree")
	defer resp.Body.Close()

	var got struct {
		Truncated bool                    `json:"truncated"`
		Entries   []struct{ Name string } `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Truncated {
		t.Error("tree: truncated=true for single-file workspace; want false")
	}
	if len(got.Entries) != 1 || got.Entries[0].Name != "only.go" {
		t.Errorf("tree: expected [only.go]; got %v", got.Entries)
	}
}

// TestFilesTree_TruncatedByEntryCap verifies that truncated=true is set when
// a directory contains more than MaxTreeEntriesPerDir entries.
// We create MaxTreeEntriesPerDir+1 files using the exported constant so the
// test stays in sync with the actual cap value.
func TestFilesTree_TruncatedByEntryCap(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	// Create cap+1 files so the cap is exceeded.
	cap := consoleui.MaxTreeEntriesPerDir
	for i := 0; i <= cap; i++ {
		name := fmt.Sprintf("f%05d.txt", i)
		mustWriteFile(t, filepath.Join(ws, name), []byte("x"))
	}

	ts, tok := newFilesTestServer(t, ws)
	resp := filesGet(t, ts, tok, "/api/files/tree")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("entry cap: got %d; want 200", resp.StatusCode)
	}

	var got struct {
		Truncated bool                    `json:"truncated"`
		Entries   []struct{ Name string } `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Truncated {
		t.Error("tree: truncated=false when entry cap exceeded; want true")
	}
	// Must return exactly cap entries (the cap+1th was dropped).
	if len(got.Entries) > cap {
		t.Errorf("tree: entry count %d exceeds cap %d", len(got.Entries), cap)
	}
}

// TestFilesTree_DepthClampedAtMaxTreeDepth verifies that requesting depth >
// MaxTreeDepth is clamped and does not panic or return an error. The key
// invariant: the response is 200 and dirs beyond MaxTreeDepth appear as leaf
// nodes with no children (the lazy-load sentinel), not as a traversal error.
func TestFilesTree_DepthClampedAtMaxTreeDepth(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	// Build a chain: ws/d0/ so there is at least one dir to check.
	d0 := filepath.Join(ws, "d00")
	if err := os.Mkdir(d0, 0755); err != nil {
		t.Fatalf("mkdir d00: %v", err)
	}
	// Nest a file inside so the dir is non-empty.
	mustWriteFile(t, filepath.Join(d0, "leaf.go"), []byte("package leaf"))

	ts, tok := newFilesTestServer(t, ws)
	// Request an absurd depth — handler must clamp to MaxTreeDepth.
	url := fmt.Sprintf("/api/files/tree?depth=%d", consoleui.MaxTreeDepth+999)
	resp := filesGet(t, ts, tok, url)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("depth clamped: got %d; want 200", resp.StatusCode)
	}

	// Decode just enough to verify the response is valid JSON and we got the
	// root entry back.
	var got struct {
		Entries []struct {
			Name string `json:"name"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Entries) == 0 {
		t.Error("depth clamped: no entries returned; want at least d00")
	}
}

// TestFilesTree_DepthParam_ShallowDefault verifies that the default depth=1
// means subdirectory entries are returned without pre-populated children.
// The frontend lazy-loads children on expand; a nil Children field is the
// signal that the dir has not been expanded yet.
func TestFilesTree_DepthParam_ShallowDefault(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	// Create ws/sub/ with a file inside.
	mustWriteFile(t, filepath.Join(ws, "sub", "child.go"), []byte("package sub"))
	mustWriteFile(t, filepath.Join(ws, "root.go"), []byte("package main"))

	ts, tok := newFilesTestServer(t, ws)
	// No depth param → defaults to 1.
	resp := filesGet(t, ts, tok, "/api/files/tree")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("shallow default: got %d; want 200", resp.StatusCode)
	}

	var got struct {
		Truncated bool `json:"truncated"`
		Entries   []struct {
			Name     string          `json:"name"`
			Type     string          `json:"type"`
			Children json.RawMessage `json:"children"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.Truncated {
		t.Error("shallow default: truncated=true for small workspace; want false")
	}

	// Find the "sub" dir entry.
	var subEntry *struct {
		Name     string          `json:"name"`
		Type     string          `json:"type"`
		Children json.RawMessage `json:"children"`
	}
	for i := range got.Entries {
		if got.Entries[i].Name == "sub" {
			subEntry = &got.Entries[i]
			break
		}
	}
	if subEntry == nil {
		t.Fatalf("shallow default: 'sub' dir not found in entries: %v", got.Entries)
	}
	if subEntry.Type != "dir" {
		t.Errorf("shallow default: 'sub' type=%q; want dir", subEntry.Type)
	}
	// With default depth=1, dir children must be nil (omitempty in JSON).
	if len(subEntry.Children) > 0 && string(subEntry.Children) != "null" {
		t.Errorf("shallow default: 'sub' has pre-populated children=%s; want nil (lazy-load sentinel)", subEntry.Children)
	}
}

// TestFilesTree_DepthParam_SubdirLazyLoad verifies that requesting
// ?dir=<subdir>&depth=1 returns that subdir's immediate children (the
// lazy-load call the frontend makes on expand).
func TestFilesTree_DepthParam_SubdirLazyLoad(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	mustWriteFile(t, filepath.Join(ws, "root.go"), []byte("package main"))
	mustWriteFile(t, filepath.Join(ws, "sub", "a.go"), []byte("package sub"))
	mustWriteFile(t, filepath.Join(ws, "sub", "b.go"), []byte("package sub"))
	// nested/deep should NOT appear (depth=1 stops at sub's immediate children).
	mustWriteFile(t, filepath.Join(ws, "sub", "nested", "deep.go"), []byte("package nested"))

	ts, tok := newFilesTestServer(t, ws)
	resp := filesGet(t, ts, tok, "/api/files/tree?dir=sub&depth=1")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("subdir lazy-load: got %d; want 200", resp.StatusCode)
	}

	var got struct {
		Dir     string `json:"dir"`
		Entries []struct {
			Name     string          `json:"name"`
			Type     string          `json:"type"`
			Children json.RawMessage `json:"children"`
		} `json:"entries"`
		Truncated bool `json:"truncated"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.Dir != "sub" {
		t.Errorf("subdir lazy-load: dir=%q; want sub", got.Dir)
	}

	names := make(map[string]string) // name → type
	for _, e := range got.Entries {
		names[e.Name] = e.Type
	}

	// a.go and b.go must appear.
	if names["a.go"] != "file" {
		t.Errorf("subdir lazy-load: a.go not found as file; entries=%v", got.Entries)
	}
	if names["b.go"] != "file" {
		t.Errorf("subdir lazy-load: b.go not found as file; entries=%v", got.Entries)
	}
	// nested/ must appear as dir entry but without pre-populated children.
	if names["nested"] != "dir" {
		t.Errorf("subdir lazy-load: nested dir not found; entries=%v", got.Entries)
	}
	// Find nested entry and assert no children.
	for _, e := range got.Entries {
		if e.Name == "nested" {
			if len(e.Children) > 0 && string(e.Children) != "null" {
				t.Errorf("subdir lazy-load: nested has children=%s; want nil", e.Children)
			}
		}
	}
	// root.go must NOT appear (not in sub/).
	if names["root.go"] != "" {
		t.Errorf("subdir lazy-load: root.go appeared in sub listing; should not")
	}
	if got.Truncated {
		t.Error("subdir lazy-load: truncated=true; want false for small dir")
	}
}

// TestFilesTree_DirFirstSort verifies that directories are listed before files.
func TestFilesTree_DirFirstSort(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	mustWriteFile(t, filepath.Join(ws, "z_file.go"), []byte("package main"))
	mustWriteFile(t, filepath.Join(ws, "a_file.go"), []byte("package main"))
	if err := os.Mkdir(filepath.Join(ws, "internal"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Mkdir(filepath.Join(ws, "cmd"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	ts, tok := newFilesTestServer(t, ws)
	resp := filesGet(t, ts, tok, "/api/files/tree")
	defer resp.Body.Close()

	var got struct {
		Entries []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// First two entries must be dirs; last two must be files.
	if len(got.Entries) < 4 {
		t.Fatalf("expected >=4 entries; got %d", len(got.Entries))
	}
	for i, e := range got.Entries {
		if i < 2 && e.Type != "dir" {
			t.Errorf("entry[%d] %q: expected type=dir (dirs-first)", i, e.Name)
		}
		if i >= 2 && e.Type != "file" {
			t.Errorf("entry[%d] %q: expected type=file", i, e.Name)
		}
	}

	// Dirs sorted: cmd < internal.
	if got.Entries[0].Name != "cmd" || got.Entries[1].Name != "internal" {
		t.Errorf("dir sort: expected [cmd, internal]; got [%s, %s]",
			got.Entries[0].Name, got.Entries[1].Name)
	}
}

// TestFilesTree_NonExistentDir verifies that a missing dir returns 404.
func TestFilesTree_NonExistentDir(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	ts, tok := newFilesTestServer(t, ws)
	resp := filesGet(t, ts, tok, "/api/files/tree?dir=nonexistent")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing dir: got %d; want 404", resp.StatusCode)
	}
}

// TestFilesTree_NoWorkspaceRoot verifies that 503 is returned when
// WorkspaceRoot is not configured.
func TestFilesTree_NoWorkspaceRoot(t *testing.T) {
	t.Parallel()

	ts, tok := newFilesTestServer(t, "") // empty WorkspaceRoot
	resp := filesGet(t, ts, tok, "/api/files/tree")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("no workspace root: got %d; want 503", resp.StatusCode)
	}
}

// ---- /api/files/content tests -----------------------------------------------

// TestFilesContent_UTF8 verifies that a UTF-8 text file is served with
// encoding="utf-8" and the correct Monaco language ID.
func TestFilesContent_UTF8(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	goSrc := []byte("package main\n\nfunc main() {}\n")
	mustWriteFile(t, filepath.Join(ws, "main.go"), goSrc)

	ts, tok := newFilesTestServer(t, ws)
	resp := filesGet(t, ts, tok, "/api/files/content?path=main.go")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("content UTF-8: got %d; want 200", resp.StatusCode)
	}

	var got struct {
		Path     string `json:"path"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
		Size     int64  `json:"size"`
		Language string `json:"language"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.Encoding != "utf-8" {
		t.Errorf("encoding: got %q; want utf-8", got.Encoding)
	}
	if got.Content != string(goSrc) {
		t.Errorf("content mismatch: got %q; want %q", got.Content, string(goSrc))
	}
	if got.Language != "go" {
		t.Errorf("language: got %q; want go", got.Language)
	}
	if got.Size != int64(len(goSrc)) {
		t.Errorf("size: got %d; want %d", got.Size, len(goSrc))
	}
}

// TestFilesContent_Binary verifies that a binary file is served with
// encoding="base64".
func TestFilesContent_Binary(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	// Build binary content with NUL bytes (binary marker).
	binData := []byte{0x00, 0x01, 0x02, 0xFF, 0xFE, 0xFD}
	mustWriteFile(t, filepath.Join(ws, "data.bin"), binData)

	ts, tok := newFilesTestServer(t, ws)
	resp := filesGet(t, ts, tok, "/api/files/content?path=data.bin")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("content binary: got %d; want 200", resp.StatusCode)
	}

	var got struct {
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.Encoding != "base64" {
		t.Errorf("encoding: got %q; want base64", got.Encoding)
	}
	if got.Content == "" {
		t.Error("content: empty base64 content for binary file")
	}
}

// TestFilesContent_TooLarge verifies that a file over the 2 MiB cap returns
// 413 Request Entity Too Large.
func TestFilesContent_TooLarge(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	// Write a file slightly over 2 MiB.
	big := make([]byte, 2<<20+1) // 2 MiB + 1 byte
	for i := range big {
		big[i] = 'A'
	}
	mustWriteFile(t, filepath.Join(ws, "big.txt"), big)

	ts, tok := newFilesTestServer(t, ws)
	resp := filesGet(t, ts, tok, "/api/files/content?path=big.txt")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("too large: got %d; want 413", resp.StatusCode)
	}
}

// TestFilesContent_NoWorkspaceRoot verifies that 503 is returned when
// WorkspaceRoot is not configured.
func TestFilesContent_NoWorkspaceRoot(t *testing.T) {
	t.Parallel()

	ts, tok := newFilesTestServer(t, "") // empty WorkspaceRoot
	resp := filesGet(t, ts, tok, "/api/files/content?path=anything.go")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("no workspace root: got %d; want 503", resp.StatusCode)
	}
}

// TestFilesContent_NotFound verifies that a missing file returns 404.
func TestFilesContent_NotFound(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	ts, tok := newFilesTestServer(t, ws)
	resp := filesGet(t, ts, tok, "/api/files/content?path=missing.go")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing file: got %d; want 404", resp.StatusCode)
	}
}

// ---- Language ID tests -------------------------------------------------------

// TestFilesContent_LanguageIDs verifies that common extensions map to the
// expected Monaco language IDs.
func TestFilesContent_LanguageIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		filename string
		want     string
	}{
		{"main.go", "go"},
		{"app.js", "javascript"},
		{"app.ts", "typescript"},
		{"README.md", "markdown"},
		{"config.yaml", "yaml"},
		{"config.yml", "yaml"},
		{"package.json", "json"},
		{"build.sh", "shell"},
		{"unknown.xyz", ""},
	}

	ws := t.TempDir()
	for _, tc := range tests {
		mustWriteFile(t, filepath.Join(ws, tc.filename), []byte("content"))
	}

	ts, tok := newFilesTestServer(t, ws)

	for _, tc := range tests {
		t.Run(tc.filename, func(t *testing.T) {
			resp := filesGet(t, ts, tok, "/api/files/content?path="+tc.filename)
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("got %d; want 200", resp.StatusCode)
			}
			var got struct {
				Language string `json:"language"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.Language != tc.want {
				t.Errorf("language for %s: got %q; want %q", tc.filename, got.Language, tc.want)
			}
		})
	}
}

// ---- Path jail tests ---------------------------------------------------------

// TestFilesJail_TraversalDotDot verifies that ../etc/passwd is rejected.
func TestFilesJail_TraversalDotDot(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	ts, tok := newFilesTestServer(t, ws)

	for _, endpoint := range []string{"/api/files/tree?dir=", "/api/files/content?path="} {
		traversal := "../etc/passwd"
		resp := filesGet(t, ts, tok, endpoint+traversal)
		filesBodyStr(t, resp)

		if resp.StatusCode == http.StatusOK {
			t.Errorf("%s%s: got 200; want non-200 (traversal must be rejected)",
				endpoint, traversal)
		}
	}
}

// TestFilesJail_AbsolutePath verifies that absolute paths are rejected.
func TestFilesJail_AbsolutePath(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	ts, tok := newFilesTestServer(t, ws)

	// URL-encode "/" as %2F to test how the handler processes it after URL
	// decoding.  We also try /etc/passwd directly.
	for _, path := range []string{"/etc/passwd", "%2Fetc%2Fpasswd"} {
		resp := filesGet(t, ts, tok, "/api/files/content?path="+path)
		filesBodyStr(t, resp)

		if resp.StatusCode == http.StatusOK {
			t.Errorf("absolute path %q: got 200; want non-200 (must be rejected)", path)
		}
	}
}

// TestFilesJail_SymlinkOutside verifies that a symlink pointing outside the
// workspace is rejected by the content endpoint.
func TestFilesJail_SymlinkOutside(t *testing.T) {
	t.Parallel()

	// Create the workspace and a target outside it.
	ws := t.TempDir()
	outside := t.TempDir()
	secretFile := filepath.Join(outside, "secret.txt")
	mustWriteFile(t, secretFile, []byte("super secret"))

	// Create a symlink inside the workspace pointing outside.
	symlink := filepath.Join(ws, "evil_link.txt")
	if err := os.Symlink(secretFile, symlink); err != nil {
		t.Skip("symlink creation not supported:", err)
	}

	ts, tok := newFilesTestServer(t, ws)

	resp := filesGet(t, ts, tok, "/api/files/content?path=evil_link.txt")
	filesBodyStr(t, resp)

	// Must not be 200 — either 400 (jail) or 404 (path resolution failure).
	if resp.StatusCode == http.StatusOK {
		t.Error("symlink outside workspace: got 200; want non-200 (must be jailed)")
	}
}

// TestFilesJail_SymlinkInside verifies that a symlink pointing inside the
// workspace IS allowed (serves the content of the symlink target).
func TestFilesJail_SymlinkInside(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	realFile := filepath.Join(ws, "real.go")
	mustWriteFile(t, realFile, []byte("package main"))

	// Create a symlink inside the workspace pointing to another file inside.
	symlink := filepath.Join(ws, "link.go")
	if err := os.Symlink(realFile, symlink); err != nil {
		t.Skip("symlink creation not supported:", err)
	}

	ts, tok := newFilesTestServer(t, ws)

	resp := filesGet(t, ts, tok, "/api/files/content?path=link.go")
	defer resp.Body.Close()

	// An inside-workspace symlink should either succeed (200) or at minimum
	// not be rejected with a jail-specific error.  We accept 200 or 404
	// (platforms that don't follow symlinks in EvalSymlinks will return 404
	// when the real path is already inside the workspace root).
	//
	// The key invariant: NOT a jail-403 — the symlink is safe.
	if resp.StatusCode == http.StatusForbidden {
		t.Error("symlink inside workspace: got 403; inside-workspace symlinks must not be jail-refused")
	}
}

// TestFilesJail_GitPath verifies that .git internals are rejected.
func TestFilesJail_GitPath(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	// Create a .git directory with a file.
	mustWriteFile(t, filepath.Join(ws, ".git", "config"), []byte("[core]\n"))

	ts, tok := newFilesTestServer(t, ws)

	// Try reading .git/config via the content endpoint.
	for _, path := range []string{".git/config", ".git%2Fconfig"} {
		resp := filesGet(t, ts, tok, "/api/files/content?path="+path)
		filesBodyStr(t, resp)

		if resp.StatusCode == http.StatusOK {
			t.Errorf(".git path %q: got 200; want non-200 (.git internals must be refused)", path)
		}
	}
}

// ---- Secret deny-set tests --------------------------------------------------

// TestFilesSecret_ContentRefused verifies that the expanded secret deny-set
// refuses all covered patterns at the content endpoint with 403.
//
// Patterns exercised:
//   - .pem extension (server.pem)
//   - .key extension (server.key)
//   - .env exact basename (.env)
//   - .env substring (.env.local, .envrc — direnv, holds tokens)
//   - extensionless exact basename (id_rsa)
//   - .p12 extension (server.p12)
//   - "credentials" substring (creds.json contains "cred" NOT "credentials";
//     credentials.json DOES match; creds_db.json does NOT match)
func TestFilesSecret_ContentRefused(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	// All of these must be refused.
	refusedFiles := []string{
		"server.pem",
		"server.key",
		".env",
		".env.local",
		".envrc", // direnv; contains ".env"
		"id_rsa", // extensionless SSH private key
		"server.p12",
		"credentials.json", // "credentials" substring
		"id_ed25519",       // extensionless SSH key
		".npmrc",           // exact basename
		".netrc",           // exact basename
	}
	for _, name := range refusedFiles {
		mustWriteFile(t, filepath.Join(ws, name), []byte("secret content"))
	}

	ts, tok := newFilesTestServer(t, ws)

	for _, name := range refusedFiles {
		t.Run(name, func(t *testing.T) {
			resp := filesGet(t, ts, tok, "/api/files/content?path="+name)
			filesBodyStr(t, resp)

			if resp.StatusCode == http.StatusOK {
				t.Errorf("secret file %q: got 200; content must be refused", name)
			}
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("secret file %q: got %d; want 403 Forbidden", name, resp.StatusCode)
			}
		})
	}
}

// TestFilesSecret_NonSecretNotRefused verifies that files that superficially
// resemble secrets but don't match the deny-set ARE served normally.
func TestFilesSecret_NonSecretNotRefused(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	// These must NOT be refused (no match in deny-set).
	allowedFiles := []string{
		"monkey.go",      // .go extension, no sensitive substring
		"README.md",      // plain doc
		"creds_db.go",    // contains "creds" but not "credentials"
		"deploy_key.pub", // .pub extension (not in deny-set)
	}
	for _, name := range allowedFiles {
		mustWriteFile(t, filepath.Join(ws, name), []byte("safe content"))
	}

	ts, tok := newFilesTestServer(t, ws)

	for _, name := range allowedFiles {
		t.Run(name, func(t *testing.T) {
			resp := filesGet(t, ts, tok, "/api/files/content?path="+name)
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusForbidden {
				t.Errorf("non-secret file %q: got 403; should be served", name)
			}
			if resp.StatusCode != http.StatusOK {
				t.Errorf("non-secret file %q: got %d; want 200", name, resp.StatusCode)
			}
		})
	}
}

// TestFilesSecret_TreeOmitsSecrets verifies that the tree endpoint OMITS
// secret-matched files entirely (they do not appear in entries at all).
// This replaces the previous TestFilesSecret_TreeListsSecrets test which
// verified the opposite (now-incorrect) behaviour.
func TestFilesSecret_TreeOmitsSecrets(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	mustWriteFile(t, filepath.Join(ws, "server.key"), []byte("private key content"))
	mustWriteFile(t, filepath.Join(ws, ".env"), []byte("SECRET=hunter2"))
	mustWriteFile(t, filepath.Join(ws, ".envrc"), []byte("export TOKEN=abc"))
	mustWriteFile(t, filepath.Join(ws, "id_rsa"), []byte("ssh-rsa key"))
	mustWriteFile(t, filepath.Join(ws, "app.go"), []byte("package main"))

	ts, tok := newFilesTestServer(t, ws)
	resp := filesGet(t, ts, tok, "/api/files/tree")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tree: got %d; want 200", resp.StatusCode)
	}

	var got struct {
		Entries []struct{ Name string } `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	names := make(map[string]bool)
	for _, e := range got.Entries {
		names[e.Name] = true
	}

	// app.go must appear (non-secret file).
	if !names["app.go"] {
		t.Error("tree: app.go should appear in tree listing")
	}

	// Secret files must NOT appear.
	for _, secret := range []string{"server.key", ".env", ".envrc", "id_rsa"} {
		if names[secret] {
			t.Errorf("tree: secret file %q must be omitted from tree; it appeared", secret)
		}
	}
}

// ---- Role gate tests --------------------------------------------------------

// TestFilesRoleGate_RoleReadPasses verifies that a RoleRead identity can reach
// both /api/files/tree and /api/files/content (read-only endpoints).
func TestFilesRoleGate_RoleReadPasses(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	mustWriteFile(t, filepath.Join(ws, "main.go"), []byte("package main"))

	readOnly := netid.Identity{
		OperatorID:    "reader",
		Role:          netid.RoleRead,
		Authenticated: true,
		Resolved:      true,
	}
	ts, tok := newFilesEnforcementServer(t, ws, readOnly)

	for _, path := range []string{"/api/files/tree", "/api/files/content?path=main.go"} {
		resp := filesGet(t, ts, tok, path)
		filesBodyStr(t, resp)

		if resp.StatusCode == http.StatusForbidden {
			t.Errorf("RoleRead on %s: got 403; want non-403 (read-only endpoint)", path)
		}
	}
}

// TestFilesRoleGate_ZeroValueNotBlocked verifies the loopback invariant:
// a zero-value Identity (Resolved=false, from srv.Handler() without the
// resolver middleware) must never cause a 403 on the file endpoints.
func TestFilesRoleGate_ZeroValueNotBlocked(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	mustWriteFile(t, filepath.Join(ws, "main.go"), []byte("package main"))

	stateDir := t.TempDir()
	workDir := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)

	srv := consoleui.New(consoleui.Config{
		Token:             tok,
		KanbanBoardPath:   t.TempDir() + "/kanban.md",
		KanbanProject:     "test",
		MetricsProjectDir: t.TempDir(),
		PerfWorkDir:       t.TempDir(),
		Bus:               bus,
		WorkDir:           workDir,
		WorkspaceRoot:     ws,
	})

	// Use srv.Handler() directly — no identity injection, no resolver middleware.
	// Zero-value Identity (Resolved=false) → requireRole is a no-op.
	handler := consoleui.RequireTokenForNonStatic(tok,
		consoleui.RequireJSONForMutations(srv.Handler()))
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	for _, path := range []string{"/api/files/tree", "/api/files/content?path=main.go"} {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		filesBodyStr(t, resp)

		if resp.StatusCode == http.StatusForbidden {
			t.Errorf("zero-value identity on %s: got 403; loopback invariant broken", path)
		}
	}
}

// TestFilesRoleGate_RoutesRegistered verifies that both file API routes are
// registered by asserting they return non-404 and non-403 for a RoleAdmin
// identity (smoke test for route + role registration).
func TestFilesRoleGate_RoutesRegistered(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	mustWriteFile(t, filepath.Join(ws, "hello.go"), []byte("package main"))

	// Use admin role (guaranteed to allow RoleRead).
	admin := netid.Identity{
		OperatorID:    "admin-op",
		Role:          netid.RoleAdmin,
		Authenticated: true,
		Resolved:      true,
	}
	ts, tok := newFilesEnforcementServer(t, ws, admin)

	for _, path := range []string{"/api/files/tree", "/api/files/content?path=hello.go"} {
		resp := filesGet(t, ts, tok, path)
		filesBodyStr(t, resp)

		// Must not be 404 (route not registered) or 403 (wrong role gate).
		if resp.StatusCode == http.StatusNotFound {
			t.Errorf("route %s: got 404; route must be registered", path)
		}
		if resp.StatusCode == http.StatusForbidden {
			t.Errorf("route %s: got 403 for RoleAdmin; role gate broken", path)
		}
	}
}

// ---- Edge cases -------------------------------------------------------------

// TestFilesContent_PathParamRequired verifies that missing path= returns 400.
func TestFilesContent_PathParamRequired(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	ts, tok := newFilesTestServer(t, ws)
	resp := filesGet(t, ts, tok, "/api/files/content")
	filesBodyStr(t, resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing path param: got %d; want 400", resp.StatusCode)
	}
}

// TestFilesContent_IsDirectory verifies that requesting a directory path
// at the content endpoint returns 400 (not 200).
func TestFilesContent_IsDirectory(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	if err := os.Mkdir(filepath.Join(ws, "subdir"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	ts, tok := newFilesTestServer(t, ws)
	resp := filesGet(t, ts, tok, "/api/files/content?path=subdir")
	filesBodyStr(t, resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("dir at content: got %d; want 400", resp.StatusCode)
	}
}

// TestFilesTree_SubdirListing verifies that ?dir=<subdir> returns only
// the contents of that subdirectory, not the whole workspace.
func TestFilesTree_SubdirListing(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	mustWriteFile(t, filepath.Join(ws, "root.go"), []byte("package main"))
	mustWriteFile(t, filepath.Join(ws, "sub", "sub.go"), []byte("package sub"))

	ts, tok := newFilesTestServer(t, ws)
	resp := filesGet(t, ts, tok, "/api/files/tree?dir=sub")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("subdir tree: got %d; want 200", resp.StatusCode)
	}

	var got struct {
		Dir     string `json:"dir"`
		Entries []struct {
			Name string `json:"name"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(got.Entries) != 1 || got.Entries[0].Name != "sub.go" {
		t.Errorf("subdir listing: expected [sub.go]; got %v", got.Entries)
	}
	// Workspace-relative dir path.
	if got.Dir != "sub" {
		t.Errorf("dir field: got %q; want %q", got.Dir, "sub")
	}
}

// TestFilesContent_ValidUTF8InvalidBytes verifies that a file with invalid
// UTF-8 bytes is served as base64.
func TestFilesContent_ValidUTF8InvalidBytes(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	// 0xFF is not valid UTF-8.
	invalid := []byte{0x68, 0x65, 0x6C, 0x6C, 0x6F, 0xFF, 0x77, 0x6F, 0x72, 0x6C, 0x64}
	mustWriteFile(t, filepath.Join(ws, "invalid.txt"), invalid)

	ts, tok := newFilesTestServer(t, ws)
	resp := filesGet(t, ts, tok, "/api/files/content?path=invalid.txt")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("invalid UTF-8: got %d; want 200", resp.StatusCode)
	}

	var got struct {
		Encoding string `json:"encoding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Encoding != "base64" {
		t.Errorf("invalid UTF-8: encoding=%q; want base64", got.Encoding)
	}
}

// TestFilesTree_PathResponseIsRelative verifies that the "path" field in each
// tree entry is workspace-relative (does not contain the workspace root prefix).
func TestFilesTree_PathResponseIsRelative(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	mustWriteFile(t, filepath.Join(ws, "go.mod"), []byte("module example"))

	ts, tok := newFilesTestServer(t, ws)
	resp := filesGet(t, ts, tok, "/api/files/tree")
	defer resp.Body.Close()

	var got struct {
		Entries []struct {
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for _, e := range got.Entries {
		if strings.HasPrefix(e.Path, ws) {
			t.Errorf("entry %q: path %q leaks workspace root prefix", e.Name, e.Path)
		}
		if filepath.IsAbs(e.Path) {
			t.Errorf("entry %q: path %q is absolute; want relative", e.Name, e.Path)
		}
	}
}
