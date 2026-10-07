// Package consoleui — transcript.go
//
// Transcript is the append-only chat persistence layer.
//
// # Storage layout
//
//	<workDir>/chats/<conversationId>.ndjson      the transcript
//	<workDir>/chats/<conversationId>.meta.json   per-conversation meta (below)
//
// Each NDJSON line is a TranscriptEntry.  New lines are appended with O_APPEND
// so concurrent writers from the same process are safe; flock provides
// cross-process safety for tools that read/tail the file.
//
// # Conversation meta
//
// The meta file maps a runtime to that runtime's own session id for the
// conversation (today only claude's, so a follow-up one-shot turn can pass
// `--resume`). It is a small JSON object rewritten atomically, kept out of the
// transcript so readers of the transcript (the UI backfill, share, export) see
// no new entry kinds.
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
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/knowledge"
	"github.com/bakw00ds/yakos/internal/runtime"
	"github.com/bakw00ds/yakos/internal/statepath"
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
	// RoleRoute records where the router sent a turn and why (K-148). Runtime and
	// Model say where, Text is the reason, RuleID the rule. An older reader that
	// does not know the role skips the line.
	RoleRoute TranscriptRole = "route"
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

	// Runtime is the dispatch runtime (claude|codex|agy), set on user turns.
	Runtime string `json:"runtime,omitempty"`

	// Model is the resolved model tier, set on user turns and summary turns.
	Model string `json:"model,omitempty"`

	// ExitCode is set on summary turns.
	ExitCode int `json:"exit_code,omitempty"`

	// DurationS is set on summary turns.
	DurationS float64 `json:"duration_s,omitempty"`

	// TotalCostUSD is set on summary turns.
	TotalCostUSD float64 `json:"total_cost_usd,omitempty"`

	// RuleID, FallbackFrom and Pinned are set on route turns (K-148): the router
	// rule, the runtime a fallback skipped, and who fixed the runtime (override,
	// pane or router).
	RuleID       string `json:"rule_id,omitempty"`
	FallbackFrom string `json:"fallback_from,omitempty"`
	Pinned       string `json:"pinned,omitempty"`
}

// Transcripts manages per-conversation NDJSON transcript files.
type Transcripts struct {
	// chatsDir is the absolute path to <workDir>/chats/.
	chatsDir string

	// metaMu serializes read-modify-write of the per-conversation meta files
	// within this process.
	metaMu sync.Mutex
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

	// S-2 R20: a transcript holds the complete conversation (prompts,
	// streamed replies, cost lines). Same treatment as the dispatch log
	// (R12): private directory and file, tightened even when they already
	// exist from a version that wrote 0755/0644.
	if err := statepath.SecureDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("transcript: %w", err)
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600) //nolint:gosec
	if err != nil {
		return fmt.Errorf("transcript: open: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := statepath.SecureFile(f); err != nil {
		return fmt.Errorf("transcript: %w", err)
	}

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
	f, err := os.Open(path) //nolint:gosec // path built and validated by transcriptPath
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("transcript: read: %w", err)
	}
	defer func() { _ = f.Close() }()
	// The owner is on the first user turn, which is the first line of a real
	// transcript, so stop there instead of reading a conversation that may be
	// many megabytes: every dispatch asks (see handleChatDispatch).
	r := bufio.NewReaderSize(f, 64<<10)
	for {
		line, readErr := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var entry TranscriptEntry
			if json.Unmarshal(line, &entry) == nil && entry.Role == RoleUser && entry.OperatorID != "" {
				return entry.OperatorID, nil
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return "", nil
			}
			return "", fmt.Errorf("transcript: read: %w", readErr)
		}
	}
}

// errTranscriptForbidden is returned by Read when the caller's operatorID does
// not match the conversation owner.  HTTP handler must return 403.
var errTranscriptForbidden = errors.New("transcript: access denied (operator mismatch)")

// ---- conversation meta: native session ids -------------------------------------

// maxMetaBytes bounds how much of a meta file is read. Real files are under a
// hundred bytes.
const maxMetaBytes = 64 << 10

