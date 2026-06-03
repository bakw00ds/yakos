package mcpserver_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/mcpserver"
)

// ---- helpers ----------------------------------------------------------------

// newHTTPTestServer creates an httptest.Server for the streamable HTTP MCP transport.
func newHTTPTestServer(t *testing.T, token string, cfg mcpserver.Config) (*httptest.Server, *mcpserver.StreamHTTPClient) {
	t.Helper()
	srv := mcpserver.NewHTTPServer(mcpserver.HTTPConfig{
		WriteToken: token,
		MCPConfig:  cfg,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	client := &mcpserver.StreamHTTPClient{
		BaseURL:    ts.URL,
		Token:      token,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
	return ts, client
}

// callHTTP sends one request and returns the first response frame.
func callHTTP(t *testing.T, client *mcpserver.StreamHTTPClient, method string, params interface{}) map[string]interface{} {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results, err := client.Call(ctx, method, 1, params)
	if err != nil {
		t.Fatalf("Call %s: %v", method, err)
	}
	if len(results) == 0 {
		t.Fatalf("Call %s: no response frames", method)
	}
	return results[0]
}

// readAllNDJSON reads newline-delimited JSON frames from r until EOF.
func readAllNDJSON(r io.Reader) ([]map[string]interface{}, error) {
	var frames []map[string]interface{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			return frames, err
		}
		frames = append(frames, m)
	}
	return frames, sc.Err()
}

// postMCP sends a raw body to /mcp and returns the raw HTTP response.
func postMCP(t *testing.T, ts *httptest.Server, token, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost, ts.URL+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	cl := &http.Client{Timeout: 5 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// ---- initialize tests -------------------------------------------------------

func TestHTTP_Initialize_Returns200(t *testing.T) {
	t.Parallel()
	_, client := newHTTPTestServer(t, "tok", defaultCfg(t))
	resp := callHTTP(t, client, "initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
	})
	if resp["error"] != nil {
		t.Errorf("unexpected error: %v", resp["error"])
	}
	result, ok := resp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("result not a map: %T", resp["result"])
	}
	if result["protocolVersion"] == "" {
		t.Error("protocolVersion should not be empty")
	}
}

func TestHTTP_Initialize_ServerInfo(t *testing.T) {
	t.Parallel()
	cfg := defaultCfg(t)
	cfg.Version = "0.test.1"
	_, client := newHTTPTestServer(t, "tok", cfg)
	resp := callHTTP(t, client, "initialize", nil)
	result, ok := resp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("result not a map: %T", resp["result"])
	}
	serverInfo, ok := result["serverInfo"].(map[string]interface{})
	if !ok {
		t.Fatalf("serverInfo not a map: %T", result["serverInfo"])
	}
	if serverInfo["version"] != "0.test.1" {
		t.Errorf("version=%v; want 0.test.1", serverInfo["version"])
	}
}

// ---- auth tests -------------------------------------------------------------

func TestHTTP_Auth_MissingToken_401(t *testing.T) {
	t.Parallel()
	ts, _ := newHTTPTestServer(t, "secret", defaultCfg(t))
	resp := postMCP(t, ts, "", `{"jsonrpc":"2.0","id":1,"method":"initialize"}`+"\n")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d; want 401", resp.StatusCode)
	}
}

func TestHTTP_Auth_WrongToken_401(t *testing.T) {
	t.Parallel()
	ts, _ := newHTTPTestServer(t, "secret", defaultCfg(t))
	resp := postMCP(t, ts, "wrong", `{"jsonrpc":"2.0","id":1,"method":"initialize"}`+"\n")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d; want 401", resp.StatusCode)
	}
}

func TestHTTP_Auth_NoAuthRequired_WhenTokenEmpty(t *testing.T) {
	t.Parallel()
	_, client := newHTTPTestServer(t, "", defaultCfg(t))
	client.Token = ""
	resp := callHTTP(t, client, "initialize", nil)
	if resp["error"] != nil {
		t.Errorf("unexpected error: %v", resp["error"])
	}
}

