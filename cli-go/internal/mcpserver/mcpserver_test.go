package mcpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/mcpserver"
)

// ---- test helpers -----------------------------------------------------------

// session drives one MCP stdio session against a string of newline-delimited
// JSON requests. Returns all response lines as decoded maps.
func session(t *testing.T, cfg mcpserver.Config, requests ...string) []map[string]interface{} {
	t.Helper()
	input := strings.Join(requests, "\n") + "\n"
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := mcpserver.Serve(ctx, cfg, strings.NewReader(input), &out); err != nil && err != ctx.Err() {
		t.Fatalf("Serve error: %v", err)
	}
	var results []map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("unmarshal response line %q: %v", line, err)
		}
		results = append(results, m)
	}
	return results
}

// defaultCfg returns a Config with a temp workspace and no yakosRoot.
func defaultCfg(t *testing.T) mcpserver.Config {
	t.Helper()
	return mcpserver.Config{
		WorkspaceRoot: t.TempDir(),
		Version:       "0.test.0",
	}
}

// cfgWithBoard returns a Config whose workspace already has a kanban.md.
func cfgWithBoard(t *testing.T, content string) mcpserver.Config {
	t.Helper()
	tmp := t.TempDir()
	boardDir := filepath.Join(tmp, "work", "current")
	if err := os.MkdirAll(boardDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(boardDir, "kanban.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write kanban: %v", err)
	}
	return mcpserver.Config{WorkspaceRoot: tmp, Version: "0.test.0"}
}

// initReq returns an initialize JSON-RPC request string.
func initReq(id int) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{}}}`, id)
}

// callReq returns a tools/call JSON-RPC request string.
func callReq(id int, toolName string, args interface{}) string {
	argsB, _ := json.Marshal(args)
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, id, toolName, argsB)
}

// listReq returns a tools/list JSON-RPC request string.
func listReq(id int) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/list","params":{}}`, id)
}

// findByID returns the response map with the given id field.
func findByID(t *testing.T, results []map[string]interface{}, id int) map[string]interface{} {
	t.Helper()
	for _, r := range results {
		if v, ok := r["id"]; ok {
			switch n := v.(type) {
			case float64:
				if int(n) == id {
					return r
				}
			}
		}
	}
	t.Fatalf("no response with id=%d in %v", id, results)
	return nil
}

func hasError(m map[string]interface{}) bool {
	_, ok := m["error"]
	return ok
}

func isToolError(m map[string]interface{}) bool {
	result, ok := m["result"].(map[string]interface{})
	if !ok {
		return false
	}
	isErr, _ := result["isError"].(bool)
	return isErr
}

func resultText(m map[string]interface{}) string {
	result, ok := m["result"].(map[string]interface{})
	if !ok {
		return ""
	}
	content, ok := result["content"].([]interface{})
	if !ok || len(content) == 0 {
		return ""
	}
	item, ok := content[0].(map[string]interface{})
	if !ok {
		return ""
	}
	text, _ := item["text"].(string)
	return text
}

// ---- initialize tests -------------------------------------------------------

func TestInitialize_ReturnsProtocolVersion(t *testing.T) {
	cfg := defaultCfg(t)
	results := session(t, cfg, initReq(1))
	if len(results) == 0 {
		t.Fatal("no response")
	}
	resp := findByID(t, results, 1)
	if hasError(resp) {
		t.Fatalf("unexpected error: %v", resp["error"])
	}
	result, _ := resp["result"].(map[string]interface{})
	if result == nil {
		t.Fatal("result is nil")
	}
	if pv, _ := result["protocolVersion"].(string); pv == "" {
		t.Error("protocolVersion should not be empty")
	}
}

func TestInitialize_ReturnsServerInfo(t *testing.T) {
	cfg := defaultCfg(t)
	results := session(t, cfg, initReq(1))
	resp := findByID(t, results, 1)
	result, _ := resp["result"].(map[string]interface{})
	info, _ := result["serverInfo"].(map[string]interface{})
	if info == nil {
		t.Fatal("serverInfo is nil")
	}
	if name, _ := info["name"].(string); name != "yakos" {
		t.Errorf("serverInfo.name=%q; want 'yakos'", name)
	}
	if ver, _ := info["version"].(string); ver == "" {
		t.Error("serverInfo.version should not be empty")
	}
}

