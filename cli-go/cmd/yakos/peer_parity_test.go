// peer_parity_test.go — Phase 1 parity tests for `yakos peer` (rank 31).
//
// Design notes:
//
//  1. All tests call peer.Run directly with temp-dir coord paths via
//     CoordDirFn; no /var/lib/yakos access or root privilege required.
//
//  2. The bash peer.sh manages multi-developer coordination via shared
//     NDJSON event logs in a coord directory:
//     status        — list active peer sessions
//     log           — tail activity.ndjson
//     claim         — emit claim_confirmed event
//     release       — emit claim_released event
//     claims        — parse and display active-claims.json
//     deadlock      — wait-for graph from activity log vs. active-claims
//     propose-mode  — emit mode_proposal and poll for mode_response
//     respond-mode  — emit mode_response
//     handoff       — emit peer_handoff or peer_handoff_response
//
//  3. Parity is verified behaviourally (output shape, NDJSON content,
//     error conditions) rather than byte-for-byte.
//
// Critical scenarios:
//
//	(a) status: coord not configured → advisory message
//	(b) status: no sessions → advisory
//	(c) status: sessions listed with ts/kind/agent columns
//	(d) log: no activity file → advisory
//	(e) log: last 50 events returned (no --since)
//	(f) log: --since filters older lines
//	(g) claim: emits claim_confirmed to activity + session file
//	(h) claim: output message includes file + user@host
//	(i) claim: NDJSON byte-identical keys (ts, kind, actor, detail)
//	(j) release: emits claim_released to activity + session file
//	(k) claims: no active-claims.json → advisory
//	(l) claims: parsed and formatted table output
//	(m) deadlock: no files → advisory
//	(n) deadlock: claim_intent blocked → wait-for edge printed
//	(o) propose-mode: missing --mode → error
//	(p) propose-mode: invalid --mode → error
//	(q) propose-mode: missing --targets → error
//	(r) propose-mode: emits mode_proposal event
//	(s) propose-mode: timeout emits default_serialize
//	(t) respond-mode: missing --to → error
//	(u) respond-mode: ack emitted correctly
//	(v) respond-mode: reject emitted correctly
//	(w) handoff: emits peer_handoff event
//	(x) handoff: --ack emits peer_handoff_response
//	(y) handoff: --reject emits peer_handoff_response
//	(z) unknown subcommand → error
//	(aa) help text key phrases
//	(ab) peer entry in portedCommands
//	(ac) session file uses user@host-pid.ndjson naming
//	(ad) claim: missing <file> → error
//	(ae) release: missing <file> → error
//	(af) respond-mode: --ack and --reject mutually exclusive
//	(ag) handoff: missing --to → error
//	(ah) handoff: missing --completed-scope → error
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/mailbox"
	"github.com/bakw00ds/yakos/internal/peer"
)

// ---- helpers ----------------------------------------------------------------

var fixedNow = time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)

func newPeerCfg(t *testing.T, sub string, args ...string) (peer.Config, string) {
	t.Helper()
	coordRoot := t.TempDir()
	return peer.Config{
		Subcommand: sub,
		Args:       args,
		HomeDir:    t.TempDir(),
		User:       "testuser",
		Host:       "testhost",
		PID:        1234,
		Now:        func() time.Time { return fixedNow },
		CoordDirFn: func(project string) string {
			return filepath.Join(coordRoot, project)
		},
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}, coordRoot
}

// makeCoord creates a writable coord directory for the given project.
func makeCoord(t *testing.T, coordRoot, project string) string {
	t.Helper()
	d := filepath.Join(coordRoot, project)
	if err := os.MkdirAll(d, 0755); err != nil {
		t.Fatalf("makeCoord: %v", err)
	}
	return d
}

func peerOut(cfg peer.Config) string { return cfg.Writer.(*bytes.Buffer).String() }

// appendNDJSON writes a NDJSON line to path (creates parent dirs).
func appendNDJSON(t *testing.T, path string, obj interface{}) {
	t.Helper()
	data, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("appendNDJSON marshal: %v", err)
	}
	if err := mailbox.AppendLine(path, data); err != nil {
		t.Fatalf("appendNDJSON: %v", err)
	}
}

