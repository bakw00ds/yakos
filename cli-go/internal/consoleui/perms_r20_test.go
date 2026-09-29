package consoleui_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/bakw00ds/yakos/internal/consoleui"
)

// S-2 R20 (s2-daemon-security-review-2026-09-21.md): chat transcripts (the
// full conversation text) and saved workflow YAML were written 0644 in 0755
// directories, readable by any local user.

func TestTranscript_Append_PrivateModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	workDir := t.TempDir()
	tr := consoleui.NewTranscriptsForTest(workDir)
	entry := consoleui.TranscriptEntry{ConversationID: "conv-r20", OperatorID: "alice", Role: consoleui.RoleUser, Text: "secret prompt"}
	if err := tr.Append(entry); err != nil {
		t.Fatalf("Append: %v", err)
	}
	dir := filepath.Join(workDir, "chats")
	file := filepath.Join(dir, "conv-r20.ndjson")
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("chats dir mode=%v err=%v; want 0700", fi.Mode().Perm(), err)
	}
	if fi, err := os.Stat(file); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("transcript mode=%v err=%v; want 0600", fi.Mode().Perm(), err)
	}
}

// An install upgraded from a version that wrote 0755/0644 must be tightened
// on the next append, not just for new files.
func TestTranscript_Append_TightensExisting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	workDir := t.TempDir()
	dir := filepath.Join(workDir, "chats")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "conv-old.ndjson")
	if err := os.WriteFile(file, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0o644); err != nil {
		t.Fatal(err)
	}
	tr := consoleui.NewTranscriptsForTest(workDir)
	if err := tr.Append(consoleui.TranscriptEntry{ConversationID: "conv-old", OperatorID: "alice", Role: consoleui.RoleUser, Text: "x"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Errorf("existing chats dir mode=%o; want 0700", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(file); fi.Mode().Perm() != 0o600 {
		t.Errorf("existing transcript mode=%o; want 0600", fi.Mode().Perm())
	}
}

func TestFlows_SaveWorkflow_PrivateModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	ts, tok, workDir := newFlowsTestServer(t)
	body, _ := json.Marshal(map[string]string{"name": "my-flow", "yaml": minimalYAML, "version": ""})
	resp := authedPost(t, ts.URL+"/flows/api/workflow", tok, string(body))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save: status=%d body=%s", resp.StatusCode, bodyStr(t, resp))
	}
	dir := filepath.Join(workDir, "workflows")
	file := filepath.Join(dir, "my-flow.yaml")
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("workflows dir mode=%v err=%v; want 0700", fi.Mode().Perm(), err)
	}
	if fi, err := os.Stat(file); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("workflow yaml mode=%v err=%v; want 0600", fi.Mode().Perm(), err)
	}
}
