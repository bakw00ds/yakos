package runtime

import (
	"bytes"
	"encoding/json"
)

// splitLines splits b on '\n' and returns non-empty trimmed line slices.
func splitLines(b []byte) [][]byte {
	raw := bytes.Split(b, []byte{'\n'})
	out := make([][]byte, 0, len(raw))
	for _, l := range raw {
		t := bytes.TrimSpace(l)
		if len(t) > 0 {
			out = append(out, t)
		}
	}
	return out
}

// extractJSONStringField parses line as a JSON object and returns the string
// value of field. Returns "" on any error.
func extractJSONStringField(line []byte, field string) string {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(line, &obj); err != nil {
		return ""
	}
	raw, ok := obj[field]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// extractAssistantText parses a claude stream-json "assistant" event and
// returns the concatenated text from all text content blocks.
//
// Expected shape:
//
//	{"type":"assistant","message":{"content":[{"type":"text","text":"..."},...]}}
func extractAssistantText(line []byte) string {
	var event struct {
		Type    string `json:"type"`
		Message struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &event); err != nil {
		return ""
	}
	var out string
	for _, block := range event.Message.Content {
		if block.Type == "text" {
			out += block.Text
		}
	}
	return out
}
