package wsbus

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestFilesChangedPayloadNoContent is the wire-level invariant test.
// It asserts that FilesChangedPayload has no content-bearing field and that a
// marshalled event contains no bytes/content key.
func TestFilesChangedPayloadNoContent(t *testing.T) {
	t.Parallel()

	// Structural check: forbidden field names on the payload type.
	forbidden := []string{"Content", "Data", "Body", "Bytes", "Text", "Raw"}
	typ := reflect.TypeOf(FilesChangedPayload{})
	for _, name := range forbidden {
		if _, ok := typ.FieldByName(name); ok {
			t.Errorf("FilesChangedPayload has forbidden field %q — paths-only invariant violated", name)
		}
	}

	// Wire check: a published event must not contain any content key.
	bus := New()
	defer bus.Stop()

	sub := bus.Subscribe(TopicFilesChanged)
	defer sub.Unsubscribe()

	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	bus.Publish(TopicFilesChanged, FilesChangedPayload{
		Path:   "src/main.go",
		Action: "modified",
		TS:     ts,
	})

	select {
	case ev := <-sub.C():
		// Unmarshal the raw payload and check for content-bearing keys.
		var m map[string]json.RawMessage
		if err := json.Unmarshal(ev.Payload, &m); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		for k := range m {
			lower := strings.ToLower(k)
			for _, forbidden := range []string{"content", "data", "body", "bytes", "text", "raw"} {
				if lower == forbidden {
					t.Errorf("files.changed payload contains forbidden key %q", k)
				}
			}
		}
		// Verify only the expected keys are present.
		for _, required := range []string{"path", "action", "ts"} {
			if _, ok := m[required]; !ok {
				t.Errorf("files.changed payload missing expected key %q", required)
			}
		}
		// Verify path value is correct.
		var path string
		if err := json.Unmarshal(m["path"], &path); err != nil {
			t.Fatalf("unmarshal path: %v", err)
		}
		if path != "src/main.go" {
			t.Errorf("path = %q; want %q", path, "src/main.go")
		}
		// Verify action value.
		var action string
		if err := json.Unmarshal(m["action"], &action); err != nil {
			t.Fatalf("unmarshal action: %v", err)
		}
		if action != "modified" {
			t.Errorf("action = %q; want %q", action, "modified")
		}

	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for files.changed event")
	}
}

// TestTopicFilesChangedConstant verifies the topic constant value is stable.
func TestTopicFilesChangedConstant(t *testing.T) {
	t.Parallel()
	if TopicFilesChanged != "files.changed" {
		t.Errorf("TopicFilesChanged = %q; want %q", TopicFilesChanged, "files.changed")
	}
}
