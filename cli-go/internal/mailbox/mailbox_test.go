package mailbox_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/mailbox"
)

// ---- FormatTS ---------------------------------------------------------------

func TestFormatTS_UTC(t *testing.T) {
	ref := time.Date(2026, 6, 2, 15, 30, 45, 0, time.UTC)
	got := mailbox.FormatTS(ref)
	want := "2026-06-02T15:30:45Z"
	if got != want {
		t.Errorf("FormatTS: got %q want %q", got, want)
	}
}

func TestFormatTS_ConvertsTZ(t *testing.T) {
	// Non-UTC location should still produce a Z-suffix UTC string.
	loc := time.FixedZone("EST", -5*3600)
	ref := time.Date(2026, 6, 2, 10, 0, 0, 0, loc) // 15:00 UTC
	got := mailbox.FormatTS(ref)
	if !strings.HasSuffix(got, "Z") {
		t.Errorf("FormatTS: missing Z suffix in %q", got)
	}
	if !strings.HasPrefix(got, "2026-06-02T15:") {
		t.Errorf("FormatTS: unexpected UTC value %q", got)
	}
}

func TestFormatExpiry_AddsTTL(t *testing.T) {
	ref := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	got := mailbox.FormatExpiry(ref, 1800)
	want := "2026-06-02T12:30:00Z"
	if got != want {
		t.Errorf("FormatExpiry(+1800): got %q want %q", got, want)
	}
}

// ---- SessionFileName ---------------------------------------------------------

func TestSessionFileName(t *testing.T) {
	got := mailbox.SessionFileName("alice", "devbox", 42)
	want := "alice@devbox-42.ndjson"
	if got != want {
		t.Errorf("SessionFileName: got %q want %q", got, want)
	}
}

// ---- Event.Marshal -----------------------------------------------------------

func TestEvent_Marshal_FieldOrder(t *testing.T) {
	e := mailbox.Event{
		Ts:   "2026-06-02T12:00:00Z",
		Kind: "claim_confirmed",
		Actor: mailbox.Actor{
			User:      "alice",
			Host:      "devbox",
			PID:       99,
			SessionID: "manual",
			Agent:     "operator",
		},
	}
	line, err := e.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(line)
	// ts must appear before kind (JSON field order matches struct order).
	tsIdx := strings.Index(s, `"ts"`)
	kindIdx := strings.Index(s, `"kind"`)
	if tsIdx < 0 || kindIdx < 0 {
		t.Fatalf("Marshal: missing ts or kind in %q", s)
	}
	if tsIdx > kindIdx {
		t.Errorf("Marshal: expected ts before kind in %q", s)
	}
}

func TestEvent_Marshal_RoundTrip(t *testing.T) {
	detail, _ := json.Marshal(map[string]interface{}{
		"path":        "src/foo.go",
		"ttl_seconds": 1800,
	})
	e := mailbox.Event{
		Ts:   "2026-06-02T12:00:00Z",
		Kind: "claim_confirmed",
		Actor: mailbox.Actor{
			User: "bob", Host: "host2", PID: 7, SessionID: "s1", Agent: "lead",
		},
		Detail: json.RawMessage(detail),
	}
	line, err := e.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back mailbox.Event
	if err := json.Unmarshal(line, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.Kind != e.Kind {
		t.Errorf("Kind: got %q want %q", back.Kind, e.Kind)
	}
	if back.Actor.User != "bob" {
		t.Errorf("Actor.User: got %q want bob", back.Actor.User)
	}
}

// ---- AppendLine + ReadLines --------------------------------------------------

func TestAppendLine_CreatesFileAndDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "activity.ndjson")
	line := []byte(`{"ts":"2026-06-02T12:00:00Z","kind":"test"}`)

	if err := mailbox.AppendLine(path, line); err != nil {
		t.Fatalf("AppendLine: %v", err)
	}
	lines, err := mailbox.ReadLines(path)
	if err != nil {
		t.Fatalf("ReadLines: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected 1 line; got %d", len(lines))
	}
	if string(lines[0]) != string(line) {
		t.Errorf("line mismatch: got %q want %q", lines[0], line)
	}
}

func TestAppendLine_MultipleAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "activity.ndjson")
	lines := []string{
		`{"ts":"2026-06-02T12:00:00Z","kind":"a"}`,
		`{"ts":"2026-06-02T12:00:01Z","kind":"b"}`,
		`{"ts":"2026-06-02T12:00:02Z","kind":"c"}`,
	}
	for _, l := range lines {
		if err := mailbox.AppendLine(path, []byte(l)); err != nil {
			t.Fatalf("AppendLine: %v", err)
		}
	}
	got, err := mailbox.ReadLines(path)
	if err != nil {
		t.Fatalf("ReadLines: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 lines; got %d", len(got))
	}
	for i, l := range lines {
		if string(got[i]) != l {
			t.Errorf("line[%d]: got %q want %q", i, got[i], l)
		}
	}
}

