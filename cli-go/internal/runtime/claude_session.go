package runtime

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
)

// sessionIDRe is the shape of a native claude session id that may be placed on
// argv as `--resume <id>`. It mirrors the dispatch identity-field alphabet
// (alphanumeric first, then . _ : -, at most 128 chars), which also covers the
// UUIDs claude mints. A leading '-' can never occur, so the id cannot be
// parsed as a flag.
var sessionIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// ValidSessionID reports whether id is safe to pass to `claude --resume`.
func ValidSessionID(id string) bool { return sessionIDRe.MatchString(id) }

// SystemModelID returns the concrete model id a stream-json "system" line names
// (the init line claude prints when a session starts), or "" for any other line,
// a system line without a model, and a name that is not a plain model identifier
// (ValidModelID). It is cheap on the lines that matter least: a line that does
// not mention both words is rejected before any JSON is parsed.
func SystemModelID(line []byte) string {
	if !bytes.Contains(line, []byte(`"system"`)) || !bytes.Contains(line, []byte(`"model"`)) {
		return ""
	}
	if extractJSONStringField(line, "type") != "system" {
		return ""
	}
	if id := extractJSONStringField(line, "model"); ValidModelID(id) {
		return id
	}
	return ""
}

// ResultFailed reports whether line is a stream-json terminal "result" line that
// says the turn failed: is_error is true, or the subtype is an error one
// (error_during_execution, error_max_turns, ...). Any other line, and a result
// that reports success, is false.
func ResultFailed(line []byte) bool {
	if extractJSONStringField(line, "type") != "result" {
		return false
	}
	var res struct {
		Subtype string `json:"subtype"`
		IsError bool   `json:"is_error"`
	}
	if err := json.Unmarshal(line, &res); err != nil {
		return false
	}
	return res.IsError || strings.HasPrefix(res.Subtype, "error")
}

// ResultSessionID returns the claude session id carried by a stream-json
// terminal "result" line, or "" for any other line, a result without a usable
// id, an id that fails ValidSessionID, or a result that reports an error. The
// id is what makes a later one-shot chat turn able to continue the conversation
// (`--resume`), so it is checked before it is ever stored or used.
//
// An error result is skipped because claude stamps it with the session id it
// was asked to resume: `claude --resume <unknown-id>` prints
// {"type":"result","subtype":"error_during_execution","is_error":true,
// "session_id":"<unknown-id>"} and exits 1. Storing that id again would pin
// the conversation to a session that does not exist.
func ResultSessionID(line []byte) string {
	if extractJSONStringField(line, "type") != "result" {
		return ""
	}
	var res struct {
		SessionID string `json:"session_id"`
		IsError   bool   `json:"is_error"`
	}
	if err := json.Unmarshal(line, &res); err != nil || res.IsError || !ValidSessionID(res.SessionID) {
		return ""
	}
	return res.SessionID
}
