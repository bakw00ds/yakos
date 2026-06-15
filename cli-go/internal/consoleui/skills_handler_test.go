package consoleui_test

// skills_handler_test.go — unit tests for GET /api/skills.
//
// Coverage:
//   - Happy path: valid yakosRoot with agents returns populated agents + commands.
//   - Command list is always present (even when yakosRoot is empty / invalid).
//   - System-prompt body (Prompt field) is NEVER present in the JSON response.
//   - Wrong method (POST) → 405.
//   - Rate-limit class: no unbounded surfaces (the endpoint is GET-only,
//     inherits the project default rate-limit class from the edge middleware).
//
// Determinism: no time.Sleep, no subprocess calls, no LLM calls.
// All filesystem operations are scoped to temporary directories.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// buildFakeYakosRoot creates a minimal yakosRoot with a lib/agents directory
// containing a single agent .md file.  Returns the root path.
func buildFakeYakosRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	agentsDir := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll agents dir: %v", err)
	}
	// Minimal agent frontmatter + body.
	agentMD := `---
id: testworker
model: sonnet
---

## Purpose

Test specialist for skills_handler tests.

Does things for tests.
`
	if err := os.WriteFile(filepath.Join(agentsDir, "testworker.md"), []byte(agentMD), 0o644); err != nil {
		t.Fatalf("write testworker.md: %v", err)
	}
	return root
}

// newSkillsTestServer builds a consoleui.Server with the supplied yakosRoot
// and returns the test server + bearer token.
func newSkillsTestServer(t *testing.T, yakosRoot string) (*httptest.Server, string) {
	t.Helper()
	stateDir := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)

	srv := consoleui.MustNew(t, consoleui.Config{
		Token:             tok,
		KanbanBoardPath:   filepath.Join(t.TempDir(), "kanban.md"),
		KanbanProject:     "test",
		MetricsProjectDir: t.TempDir(),
		PerfWorkDir:       t.TempDir(),
		Bus:               bus,
		WorkDir:           t.TempDir(),
		WorkspaceRoot:     t.TempDir(),
		YakosRoot:         yakosRoot,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, tok
}

func doSkillsGET(t *testing.T, ts *httptest.Server, tok string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/skills", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/skills: %v", err)
	}
	return resp
}

// TestSkillsHandler_Happy verifies the endpoint returns agents + commands.
func TestSkillsHandler_Happy(t *testing.T) {
	root := buildFakeYakosRoot(t)
	ts, tok := newSkillsTestServer(t, root)

	resp := doSkillsGET(t, ts, tok)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}

	var got struct {
		Agents []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Runtime     string `json:"runtime"`
		} `json:"agents"`
		Commands []struct {
			Name    string `json:"name"`
			Summary string `json:"summary"`
		} `json:"commands"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Agents: should contain our "testworker" entry.
	if len(got.Agents) == 0 {
		t.Fatal("want at least one agent, got none")
	}
	var found bool
	for _, a := range got.Agents {
		if a.Name == "testworker" {
			found = true
			if a.Runtime == "" {
				t.Error("want non-empty runtime for testworker")
			}
			if a.Description == "" {
				t.Error("want non-empty description for testworker")
			}
		}
	}
	if !found {
		t.Errorf("testworker not found in agents: %+v", got.Agents)
	}

	// Commands: static list must be present.
	if len(got.Commands) == 0 {
		t.Fatal("want at least one command, got none")
	}
	var clearFound bool
	for _, c := range got.Commands {
		if c.Name == "clear" {
			clearFound = true
		}
	}
	if !clearFound {
		t.Errorf("'clear' command not found in commands: %+v", got.Commands)
	}

	// Cache-Control must be no-store.
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control: want %q, got %q", "no-store", cc)
	}
}

// TestSkillsHandler_NoSystemPromptInResponse verifies that the system-prompt
// body is never included in the JSON response (security gate).
//
// The "prompt" JSON key must not appear.  The description field is intentionally
// included (it is a short metadata summary, not the full system-prompt body).
// We verify the full body text is absent — only the first-line description is OK.
func TestSkillsHandler_NoSystemPromptInResponse(t *testing.T) {
	// Use an agent whose full body contains text not in the description line,
	// so we can tell them apart in the response.
	root := t.TempDir()
	agentsDir := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	agentMD := `---
id: secretagent
model: sonnet
---

## Purpose

Short description line.

## Secret body section

FULL_SYSTEM_PROMPT_BODY_SENTINEL: do not send this to the browser
`
	if err := os.WriteFile(filepath.Join(agentsDir, "secretagent.md"), []byte(agentMD), 0o644); err != nil {
		t.Fatalf("write secretagent.md: %v", err)
	}

	ts, tok := newSkillsTestServer(t, root)
	resp := doSkillsGET(t, ts, tok)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	// The raw JSON must not contain the "prompt" JSON key.
	if strings.Contains(strings.ToLower(string(body)), `"prompt"`) {
		t.Errorf("response body contains 'prompt' JSON field — system-prompt leakage: %s", body)
	}
	// The sentinel string from the full system-prompt body must not appear.
	if strings.Contains(string(body), "FULL_SYSTEM_PROMPT_BODY_SENTINEL") {
		t.Errorf("response body contains system-prompt body sentinel — leakage: %s", body)
	}
	// The "Secret body section" heading must not appear.
	if strings.Contains(string(body), "Secret body section") {
		t.Errorf("response body contains system-prompt heading — leakage: %s", body)
	}
}

// TestSkillsHandler_EmptyYakosRoot verifies that commands are still returned
// when yakosRoot is empty (graceful degradation).
func TestSkillsHandler_EmptyYakosRoot(t *testing.T) {
	ts, tok := newSkillsTestServer(t, "")

	resp := doSkillsGET(t, ts, tok)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}

	var got struct {
		Agents   []json.RawMessage `json:"agents"`
		Commands []json.RawMessage `json:"commands"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Commands are always present.
	if len(got.Commands) == 0 {
		t.Error("want at least one command even with empty yakosRoot")
	}
	// Agents may be empty when yakosRoot is not configured — that's OK.
}

// TestSkillsHandler_MethodNotAllowed verifies that POST → 405.
func TestSkillsHandler_MethodNotAllowed(t *testing.T) {
	root := buildFakeYakosRoot(t)
	ts, tok := newSkillsTestServer(t, root)

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/skills", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/skills: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("want 405, got %d", resp.StatusCode)
	}
}
