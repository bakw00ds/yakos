// supervise_parity_test.go — Phase 1 parity tests for `yakos supervise`.
//
// Design notes:
//
//  1. All tests call supervise.Run directly with temp-dir paths; no bash
//     runtime or real ~/.yakos-state directory is required.
//
//  2. The bash supervise.sh manages the shadow-agent supervisor lifecycle:
//     enable, disable, status, tail, clear, set, pending, ack, ack-all.
//     State lives in .yakos.yml (supervisor block) and supervisor-acks.ndjson.
//
//  3. Parity is verified behaviourally (output shape, error conditions,
//     filesystem effects) rather than byte-for-byte.
//
// Critical scenarios:
//
//	(a) enable → sets supervisor.enabled: true in .yakos.yml
//	(b) disable → sets supervisor.enabled: false in .yakos.yml
//	(c) status → prints enabled state + buffer + counter + findings
//	(d) tail → prints last N findings
//	(e) clear → removes buffer + findings + counter
//	(f) set → updates a known key in .yakos.yml
//	(g) set unknown key → error
//	(h) pending → lists unacked escalation findings
//	(i) ack → writes ack record; removes from pending
//	(j) ack-all → writes ack records for all pending
//	(k) unknown subcommand → error with hint
//	(l) help text contains key phrases
//	(m) supervise entry in portedCommands
//	(n) deriveFindingID format
//	(o) AckRecord JSON round-trip
//	(p) pending skips non-escalation actions
//	(q) acked findings excluded from pending
//	(r) set no block → creates fresh supervisor block
//	(s) enable no .yakos.yml → error
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/supervise"
)

// ---- helpers ----------------------------------------------------------------

func newSuperviseCfg(t *testing.T, sub string) supervise.Config {
	t.Helper()
	home := t.TempDir()
	var out, errOut bytes.Buffer
	return supervise.Config{
		Subcommand:       sub,
		HomeDir:          home,
		AgentControlRoot: filepath.Join(home, "agent-control"),
		NowFn:            func() time.Time { return time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC) },
		UserFn:           func() string { return "testoperator" },
		Writer:           &out,
		ErrWriter:        &errOut,
		TailN:            10,
	}
}

func superviseOut(cfg supervise.Config) string { return cfg.Writer.(*bytes.Buffer).String() }

func makeSuperviseProject(t *testing.T, cfg supervise.Config, slug string) (supervise.Config, string) {
	t.Helper()
	repo := t.TempDir()
	acSlug := filepath.Join(cfg.AgentControlRoot, slug)
	if err := os.MkdirAll(filepath.Join(acSlug, "work", "current"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	ppFile := filepath.Join(acSlug, ".project-path")
	if err := os.WriteFile(ppFile, []byte(repo+"\n"), 0644); err != nil {
		t.Fatalf("write .project-path: %v", err)
	}
	cfg.Project = slug
	return cfg, repo
}

func writeSuperviseYAML(t *testing.T, repo, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, ".yakos.yml"), []byte(content), 0644); err != nil {
		t.Fatalf("write .yakos.yml: %v", err)
	}
}

func writeSuperviseFindings(t *testing.T, cfg supervise.Config, slug string, lines []string) {
	t.Helper()
	p := filepath.Join(cfg.AgentControlRoot, slug, "work", "current", "supervisor-findings.ndjson")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatalf("write findings: %v", err)
	}
}

func escalation(ts, action, rationale string) string {
	b, _ := json.Marshal(map[string]string{
		"ts":                 ts,
		"recommended_action": action,
		"rationale":          rationale,
	})
	return string(b)
}

// ---- scenario (a): enable ---------------------------------------------------

func TestSuperviseParity_Enable_SetsTrue(t *testing.T) {
	cfg := newSuperviseCfg(t, "enable")
	cfg, repo := makeSuperviseProject(t, cfg, "proj")
	writeSuperviseYAML(t, repo, "name: proj\nsupervisor:\n  enabled: false\n")

	res, err := supervise.Run(cfg)
	if err != nil {
		t.Fatalf("Run enable: %v", err)
	}
	if res.Enabled == nil || !*res.Enabled {
		t.Error("expected Enabled=true in result")
	}

	data, _ := os.ReadFile(filepath.Join(repo, ".yakos.yml"))
	if !strings.Contains(string(data), "enabled: true") {
		t.Errorf("expected enabled: true in .yakos.yml; got:\n%s", data)
	}
	out := superviseOut(cfg)
	if !strings.Contains(out, "Supervisor enabled") {
		t.Errorf("expected 'Supervisor enabled' in output; got: %q", out)
	}
}