// readAllEvents reads all NDJSON lines from path and unmarshals them.
func readAllEvents(t *testing.T, path string) []map[string]interface{} {
	t.Helper()
	lines, err := mailbox.ReadLines(path)
	if err != nil {
		t.Fatalf("readAllEvents: %v", err)
	}
	var out []map[string]interface{}
	for _, l := range lines {
		var m map[string]interface{}
		if err := json.Unmarshal(l, &m); err != nil {
			t.Fatalf("readAllEvents unmarshal: %v — line: %s", err, l)
		}
		out = append(out, m)
	}
	return out
}

// ---- scenario (a): status coord not configured → advisory -------------------

func TestPeerParity_Status_CoordNotConfigured(t *testing.T) {
	cfg, _ := newPeerCfg(t, "status", "nonexistent-project")
	_, err := peer.Run(cfg)
	if err == nil {
		t.Fatal("expected error when coord not configured")
	}
	if !strings.Contains(err.Error(), "coord not configured") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (b): status no sessions → advisory ----------------------------

func TestPeerParity_Status_NoSessions(t *testing.T) {
	cfg, coordRoot := newPeerCfg(t, "status", "myapp")
	makeCoord(t, coordRoot, "myapp")
	res, err := peer.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := peerOut(cfg)
	if !strings.Contains(o, "no peer sessions on record") {
		t.Errorf("expected no-sessions advisory; got: %q", o)
	}
	if res.Project != "myapp" {
		t.Errorf("Project: got %q", res.Project)
	}
}

// ---- scenario (c): status sessions listed with columns ----------------------

func TestPeerParity_Status_SessionsListed(t *testing.T) {
	cfg, coordRoot := newPeerCfg(t, "status", "myapp")
	coordDir := makeCoord(t, coordRoot, "myapp")
	sessDir := filepath.Join(coordDir, "sessions")
	if err := os.MkdirAll(sessDir, 0755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	// Write a session file.
	sessFile := filepath.Join(sessDir, "alice@devbox-99.ndjson")
	appendNDJSON(t, sessFile, map[string]interface{}{
		"ts":   "2026-06-02T10:00:00Z",
		"kind": "claim_confirmed",
		"actor": map[string]interface{}{
			"user": "alice", "host": "devbox", "pid": 99,
			"session_id": "manual", "agent": "operator",
		},
	})

	if _, err := peer.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := peerOut(cfg)
	if !strings.Contains(o, "SESSION") || !strings.Contains(o, "LAST_EVENT") {
		t.Errorf("expected header columns; got: %q", o)
	}
	if !strings.Contains(o, "alice@devbox-99") {
		t.Errorf("expected session name in output; got: %q", o)
	}
	if !strings.Contains(o, "claim_confirmed") {
		t.Errorf("expected event kind; got: %q", o)
	}
	if !strings.Contains(o, "operator") {
		t.Errorf("expected agent; got: %q", o)
	}
}

// ---- scenario (d): log no activity file → advisory --------------------------

func TestPeerParity_Log_NoActivityFile(t *testing.T) {
	cfg, coordRoot := newPeerCfg(t, "log", "myapp")
	makeCoord(t, coordRoot, "myapp")
	if _, err := peer.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := peerOut(cfg)
	if !strings.Contains(o, "no activity yet") {
		t.Errorf("expected no-activity advisory; got: %q", o)
	}
}

// ---- scenario (e): log last 50 events (no --since) --------------------------

func TestPeerParity_Log_Last50(t *testing.T) {
	cfg, coordRoot := newPeerCfg(t, "log", "myapp")
	coordDir := makeCoord(t, coordRoot, "myapp")
	logPath := filepath.Join(coordDir, "activity.ndjson")
	// Write 60 events; expect only last 50.
	for i := 0; i < 60; i++ {
		appendNDJSON(t, logPath, map[string]interface{}{
			"ts":   "2026-06-02T12:00:00Z",
			"kind": "test_event",
			"seq":  i,
		})
	}
	if _, err := peer.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	lines := strings.Split(strings.TrimRight(peerOut(cfg), "\n"), "\n")
	if len(lines) != 50 {
		t.Errorf("expected 50 lines; got %d", len(lines))
	}
}

// ---- scenario (f): log --since filters older lines --------------------------

func TestPeerParity_Log_Since(t *testing.T) {
	cfg, coordRoot := newPeerCfg(t, "log", "--since", "2026-06-02T00:00:00Z", "myapp")
	coordDir := makeCoord(t, coordRoot, "myapp")
	logPath := filepath.Join(coordDir, "activity.ndjson")
	appendNDJSON(t, logPath, map[string]interface{}{"ts": "2026-05-01T00:00:00Z", "kind": "old"})
	appendNDJSON(t, logPath, map[string]interface{}{"ts": "2026-06-02T00:00:00Z", "kind": "new"})
	appendNDJSON(t, logPath, map[string]interface{}{"ts": "2026-06-02T12:00:00Z", "kind": "newer"})

	if _, err := peer.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := peerOut(cfg)
	if strings.Contains(o, `"old"`) {
		t.Errorf("old event should be filtered out; got: %q", o)
	}
	if !strings.Contains(o, `"new"`) || !strings.Contains(o, `"newer"`) {
		t.Errorf("expected new/newer events; got: %q", o)
	}
}

// ---- scenario (g): claim emits claim_confirmed to activity + session --------

func TestPeerParity_Claim_EmitsEvents(t *testing.T) {
	cfg, coordRoot := newPeerCfg(t, "claim", "src/main.go", "myapp")
	coordDir := makeCoord(t, coordRoot, "myapp")

	res, err := peer.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.EventsEmitted != 2 {
		t.Errorf("expected 2 events emitted; got %d", res.EventsEmitted)
	}

	// Activity log
	events := readAllEvents(t, filepath.Join(coordDir, "activity.ndjson"))
	if len(events) != 1 {
		t.Fatalf("activity: expected 1 event; got %d", len(events))
	}
	if events[0]["kind"] != "claim_confirmed" {
		t.Errorf("activity kind: got %v", events[0]["kind"])
	}

	// Session file
	sessFile := filepath.Join(coordDir, "sessions", "testuser@testhost-1234.ndjson")
	if _, err := os.Stat(sessFile); err != nil {
		t.Errorf("session file not created: %v", err)
	}
}

// ---- scenario (h): claim output includes file + user@host -------------------

func TestPeerParity_Claim_Output(t *testing.T) {
	cfg, coordRoot := newPeerCfg(t, "claim", "src/foo.go", "myapp")
	makeCoord(t, coordRoot, "myapp")

	if _, err := peer.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := peerOut(cfg)
	if !strings.Contains(o, "src/foo.go") {
		t.Errorf("expected file in output; got: %q", o)
	}
	if !strings.Contains(o, "testuser@testhost") {
		t.Errorf("expected user@host in output; got: %q", o)
	}
	if !strings.Contains(o, "30min TTL") {
		t.Errorf("expected TTL hint; got: %q", o)
	}
}

// ---- scenario (i): claim NDJSON keys match bash output ----------------------

func TestPeerParity_Claim_ByteIdenticalKeys(t *testing.T) {
	cfg, coordRoot := newPeerCfg(t, "claim", "src/bar.go", "myapp")
	coordDir := makeCoord(t, coordRoot, "myapp")

	if _, err := peer.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	lines, _ := mailbox.ReadLines(filepath.Join(coordDir, "activity.ndjson"))
	if len(lines) == 0 {
		t.Fatal("no lines in activity log")
	}
	var m map[string]interface{}
	if err := json.Unmarshal(lines[0], &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"ts", "kind", "actor", "detail"} {
		if _, ok := m[key]; !ok {
			t.Errorf("missing key %q in event: %s", key, lines[0])
		}
	}
	actor, _ := m["actor"].(map[string]interface{})
	for _, key := range []string{"user", "host", "pid", "session_id", "agent"} {
		if _, ok := actor[key]; !ok {
			t.Errorf("actor missing key %q", key)
		}
	}
	detail, _ := m["detail"].(map[string]interface{})
	for _, key := range []string{"path", "ttl_seconds", "expires_at", "source"} {
		if _, ok := detail[key]; !ok {
			t.Errorf("detail missing key %q", key)
		}
	}
}

// ---- scenario (j): release emits claim_released to activity + session -------

func TestPeerParity_Release_EmitsEvents(t *testing.T) {
	cfg, coordRoot := newPeerCfg(t, "release", "src/main.go", "myapp")
	coordDir := makeCoord(t, coordRoot, "myapp")

	res, err := peer.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.EventsEmitted != 2 {
		t.Errorf("expected 2 events; got %d", res.EventsEmitted)
	}
	events := readAllEvents(t, filepath.Join(coordDir, "activity.ndjson"))
	if len(events) != 1 || events[0]["kind"] != "claim_released" {
		t.Errorf("expected claim_released; got %v", events)
	}
	o := peerOut(cfg)
	if !strings.Contains(o, "released") {
		t.Errorf("expected release message; got: %q", o)
	}
}

// ---- scenario (k): claims no file → advisory --------------------------------

func TestPeerParity_Claims_NoFile(t *testing.T) {
	cfg, coordRoot := newPeerCfg(t, "claims", "myapp")
	makeCoord(t, coordRoot, "myapp")
	if _, err := peer.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := peerOut(cfg)
	if !strings.Contains(o, "no active-claims.json") {
		t.Errorf("expected no-claims advisory; got: %q", o)
	}
}

// ---- scenario (l): claims table output --------------------------------------

func TestPeerParity_Claims_TableOutput(t *testing.T) {
	cfg, coordRoot := newPeerCfg(t, "claims", "myapp")
	coordDir := makeCoord(t, coordRoot, "myapp")

	claimsData := `{
		"generated_at": "2026-06-02T12:00:00Z",
		"claims": [{
			"path": "src/main.go",
			"owners": [{
				"status": "active",
				"expires_at": "2026-06-02T12:30:00Z",
				"user": "alice",
				"host": "devbox",
				"agent": "lead"
			}]
		}]
	}`
	if err := mailbox.WriteFile(filepath.Join(coordDir, "active-claims.json"), []byte(claimsData)); err != nil {
		t.Fatalf("write claims: %v", err)
	}

	if _, err := peer.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := peerOut(cfg)
	if !strings.Contains(o, "src/main.go") {
		t.Errorf("expected path in output; got: %q", o)
	}
	if !strings.Contains(o, "alice@devbox") {
		t.Errorf("expected owner in output; got: %q", o)
	}
	if !strings.Contains(o, "PATH") || !strings.Contains(o, "STATUS") {
		t.Errorf("expected header; got: %q", o)
	}
}

// ---- scenario (m): deadlock no files → advisory -----------------------------

func TestPeerParity_Deadlock_NoFiles(t *testing.T) {
	cfg, coordRoot := newPeerCfg(t, "deadlock", "myapp")
	makeCoord(t, coordRoot, "myapp")
	if _, err := peer.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := peerOut(cfg)
	if !strings.Contains(o, "no claims or activity log") {
		t.Errorf("expected advisory; got: %q", o)
	}
}

// ---- scenario (n): deadlock wait-for edge printed ---------------------------

func TestPeerParity_Deadlock_WaitForEdge(t *testing.T) {
	cfg, coordRoot := newPeerCfg(t, "deadlock", "myapp")
	coordDir := makeCoord(t, coordRoot, "myapp")

	// active-claims: alice@devbox holds src/foo.go
	claimsData := `{
		"generated_at": "2026-06-02T12:00:00Z",
		"claims": [{
			"path": "src/foo.go",
			"owners": [{"user":"alice","host":"devbox","pid":10,"status":"active","expires_at":"2026-06-02T12:30:00Z"}]
		}]
	}`
	if err := mailbox.WriteFile(filepath.Join(coordDir, "active-claims.json"), []byte(claimsData)); err != nil {
		t.Fatalf("write claims: %v", err)
	}

	// activity: bob@devbox2 has a claim_intent on the same path
	appendNDJSON(t, filepath.Join(coordDir, "activity.ndjson"), map[string]interface{}{
		"ts":   "2026-06-02T12:00:01Z",
		"kind": "claim_intent",
		"actor": map[string]interface{}{
			"user": "bob", "host": "devbox2", "pid": 20,
		},
		"detail": map[string]interface{}{"path": "src/foo.go"},
	})

	if _, err := peer.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := peerOut(cfg)
	if !strings.Contains(o, "bob@devbox2") {
		t.Errorf("expected bob in wait-for; got: %q", o)
	}
	if !strings.Contains(o, "alice@devbox") {
		t.Errorf("expected alice in wait-for; got: %q", o)
	}
	if !strings.Contains(o, "src/foo.go") {
		t.Errorf("expected path in wait-for; got: %q", o)
	}
}

// ---- scenario (o): propose-mode missing --mode → error ----------------------

func TestPeerParity_ProposeMode_MissingMode(t *testing.T) {
	cfg, coordRoot := newPeerCfg(t, "propose-mode", "--targets", "src/", "myapp")
	makeCoord(t, coordRoot, "myapp")
	_, err := peer.Run(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--mode required") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (p): propose-mode invalid --mode → error ----------------------

func TestPeerParity_ProposeMode_InvalidMode(t *testing.T) {
	cfg, coordRoot := newPeerCfg(t, "propose-mode", "--mode", "badmode", "--targets", "src/", "myapp")
	makeCoord(t, coordRoot, "myapp")
	_, err := peer.Run(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "must be parallel|serialize|defer") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (q): propose-mode missing --targets → error -------------------

func TestPeerParity_ProposeMode_MissingTargets(t *testing.T) {
	cfg, coordRoot := newPeerCfg(t, "propose-mode", "--mode", "parallel", "myapp")
	makeCoord(t, coordRoot, "myapp")
	_, err := peer.Run(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--targets required") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (r): propose-mode emits mode_proposal event -------------------

func TestPeerParity_ProposeMode_EmitsEvent(t *testing.T) {
	// Use a very short timeout so the test does not hang waiting for a response.
	cfg, coordRoot := newPeerCfg(t, "propose-mode",
		"--mode", "parallel", "--targets", "src/", "--timeout", "0", "myapp")
	coordDir := makeCoord(t, coordRoot, "myapp")

	_, err := peer.Run(cfg) // timeout produces non-fatal path (no error from Run)
	// Timeout → "TIMEOUT: no peer response" path (not an error return)
	_ = err

	events := readAllEvents(t, filepath.Join(coordDir, "activity.ndjson"))
	if len(events) == 0 {
		t.Fatal("no events emitted")
	}
	// First event must be the proposal.
	if events[0]["kind"] != "mode_proposal" {
		t.Errorf("expected mode_proposal; got %v", events[0]["kind"])
	}
	detail, _ := events[0]["detail"].(map[string]interface{})
	if detail["proposed_mode"] != "parallel" {
		t.Errorf("proposed_mode: got %v", detail["proposed_mode"])
	}
}

// ---- scenario (s): propose-mode timeout emits default_serialize -------------

func TestPeerParity_ProposeMode_Timeout(t *testing.T) {
	cfg, coordRoot := newPeerCfg(t, "propose-mode",
		"--mode", "serialize", "--targets", "src/", "--timeout", "0", "myapp")
	coordDir := makeCoord(t, coordRoot, "myapp")

	peer.Run(cfg) //nolint:errcheck

	o := peerOut(cfg)
	if !strings.Contains(o, "TIMEOUT") {
		t.Errorf("expected TIMEOUT message; got: %q", o)
	}
	events := readAllEvents(t, filepath.Join(coordDir, "activity.ndjson"))
	// Should have proposal + timeout response
	kinds := make([]string, len(events))
	for i, e := range events {
		kinds[i], _ = e["kind"].(string)
	}
	found := false
	for _, k := range kinds {
		if k == "mode_response" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected mode_response (timeout) event; kinds: %v", kinds)
	}
}

// ---- scenario (t): respond-mode missing --to → error ------------------------

func TestPeerParity_RespondMode_MissingTo(t *testing.T) {
	cfg, coordRoot := newPeerCfg(t, "respond-mode", "--ack", "myapp")
	makeCoord(t, coordRoot, "myapp")
	_, err := peer.Run(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--to") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (u): respond-mode ack emitted ---------------------------------

func TestPeerParity_RespondMode_Ack(t *testing.T) {
	cfg, coordRoot := newPeerCfg(t, "respond-mode", "--to", "prop-001", "--ack", "myapp")
	coordDir := makeCoord(t, coordRoot, "myapp")

	res, err := peer.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.EventsEmitted != 2 {
		t.Errorf("expected 2 events; got %d", res.EventsEmitted)
	}
	events := readAllEvents(t, filepath.Join(coordDir, "activity.ndjson"))
	if len(events) != 1 || events[0]["kind"] != "mode_response" {
		t.Fatalf("expected mode_response; got %v", events)
	}
	detail, _ := events[0]["detail"].(map[string]interface{})
	if detail["response"] != "ack" {
		t.Errorf("response: got %v", detail["response"])
	}
	if detail["proposal_id"] != "prop-001" {
		t.Errorf("proposal_id: got %v", detail["proposal_id"])
	}
	o := peerOut(cfg)
	if !strings.Contains(o, "responded ack") {
		t.Errorf("expected ack message; got: %q", o)
	}
}

// ---- scenario (v): respond-mode reject emitted ------------------------------

func TestPeerParity_RespondMode_Reject(t *testing.T) {
	cfg, coordRoot := newPeerCfg(t, "respond-mode", "--to", "prop-002", "--reject", "--reason", "conflicting", "myapp")
	coordDir := makeCoord(t, coordRoot, "myapp")

	if _, err := peer.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := readAllEvents(t, filepath.Join(coordDir, "activity.ndjson"))
	detail, _ := events[0]["detail"].(map[string]interface{})
	if detail["response"] != "reject" {
		t.Errorf("response: got %v", detail["response"])
	}
	if detail["reason"] != "conflicting" {
		t.Errorf("reason: got %v", detail["reason"])
	}
}

// ---- scenario (w): handoff emits peer_handoff event -------------------------

func TestPeerParity_Handoff_Emit(t *testing.T) {
	cfg, coordRoot := newPeerCfg(t, "handoff",
		"--to", "bob@devbox2",
		"--completed-scope", "auth module",
		"--notes", "tests pass",
		"--next-action", "deploy to staging",
		"myapp",
	)
	coordDir := makeCoord(t, coordRoot, "myapp")

	res, err := peer.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.EventsEmitted != 2 {
		t.Errorf("expected 2 events; got %d", res.EventsEmitted)
	}
	events := readAllEvents(t, filepath.Join(coordDir, "activity.ndjson"))
	if len(events) != 1 || events[0]["kind"] != "peer_handoff" {
		t.Fatalf("expected peer_handoff; got %v", events)
	}
	detail, _ := events[0]["detail"].(map[string]interface{})
	if detail["to"] != "bob@devbox2" {
		t.Errorf("to: got %v", detail["to"])
	}
	if detail["completed_scope"] != "auth module" {
		t.Errorf("completed_scope: got %v", detail["completed_scope"])
	}
	o := peerOut(cfg)
	if !strings.Contains(o, "handoff sent to bob@devbox2") {
		t.Errorf("expected handoff message; got: %q", o)
	}
	if !strings.Contains(o, "release any claims") {
		t.Errorf("expected reminder; got: %q", o)
	}
}

// ---- scenario (x): handoff --ack emits peer_handoff_response ----------------

func TestPeerParity_Handoff_Ack(t *testing.T) {
	cfg, coordRoot := newPeerCfg(t, "handoff", "--ack", "ho-001", "myapp")
	coordDir := makeCoord(t, coordRoot, "myapp")

	if _, err := peer.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := readAllEvents(t, filepath.Join(coordDir, "activity.ndjson"))
	if events[0]["kind"] != "peer_handoff_response" {
		t.Errorf("expected peer_handoff_response; got %v", events[0]["kind"])
	}
	detail, _ := events[0]["detail"].(map[string]interface{})
	if detail["response"] != "ack" {
		t.Errorf("response: got %v", detail["response"])
	}
	o := peerOut(cfg)
	if !strings.Contains(o, "handoff ack") {
		t.Errorf("expected ack message; got: %q", o)
	}
}

// ---- scenario (y): handoff --reject emits peer_handoff_response -------------

func TestPeerParity_Handoff_Reject(t *testing.T) {
	cfg, coordRoot := newPeerCfg(t, "handoff", "--reject", "ho-002", "--reason", "not ready", "myapp")
	coordDir := makeCoord(t, coordRoot, "myapp")

	if _, err := peer.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := readAllEvents(t, filepath.Join(coordDir, "activity.ndjson"))
	detail, _ := events[0]["detail"].(map[string]interface{})
	if detail["response"] != "reject" {
		t.Errorf("response: got %v", detail["response"])
	}
	if detail["reason"] != "not ready" {
		t.Errorf("reason: got %v", detail["reason"])
	}
}

// ---- scenario (z): unknown subcommand → error --------------------------------

func TestPeerParity_UnknownSubcommand(t *testing.T) {
	cfg, _ := newPeerCfg(t, "nope")
	_, err := peer.Run(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (aa): help text key phrases -----------------------------------

func TestPeerParity_HelpText(t *testing.T) {
	var buf bytes.Buffer
	peer.PrintHelp(&buf)
	h := buf.String()
	for _, phrase := range []string{
		"yakos peer", "status", "log", "claim", "release",
		"claims", "deadlock", "propose-mode", "respond-mode", "handoff",
		"coord", "multi-dev",
	} {
		if !strings.Contains(h, phrase) {
			t.Errorf("help missing %q; got:\n%s", phrase, h)
		}
	}
}

// ---- scenario (ab): peer entry in portedCommands ----------------------------

func TestPeerParity_PortedCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "peer" {
			return
		}
	}
	t.Error("expected 'peer' in portedCommands; not found")
}

// ---- scenario (ac): session file uses user@host-pid.ndjson naming -----------

func TestPeerParity_SessionFileNaming(t *testing.T) {
	cfg, coordRoot := newPeerCfg(t, "claim", "src/naming.go", "myapp")
	coordDir := makeCoord(t, coordRoot, "myapp")

	if _, err := peer.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	sessFile := filepath.Join(coordDir, "sessions", "testuser@testhost-1234.ndjson")
	if _, err := os.Stat(sessFile); err != nil {
		t.Errorf("expected session file at %s: %v", sessFile, err)
	}
}

// ---- scenario (ad): claim missing <file> → error ----------------------------

func TestPeerParity_Claim_MissingFile(t *testing.T) {
	cfg, coordRoot := newPeerCfg(t, "claim", "myapp")
	makeCoord(t, coordRoot, "myapp")
	// With just one arg that looks like a project, the file arg is empty.
	// Rebuild config with no args.
	cfg2, coordRoot2 := newPeerCfg(t, "claim")
	makeCoord(t, coordRoot2, "myapp")
	_, err := peer.Run(cfg2)
	_ = cfg
	if err == nil {
		t.Fatal("expected error for missing <file>")
	}
	if !strings.Contains(err.Error(), "<file> required") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (ae): release missing <file> → error --------------------------

func TestPeerParity_Release_MissingFile(t *testing.T) {
	cfg, _ := newPeerCfg(t, "release")
	_, err := peer.Run(cfg)
	if err == nil {
		t.Fatal("expected error for missing <file>")
	}
	if !strings.Contains(err.Error(), "<file> required") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (af): respond-mode --ack and --reject mutually exclusive ------

func TestPeerParity_RespondMode_MutuallyExclusive(t *testing.T) {
	cfg, coordRoot := newPeerCfg(t, "respond-mode", "--to", "prop-001", "--ack", "--reject", "myapp")
	makeCoord(t, coordRoot, "myapp")
	_, err := peer.Run(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (ag): handoff missing --to → error ----------------------------

func TestPeerParity_Handoff_MissingTo(t *testing.T) {
	cfg, coordRoot := newPeerCfg(t, "handoff", "--completed-scope", "auth", "myapp")
	makeCoord(t, coordRoot, "myapp")
	_, err := peer.Run(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--to") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (ah): handoff missing --completed-scope → error ---------------

func TestPeerParity_Handoff_MissingCompletedScope(t *testing.T) {
	cfg, coordRoot := newPeerCfg(t, "handoff", "--to", "bob@devbox2", "myapp")
	makeCoord(t, coordRoot, "myapp")
	_, err := peer.Run(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--completed-scope") {
		t.Errorf("unexpected error: %v", err)
	}
}