// conversationMeta is the content of <conversationId>.meta.json.
type conversationMeta struct {
	// NativeSessions maps a runtime name to that runtime's own session id for
	// this conversation.
	NativeSessions map[string]string `json:"native_sessions,omitempty"`

	// Owner is the operator the native sessions belong to: the one whose turn
	// produced them. A native session carries the whole conversation (every
	// prompt, answer and tool result), so resuming it is reading it. It is
	// handed out only to its owner; without this, any operator who knew a
	// conversationId could `--resume` someone else's session once the owner's
	// turn had ended (sec-324 F1).
	Owner string `json:"owner_operator_id,omitempty"`

	// ResumeFailures counts, per runtime, the consecutive turns that tried to
	// resume the stored session and failed. A session that is gone is normally
	// recognised from the CLI's own message; the count is the backstop for a
	// message that changes wording, so a dead id cannot fail every turn forever.
	ResumeFailures map[string]int `json:"resume_failures,omitempty"`

	// KnowledgeSHA, KnowledgeBytes and KnowledgeParts describe the knowledge
	// block composed for a non-claude conversation (K-149). The block's text is
	// in <conversationId>.knowledge.txt; it is composed once and every later
	// turn re-sends those bytes, so the prefix stays byte-stable.
	KnowledgeSHA   string           `json:"knowledge_sha,omitempty"`
	KnowledgeBytes int              `json:"knowledge_bytes,omitempty"`
	KnowledgeParts []knowledge.Part `json:"knowledge_parts,omitempty"`
}

// errNativeSessionOwner is returned when a native session operation names an
// operator other than the one the conversation's sessions belong to.
var errNativeSessionOwner = errors.New("transcript: native session belongs to another operator")

// metaPath returns the meta file path for a conversation, validated exactly like
// transcriptPath.
func (tr *Transcripts) metaPath(id string) (string, error) {
	if err := validateConversationID(id); err != nil {
		return "", err
	}
	clean := filepath.Clean(filepath.Join(tr.chatsDir, id+".meta.json"))
	if !strings.HasPrefix(clean, tr.chatsDir+string(filepath.Separator)) {
		return "", errors.New("transcript: conversation_id escapes chats directory")
	}
	return clean, nil
}

// readMeta loads a meta file. Anything unreadable, oversize or malformed is an
// empty meta (the file is rewritten whole on the next change), and any stored
// session id that fails the argv-safety check is dropped: the file is local
// state, but its ids end up on a command line.
func readMeta(path string) conversationMeta {
	var m conversationMeta
	f, err := os.Open(path) //nolint:gosec // path built by metaPath
	if err != nil {
		return m
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxMetaBytes+1))
	if err != nil || len(data) > maxMetaBytes {
		return conversationMeta{}
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return conversationMeta{}
	}
	for rt, id := range m.NativeSessions {
		if !isKnownRuntime(rt) || !runtime.ValidSessionID(id) {
			delete(m.NativeSessions, rt)
		}
	}
	// The owner is only ever compared, never used on a command line or in a path,
	// so it is not held to the identity-field alphabet (a certificate CN can
	// carry '@' or a space); a value too long to be a name is a damaged file.
	if len(m.Owner) > 512 {
		return conversationMeta{}
	}
	for rt, n := range m.ResumeFailures {
		if !isKnownRuntime(rt) || n <= 0 || n > maxResumeFailures {
			delete(m.ResumeFailures, rt)
		}
	}
	if !validKnowledgeMeta(&m) {
		m.KnowledgeSHA, m.KnowledgeBytes, m.KnowledgeParts = "", 0, nil
	}
	return m
}

// maxResumeFailures bounds the stored failure count (a damaged file cannot
// make it huge); resumeFailureLimit in the handler is far below it.
const maxResumeFailures = 1000