func TestInitialize_ReturnsToolsCapability(t *testing.T) {
	cfg := defaultCfg(t)
	results := session(t, cfg, initReq(1))
	resp := findByID(t, results, 1)
	result, _ := resp["result"].(map[string]interface{})
	caps, _ := result["capabilities"].(map[string]interface{})
	if caps == nil {
		t.Fatal("capabilities is nil")
	}
	if caps["tools"] == nil {
		t.Error("capabilities.tools should not be nil")
	}
}

// ---- tools/list tests -------------------------------------------------------

func TestToolsList_ReturnsList(t *testing.T) {
	cfg := defaultCfg(t)
	results := session(t, cfg, initReq(1), listReq(2))
	resp := findByID(t, results, 2)
	if hasError(resp) {
		t.Fatalf("unexpected error: %v", resp["error"])
	}
	result, _ := resp["result"].(map[string]interface{})
	tools, _ := result["tools"].([]interface{})
	if len(tools) == 0 {
		t.Fatal("tools list should not be empty")
	}
}

func TestToolsList_Returns8Tools(t *testing.T) {
	cfg := defaultCfg(t)
	results := session(t, cfg, listReq(1))
	resp := findByID(t, results, 1)
	result, _ := resp["result"].(map[string]interface{})
	tools, _ := result["tools"].([]interface{})
	if len(tools) != 8 {
		t.Errorf("expected 8 tools; got %d", len(tools))
	}
}

func TestToolsList_AllToolsHaveInputSchema(t *testing.T) {
	cfg := defaultCfg(t)
	results := session(t, cfg, listReq(1))
	resp := findByID(t, results, 1)
	result, _ := resp["result"].(map[string]interface{})
	tools, _ := result["tools"].([]interface{})
	for _, raw := range tools {
		tool, _ := raw.(map[string]interface{})
		name, _ := tool["name"].(string)
		if tool["inputSchema"] == nil {
			t.Errorf("tool %q has no inputSchema", name)
		}
		if tool["description"] == nil {
			t.Errorf("tool %q has no description", name)
		}
	}
}

func TestToolsList_ContainsExpectedTools(t *testing.T) {
	expected := []string{
		"yakos.dispatch",
		"yakos.kanban.list",
		"yakos.kanban.add",
		"yakos.kanban.move",
		"yakos.kanban.done",
		"yakos.refresh",
		"yakos.supervise.run",
		"yakos.supervise.ack",
	}
	cfg := defaultCfg(t)
	results := session(t, cfg, listReq(1))
	resp := findByID(t, results, 1)
	result, _ := resp["result"].(map[string]interface{})
	tools, _ := result["tools"].([]interface{})

	got := make(map[string]bool)
	for _, raw := range tools {
		tool, _ := raw.(map[string]interface{})
		name, _ := tool["name"].(string)
		got[name] = true
	}
	for _, want := range expected {
		if !got[want] {
			t.Errorf("tools list missing %q", want)
		}
	}
}

// ---- error mapping tests ----------------------------------------------------

func TestParseError_MalformedJSON(t *testing.T) {
	cfg := defaultCfg(t)
	results := session(t, cfg, `{not valid json}`)
	if len(results) == 0 {
		t.Fatal("expected error response for malformed JSON")
	}
	if !hasError(results[0]) {
		t.Error("expected JSON-RPC error for malformed input")
	}
	errObj, _ := results[0]["error"].(map[string]interface{})
	code, _ := errObj["code"].(float64)
	if int(code) != -32700 {
		t.Errorf("expected parse error (-32700); got %d", int(code))
	}
}

func TestMethodNotFound_UnknownMethod(t *testing.T) {
	cfg := defaultCfg(t)
	results := session(t, cfg, `{"jsonrpc":"2.0","id":1,"method":"yakos.nonexistent"}`)
	resp := findByID(t, results, 1)
	if !hasError(resp) {
		t.Error("expected error for unknown method")
	}
	errObj, _ := resp["error"].(map[string]interface{})
	code, _ := errObj["code"].(float64)
	if int(code) != -32601 {
		t.Errorf("expected method not found (-32601); got %d", int(code))
	}
}

