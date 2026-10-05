package runtime

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Integrity checks for the recordings made with the adapters' argv in
// tests/fixtures/runtime-streams (see adapter-argv-recordings.md): every line is
// a JSON object (codex: a "type" key; agy: an "event" key), and each recording
// keeps the shape the adapters and the stream parsers (K-135 / K-144) rely on.

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

// ---- agy 1.2.17 recordings ------------------------------------------------------

func readAgyFixture(t *testing.T, name string) []map[string]any {
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
		kind, _ := ev["event"].(string)
		if kind != "init" && kind != "step_update" && kind != "result" {
			t.Fatalf("%s: unexpected event kind %q in %v", name, kind, ev)
		}
		if _, ok := ev[kind].(map[string]any); !ok {
			t.Fatalf("%s: event %q must carry its payload under the same key: %v", name, kind, ev)
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

func agyPayload(ev map[string]any) map[string]any { return ev[ev["event"].(string)].(map[string]any) }

func agyResult(t *testing.T, events []map[string]any) map[string]any {
	t.Helper()
	last := events[len(events)-1]
	if last["event"] != "result" {
		t.Fatalf("the last event must be the result, got %v", last["event"])
	}
	return agyPayload(last)
}

func TestAgyStreamFixtures_SuccessShape(t *testing.T) {
	for _, name := range []string{
		"agy-stream-json-1.2.17-conversation-turn1.ndjson",
		"agy-stream-json-1.2.17-conversation-turn2.ndjson",
		"agy-stream-json-1.2.17-sandbox-denied.ndjson",
	} {
		t.Run(name, func(t *testing.T) {
			ev := readAgyFixture(t, name)
			if ev[0]["event"] != "init" {
				t.Fatalf("the first event is init, got %v", ev[0]["event"])
			}
			if id, _ := ev[0]["conversation_id"].(string); len(id) != 36 {
				t.Errorf("init must carry the conversation id: %v", ev[0]["conversation_id"])
			}
			init := agyPayload(ev[0])
			for _, k := range []string{"cwd", "permission_mode", "tools"} {
				if _, ok := init[k]; !ok {
					t.Errorf("init lacks %s", k)
				}
			}
			if init["permission_mode"] != "always-proceed" {
				t.Errorf("--dangerously-skip-permissions shows as permission_mode always-proceed, got %v", init["permission_mode"])
			}
			res := agyResult(t, ev)
			if res["status"] != "SUCCESS" {
				t.Errorf("result.status = %v", res["status"])
			}
			usage, _ := res["usage"].(map[string]any)
			for _, k := range []string{"input_tokens", "output_tokens", "thinking_tokens", "cache_read_tokens", "total_tokens"} {
				if _, ok := usage[k].(float64); !ok {
					t.Errorf("result.usage lacks numeric %s: %v", k, usage)
				}
			}
			if resp, _ := res["response"].(string); strings.TrimSpace(resp) == "" {
				t.Error("result.response is the final answer parsers extract")
			}
		})
	}
}

func TestAgyStreamFixtures_ResumeContinuesTheConversation(t *testing.T) {
	t1 := readAgyFixture(t, "agy-stream-json-1.2.17-conversation-turn1.ndjson")
	t2 := readAgyFixture(t, "agy-stream-json-1.2.17-conversation-turn2.ndjson")
	if t1[0]["conversation_id"] != t2[0]["conversation_id"] {
		t.Errorf("--conversation must keep the id: %v then %v", t1[0]["conversation_id"], t2[0]["conversation_id"])
	}
	maxIndex := func(ev []map[string]any) (hi float64, lo float64) {
		lo = 1e9
		for _, e := range ev {
			if e["event"] != "step_update" {
				continue
			}
			i, _ := agyPayload(e)["step_index"].(float64)
			if i > hi {
				hi = i
			}
			if i < lo {
				lo = i
			}
		}
		return hi, lo
	}
	hi1, _ := maxIndex(t1)
	_, lo2 := maxIndex(t2)
	if lo2 <= hi1 {
		t.Errorf("step_index must continue across turns: turn 1 ends at %v, turn 2 starts at %v", hi1, lo2)
	}
	r1, r2 := agyResult(t, t1), agyResult(t, t2)
	if r2["num_turns"] != float64(2) || r1["num_turns"] != float64(1) {
		t.Errorf("num_turns = %v then %v, want 1 then 2", r1["num_turns"], r2["num_turns"])
	}
	in1 := r1["usage"].(map[string]any)["input_tokens"].(float64)
	in2 := r2["usage"].(map[string]any)["input_tokens"].(float64)
	if in2 <= in1 {
		t.Errorf("result.usage is cumulative across turns: turn 2 reported %v after turn 1's %v", in2, in1)
	}
}

func TestAgyStreamFixtures_EffortConflictIsASingleErrorResult(t *testing.T) {
	ev := readAgyFixture(t, "agy-stream-json-1.2.17-effort-conflict.ndjson")
	if len(ev) != 1 {
		t.Fatalf("a rejected invocation prints only the result event, got %d events", len(ev))
	}
	res := agyResult(t, ev)
	if res["status"] != "ERROR" {
		t.Errorf("status = %v, want ERROR", res["status"])
	}
	if msg, _ := res["error"].(string); !strings.Contains(msg, "conflicts with --effort") {
		t.Errorf("the error must name the conflict: %q", msg)
	}
	if res["conversation_id"] != "" {
		t.Errorf("no conversation was created: %v", res["conversation_id"])
	}
}

func TestAgyStreamFixtures_SandboxDeniesAnOutsideWrite(t *testing.T) {
	var denied bool
	for _, e := range readAgyFixture(t, "agy-stream-json-1.2.17-sandbox-denied.ndjson") {
		if e["event"] != "step_update" {
			continue
		}
		p := agyPayload(e)
		if p["step_type"] != "tool" || p["state"] != "DONE" {
			continue
		}
		info, _ := p["tool_info"].(map[string]any)
		if out, _ := info["output"].(string); strings.Contains(out, "Operation not permitted") {
			denied = true
			if info["name"] != "run_command" {
				t.Errorf("tool name = %v", info["name"])
			}
		}
	}
	if !denied {
		t.Error("the sandbox fixture must show a run_command denied with Operation not permitted")
	}
}

// TestRecordingsContainNoPersonalData guards the redaction promised in
// adapter-argv-recordings.md for the files that note describes.
func TestRecordingsContainNoPersonalData(t *testing.T) {
	var files []string
	for _, pat := range []string{"agy-stream-json-1.2.17-conversation-*.ndjson", "agy-stream-json-1.2.17-effort-conflict.ndjson",
		"agy-stream-json-1.2.17-sandbox-denied.ndjson", "codex-exec-json-0.154.0.ndjson", "codex-exec-json-0.154.0-resume.ndjson",
		"codex-exec-json-0.154.0-subagent.ndjson", "codex-exec-json-0.154.0-auth-failure.ndjson"} {
		m, _ := filepath.Glob(filepath.Join(streamFixtureDir, pat))
		files = append(files, m...)
	}
	if len(files) == 0 {
		t.Skip("fixtures not reachable from the package dir")
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"/Users/tw", "claude-501", "bakw00ds", "@", "/private/tmp", "scratchpad"} {
			if strings.Contains(string(b), bad) {
				t.Errorf("%s contains %q", filepath.Base(f), bad)
			}
		}
	}
}