// ---- scenario (b): disable --------------------------------------------------

func TestSuperviseParity_Disable_SetsFalse(t *testing.T) {
	cfg := newSuperviseCfg(t, "disable")
	cfg, repo := makeSuperviseProject(t, cfg, "proj")
	writeSuperviseYAML(t, repo, "supervisor:\n  enabled: true\n")

	res, err := supervise.Run(cfg)
	if err != nil {
		t.Fatalf("Run disable: %v", err)
	}
	if res.Enabled == nil || *res.Enabled {
		t.Error("expected Enabled=false in result")
	}

	data, _ := os.ReadFile(filepath.Join(repo, ".yakos.yml"))
	if !strings.Contains(string(data), "enabled: false") {
		t.Errorf("expected enabled: false; got:\n%s", data)
	}
}

// ---- scenario (c): status ---------------------------------------------------

func TestSuperviseParity_Status_PrintsState(t *testing.T) {
	cfg := newSuperviseCfg(t, "status")
	cfg, repo := makeSuperviseProject(t, cfg, "proj")
	writeSuperviseYAML(t, repo, "supervisor:\n  enabled: true\n  runtime: claude\n  block_on_critical: true\n")

	_, err := supervise.Run(cfg)
	if err != nil {
		t.Fatalf("Run status: %v", err)
	}
	out := superviseOut(cfg)
	if !strings.Contains(out, "yakos supervise status") {
		t.Errorf("expected header; got: %q", out)
	}
	if !strings.Contains(out, "YES") {
		t.Errorf("expected YES for enabled; got: %q", out)
	}
	if !strings.Contains(out, "runtime") {
		t.Errorf("expected runtime key; got: %q", out)
	}
}

// ---- scenario (d): tail -----------------------------------------------------

func TestSuperviseParity_Tail_NoFindings(t *testing.T) {
	cfg := newSuperviseCfg(t, "tail")
	cfg, repo := makeSuperviseProject(t, cfg, "proj")
	writeSuperviseYAML(t, repo, "supervisor:\n  enabled: true\n")

	_, err := supervise.Run(cfg)
	if err != nil {
		t.Fatalf("Run tail: %v", err)
	}
	out := superviseOut(cfg)
	if !strings.Contains(out, "no findings yet") {
		t.Errorf("expected 'no findings yet'; got: %q", out)
	}
}

func TestSuperviseParity_Tail_PrintsLastN(t *testing.T) {
	cfg := newSuperviseCfg(t, "tail")
	cfg, repo := makeSuperviseProject(t, cfg, "proj")
	cfg.TailN = 2
	writeSuperviseYAML(t, repo, "supervisor:\n  enabled: true\n")
	writeSuperviseFindings(t, cfg, "proj", []string{
		`{"ts":"2026-06-01T12:00:00Z","n":1}`,
		`{"ts":"2026-06-01T12:00:01Z","n":2}`,
		`{"ts":"2026-06-01T12:00:02Z","n":3}`,
	})

	_, err := supervise.Run(cfg)
	if err != nil {
		t.Fatalf("Run tail: %v", err)
	}
	out := superviseOut(cfg)
	if !strings.Contains(out, `"n":3`) {
		t.Errorf("expected last entry in output; got: %q", out)
	}
	if strings.Contains(out, `"n":1`) {
		t.Errorf("should not include first entry; got: %q", out)
	}
}

// ---- scenario (e): clear ----------------------------------------------------

