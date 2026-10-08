package dispatch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGatewayEvent_FieldsOnceAndHygiene(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.ndjson")
	a := newAccountAt(Request{AgentName: "anthropic-gateway"}, path, time.Now().Add(-time.Second))
	a.Gateway(GatewayEvent{
		Surface: "anthropic-gateway", Endpoint: "messages", Class: "subagent",
		ModelIn: "claude-opus-4-5", ModelOut: "claude-haiku-4-5-20251001", Billing: "api",
		Status: 200, Stream: true, Rewritten: true, InputTokens: 25, OutputTokens: 15, CacheRead: 100, CacheCreate: -5,
	})
	a.Gateway(GatewayEvent{Surface: "anthropic-gateway", Endpoint: "models"}) // second call: no-op
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 1 {
		t.Fatalf("%d lines: %s", len(lines), b)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &m); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"type": "gateway_request", "surface": "anthropic-gateway", "endpoint": "messages", "route_class": "subagent",
		"model_in": "claude-opus-4-5", "model_out": "claude-haiku-4-5-20251001", "billing": "api",
		"status": 200.0, "stream": true, "rewritten": true, "input_tokens": 25.0, "output_tokens": 15.0, "cache_read_tokens": 100.0,
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %v, want %v", k, m[k], v)
		}
	}
	if _, has := m["cache_creation_tokens"]; has {
		t.Error("a negative token count was written")
	}
	if d, _ := m["duration_s"].(float64); d <= 0 {
		t.Errorf("duration_s %v", m["duration_s"])
	}
}

// A value that is not a bounded identifier (a header value, a path, a secret)
// is dropped, not written.
func TestGatewayEvent_DropsUnboundedStrings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.ndjson")
	a := newAccountAt(Request{}, path, time.Now())
	a.Gateway(GatewayEvent{
		Surface: "anthropic-gateway", Endpoint: "messages", Class: "x y", ModelIn: "Bearer sk-ant-oat01-SECRET",
		ModelOut: "/Users/me/secret", Refused: strings.Repeat("a", 200), Status: 403,
	})
	b, _ := os.ReadFile(path)
	for _, bad := range []string{"SECRET", "/Users", "Bearer", "aaaa", "x y"} {
		if strings.Contains(string(b), bad) {
			t.Errorf("event contains %q: %s", bad, b)
		}
	}
}
