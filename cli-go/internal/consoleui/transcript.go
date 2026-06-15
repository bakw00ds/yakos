// Package consoleui — transcript.go
//
// Transcript is the append-only chat persistence layer.
//
// # Storage layout
//
//	<workDir>/chats/<conversationId>.ndjson
//
// Each NDJSON line is a TranscriptEntry.  New lines are appended with O_APPEND
// so concurrent writers from the same process are safe; flock provides
// cross-process safety for tools that read/tail the file.
//
// # Path traversal guard
//
// conversationId is validated against conversationIDRe BEFORE any path
// construction.  After validation the path is filepath.Clean'd and its prefix
// is verified against the expected chats directory (defense-in-depth).
// An invalid ID causes a generic 400 — no ID-echo in error messages.
//
// # NO secrets
//
// TranscriptEntry intentionally carries no token / bearer credential fields.
// The only identity it records is operatorID (self-asserted attribution).
package consoleui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/dispatch"
)

// conversationIDRe is the strict allow-list for conversation IDs used in
// file path construction.  Matches the same format as dispatch identity fields
// (alphanumeric-first, allows '.', '_', ':', '-', max 128 chars) — validated
// by dispatch.ValidateIdentityField which uses the same underlying regex.
//
// We ALSO verify filepath.Clean + prefix after that, as defense-in-depth.
var conversationIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:\-]{0,127}$`)

// TranscriptRole is the role of one turn in a chat transcript.
type TranscriptRole string

const (
	RoleUser      TranscriptRole = "user"
	RoleAssistant TranscriptRole = "assistant"
	RoleSummary   TranscriptRole = "summary"
)

// TranscriptEntry is one NDJSON line in a chat transcript file.
//
// Schema is additive-optional: new fields should carry omitempty so readers
// built against older schema remain compatible.
//
// No secrets — no bearer token, no API key.  Only self-asserted identity
// (operatorID) and dispatch parameters.
type TranscriptEntry struct {
	// TS is the UTC timestamp of this entry (RFC3339Nano).
	TS string `json:"ts"`

	// SessionID is the UI session that produced this entry.
	SessionID string `json:"session_id"`

	// ConversationID is the multi-turn conversation this entry belongs to.
	ConversationID string `json:"conversation_id"`

	// OperatorID is the self-asserted operator (attribution only).
	OperatorID string `json:"operator_id,omitempty"`

	// Role is "user" | "assistant" | "summary".
	Role TranscriptRole `json:"role"`

	// Text is the turn content.
	//   - Role "user": the dispatch task prompt.
	//   - Role "assistant": one chunk of streamed token text.
	//   - Role "summary": the terminal summary (cost, exit code, duration).
	Text string `json:"text"`

	// Runtime is the dispatch runtime (claude|codex|agy|gemini), set on user turns.
	Runtime string `json:"runtime,omitempty"`

	// Model is the resolved model tier, set on user turns and summary turns.
	Model string `json:"model,omitempty"`

	// ExitCode is set on summary turns.
	ExitCode int `json:"exit_code,omitempty"`

	// DurationS is set on summary turns.
	DurationS float64 `json:"duration_s,omitempty"`

	// TotalCostUSD is set on summary turns.
	TotalCostUSD float64 `json:"total_cost_usd,omitempty"`
}

// Transcripts manages per-conversation NDJSON transcript files.
type Transcripts struct {
	// chatsDir is the absolute path to <workDir>/chats/.
	chatsDir string
}

// NewTranscripts constructs a Transcripts rooted at <workDir>/chats.
// workDir is the yakOS work/current directory (e.g. <workspace>/work/current).
// The chats directory is created lazily on first Append.
func NewTranscripts(workDir string) *Transcripts {
	return &Transcripts{
		chatsDir: filepath.Join(workDir, "chats"),
	}
}

// validateConversationID checks that id is safe to use in a file path.
// Returns a descriptive error (never echoing the id value) on failure.
func validateConversationID(id string) error {
	if id == "" {
		return errors.New("transcript: conversation_id is required")
	}
	// Reuse dispatch.ValidateIdentityField which enforces the same regex.
	if err := dispatch.ValidateIdentityField("conversation_id", id); err != nil {
		return errors.New("transcript: invalid conversation_id format")
	}
	return nil
}

// transcriptPath returns the absolute path for a conversation file.
// Validates id and applies filepath.Clean + prefix check as defense-in-depth.
func (tr *Transcripts) transcriptPath(id string) (string, error) {
	if err := validateConversationID(id); err != nil {
		return "", err
	}
	raw := filepath.Join(tr.chatsDir, id+".ndjson")
	clean := filepath.Clean(raw)
	// Verify the cleaned path is still inside chatsDir (no ../escape).
	if !strings.HasPrefix(clean, tr.chatsDir+string(filepath.Separator)) &&
		clean != tr.chatsDir {
		return "", errors.New("transcript: conversation_id escapes chats directory")
	}
	return clean, nil
}

// Append adds one TranscriptEntry to the conversation file.
// Creates the chats directory and file if they do not yet exist.
// Thread-safe for concurrent callers in the same process (O_APPEND);
// cross-process safety via flock (same pattern as dispatch.appendEvent).
func (tr *Transcripts) Append(entry TranscriptEntry) error {
	if entry.ConversationID == "" {
		return errors.New("transcript: entry has no conversation_id")
	}
	path, err := tr.transcriptPath(entry.ConversationID)
	if err != nil {
		return err
	}

	if entry.TS == "" {
		entry.TS = time.Now().UTC().Format(time.RFC3339Nano)
	}

	line, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("transcript: marshal: %w", err)
	}
	line = append(line, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { //nolint:gosec
		return fmt.Errorf("transcript: mkdir: %w", err)
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644) //nolint:gosec
	if err != nil {
		return fmt.Errorf("transcript: open: %w", err)
	}
	defer func() { _ = f.Close() }()

	// flock for cross-process append safety (same as dispatch.appendEvent).
	lockFile(f)
	defer unlockFile(f)

	_, err = f.Write(line)
	return err
}

// Read returns all TranscriptEntries for the given conversationID.
// Returns an empty slice when no transcript file exists (not an error).
// ownerOperatorID must match the operator_id in the first entry of the file;
// if the file is non-empty and the first entry's operatorID does not match,
// Read returns errTranscriptForbidden (caller returns 403).
//
// Empty ownerOperatorID skips the ownership check (daemon-internal calls).
//
// This calls ReadShared with sharedAccess=false.  Use ReadShared directly
// when the caller has been verified to have shared access.
func (tr *Transcripts) Read(conversationID, ownerOperatorID string) ([]TranscriptEntry, error) {
	return tr.ReadShared(conversationID, ownerOperatorID, false)
}

// ReadShared returns all TranscriptEntries for the given conversationID,
// with optional shared-access bypass.
//
// When sharedAccess is true, the ownership check is bypassed — the session
// has been verified to be shared (via ChatHub.IsShared) before this call.
// This mirrors the ChatHub.Route shared-session visibility rule: if the hub
// delivers SSE frames to all operators for a shared session, the transcript
// backfill must also be readable by those operators.
//
// When sharedAccess is false, the standard owner-only check applies.
// errTranscriptForbidden is returned when the ownerOperatorID does not match
// the conversation owner.
//
// Empty ownerOperatorID AND sharedAccess=false skips the ownership check
// entirely (daemon-internal calls).
func (tr *Transcripts) ReadShared(conversationID, ownerOperatorID string, sharedAccess bool) ([]TranscriptEntry, error) {
	path, err := tr.transcriptPath(conversationID)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no transcript yet
		}
		return nil, fmt.Errorf("transcript: read: %w", err)
	}

	var entries []TranscriptEntry
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		var entry TranscriptEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			// Skip malformed lines (append-only log; partial writes at crash).
			continue
		}
		entries = append(entries, entry)
	}

	// Ownership check: the first user-turn entry with a non-empty operatorID
	// establishes the conversation owner.
	//
	// M1 fail-closed: when an operatorId is supplied and the file is non-empty
	// but contains no user turn with an operatorID, we deny access (403) rather
	// than granting it.  This prevents an existence oracle (seeing content when
	// no ownership is established) and is forward-compatible with future
	// explicit owner persistence.
	//
	// Shared-access bypass: when sharedAccess is true, the hub has already
	// verified the session is shared (ChatHub.IsShared).  The ownership check is
	// still performed to locate the first user turn (to prove the file has a
	// real owner), but a mismatch is NOT treated as forbidden — the caller is an
	// authorised watcher, not the owner.  errTranscriptForbidden is only returned
	// when the file is non-empty but has NO user turn (M1 fail-closed invariant).
	if !sharedAccess && ownerOperatorID != "" && len(entries) > 0 {
		ownerEstablished := false
		for _, e := range entries {
			if e.Role == RoleUser && e.OperatorID != "" {
				ownerEstablished = true
				if e.OperatorID != ownerOperatorID {
					return nil, errTranscriptForbidden
				}
				break
			}
		}
		if !ownerEstablished {
			// Non-empty file with no user turn → fail closed (403).
			return nil, errTranscriptForbidden
		}
	}

	// Shared-access path: M1 fail-closed still applies when the file is
	// non-empty but has no established user-turn owner.  A watcher of a shared
	// session must not read orphaned transcripts (no-owner = undefined provenance).
	// Ownership mismatch on its own is NOT forbidden (the watcher is not the owner
	// by design — that is the whole point of watch mode).
	if sharedAccess && ownerOperatorID != "" && len(entries) > 0 {
		ownerEstablished := false
		for _, e := range entries {
			if e.Role == RoleUser && e.OperatorID != "" {
				ownerEstablished = true
				break
			}
		}
		if !ownerEstablished {
			// Non-empty file with no user turn → fail closed even for watchers.
			return nil, errTranscriptForbidden
		}
	}

	return entries, nil
}

// errTranscriptForbidden is returned by Read when the caller's operatorID does
// not match the conversation owner.  HTTP handler must return 403.
var errTranscriptForbidden = errors.New("transcript: access denied (operator mismatch)")