func TestInvalidRequest_MissingMethod(t *testing.T) {
	cfg := defaultCfg(t)
	results := session(t, cfg, `{"jsonrpc":"2.0","id":1}`)
	resp := findByID(t, results, 1)
	if !hasError(resp) {
		t.Error("expected error for missing method")
	}
	errObj, _ := resp["error"].(map[string]interface{})
	code, _ := errObj["code"].(float64)
	if int(code) != -32600 {
		t.Errorf("expected invalid request (-32600); got %d", int(code))
	}
}

// ---- initialized notification -----------------------------------------------

func TestInitializedNotification_NoResponse(t *testing.T) {
	cfg := defaultCfg(t)
	// initialized has no id — server must not emit a response for it.
	// We send init + notification + list; expect exactly 2 responses (init + list).
	results := session(t, cfg,
		initReq(1),
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		listReq(2),
	)
	if len(results) != 2 {
		t.Errorf("expected 2 responses (init + list); got %d", len(results))
	}
}

// ---- tools/call — yakos.kanban.list -----------------------------------------

func TestKanbanList_EmptyBoard(t *testing.T) {
	cfg := defaultCfg(t)
	results := session(t, cfg, callReq(1, "yakos.kanban.list", map[string]interface{}{}))
	resp := findByID(t, results, 1)
	if hasError(resp) || isToolError(resp) {
		t.Fatalf("unexpected error: %v", resp)
	}
	text := resultText(resp)
	if !strings.Contains(text, `"items"`) {
		t.Errorf("expected items key in response; got %q", text)
	}
}

func TestKanbanList_WithItems(t *testing.T) {
	content := `# Kanban — test

## TODO
- [ ] K-1 — task one
  - category: other
  - assigned: unassigned
  - blockers: none
  - notes:

## IN PROGRESS
- [ ] K-2 — task two
  - category: feat
  - assigned: alice
  - blockers: none
  - notes:

## DONE
`
	cfg := cfgWithBoard(t, content)
	results := session(t, cfg, callReq(1, "yakos.kanban.list", map[string]interface{}{}))
	resp := findByID(t, results, 1)
	if isToolError(resp) {
		t.Fatalf("unexpected tool error: %s", resultText(resp))
	}
	text := resultText(resp)
	if !strings.Contains(text, "K-1") {
		t.Errorf("expected K-1 in response; got %q", text)
	}
	if !strings.Contains(text, "K-2") {
		t.Errorf("expected K-2 in response; got %q", text)
	}
}

func TestKanbanList_FilterByColumn(t *testing.T) {
	content := `# Kanban — test

## TODO
- [ ] K-1 — todo task
  - category: other
  - assigned: unassigned
  - blockers: none
  - notes:

## IN PROGRESS
- [ ] K-2 — in progress task
  - category: other
  - assigned: alice
  - blockers: none
  - notes:

## DONE
`
	cfg := cfgWithBoard(t, content)
	results := session(t, cfg, callReq(1, "yakos.kanban.list", map[string]interface{}{"column": "TODO"}))
	resp := findByID(t, results, 1)
	text := resultText(resp)
	if !strings.Contains(text, "K-1") {
		t.Errorf("expected K-1; got %q", text)
	}
	if strings.Contains(text, "K-2") {
		t.Errorf("should not include K-2 (wrong column); got %q", text)
	}
}

func TestKanbanList_UnknownFieldRejected(t *testing.T) {
	cfg := defaultCfg(t)
	results := session(t, cfg, callReq(1, "yakos.kanban.list", map[string]interface{}{"unknownField": true}))
	resp := findByID(t, results, 1)
	if !isToolError(resp) {
		t.Error("expected tool error for unknown field")
	}
}

// ---- tools/call — yakos.kanban.add ------------------------------------------

func TestKanbanAdd_CreatesTask(t *testing.T) {
	cfg := defaultCfg(t)
	results := session(t, cfg, callReq(1, "yakos.kanban.add", map[string]interface{}{"title": "implement health endpoint"}))
	resp := findByID(t, results, 1)
	if isToolError(resp) {
		t.Fatalf("unexpected tool error: %s", resultText(resp))
	}
	text := resultText(resp)
	if !strings.Contains(text, `"id"`) {
		t.Errorf("expected id in response; got %q", text)
	}
	if !strings.Contains(text, "K-1") {
		t.Errorf("expected K-1 as first id; got %q", text)
	}
}