func TestAppendLine_TrailingNewline(t *testing.T) {
	// The file on disk must have each line terminated by \n (byte-identical
	// to bash printf '%s\n').
	dir := t.TempDir()
	path := filepath.Join(dir, "activity.ndjson")
	if err := mailbox.AppendLine(path, []byte(`{"kind":"x"}`)); err != nil {
		t.Fatalf("AppendLine: %v", err)
	}
	data, _ := os.ReadFile(path)
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Errorf("file should end with \\n; got %q", data)
	}
}

func TestReadLines_MissingFile(t *testing.T) {
	dir := t.TempDir()
	lines, err := mailbox.ReadLines(filepath.Join(dir, "missing.ndjson"))
	if err != nil {
		t.Fatalf("expected nil error for missing file; got %v", err)
	}
	if lines != nil {
		t.Errorf("expected nil lines for missing file; got %v", lines)
	}
}

func TestReadLines_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.ndjson")
	if err := os.WriteFile(path, []byte{}, 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	lines, err := mailbox.ReadLines(path)
	if err != nil {
		t.Fatalf("ReadLines empty: %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("expected 0 lines; got %d", len(lines))
	}
}

// ---- AppendEvent ------------------------------------------------------------

func TestAppendEvent_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "activity.ndjson")
	ts := mailbox.FormatTS(time.Date(2026, 6, 2, 9, 0, 0, 0, time.UTC))
	e := mailbox.Event{
		Ts:   ts,
		Kind: "mode_proposal",
		Actor: mailbox.Actor{
			User: "charlie", Host: "box3", PID: 555,
			SessionID: "operator", Agent: "lead",
		},
	}
	if err := mailbox.AppendEvent(path, e); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	lines, err := mailbox.ReadLines(path)
	if err != nil || len(lines) != 1 {
		t.Fatalf("ReadLines after AppendEvent: err=%v len=%d", err, len(lines))
	}
	var back mailbox.Event
	if err := json.Unmarshal(lines[0], &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.Kind != "mode_proposal" {
		t.Errorf("Kind: got %q", back.Kind)
	}
	if back.Actor.PID != 555 {
		t.Errorf("Actor.PID: got %d", back.Actor.PID)
	}
}

// ---- LastLine ---------------------------------------------------------------

func TestLastLine_Empty(t *testing.T) {
	dir := t.TempDir()
	last, err := mailbox.LastLine(filepath.Join(dir, "missing.ndjson"))
	if err != nil {
		t.Fatalf("LastLine missing: %v", err)
	}
	if last != "" {
		t.Errorf("expected empty; got %q", last)
	}
}

func TestLastLine_ReturnsTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "activity.ndjson")
	for _, l := range []string{`{"kind":"a"}`, `{"kind":"b"}`, `{"kind":"c"}`} {
		_ = mailbox.AppendLine(path, []byte(l))
	}
	last, err := mailbox.LastLine(path)
	if err != nil {
		t.Fatalf("LastLine: %v", err)
	}
	if last != `{"kind":"c"}` {
		t.Errorf("LastLine: got %q", last)
	}
}

// ---- FilterSince ------------------------------------------------------------

func TestFilterSince_NoFilter(t *testing.T) {
	lines := [][]byte{
		[]byte(`{"ts":"2026-05-01T00:00:00Z","kind":"a"}`),
		[]byte(`{"ts":"2026-06-01T00:00:00Z","kind":"b"}`),
	}
	got := mailbox.FilterSince(lines, "")
	if len(got) != 2 {
		t.Errorf("FilterSince empty: expected 2; got %d", len(got))
	}
}

func TestFilterSince_FiltersOldLines(t *testing.T) {
	lines := [][]byte{
		[]byte(`{"ts":"2026-05-01T00:00:00Z","kind":"old"}`),
		[]byte(`{"ts":"2026-06-01T00:00:00Z","kind":"new"}`),
		[]byte(`{"ts":"2026-06-02T00:00:00Z","kind":"newer"}`),
	}
	got := mailbox.FilterSince(lines, "2026-06-01T00:00:00Z")
	if len(got) != 2 {
		t.Fatalf("FilterSince: expected 2; got %d", len(got))
	}
	var obj struct {
		Kind string `json:"kind"`
	}
	_ = json.Unmarshal(got[0], &obj)
	if obj.Kind != "new" {
		t.Errorf("FilterSince[0]: expected new; got %q", obj.Kind)
	}
}