// writeMeta replaces the meta file atomically (temp file in the same directory,
// then rename) with owner-only permissions, like the transcript itself.
func (tr *Transcripts) writeMeta(path string, m conversationMeta) error {
	dir := filepath.Dir(path)
	if err := statepath.SecureDir(dir); err != nil {
		return fmt.Errorf("transcript: %w", err)
	}
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("transcript: marshal meta: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".meta-*.tmp") // 0600
	if err != nil {
		return fmt.Errorf("transcript: meta temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("transcript: write meta: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("transcript: write meta: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("transcript: replace meta: %w", err)
	}
	return nil
}

// NativeSession returns the runtime's own session id stored for the
// conversation, or "" when there is none (a first turn, an unknown runtime, an
// invalid conversation id, a damaged file) or when operatorID is not the
// operator the sessions belong to. The operator is a parameter, not a separate
// check, so no caller can read a session without saying whose it must be.
func (tr *Transcripts) NativeSession(conversationID, rt, operatorID string) string {
	path, err := tr.metaPath(conversationID)
	if err != nil || operatorID == "" {
		return ""
	}
	tr.metaMu.Lock()
	defer tr.metaMu.Unlock()
	m := readMeta(path)
	if m.Owner != operatorID {
		return ""
	}
	return m.NativeSessions[rt]
}

// SetNativeSession records the runtime's own session id for the conversation on
// behalf of operatorID, who becomes the owner of the conversation's sessions if
// it has none yet. It refuses a runtime this package does not know, an id that
// is not safe to put on a command line, and an operator other than the owner, so
// what is stored can always be used as it stands and only by whom it belongs to.
// A stored id means a turn worked, so the resume failure count resets.
func (tr *Transcripts) SetNativeSession(conversationID, rt, sessionID, operatorID string) error {
	if !isKnownRuntime(rt) {
		return errors.New("transcript: unknown runtime for native session")
	}
	if !runtime.ValidSessionID(sessionID) {
		return errors.New("transcript: invalid native session id")
	}
	if operatorID == "" {
		return errors.New("transcript: native session needs an operator")
	}
	path, err := tr.metaPath(conversationID)
	if err != nil {
		return err
	}
	tr.metaMu.Lock()
	defer tr.metaMu.Unlock()
	m := readMeta(path)
	if m.Owner != "" && m.Owner != operatorID {
		return errNativeSessionOwner
	}
	if m.Owner == operatorID && m.NativeSessions[rt] == sessionID && m.ResumeFailures[rt] == 0 {
		return nil
	}
	m.Owner = operatorID
	if m.NativeSessions == nil {
		m.NativeSessions = make(map[string]string)
	}
	m.NativeSessions[rt] = sessionID
	delete(m.ResumeFailures, rt)
	return tr.writeMeta(path, m)
}

// ClearNativeSession forgets the runtime's stored session id on behalf of
// operatorID, so the next turn starts a fresh native session. Clearing what is
// not stored is not an error; clearing another operator's session is refused.
func (tr *Transcripts) ClearNativeSession(conversationID, rt, operatorID string) error {
	path, err := tr.metaPath(conversationID)
	if err != nil {
		return err
	}
	tr.metaMu.Lock()
	defer tr.metaMu.Unlock()
	m := readMeta(path)
	if m.Owner != "" && m.Owner != operatorID {
		return errNativeSessionOwner
	}
	_, hadSession := m.NativeSessions[rt]
	_, hadFailures := m.ResumeFailures[rt]
	if !hadSession && !hadFailures {
		return nil
	}
	delete(m.NativeSessions, rt)
	delete(m.ResumeFailures, rt)
	return tr.writeMeta(path, m)
}

// ResumeFailures returns how many consecutive turns have failed while resuming
// the runtime's stored session (0 for an operator who is not the owner).
func (tr *Transcripts) ResumeFailures(conversationID, rt, operatorID string) int {
	path, err := tr.metaPath(conversationID)
	if err != nil || operatorID == "" {
		return 0
	}
	tr.metaMu.Lock()
	defer tr.metaMu.Unlock()
	m := readMeta(path)
	if m.Owner != operatorID {
		return 0
	}
	return m.ResumeFailures[rt]
}

// NoteResumeFailure records that a turn resumed the runtime's stored session and
// failed, and returns how many such turns in a row there have been. It never
// counts for an operator who is not the owner.
func (tr *Transcripts) NoteResumeFailure(conversationID, rt, operatorID string) (int, error) {
	if !isKnownRuntime(rt) {
		return 0, errors.New("transcript: unknown runtime for native session")
	}
	path, err := tr.metaPath(conversationID)
	if err != nil {
		return 0, err
	}
	tr.metaMu.Lock()
	defer tr.metaMu.Unlock()
	m := readMeta(path)
	if m.Owner == "" || m.Owner != operatorID {
		return 0, errNativeSessionOwner
	}
	if m.ResumeFailures == nil {
		m.ResumeFailures = make(map[string]int)
	}
	m.ResumeFailures[rt]++
	n := m.ResumeFailures[rt]
	return n, tr.writeMeta(path, m)
}
