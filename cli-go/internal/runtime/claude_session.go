package runtime

import (
	"encoding/json"
	"regexp"
)

// sessionIDRe is the shape of a native claude session id that may be placed on
// argv as `--resume <id>`. It mirrors the dispatch identity-field alphabet
// (alphanumeric first, then . _ : -, at most 128 chars), which also covers the
// UUIDs claude mints. A leading '-' can never occur, so the id cannot be
// parsed as a flag.
var sessionIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// ValidSessionID reports whether id is safe to pass to `claude --resume`.
func ValidSessionID(id string) bool { return sessionIDRe.MatchString(id) }

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
