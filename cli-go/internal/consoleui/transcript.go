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
	"log/slog"
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
	// RoleError records a server-side dispatch failure.  The Text field carries
	// a terse error message (no internal paths or stack traces) suitable for
	// display in the chat UI.  Persisted so the conversation record reflects the
	// failure rather than ending with only a user turn.
	RoleError TranscriptRole = "error"
	// RoleQuestionAnswer records the operator's answer to an AskUserQuestion
	// tool call (P2c).  The Text field carries a JSON-encoded map[string]string
	// of {questionText: chosenOptionLabel}.  Persisted after successful delivery
	// to the engine so the conversation record reflects the answer.
	RoleQuestionAnswer TranscriptRole = "question_answer"
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

	// Role is "user" | "assistant" | "summary" | "error".
	Role TranscriptRole `json:"role"`

	// Text is the turn content.
	//   - Role "user": the dispatch task prompt.
	//   - Role "assistant": one chunk of streamed token text.
	//   - Role "summary": the terminal summary (cost, exit code, duration).
	//   - Role "error": terse server-side dispatch failure message (no internal
	//     paths or stack traces); persisted so the conversation record reflects
	//     the failure rather than ending with only a user turn.
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

	// Ownership check and M1 fail-closed guard.
	//
	// findFirstUserOwner scans entries for the first user turn with a non-empty
	// operatorID and returns (ownerID, true) when found, or ("", false) when the
	// file is non-empty but has no such turn.
	//
	// M1 fail-closed: if ownerOperatorID is supplied and the file is non-empty
	// but contains no user turn with an operatorID, we deny access (403) to
	// prevent an existence oracle and preserve forward-compatibility with future
	// explicit owner persistence.
	if ownerOperatorID != "" && len(entries) > 0 {
		firstOwner, established := findFirstUserOwner(entries)
		if !established {
			// Non-empty file with no user turn → fail closed (403) for all callers.
			return nil, errTranscriptForbidden
		}
		// For non-shared (owner-only) reads: the caller must be the owner, OR
		// the conversation was created by a legacy browser-minted random token.
		//
		// Legacy-token adoption rule (same-identity reclaim across runs/restarts):
		//   A legacy random token ("op-XXXX") was minted by app.js localStorage
		//   and cannot correspond to any real authenticated identity.  Any caller
		//   — the now-stable loopback identity after the fix, OR an authenticated
		//   networked user — may safely adopt such a conversation, because no
		//   human operator can legitimately "own" it in a security sense: those
		//   tokens are not tied to any persistent user account.
		//
		// Security invariant preserved (PR #229 forged-answer protection):
		//   If firstOwner is NOT a legacy random token (i.e. it looks like a real
		//   username or cert CN), the mismatch is always denied — no authenticated
		//   user can silently take over another authenticated user's conversation.
		//   The ErrOwnerConflict checks in the interactive manager are unaffected.
		if !sharedAccess && firstOwner != ownerOperatorID {
			if !isLegacyRandomToken(firstOwner) {
				// firstOwner is a real identity (username / cert CN / stable lbop-
				// token); refuse — this is a different user's conversation.
				return nil, errTranscriptForbidden
			}
			// firstOwner is a legacy random token: allow adoption.
			// The conversation has no stable human owner; the current caller
			// may re-claim it without security risk.
			//
			// Emit an audit log so every adoption (legitimate or otherwise)
			// is traceable in the daemon log.
			slog.Info("transcript: legacy-token conversation adopted",
				"conversationId", conversationID,
				"by", ownerOperatorID,
				"legacyOwner", firstOwner)
		}
		// For shared reads: ownership mismatch is allowed (the caller is an
		// authorised watcher); only the "no owner established" case above is denied.
	}

	return entries, nil
}

// findFirstUserOwner scans entries for the first user-role turn with a
// non-empty operatorID.  Returns (ownerID, true) when found; ("", false)
// when no such entry exists.
func findFirstUserOwner(entries []TranscriptEntry) (string, bool) {
	for _, e := range entries {
		if e.Role == RoleUser && e.OperatorID != "" {
			return e.OperatorID, true
		}
	}
	return "", false
}

// FirstUserOwner returns the operatorID established by the first user-turn
// entry in the transcript file for conversationID, without performing any
// ownership check.  Returns ("", nil) when the file does not exist or is
// empty.  Returns ("", nil) when the file is non-empty but has no user turn
// with an operatorID (M1 case — caller must treat as unowned).
//
// This is used by handleChatTranscript to anchor the hub's IsConversationShared
// lookup to the transcript's established owner, preventing an attacker from
// registering a shared session under a victim's conversationID.
func (tr *Transcripts) FirstUserOwner(conversationID string) (string, error) {
	path, err := tr.transcriptPath(conversationID)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("transcript: read: %w", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		var entry TranscriptEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Role == RoleUser && entry.OperatorID != "" {
			return entry.OperatorID, nil
		}
	}
	return "", nil
}

// errTranscriptForbidden is returned by Read when the caller's operatorID does
// not match the conversation owner.  HTTP handler must return 403.
var errTranscriptForbidden = errors.New("transcript: access denied (operator mismatch)")