func TestSuperviseParity_Clear_RemovesFiles(t *testing.T) {
	cfg := newSuperviseCfg(t, "clear")
	cfg, repo := makeSuperviseProject(t, cfg, "proj")
	writeSuperviseYAML(t, repo, "supervisor:\n  enabled: true\n")

	wc := filepath.Join(cfg.AgentControlRoot, "proj", "work", "current")
	for _, name := range []string{"supervisor-buffer.ndjson", "supervisor-findings.ndjson", ".supervisor-counter"} {
		if err := os.WriteFile(filepath.Join(wc, name), []byte("data\n"), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	res, err := supervise.Run(cfg)
	if err != nil {
		t.Fatalf("Run clear: %v", err)
	}
	if res.ClearedCount != 3 {
		t.Errorf("expected ClearedCount=3; got %d", res.ClearedCount)
	}
	out := superviseOut(cfg)
	if !strings.Contains(out, "removed 3") {
		t.Errorf("expected 'removed 3'; got: %q", out)
	}
}

// ---- scenario (f): set known key --------------------------------------------

func TestSuperviseParity_Set_KnownKey(t *testing.T) {
	cfg := newSuperviseCfg(t, "set")
	cfg, repo := makeSuperviseProject(t, cfg, "proj")
	writeSuperviseYAML(t, repo, "supervisor:\n  enabled: true\n  block_on_critical: true\n")
	cfg.Key = "block_on_critical"
	cfg.Value = "false"

	_, err := supervise.Run(cfg)
	if err != nil {
		t.Fatalf("Run set: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(repo, ".yakos.yml"))
	if !strings.Contains(string(data), "block_on_critical: false") {
		t.Errorf("expected block_on_critical: false; got:\n%s", data)
	}
}

// ---- scenario (g): set unknown key ------------------------------------------

func TestSuperviseParity_Set_UnknownKey_Error(t *testing.T) {
	cfg := newSuperviseCfg(t, "set")
	cfg, repo := makeSuperviseProject(t, cfg, "proj")
	writeSuperviseYAML(t, repo, "supervisor:\n  enabled: true\n")
	cfg.Key = "bad_key"
	cfg.Value = "val"

	_, err := supervise.Run(cfg)
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
	if !strings.Contains(err.Error(), "unknown key") {
		t.Errorf("expected 'unknown key' error; got: %v", err)
	}
}

// ---- scenario (h): pending --------------------------------------------------

func TestSuperviseParity_Pending_WithEscalations(t *testing.T) {
	cfg := newSuperviseCfg(t, "pending")
	cfg, repo := makeSuperviseProject(t, cfg, "proj")
	writeSuperviseYAML(t, repo, "supervisor:\n  enabled: true\n")
	writeSuperviseFindings(t, cfg, "proj", []string{
		escalation("2026-06-01T12:00:00Z", "surface_to_operator", "needs attention"),
	})

	res, err := supervise.Run(cfg)
	if err != nil {
		t.Fatalf("Run pending: %v", err)
	}
	if res.PendingCount != 1 {
		t.Errorf("expected PendingCount=1; got %d", res.PendingCount)
	}
	out := superviseOut(cfg)
	if !strings.Contains(out, "surface_to_operator") {
		t.Errorf("expected action in output; got: %q", out)
	}
	if !strings.Contains(out, "needs attention") {
		t.Errorf("expected rationale in output; got: %q", out)
	}
	if !strings.Contains(out, "f-") {
		t.Errorf("expected finding ID; got: %q", out)
	}
}

// ---- scenario (i): ack ------------------------------------------------------

func TestSuperviseParity_Ack_HappyPath(t *testing.T) {
	cfg := newSuperviseCfg(t, "ack")
	cfg, repo := makeSuperviseProject(t, cfg, "proj")
	writeSuperviseYAML(t, repo, "supervisor:\n  enabled: true\n")
	ts := "2026-06-01T12:00:00Z"
	writeSuperviseFindings(t, cfg, "proj", []string{
		escalation(ts, "surface_to_operator", "something"),
	})

	// Derive the finding ID for line 1.
	fid := "f-" + strings.ReplaceAll(ts, ":", "-") + "-proj-1"
	cfg.FindingID = fid
	cfg.Note = "all clear"

	res, err := supervise.Run(cfg)
	if err != nil {
		t.Fatalf("Run ack: %v", err)
	}
	if res.AckedID != fid {
		t.Errorf("expected AckedID=%s; got %s", fid, res.AckedID)
	}

	ackFile := filepath.Join(cfg.HomeDir, ".yakos-state", "supervisor-acks.ndjson")
	data, _ := os.ReadFile(ackFile)
	if !strings.Contains(string(data), fid) {
		t.Errorf("expected finding ID in ack file; got: %s", data)
	}
	if !strings.Contains(string(data), "all clear") {
		t.Errorf("expected note in ack file; got: %s", data)
	}
}

func TestSuperviseParity_Ack_InvalidID_Error(t *testing.T) {
	cfg := newSuperviseCfg(t, "ack")
	cfg, repo := makeSuperviseProject(t, cfg, "proj")
	writeSuperviseYAML(t, repo, "supervisor:\n  enabled: true\n")
	writeSuperviseFindings(t, cfg, "proj", []string{
		escalation("2026-06-01T12:00:00Z", "surface_to_operator", "something"),
	})
	cfg.FindingID = "f-nonexistent"

	_, err := supervise.Run(cfg)
	if err == nil {
		t.Fatal("expected error for invalid finding ID")
	}
	if !strings.Contains(err.Error(), "not found in pending") {
		t.Errorf("expected 'not found in pending' error; got: %v", err)
	}
}

// ---- scenario (j): ack-all --------------------------------------------------

func TestSuperviseParity_AckAll_HappyPath(t *testing.T) {
	cfg := newSuperviseCfg(t, "ack-all")
	cfg, repo := makeSuperviseProject(t, cfg, "proj")
	writeSuperviseYAML(t, repo, "supervisor:\n  enabled: true\n")
	writeSuperviseFindings(t, cfg, "proj", []string{
		escalation("2026-06-01T12:00:00Z", "surface_to_operator", "first"),
		escalation("2026-06-01T12:00:01Z", "block_next_tool", "second"),
		`{"ts":"2026-06-01T12:00:02Z","recommended_action":"continue","rationale":"ok"}`,
	})
	cfg.Note = "operator review complete"

	res, err := supervise.Run(cfg)
	if err != nil {
		t.Fatalf("Run ack-all: %v", err)
	}
	if res.AckedCount != 2 {
		t.Errorf("expected AckedCount=2; got %d", res.AckedCount)
	}
	out := superviseOut(cfg)
	if !strings.Contains(out, "acknowledged 2 finding(s)") {
		t.Errorf("expected '2 finding(s)'; got: %q", out)
	}
}

// ---- scenario (k): unknown subcommand ---------------------------------------

func TestSuperviseParity_UnknownSubcommand_Error(t *testing.T) {
	cfg := newSuperviseCfg(t, "bogus")

	_, err := supervise.Run(cfg)
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("expected 'unknown subcommand'; got: %v", err)
	}
	if !strings.Contains(err.Error(), "yakos supervise help") {
		t.Errorf("expected help hint; got: %v", err)
	}
}

// ---- scenario (l): help text ------------------------------------------------

func TestSuperviseParity_HelpText_KeyPhrases(t *testing.T) {
	var buf bytes.Buffer
	supervise.PrintHelp(&buf)
	help := buf.String()

	phrases := []string{
		"yakos supervise",
		"enable",
		"disable",
		"status",
		"tail",
		"clear",
		"set",
		"pending",
		"ack",
		"ack-all",
		"block_on_critical",
		"YAKOS_SUPERVISOR_DISABLE",
		"supervisor-acks.ndjson",
		"finding_id",
	}
	for _, p := range phrases {
		if !strings.Contains(help, p) {
			t.Errorf("help text missing phrase %q", p)
		}
	}
}

// ---- scenario (m): portedCommands entry -------------------------------------

func TestSuperviseParity_PortedCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "supervise" {
			return
		}
	}
	t.Error("expected 'supervise' in portedCommands; not found")
}

// ---- scenario (n): deriveFindingID ------------------------------------------

func TestSuperviseParity_DeriveFindingID_CovidColons(t *testing.T) {
	// We test via the pending output — the finding ID must not contain colons.
	cfg := newSuperviseCfg(t, "pending")
	cfg, repo := makeSuperviseProject(t, cfg, "proj")
	writeSuperviseYAML(t, repo, "supervisor:\n  enabled: true\n")
	writeSuperviseFindings(t, cfg, "proj", []string{
		escalation("2026-06-01T12:00:00Z", "surface_to_operator", "x"),
	})

	_, err := supervise.Run(cfg)
	if err != nil {
		t.Fatalf("Run pending: %v", err)
	}
	out := superviseOut(cfg)
	// Extract finding ID from output — it must start with f- and have no colons in the ts part.
	idx := strings.Index(out, "[f-")
	if idx < 0 {
		t.Fatalf("expected finding ID in output; got: %q", out)
	}
	end := strings.Index(out[idx:], "]")
	if end < 0 {
		t.Fatalf("expected closing ] in output; got: %q", out)
	}
	fid := out[idx+1 : idx+end]
	// The timestamp part should not contain colons (they are replaced with hyphens).
	tsSection := strings.TrimPrefix(fid, "f-")
	// Everything up to the project separator.
	if strings.Contains(tsSection, ":") {
		t.Errorf("finding ID should not contain colons; got: %q", fid)
	}
}

// ---- scenario (o): AckRecord JSON round-trip --------------------------------

func TestSuperviseParity_AckRecord_JSON(t *testing.T) {
	rec := supervise.AckRecord{
		TS:        "2026-06-01T12:00:00Z",
		FindingID: "f-2026-06-01T12-00-00Z-proj-1",
		Project:   "proj",
		User:      "testoperator",
		Note:      "all good",
	}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got supervise.AckRecord
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.FindingID != rec.FindingID {
		t.Errorf("FindingID: got %q; want %q", got.FindingID, rec.FindingID)
	}
	if got.Note != rec.Note {
		t.Errorf("Note: got %q; want %q", got.Note, rec.Note)
	}
}

// ---- scenario (p): pending skips non-escalation actions --------------------

func TestSuperviseParity_Pending_SkipsNonEscalation(t *testing.T) {
	cfg := newSuperviseCfg(t, "pending")
	cfg, repo := makeSuperviseProject(t, cfg, "proj")
	writeSuperviseYAML(t, repo, "supervisor:\n  enabled: true\n")
	writeSuperviseFindings(t, cfg, "proj", []string{
		`{"ts":"2026-06-01T12:00:00Z","recommended_action":"continue","rationale":"fine"}`,
		`{"ts":"2026-06-01T12:00:01Z","recommended_action":"info","rationale":"info"}`,
		`{"ts":"2026-06-01T12:00:02Z","recommended_action":"warn","rationale":"warn"}`,
	})

	res, err := supervise.Run(cfg)
	if err != nil {
		t.Fatalf("Run pending: %v", err)
	}
	if res.PendingCount != 0 {
		t.Errorf("expected PendingCount=0 (all non-escalation); got %d", res.PendingCount)
	}
}

// ---- scenario (q): acked findings excluded from pending --------------------

func TestSuperviseParity_Pending_ExcludesAcked(t *testing.T) {
	cfg := newSuperviseCfg(t, "pending")
	cfg, repo := makeSuperviseProject(t, cfg, "proj")
	writeSuperviseYAML(t, repo, "supervisor:\n  enabled: true\n")
	writeSuperviseFindings(t, cfg, "proj", []string{
		escalation("2026-06-01T12:00:00Z", "surface_to_operator", "first"),
		escalation("2026-06-01T12:00:01Z", "halt", "second"),
	})

	// Pre-ack the first finding.
	fid1 := "f-" + strings.ReplaceAll("2026-06-01T12:00:00Z", ":", "-") + "-proj-1"
	ackFile := filepath.Join(cfg.HomeDir, ".yakos-state", "supervisor-acks.ndjson")
	if err := os.MkdirAll(filepath.Dir(ackFile), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	rec := supervise.AckRecord{
		TS:        "2026-06-01T13:00:00Z",
		FindingID: fid1,
		Project:   "proj",
		User:      "testoperator",
		Note:      "",
	}
	b, _ := json.Marshal(rec)
	if err := os.WriteFile(ackFile, append(b, '\n'), 0644); err != nil {
		t.Fatalf("write ack: %v", err)
	}

	res, err := supervise.Run(cfg)
	if err != nil {
		t.Fatalf("Run pending: %v", err)
	}
	if res.PendingCount != 1 {
		t.Errorf("expected PendingCount=1 (first acked); got %d", res.PendingCount)
	}
	out := superviseOut(cfg)
	if !strings.Contains(out, "second") {
		t.Errorf("expected second finding in output; got: %q", out)
	}
}

// ---- scenario (r): set no block → creates fresh block ----------------------

func TestSuperviseParity_Set_NoBlock_CreatesBlock(t *testing.T) {
	cfg := newSuperviseCfg(t, "set")
	cfg, repo := makeSuperviseProject(t, cfg, "proj")
	writeSuperviseYAML(t, repo, "name: proj\n")
	cfg.Key = "runtime"
	cfg.Value = "claude"

	_, err := supervise.Run(cfg)
	if err != nil {
		t.Fatalf("Run set (no block): %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(repo, ".yakos.yml"))
	if !strings.Contains(string(data), "supervisor:") {
		t.Errorf("expected supervisor block; got:\n%s", data)
	}
	if !strings.Contains(string(data), "runtime: claude") {
		t.Errorf("expected runtime: claude; got:\n%s", data)
	}
}

// ---- scenario (s): enable with missing .yakos.yml --------------------------

func TestSuperviseParity_Enable_MissingYAML_Error(t *testing.T) {
	cfg := newSuperviseCfg(t, "enable")
	cfg, _ = makeSuperviseProject(t, cfg, "proj")
	// No .yakos.yml written.

	_, err := supervise.Run(cfg)
	if err == nil {
		t.Fatal("expected error when .yakos.yml missing")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("expected 'missing' in error; got: %v", err)
	}
}