func TestFilterSince_AllFiltered(t *testing.T) {
	lines := [][]byte{
		[]byte(`{"ts":"2026-01-01T00:00:00Z","kind":"a"}`),
	}
	got := mailbox.FilterSince(lines, "2026-06-01T00:00:00Z")
	if len(got) != 0 {
		t.Errorf("expected 0; got %d", len(got))
	}
}

func TestFilterSince_SkipsMalformed(t *testing.T) {
	lines := [][]byte{
		[]byte(`not-json`),
		[]byte(`{"ts":"2026-06-02T00:00:00Z","kind":"good"}`),
	}
	got := mailbox.FilterSince(lines, "2026-06-01T00:00:00Z")
	if len(got) != 1 {
		t.Fatalf("expected 1; got %d", len(got))
	}
}

// ---- Tail -------------------------------------------------------------------

func TestTail_ShorterThanN(t *testing.T) {
	lines := [][]byte{[]byte("a"), []byte("b")}
	got := mailbox.Tail(lines, 50)
	if len(got) != 2 {
		t.Errorf("Tail short: expected 2; got %d", len(got))
	}
}

func TestTail_ExactlyN(t *testing.T) {
	lines := [][]byte{[]byte("a"), []byte("b"), []byte("c")}
	got := mailbox.Tail(lines, 3)
	if len(got) != 3 {
		t.Errorf("Tail exact: expected 3; got %d", len(got))
	}
}

func TestTail_MoreThanN(t *testing.T) {
	lines := make([][]byte, 10)
	for i := range lines {
		lines[i] = []byte{byte('a' + i)}
	}
	got := mailbox.Tail(lines, 3)
	if len(got) != 3 {
		t.Fatalf("Tail: expected 3; got %d", len(got))
	}
	if string(got[0]) != "h" || string(got[1]) != "i" || string(got[2]) != "j" {
		t.Errorf("Tail: unexpected values %v", got)
	}
}

// ---- WriteFile (atomic) -----------------------------------------------------

func TestWriteFile_Atomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "active-claims.json")
	content := []byte(`{"generated_at":"2026-06-02T12:00:00Z","claims":[]}`)
	if err := mailbox.WriteFile(path, content); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content mismatch: got %q", got)
	}
	// No stray .tmp
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("stray .tmp file should not exist")
	}
}

func TestWriteFile_CreatesDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "claims.json")
	if err := mailbox.WriteFile(path, []byte(`{}`)); err != nil {
		t.Fatalf("WriteFile nested: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

// ---- byte-identical format check -------------------------------------------

// TestByteIdenticalFormat verifies that the JSON encoding produced by
// AppendEvent is parseable by a simple JSON reader (simulating bash jq)
// and that the field keys exactly match what peer.sh emits.
func TestByteIdenticalFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "activity.ndjson")
	detail, _ := json.Marshal(map[string]interface{}{
		"path":        "src/main.go",
		"ttl_seconds": 1800,
		"expires_at":  "2026-06-02T12:30:00Z",
		"source":      "yakos peer claim",
	})
	e := mailbox.Event{
		Ts:   "2026-06-02T12:00:00Z",
		Kind: "claim_confirmed",
		Actor: mailbox.Actor{
			User:      "alice",
			Host:      "devbox",
			PID:       1234,
			SessionID: "manual",
			Agent:     "operator",
		},
		Detail: json.RawMessage(detail),
	}
	if err := mailbox.AppendEvent(path, e); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	data, _ := os.ReadFile(path)
	line := strings.TrimRight(string(data), "\n")
	// Must be valid JSON
	var generic map[string]interface{}
	if err := json.Unmarshal([]byte(line), &generic); err != nil {
		t.Fatalf("not valid JSON: %v\nline: %s", err, line)
	}
	// Required keys must be present at top level
	for _, key := range []string{"ts", "kind", "actor", "detail"} {
		if _, ok := generic[key]; !ok {
			t.Errorf("missing key %q in JSON line: %s", key, line)
		}
	}
	// Actor sub-keys
	actor, _ := generic["actor"].(map[string]interface{})
	for _, key := range []string{"user", "host", "pid", "session_id", "agent"} {
		if _, ok := actor[key]; !ok {
			t.Errorf("actor missing key %q; actor=%v", key, actor)
		}
	}
}