// ---- tools/list tests -------------------------------------------------------

func TestHTTP_ToolsList_ReturnsTools(t *testing.T) {
	t.Parallel()
	_, client := newHTTPTestServer(t, "tok", defaultCfg(t))
	resp := callHTTP(t, client, "tools/list", nil)
	if resp["error"] != nil {
		t.Errorf("unexpected error: %v", resp["error"])
	}
	result, ok := resp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("result not a map: %T", resp["result"])
	}
	tools, ok := result["tools"].([]interface{})
	if !ok {
		t.Fatalf("tools not an array: %T", result["tools"])
	}
	if len(tools) == 0 {
		t.Error("expected non-empty tools list")
	}
}

func TestHTTP_ToolsList_ContainsDispatch(t *testing.T) {
	t.Parallel()
	_, client := newHTTPTestServer(t, "tok", defaultCfg(t))
	resp := callHTTP(t, client, "tools/list", nil)
	result := resp["result"].(map[string]interface{})
	tools := result["tools"].([]interface{})
	found := false
	for _, tool := range tools {
		tm, ok := tool.(map[string]interface{})
		if !ok {
			continue
		}
		if tm["name"] == "yakos.dispatch" {
			found = true
			break
		}
	}
	if !found {
		t.Error("yakos.dispatch not found in tools list")
	}
}

// ---- tools/call tests -------------------------------------------------------

func TestHTTP_ToolsCall_KanbanList_Empty(t *testing.T) {
	t.Parallel()
	_, client := newHTTPTestServer(t, "tok", defaultCfg(t))
	resp := callHTTP(t, client, "tools/call", map[string]interface{}{
		"name":      "yakos.kanban.list",
		"arguments": map[string]interface{}{},
	})
	if resp["error"] != nil {
		t.Errorf("unexpected error: %v", resp["error"])
	}
	result, ok := resp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("result not a map: %T", resp["result"])
	}
	if result["content"] == nil {
		t.Error("expected content in result")
	}
}

func TestHTTP_ToolsCall_UnknownTool_IsError(t *testing.T) {
	t.Parallel()
	_, client := newHTTPTestServer(t, "tok", defaultCfg(t))
	resp := callHTTP(t, client, "tools/call", map[string]interface{}{
		"name":      "yakos.does.not.exist",
		"arguments": nil,
	})
	result, ok := resp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("result not a map (no error from protocol level, but isError in result): %T", resp["result"])
	}
	if result["isError"] != true {
		t.Errorf("isError=%v; want true for unknown tool", result["isError"])
	}
}

// ---- streaming / NDJSON tests -----------------------------------------------

func TestHTTP_MultipleRequests_InOneBody(t *testing.T) {
	t.Parallel()
	ts, _ := newHTTPTestServer(t, "tok", defaultCfg(t))

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"
	resp := postMCP(t, ts, "tok", body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d; want 200", resp.StatusCode)
	}
	frames, err := readAllNDJSON(resp.Body)
	if err != nil {
		t.Fatalf("read frames: %v", err)
	}
	if len(frames) != 2 {
		t.Errorf("want 2 response frames; got %d", len(frames))
	}
}

func TestHTTP_ParseError_ReturnsErrorFrame(t *testing.T) {
	t.Parallel()
	ts, _ := newHTTPTestServer(t, "tok", defaultCfg(t))

	resp := postMCP(t, ts, "tok", "this is not json\n")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d; want 200 (error in frame, not HTTP error)", resp.StatusCode)
	}
	frames, err := readAllNDJSON(resp.Body)
	if err != nil {
		t.Fatalf("read frames: %v", err)
	}
	if len(frames) == 0 {
		t.Fatal("expected an error frame")
	}
	if frames[0]["error"] == nil {
		t.Errorf("expected error frame; got %v", frames[0])
	}
}

