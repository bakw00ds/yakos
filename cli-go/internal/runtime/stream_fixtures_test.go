package runtime

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Integrity checks for the codex 0.154.0 recordings in
// tests/fixtures/runtime-streams (see codex-0.154.0-dispatch-recordings.md):
// every line is a JSON object with a type, and each recording keeps the shape
// the adapter and the stream parsers (K-135 / K-144) rely on.

const streamFixtureDir = "../../../tests/fixtures/runtime-streams"

func readStreamFixture(t *testing.T, name string) []map[string]any {
	t.Helper()
	f, err := os.Open(filepath.Join(streamFixtureDir, name))
	if err != nil {
		t.Skipf("fixture not reachable from the package dir: %v", err)
	}
	defer func() { _ = f.Close() }()
	var events []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("%s: line %q is not a JSON object: %v", name, line, err)
		}
		if _, ok := ev["type"].(string); !ok {
			t.Fatalf("%s: event without a string type: %v", name, ev)
		}
		events = append(events, ev)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatalf("%s: empty fixture", name)
	}
	return events
}

func eventTypes(events []map[string]any) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e["type"].(string)
	}
	return out
}

func TestCodexStreamFixtures_SuccessShape(t *testing.T) {
	for _, name := range []string{
		"codex-exec-json-0.154.0.ndjson",
		"codex-exec-json-0.154.0-resume.ndjson",
		"codex-exec-json-0.154.0-subagent.ndjson",
	} {
		t.Run(name, func(t *testing.T) {
			ev := readStreamFixture(t, name)
			types := eventTypes(ev)
			if types[0] != "thread.started" || types[1] != "turn.started" || types[len(types)-1] != "turn.completed" {
				t.Fatalf("want thread.started, turn.started ... turn.completed, got %v", types)
			}
			if id, _ := ev[0]["thread_id"].(string); len(id) != 36 {
				t.Errorf("thread.started must carry the session id, got %v", ev[0])
			}
			usage, _ := ev[len(ev)-1]["usage"].(map[string]any)
			for _, k := range []string{"input_tokens", "cached_input_tokens", "cache_write_input_tokens", "output_tokens", "reasoning_output_tokens"} {
				if _, ok := usage[k].(float64); !ok {
					t.Errorf("turn.completed usage lacks numeric %s: %v", k, usage)
				}
			}
			var lastMessage string
			for _, e := range ev {
				if e["type"] == "item.completed" {
					if item, _ := e["item"].(map[string]any); item["type"] == "agent_message" {
						lastMessage, _ = item["text"].(string)
					}
				}
			}
			if strings.TrimSpace(lastMessage) == "" {
				t.Error("no agent_message text found: that is the final answer parsers extract")
			}
		})
	}
}

func TestCodexStreamFixtures_ResumeKeepsTheThreadID(t *testing.T) {
	first := readStreamFixture(t, "codex-exec-json-0.154.0.ndjson")[0]["thread_id"]
	resumed := readStreamFixture(t, "codex-exec-json-0.154.0-resume.ndjson")[0]["thread_id"]
	if first != resumed || first == nil {
		t.Errorf("`exec resume` must report the resumed thread id: first %v, resumed %v", first, resumed)
	}
}

func TestCodexStreamFixtures_SubagentAndFailure(t *testing.T) {
	var sawCollab bool
	for _, e := range readStreamFixture(t, "codex-exec-json-0.154.0-subagent.ndjson") {
		if item, _ := e["item"].(map[string]any); item["type"] == "collab_tool_call" {
			sawCollab = true
			for _, k := range []string{"tool", "sender_thread_id", "receiver_thread_ids", "agents_states", "status"} {
				if _, ok := item[k]; !ok {
					t.Errorf("collab_tool_call lacks %s: %v", k, item)
				}
			}
		}
	}
	if !sawCollab {
		t.Error("the subagent fixture must contain a collab_tool_call item")
	}

	ev := readStreamFixture(t, "codex-exec-json-0.154.0-auth-failure.ndjson")
	types := eventTypes(ev)
	if types[len(types)-1] != "turn.failed" {
		t.Fatalf("a failed run ends with turn.failed, got %v", types)
	}
	if msg, _ := ev[len(ev)-1]["error"].(map[string]any)["message"].(string); msg == "" {
		t.Error("turn.failed must carry error.message")
	}
	for _, typ := range types {
		if typ == "turn.completed" {
			t.Error("a failed run must not contain turn.completed")
		}
	}
}