func TestKanbanAdd_MissingTitle(t *testing.T) {
	cfg := defaultCfg(t)
	results := session(t, cfg, callReq(1, "yakos.kanban.add", map[string]interface{}{}))
	resp := findByID(t, results, 1)
	if !isToolError(resp) {
		t.Error("expected tool error for missing title")
	}
}

func TestKanbanAdd_UnknownFieldRejected(t *testing.T) {
	cfg := defaultCfg(t)
	results := session(t, cfg, callReq(1, "yakos.kanban.add", map[string]interface{}{"title": "test", "badField": "x"}))
	resp := findByID(t, results, 1)
	if !isToolError(resp) {
		t.Error("expected tool error for unknown field")
	}
}

// ---- tools/call — yakos.kanban.move -----------------------------------------

func TestKanbanMove_MovesTask(t *testing.T) {
	content := `# Kanban — test

## TODO
- [ ] K-1 — task one
  - category: other
  - assigned: unassigned
  - blockers: none
  - notes:

## IN PROGRESS

## DONE
`
	cfg := cfgWithBoard(t, content)
	results := session(t, cfg, callReq(1, "yakos.kanban.move", map[string]interface{}{"id": "K-1", "to": "IN PROGRESS"}))
	resp := findByID(t, results, 1)
	if isToolError(resp) {
		t.Fatalf("unexpected tool error: %s", resultText(resp))
	}
	if !strings.Contains(resultText(resp), `"ok":true`) {
		t.Error("expected ok:true")
	}
}

func TestKanbanMove_UnknownTaskError(t *testing.T) {
	cfg := defaultCfg(t)
	results := session(t, cfg, callReq(1, "yakos.kanban.move", map[string]interface{}{"id": "K-99", "to": "DONE"}))
	resp := findByID(t, results, 1)
	if !isToolError(resp) {
		t.Error("expected tool error for unknown task")
	}
}

func TestKanbanMove_MissingID(t *testing.T) {
	cfg := defaultCfg(t)
	results := session(t, cfg, callReq(1, "yakos.kanban.move", map[string]interface{}{"to": "DONE"}))
	resp := findByID(t, results, 1)
	if !isToolError(resp) {
		t.Error("expected tool error for missing id")
	}
}

func TestKanbanMove_InvalidColumn(t *testing.T) {
	cfg := defaultCfg(t)
	results := session(t, cfg, callReq(1, "yakos.kanban.move", map[string]interface{}{"id": "K-1", "to": "INVALID"}))
	resp := findByID(t, results, 1)
	if !isToolError(resp) {
		t.Error("expected tool error for invalid column")
	}
}

// ---- tools/call — yakos.kanban.done -----------------------------------------

func TestKanbanDone_MovesToDone(t *testing.T) {
	content := `# Kanban — test

## TODO
- [ ] K-1 — task one
  - category: other
  - assigned: unassigned
  - blockers: none
  - notes:

## IN PROGRESS

## DONE
`
	cfg := cfgWithBoard(t, content)
	results := session(t, cfg, callReq(1, "yakos.kanban.done", map[string]interface{}{"id": "K-1"}))
	resp := findByID(t, results, 1)
	if isToolError(resp) {
		t.Fatalf("unexpected tool error: %s", resultText(resp))
	}
}

func TestKanbanDone_MissingID(t *testing.T) {
	cfg := defaultCfg(t)
	results := session(t, cfg, callReq(1, "yakos.kanban.done", map[string]interface{}{}))
	resp := findByID(t, results, 1)
	if !isToolError(resp) {
		t.Error("expected tool error for missing id")
	}
}

// ---- tools/call — yakos.dispatch (error cases) ------------------------------

func TestDispatch_MissingAgent(t *testing.T) {
	cfg := defaultCfg(t)
	results := session(t, cfg, callReq(1, "yakos.dispatch", map[string]interface{}{"task": "do something"}))
	resp := findByID(t, results, 1)
	if !isToolError(resp) {
		t.Error("expected tool error for missing agent")
	}
}