func TestHTTP_MethodNotFound_ReturnsError(t *testing.T) {
	t.Parallel()
	_, client := newHTTPTestServer(t, "tok", defaultCfg(t))
	resp := callHTTP(t, client, "no.such.method", nil)
	if resp["error"] == nil {
		t.Error("expected error for unknown method")
	}
}

func TestHTTP_ContentType_IsNDJSON(t *testing.T) {
	t.Parallel()
	ts, _ := newHTTPTestServer(t, "tok", defaultCfg(t))
	resp := postMCP(t, ts, "tok", `{"jsonrpc":"2.0","id":1,"method":"initialize"}`+"\n")
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/x-ndjson") {
		t.Errorf("Content-Type=%q; want application/x-ndjson prefix", ct)
	}
}

func TestHTTP_Notification_NoResponse(t *testing.T) {
	t.Parallel()
	// Notifications (no id, non-initialize method) get no response frame.
	ts, _ := newHTTPTestServer(t, "tok", defaultCfg(t))

	// notification has no id → isNotification=true → no frame
	// then initialize with id → produces 1 frame
	body := `{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n"
	resp := postMCP(t, ts, "tok", body)

	frames, err := readAllNDJSON(resp.Body)
	if err != nil {
		t.Fatalf("read frames: %v", err)
	}
	// Only 1 frame: from initialize.
	if len(frames) != 1 {
		t.Errorf("want 1 frame; got %d", len(frames))
	}
}

func TestHTTP_Serve_HandlerNotNil(t *testing.T) {
	t.Parallel()
	cfg := mcpserver.HTTPConfig{
		Addr:       "127.0.0.1:0",
		WriteToken: "tok",
		MCPConfig:  defaultCfg(t),
	}
	srv := mcpserver.NewHTTPServer(cfg)
	if srv.Handler() == nil {
		t.Error("Handler should not be nil")
	}
}

func TestHTTP_EmptyBody_NoFrames(t *testing.T) {
	t.Parallel()
	ts, _ := newHTTPTestServer(t, "tok", defaultCfg(t))
	resp := postMCP(t, ts, "tok", "")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200", resp.StatusCode)
	}
	frames, err := readAllNDJSON(resp.Body)
	if err != nil {
		t.Fatalf("read frames: %v", err)
	}
	if len(frames) != 0 {
		t.Errorf("want 0 frames for empty body; got %d", len(frames))
	}
}

func TestHTTP_ToolsCall_KanbanAdd_HappyPath(t *testing.T) {
	t.Parallel()
	_, client := newHTTPTestServer(t, "tok", defaultCfg(t))
	resp := callHTTP(t, client, "tools/call", map[string]interface{}{
		"name":      "yakos.kanban.add",
		"arguments": map[string]interface{}{"title": "HTTP transport test task"},
	})
	if resp["error"] != nil {
		t.Errorf("unexpected error: %v", resp["error"])
	}
}

func TestHTTP_ToolsCall_SuperviseRun(t *testing.T) {
	t.Parallel()
	_, client := newHTTPTestServer(t, "tok", defaultCfg(t))
	resp := callHTTP(t, client, "tools/call", map[string]interface{}{
		"name":      "yakos.supervise.run",
		"arguments": map[string]interface{}{"scope": "recent"},
	})
	// May return isError if no supervise state; should not be a protocol error.
	if resp["error"] != nil {
		t.Errorf("unexpected protocol error: %v", resp["error"])
	}
}

func TestHTTP_IDPreserved_InResponse(t *testing.T) {
	t.Parallel()
	ts, _ := newHTTPTestServer(t, "tok", defaultCfg(t))
	body := `{"jsonrpc":"2.0","id":42,"method":"initialize"}` + "\n"
	resp := postMCP(t, ts, "tok", body)
	frames, err := readAllNDJSON(resp.Body)
	if err != nil {
		t.Fatalf("read frames: %v", err)
	}
	if len(frames) == 0 {
		t.Fatal("no frames")
	}
	id := frames[0]["id"]
	// JSON numbers unmarshal as float64.
	if id != float64(42) {
		t.Errorf("id=%v (%T); want 42", id, id)
	}
}
