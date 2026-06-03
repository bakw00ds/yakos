// Package mailbox is the substrate for yakos peer mailbox file operations.
//
// The peer coordination store lives under the coord directory:
//
//	/var/lib/yakos/<project>/coord/
//	  activity.ndjson            — append-only cross-session event log
//	  sessions/<user>@<host>-<pid>.ndjson — per-session event log
//	  active-claims.json         — rebuilt on claim events
//
// # Wire format
//
// Each record is a single JSON object terminated by a newline (NDJSON).
// The format is byte-identical to what peer.sh produces so that bash peer
// may consume Go-written mailboxes during shadow mode without modification.
//
// # Atomic append
//
// AppendLine opens the target file with O_APPEND|O_CREATE|O_WRONLY and
// acquires an exclusive flock before writing.  This matches the O_APPEND +
// flock pattern used in cli-go/internal/dispatch/events.go and mirrors the
// bash peer.sh append pattern (printf '%s\n' "$event" >> file).
//
// # Atomic write (temp-rename)
//
// WriteFile writes content through a sibling .tmp file and renames it into
// place (Q8 pattern).  Used for active-claims.json which is rebuilt whole.
package mailbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ---- event shapes ------------------------------------------------------------

// Actor is the actor block common to all peer events.
// Field names are lowercase JSON to match bash jq output exactly.
type Actor struct {
	User      string `json:"user"`
	Host      string `json:"host"`
	PID       int    `json:"pid"`
	SessionID string `json:"session_id"`
	Agent     string `json:"agent"`
}

// Event is the top-level peer event envelope (NDJSON line).
// Fields serialise in a stable order: ts, kind, actor, detail.
// The Detail field carries a pre-encoded JSON object so callers can
// supply arbitrary subcommand-specific payloads without a union type.
type Event struct {
	Ts     string          `json:"ts"`
	Kind   string          `json:"kind"`
	Actor  Actor           `json:"actor"`
	Detail json.RawMessage `json:"detail,omitempty"`
}

// Marshal serialises e to a compact JSON line (no trailing newline).
func (e Event) Marshal() ([]byte, error) {
	return json.Marshal(e)
}

// ---- coord path helpers ------------------------------------------------------

// SessionFileName returns the canonical per-session filename given the
// components that peer.sh uses: "<user>@<host>-<pid>.ndjson".
func SessionFileName(user, host string, pid int) string {
	return fmt.Sprintf("%s@%s-%d.ndjson", user, host, pid)
}

// ---- append -----------------------------------------------------------------

// AppendLine appends line (a JSON object) followed by a newline to path,
// creating the file and any parent directories if needed.
//
// It uses O_APPEND for atomic multi-process tail appends and acquires an
// exclusive flock to prevent interleaved writes from concurrent peers — the
// same strategy as dispatch/events.go and bash's `flock` call.
//
// Errors are returned to the caller; the peer subcommand treats log errors
// as non-fatal (coord is best-effort) except where the bash source exits on
// append failure.
func AppendLine(path string, line []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { //nolint:gosec
		return fmt.Errorf("mailbox: mkdir %s: %w", filepath.Dir(path), err)
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644) //nolint:gosec
	if err != nil {
		return fmt.Errorf("mailbox: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	// Exclusive flock — best-effort; proceed without lock on filesystems
	// (e.g. NFS) that don't support flock. Per-platform impl in
	// lock_unix.go / lock_windows.go.
	if lockErr := lockFile(f); lockErr == nil {
		defer func() { _ = unlockFile(f) }()
	}

	out := append(line, '\n') //nolint:gocritic
	if _, err := f.Write(out); err != nil {
		return fmt.Errorf("mailbox: write %s: %w", path, err)
	}
	return nil
}

// AppendEvent marshals e and calls AppendLine.
func AppendEvent(path string, e Event) error {
	line, err := e.Marshal()
	if err != nil {
		return fmt.Errorf("mailbox: marshal event: %w", err)
	}
	return AppendLine(path, line)
}

// ---- atomic write -----------------------------------------------------------

// WriteFile writes content to path atomically using a sibling .tmp file and
// os.Rename (Q8 pattern).  The target directory is created if absent.
func WriteFile(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { //nolint:gosec
		return fmt.Errorf("mailbox: mkdir %s: %w", filepath.Dir(path), err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, content, 0644); err != nil { //nolint:gosec
		return fmt.Errorf("mailbox: write tmp %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("mailbox: rename %s: %w", path, err)
	}
	return nil
}

// ---- read helpers -----------------------------------------------------------

// ReadLines reads all newline-delimited lines from path.
// Returns (nil, nil) when the file does not exist.
func ReadLines(path string) ([][]byte, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("mailbox: read %s: %w", path, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	// Split on newlines; drop trailing empty entry.
	raw := data
	var lines [][]byte
	for len(raw) > 0 {
		idx := indexByte(raw, '\n')
		if idx < 0 {
			lines = append(lines, raw)
			break
		}
		if idx > 0 {
			lines = append(lines, raw[:idx])
		}
		raw = raw[idx+1:]
	}
	return lines, nil
}

// LastLine returns the last non-empty line from path, or ("", nil) when the
// file is missing or empty.
func LastLine(path string) (string, error) {
	lines, err := ReadLines(path)
	if err != nil || len(lines) == 0 {
		return "", err
	}
	return string(lines[len(lines)-1]), nil
}

// FilterSince filters NDJSON lines where the "ts" field is >= sinceISO
// (lexical comparison works for ISO-8601 UTC strings).
func FilterSince(lines [][]byte, sinceISO string) [][]byte {
	if sinceISO == "" {
		return lines
	}
	var out [][]byte
	for _, l := range lines {
		var obj struct {
			Ts string `json:"ts"`
		}
		if err := json.Unmarshal(l, &obj); err != nil {
			continue
		}
		if obj.Ts >= sinceISO {
			out = append(out, l)
		}
	}
	return out
}

// Tail returns the last n lines.
func Tail(lines [][]byte, n int) [][]byte {
	if len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
}

// ---- time helpers -----------------------------------------------------------

// FormatTS returns t as an RFC3339 UTC timestamp matching the bash
// `date -u +%Y-%m-%dT%H:%M:%SZ` format (second precision, Z suffix).
func FormatTS(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05Z")
}

// FormatExpiry adds ttlSeconds to t and formats the result as FormatTS.
func FormatExpiry(t time.Time, ttlSeconds int) string {
	return FormatTS(t.Add(time.Duration(ttlSeconds) * time.Second))
}

// ---- private helpers --------------------------------------------------------

func indexByte(b []byte, c byte) int {
	for i, v := range b {
		if v == c {
			return i
		}
	}
	return -1
}