func TestDispatch_MissingTask(t *testing.T) {
	cfg := defaultCfg(t)
	results := session(t, cfg, callReq(1, "yakos.dispatch", map[string]interface{}{"agent": "backend"}))
	resp := findByID(t, results, 1)
	if !isToolError(resp) {
		t.Error("expected tool error for missing task")
	}
}

func TestDispatch_NoYakosRootError(t *testing.T) {
	cfg := mcpserver.Config{
		WorkspaceRoot: t.TempDir(),
		YakosRoot:     "", // intentionally empty
	}
	results := session(t, cfg, callReq(1, "yakos.dispatch", map[string]interface{}{"agent": "backend", "task": "do it"}))
	resp := findByID(t, results, 1)
	if !isToolError(resp) {
		t.Error("expected tool error for missing yakos_root")
	}
}

func TestDispatch_UnknownFieldRejected(t *testing.T) {
	cfg := defaultCfg(t)
	results := session(t, cfg, callReq(1, "yakos.dispatch", map[string]interface{}{
		"agent": "backend", "task": "do it", "unknownField": "x",
	}))
	resp := findByID(t, results, 1)
	if !isToolError(resp) {
		t.Error("expected tool error for unknown field in dispatch args")
	}
}

// ---- tools/call — yakos.refresh (error cases) --------------------------------

func TestRefresh_NoYakosRootError(t *testing.T) {
	cfg := mcpserver.Config{
		WorkspaceRoot: t.TempDir(),
		YakosRoot:     "",
	}
	results := session(t, cfg, callReq(1, "yakos.refresh", map[string]interface{}{"dryRun": true}))
	resp := findByID(t, results, 1)
	if !isToolError(resp) {
		t.Error("expected tool error for missing yakos_root")
	}
}

func TestRefresh_UnknownFieldRejected(t *testing.T) {
	cfg := defaultCfg(t)
	results := session(t, cfg, callReq(1, "yakos.refresh", map[string]interface{}{"dryRun": true, "badField": 1}))
	resp := findByID(t, results, 1)
	if !isToolError(resp) {
		t.Error("expected tool error for unknown field")
	}
}

// ---- tools/call — unknown tool ----------------------------------------------

func TestToolsCall_UnknownTool(t *testing.T) {
	cfg := defaultCfg(t)
	results := session(t, cfg, callReq(1, "yakos.nonexistent", nil))
	resp := findByID(t, results, 1)
	// Should surface as tool_use_error, not a protocol error.
	if hasError(resp) {
		t.Error("unknown tool should surface as tool_use_error, not protocol error")
	}
	if !isToolError(resp) {
		t.Error("unknown tool should be isError:true")
	}
}

// ---- round-trip: initialize + tools/list ------------------------------------

func TestRoundTrip_InitThenList(t *testing.T) {
	cfg := defaultCfg(t)
	results := session(t, cfg, initReq(1), listReq(2))
	if len(results) != 2 {
		t.Fatalf("expected 2 responses; got %d", len(results))
	}
	findByID(t, results, 1) // initialize response
	listResp := findByID(t, results, 2)
	if hasError(listResp) {
		t.Fatalf("tools/list error: %v", listResp["error"])
	}
}

// ---- smoke: tools/list JSON is valid and contains all required fields -------

func TestToolsList_JSONSchema_IsValidJSON(t *testing.T) {
	cfg := defaultCfg(t)
	results := session(t, cfg, listReq(1))
	resp := findByID(t, results, 1)
	result, _ := resp["result"].(map[string]interface{})
	tools, _ := result["tools"].([]interface{})
	for _, raw := range tools {
		tool, _ := raw.(map[string]interface{})
		name, _ := tool["name"].(string)
		schema, _ := tool["inputSchema"]
		if schema == nil {
			t.Errorf("tool %q: inputSchema is nil", name)
			continue
		}
		// Re-marshal + unmarshal to confirm it's valid JSON.
		b, err := json.Marshal(schema)
		if err != nil {
			t.Errorf("tool %q: cannot marshal inputSchema: %v", name, err)
			continue
		}
		var check interface{}
		if err := json.Unmarshal(b, &check); err != nil {
			t.Errorf("tool %q: inputSchema not valid JSON: %v", name, err)
		}
	}
}
