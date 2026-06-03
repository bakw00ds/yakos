package supervise

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- test helpers -----------------------------------------------------------

func fixedNow() time.Time {
	t, _ := time.Parse(time.RFC3339, "2026-06-01T12:00:00Z")
	return t
}

func fixedUser() string { return "testuser" }

func newCfg(t *testing.T, sub string) Config {
	t.Helper()
	home := t.TempDir()
	var out, errOut bytes.Buffer
	return Config{
		Subcommand:       sub,
		HomeDir:          home,
		AgentControlRoot: filepath.Join(home, "agent-control"),
		NowFn:            fixedNow,
		UserFn:           fixedUser,
		Writer:           &out,
		ErrWriter:        &errOut,
	}
}

func cfgOut(cfg Config) string { return cfg.Writer.(*bytes.Buffer).String() }

// makeProject creates a minimal ~/agent-control/<slug>/ layout with a
// .project-path pointing at a temp project repo.
// Returns (cfg with Project set, projectRepo path).
func makeProject(t *testing.T, cfg Config, slug string) (Config, string) {
	t.Helper()
	projectRepo := t.TempDir()
	acSlug := filepath.Join(cfg.AgentControlRoot, slug)
	if err := os.MkdirAll(filepath.Join(acSlug, "work", "current"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	ppFile := filepath.Join(acSlug, ".project-path")
	if err := os.WriteFile(ppFile, []byte(projectRepo+"\n"), 0644); err != nil {
		t.Fatalf("write .project-path: %v", err)
	}
	cfg.Project = slug
	return cfg, projectRepo
}

// writeYAML writes a .yakos.yml into the project repo.
func writeYAML(t *testing.T, repo, content string) string {
	t.Helper()
	p := filepath.Join(repo, ".yakos.yml")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("write .yakos.yml: %v", err)
	}
	return p
}

// readYAML reads .yakos.yml from the project repo.
func readYAML(t *testing.T, repo string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repo, ".yakos.yml"))
	if err != nil {
		t.Fatalf("read .yakos.yml: %v", err)
	}
	return string(data)
}

// writeFindings writes an NDJSON findings file.
func writeFindings(t *testing.T, cfg Config, slug string, lines []string) string {
	t.Helper()
	p := filepath.Join(cfg.AgentControlRoot, slug, "work", "current", "supervisor-findings.ndjson")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatalf("write findings: %v", err)
	}
	return p
}

// ---- enable / disable -------------------------------------------------------

func TestEnable_NoYAML_AppendsBlock(t *testing.T) {
	cfg := newCfg(t, "enable")
	cfg, repo := makeProject(t, cfg, "proj")
	// Create empty .yakos.yml
	writeYAML(t, repo, "name: proj\n")

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run enable: %v", err)
	}

	content := readYAML(t, repo)
	if !strings.Contains(content, "supervisor:") {
		t.Errorf("expected supervisor: block; got:\n%s", content)
	}
	if !strings.Contains(content, "enabled: true") {
		t.Errorf("expected enabled: true; got:\n%s", content)
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "Supervisor enabled") {
		t.Errorf("expected 'Supervisor enabled' in output; got: %q", out)
	}
}

func TestEnable_ExistingBlock_TogglesEnabled(t *testing.T) {
	cfg := newCfg(t, "enable")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "name: proj\nsupervisor:\n  enabled: false\n  runtime: claude\n")

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run enable: %v", err)
	}

	content := readYAML(t, repo)
	if !strings.Contains(content, "enabled: true") {
		t.Errorf("expected enabled: true; got:\n%s", content)
	}
	if strings.Contains(content, "enabled: false") {
		t.Errorf("expected no 'enabled: false'; got:\n%s", content)
	}
}

func TestEnable_AlreadyEnabled_NoOp(t *testing.T) {
	cfg := newCfg(t, "enable")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "supervisor:\n  enabled: true\n")

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run enable (already enabled): %v", err)
	}
	// File should be unchanged.
	content := readYAML(t, repo)
	if !strings.Contains(content, "enabled: true") {
		t.Errorf("expected enabled: true unchanged; got:\n%s", content)
	}
}

func TestDisable_TogglesEnabled(t *testing.T) {
	cfg := newCfg(t, "disable")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "supervisor:\n  enabled: true\n")

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run disable: %v", err)
	}
	if res.Enabled == nil || *res.Enabled {
		t.Error("expected Enabled=false in result")
	}

	content := readYAML(t, repo)
	if !strings.Contains(content, "enabled: false") {
		t.Errorf("expected enabled: false; got:\n%s", content)
	}
}

func TestDisable_NoYAML_Error(t *testing.T) {
	cfg := newCfg(t, "disable")
	cfg, _ = makeProject(t, cfg, "proj")
	// No .yakos.yml written.

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error when .yakos.yml missing")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("expected 'missing' in error; got: %v", err)
	}
}

// ---- status -----------------------------------------------------------------

func TestStatus_MissingYAML(t *testing.T) {
	cfg := newCfg(t, "status")
	cfg, _ = makeProject(t, cfg, "proj")
	// No .yakos.yml.

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run status: %v", err)
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "missing") {
		t.Errorf("expected 'missing' in output; got: %q", out)
	}
}

func TestStatus_SupervisorEnabled(t *testing.T) {
	cfg := newCfg(t, "status")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "supervisor:\n  enabled: true\n  runtime: claude\n  block_on_critical: true\n")

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run status: %v", err)
	}
	if res.Enabled == nil || !*res.Enabled {
		t.Error("expected Enabled=true in result")
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "YES") {
		t.Errorf("expected 'YES' for enabled; got: %q", out)
	}
	if !strings.Contains(out, "block_on_critical") {
		t.Errorf("expected block_on_critical in output; got: %q", out)
	}
}

func TestStatus_SupervisorDisabled(t *testing.T) {
	cfg := newCfg(t, "status")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "supervisor:\n  enabled: false\n")

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run status: %v", err)
	}
	if res.Enabled == nil || *res.Enabled {
		t.Error("expected Enabled=false in result")
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "no") {
		t.Errorf("expected 'no' for disabled state; got: %q", out)
	}
}

func TestStatus_WithBufferAndFindings(t *testing.T) {
	cfg := newCfg(t, "status")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "supervisor:\n  enabled: true\n")

	wc := filepath.Join(cfg.AgentControlRoot, "proj", "work", "current")
	// Write buffer.
	bufFile := filepath.Join(wc, "supervisor-buffer.ndjson")
	if err := os.WriteFile(bufFile, []byte("{\"ts\":\"2026-06-01T12:00:00Z\"}\n"), 0644); err != nil {
		t.Fatalf("write buffer: %v", err)
	}
	// Write counter.
	counterFile := filepath.Join(wc, ".supervisor-counter")
	if err := os.WriteFile(counterFile, []byte("7\n"), 0644); err != nil {
		t.Fatalf("write counter: %v", err)
	}
	// Write findings.
	findFile := filepath.Join(wc, "supervisor-findings.ndjson")
	if err := os.WriteFile(findFile, []byte("{\"ts\":\"2026-06-01T12:00:00Z\",\"recommended_action\":\"continue\"}\n"), 0644); err != nil {
		t.Fatalf("write findings: %v", err)
	}

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run status with files: %v", err)
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "entries:") {
		t.Errorf("expected buffer entries line; got: %q", out)
	}
	if !strings.Contains(out, "7") {
		t.Errorf("expected counter value 7; got: %q", out)
	}
}

// ---- clear ------------------------------------------------------------------

func TestClear_RemovesFiles(t *testing.T) {
	cfg := newCfg(t, "clear")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "supervisor:\n  enabled: true\n")

	wc := filepath.Join(cfg.AgentControlRoot, "proj", "work", "current")
	for _, name := range []string{
		"supervisor-buffer.ndjson",
		"supervisor-findings.ndjson",
		".supervisor-counter",
	} {
		if err := os.WriteFile(filepath.Join(wc, name), []byte("data\n"), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run clear: %v", err)
	}
	if res.ClearedCount != 3 {
		t.Errorf("expected ClearedCount=3; got %d", res.ClearedCount)
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "removed 3") {
		t.Errorf("expected 'removed 3' in output; got: %q", out)
	}
	// Verify files are gone.
	for _, name := range []string{"supervisor-buffer.ndjson", "supervisor-findings.ndjson", ".supervisor-counter"} {
		if _, err := os.Stat(filepath.Join(wc, name)); !os.IsNotExist(err) {
			t.Errorf("expected %s to be removed", name)
		}
	}
}

func TestClear_NoFiles_ZeroCount(t *testing.T) {
	cfg := newCfg(t, "clear")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "supervisor:\n  enabled: true\n")

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run clear: %v", err)
	}
	if res.ClearedCount != 0 {
		t.Errorf("expected ClearedCount=0; got %d", res.ClearedCount)
	}
}

// ---- set --------------------------------------------------------------------

func TestSet_KnownKey_WritesValue(t *testing.T) {
	cfg := newCfg(t, "set")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "supervisor:\n  enabled: true\n  block_on_critical: true\n")
	cfg.Key = "block_on_critical"
	cfg.Value = "false"

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run set: %v", err)
	}
	content := readYAML(t, repo)
	if !strings.Contains(content, "block_on_critical: false") {
		t.Errorf("expected block_on_critical: false; got:\n%s", content)
	}
}

func TestSet_NewKey_AppendedToBlock(t *testing.T) {
	cfg := newCfg(t, "set")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "supervisor:\n  enabled: true\n")
	cfg.Key = "score_every_n_calls"
	cfg.Value = "5"

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run set: %v", err)
	}
	content := readYAML(t, repo)
	if !strings.Contains(content, "score_every_n_calls: 5") {
		t.Errorf("expected score_every_n_calls: 5; got:\n%s", content)
	}
}

func TestSet_NoBlock_CreatesBlock(t *testing.T) {
	cfg := newCfg(t, "set")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "name: proj\n")
	cfg.Key = "runtime"
	cfg.Value = "claude"

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run set: %v", err)
	}
	content := readYAML(t, repo)
	if !strings.Contains(content, "supervisor:") {
		t.Errorf("expected supervisor: block; got:\n%s", content)
	}
	if !strings.Contains(content, "runtime: claude") {
		t.Errorf("expected runtime: claude; got:\n%s", content)
	}
}

func TestSet_UnknownKey_Error(t *testing.T) {
	cfg := newCfg(t, "set")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "supervisor:\n  enabled: true\n")
	cfg.Key = "nonexistent_key"
	cfg.Value = "val"

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
	if !strings.Contains(err.Error(), "unknown key") {
		t.Errorf("expected 'unknown key' error; got: %v", err)
	}
}

func TestSet_InvalidBoolValue_Error(t *testing.T) {
	cfg := newCfg(t, "set")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "supervisor:\n  enabled: true\n")
	cfg.Key = "block_on_critical"
	cfg.Value = "maybe"

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for invalid bool value")
	}
	if !strings.Contains(err.Error(), "true' or 'false'") {
		t.Errorf("expected validation message; got: %v", err)
	}
}

func TestSet_InvalidModelValue_Error(t *testing.T) {
	cfg := newCfg(t, "set")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "supervisor:\n  enabled: true\n")
	cfg.Key = "model"
	cfg.Value = "gpt-4"

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for invalid model")
	}
	if !strings.Contains(err.Error(), "haiku, sonnet, or opus") {
		t.Errorf("expected model validation message; got: %v", err)
	}
}

func TestSet_InvalidScoreEveryN_Error(t *testing.T) {
	cfg := newCfg(t, "set")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "supervisor:\n  enabled: true\n")
	cfg.Key = "score_every_n_calls"
	cfg.Value = "abc"

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for non-integer score_every_n_calls")
	}
}

func TestSet_AlreadyAtValue_NoOp(t *testing.T) {
	cfg := newCfg(t, "set")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "supervisor:\n  enabled: true\n  runtime: claude\n")
	cfg.Key = "runtime"
	cfg.Value = "claude"

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run set (no-op): %v", err)
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "no change") {
		t.Errorf("expected 'no change' in output; got: %q", out)
	}
}

func TestSet_MissingYAML_Error(t *testing.T) {
	cfg := newCfg(t, "set")
	cfg, _ = makeProject(t, cfg, "proj")
	cfg.Key = "runtime"
	cfg.Value = "claude"

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error when .yakos.yml missing")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("expected 'missing' in error; got: %v", err)
	}
}

func TestSet_ValidModels(t *testing.T) {
	for _, model := range []string{"haiku", "sonnet", "opus"} {
		t.Run(model, func(t *testing.T) {
			cfg := newCfg(t, "set")
			cfg, repo := makeProject(t, cfg, "proj")
			writeYAML(t, repo, "supervisor:\n  enabled: true\n")
			cfg.Key = "model"
			cfg.Value = model

			_, err := Run(cfg)
			if err != nil {
				t.Fatalf("Run set model=%s: %v", model, err)
			}
			content := readYAML(t, repo)
			if !strings.Contains(content, "model: "+model) {
				t.Errorf("expected model: %s; got:\n%s", model, content)
			}
		})
	}
}

// ---- tail -------------------------------------------------------------------

func TestTail_NoFindings(t *testing.T) {
	cfg := newCfg(t, "tail")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "supervisor:\n  enabled: true\n")

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run tail: %v", err)
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "no findings yet") {
		t.Errorf("expected 'no findings yet'; got: %q", out)
	}
}

func TestTail_ReturnsLastN(t *testing.T) {
	cfg := newCfg(t, "tail")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "supervisor:\n  enabled: true\n")
	cfg.TailN = 2

	var lines []string
	for i := 1; i <= 5; i++ {
		lines = append(lines, `{"ts":"2026-06-01T12:00:00Z","n":`+string(rune('0'+i))+`}`)
	}
	writeFindings(t, cfg, "proj", lines)

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run tail: %v", err)
	}
	out := cfgOut(cfg)
	// Should contain last 2 entries.
	if !strings.Contains(out, `"n":4`) && !strings.Contains(out, `"n":5`) {
		t.Errorf("expected last entries in output; got: %q", out)
	}
	// Should NOT contain early entries.
	if strings.Contains(out, `"n":1`) {
		t.Errorf("should not contain early entries; got: %q", out)
	}
}

// ---- pending ----------------------------------------------------------------

func escalationFinding(ts, action, rationale string) string {
	b, _ := json.Marshal(map[string]string{
		"ts":                 ts,
		"recommended_action": action,
		"rationale":          rationale,
	})
	return string(b)
}

func TestPending_NoFindings(t *testing.T) {
	cfg := newCfg(t, "pending")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "supervisor:\n  enabled: true\n")

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run pending: %v", err)
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "no findings file") {
		t.Errorf("expected 'no findings file'; got: %q", out)
	}
}

func TestPending_AllContinue_NoPending(t *testing.T) {
	cfg := newCfg(t, "pending")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "supervisor:\n  enabled: true\n")
	writeFindings(t, cfg, "proj", []string{
		`{"ts":"2026-06-01T12:00:00Z","recommended_action":"continue","rationale":"all good"}`,
	})

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run pending: %v", err)
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "No unacknowledged escalation findings") {
		t.Errorf("expected 'No unacknowledged escalation findings'; got: %q", out)
	}
}

func TestPending_WithEscalation(t *testing.T) {
	cfg := newCfg(t, "pending")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "supervisor:\n  enabled: true\n")
	writeFindings(t, cfg, "proj", []string{
		escalationFinding("2026-06-01T12:00:00Z", "surface_to_operator", "test rationale"),
	})

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run pending: %v", err)
	}
	if res.PendingCount != 1 {
		t.Errorf("expected PendingCount=1; got %d", res.PendingCount)
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "surface_to_operator") {
		t.Errorf("expected action in output; got: %q", out)
	}
	if !strings.Contains(out, "test rationale") {
		t.Errorf("expected rationale in output; got: %q", out)
	}
	if !strings.Contains(out, "f-") {
		t.Errorf("expected finding ID prefix 'f-' in output; got: %q", out)
	}
	if !strings.Contains(out, "yakos supervise ack") {
		t.Errorf("expected ack hint in output; got: %q", out)
	}
}

func TestPending_BlockNextTool_IsEscalation(t *testing.T) {
	cfg := newCfg(t, "pending")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "supervisor:\n  enabled: true\n")
	writeFindings(t, cfg, "proj", []string{
		escalationFinding("2026-06-01T12:00:00Z", "block_next_tool", "rationale"),
	})

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run pending: %v", err)
	}
	if res.PendingCount != 1 {
		t.Errorf("expected PendingCount=1 for block_next_tool; got %d", res.PendingCount)
	}
}

func TestPending_Halt_IsEscalation(t *testing.T) {
	cfg := newCfg(t, "pending")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "supervisor:\n  enabled: true\n")
	writeFindings(t, cfg, "proj", []string{
		escalationFinding("2026-06-01T12:00:00Z", "halt", "very bad"),
	})

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run pending: %v", err)
	}
	if res.PendingCount != 1 {
		t.Errorf("expected PendingCount=1 for halt; got %d", res.PendingCount)
	}
}

// ---- ack -------------------------------------------------------------------

func TestAck_HappyPath(t *testing.T) {
	cfg := newCfg(t, "ack")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "supervisor:\n  enabled: true\n")
	writeFindings(t, cfg, "proj", []string{
		escalationFinding("2026-06-01T12:00:00Z", "surface_to_operator", "something"),
	})

	// Derive the finding ID for line 1.
	fid := deriveFindingID("2026-06-01T12:00:00Z", "proj", 1)
	cfg.FindingID = fid
	cfg.Note = "acknowledged by test"

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run ack: %v", err)
	}
	if res.AckedID != fid {
		t.Errorf("expected AckedID=%s; got %s", fid, res.AckedID)
	}
	if res.AckedCount != 1 {
		t.Errorf("expected AckedCount=1; got %d", res.AckedCount)
	}

	// Verify ack record was written.
	ackFile := ackFilePath(cfg.HomeDir)
	data, err := os.ReadFile(ackFile)
	if err != nil {
		t.Fatalf("read ack file: %v", err)
	}
	if !strings.Contains(string(data), fid) {
		t.Errorf("expected finding ID in ack file; got: %s", string(data))
	}
	if !strings.Contains(string(data), "acknowledged by test") {
		t.Errorf("expected note in ack file; got: %s", string(data))
	}

	out := cfgOut(cfg)
	if !strings.Contains(out, "Acknowledged:") {
		t.Errorf("expected 'Acknowledged:' in output; got: %q", out)
	}
}

func TestAck_MissingFindingID_Error(t *testing.T) {
	cfg := newCfg(t, "ack")
	// FindingID not set.

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error when FindingID is empty")
	}
	if !strings.Contains(err.Error(), "finding-id") {
		t.Errorf("expected 'finding-id' in error; got: %v", err)
	}
}

func TestAck_NotInPending_Error(t *testing.T) {
	cfg := newCfg(t, "ack")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "supervisor:\n  enabled: true\n")
	writeFindings(t, cfg, "proj", []string{
		escalationFinding("2026-06-01T12:00:00Z", "surface_to_operator", "something"),
	})
	cfg.FindingID = "f-invalid-finding-id"

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for non-existent finding ID")
	}
	if !strings.Contains(err.Error(), "not found in pending") {
		t.Errorf("expected 'not found in pending' error; got: %v", err)
	}
}

func TestAck_AlreadyAcked_ReportsGracefully(t *testing.T) {
	cfg := newCfg(t, "ack")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "supervisor:\n  enabled: true\n")
	writeFindings(t, cfg, "proj", []string{
		escalationFinding("2026-06-01T12:00:00Z", "surface_to_operator", "something"),
	})

	fid := deriveFindingID("2026-06-01T12:00:00Z", "proj", 1)
	cfg.FindingID = fid

	// First ack.
	if _, err := Run(cfg); err != nil {
		t.Fatalf("first ack: %v", err)
	}

	// Reset writer for second ack attempt.
	cfg.Writer = &bytes.Buffer{}
	cfg.ErrWriter = &bytes.Buffer{}
	// Second ack — pending is now empty.
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("second ack (already acked): %v", err)
	}
	// No pending findings — returns graceful "nothing to acknowledge".
	out := cfgOut(cfg)
	_ = res
	_ = out
	// The test just verifies no error is returned when already acked.
}

func TestAck_NoFindingsFile_Error(t *testing.T) {
	cfg := newCfg(t, "ack")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "supervisor:\n  enabled: true\n")
	cfg.FindingID = "f-somevalue"

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error when no findings file")
	}
}

// ---- ack-all ----------------------------------------------------------------

func TestAckAll_HappyPath(t *testing.T) {
	cfg := newCfg(t, "ack-all")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "supervisor:\n  enabled: true\n")
	writeFindings(t, cfg, "proj", []string{
		escalationFinding("2026-06-01T12:00:00Z", "surface_to_operator", "first"),
		escalationFinding("2026-06-01T12:00:01Z", "block_next_tool", "second"),
		`{"ts":"2026-06-01T12:00:02Z","recommended_action":"continue","rationale":"ok"}`,
	})
	cfg.Note = "bulk ack"

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run ack-all: %v", err)
	}
	if res.AckedCount != 2 {
		t.Errorf("expected AckedCount=2; got %d", res.AckedCount)
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "acknowledged 2 finding(s)") {
		t.Errorf("expected '2 finding(s)' in output; got: %q", out)
	}
	if !strings.Contains(out, "bulk ack") {
		t.Errorf("expected note in output; got: %q", out)
	}
}

func TestAckAll_NoFindings(t *testing.T) {
	cfg := newCfg(t, "ack-all")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "supervisor:\n  enabled: true\n")

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run ack-all: %v", err)
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "no findings file") {
		t.Errorf("expected 'no findings file'; got: %q", out)
	}
}

func TestAckAll_NoPending(t *testing.T) {
	cfg := newCfg(t, "ack-all")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "supervisor:\n  enabled: true\n")
	writeFindings(t, cfg, "proj", []string{
		`{"ts":"2026-06-01T12:00:00Z","recommended_action":"continue","rationale":"ok"}`,
	})

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run ack-all: %v", err)
	}
	if res.AckedCount != 0 {
		t.Errorf("expected AckedCount=0; got %d", res.AckedCount)
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "no pending escalation findings") {
		t.Errorf("expected 'no pending escalation findings'; got: %q", out)
	}
}

// ---- unknown subcommand -----------------------------------------------------

func TestUnknownSubcommand_Error(t *testing.T) {
	cfg := newCfg(t, "bogus")

	_, err := Run(cfg)
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

// ---- help text --------------------------------------------------------------

func TestHelp_KeyPhrases(t *testing.T) {
	var buf bytes.Buffer
	PrintHelp(&buf)
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
		"supervisor-acks.ndjson",
		"YAKOS_SUPERVISOR_DISABLE",
		"finding_id",
	}
	for _, p := range phrases {
		if !strings.Contains(help, p) {
			t.Errorf("help text missing phrase %q", p)
		}
	}
}

// ---- deriveFindingID --------------------------------------------------------

func TestDeriveFindingID_Format(t *testing.T) {
	fid := deriveFindingID("2026-06-01T12:00:00Z", "myproject", 42)
	// Colons replaced, format: f-<ts-safe>-<project>-<linenum>
	if !strings.HasPrefix(fid, "f-") {
		t.Errorf("expected 'f-' prefix; got: %s", fid)
	}
	if !strings.Contains(fid, "myproject") {
		t.Errorf("expected project in ID; got: %s", fid)
	}
	if !strings.Contains(fid, "42") {
		t.Errorf("expected linenum 42 in ID; got: %s", fid)
	}
	if strings.Contains(fid, ":") {
		t.Errorf("colons must be replaced; got: %s", fid)
	}
}

// ---- YAML helpers -----------------------------------------------------------

func TestHasSupervisorBlock(t *testing.T) {
	if !hasSupervisorBlock("supervisor:\n  enabled: true\n") {
		t.Error("expected hasSupervisorBlock=true")
	}
	if hasSupervisorBlock("name: proj\n") {
		t.Error("expected hasSupervisorBlock=false")
	}
}

func TestIsSupervisorEnabledAs(t *testing.T) {
	yes := "supervisor:\n  enabled: true\n"
	no := "supervisor:\n  enabled: false\n"
	if !isSupervisorEnabledAs(yes, "true") {
		t.Error("expected true for enabled: true content")
	}
	if isSupervisorEnabledAs(yes, "false") {
		t.Error("expected false for enabled: true content checked against 'false'")
	}
	if !isSupervisorEnabledAs(no, "false") {
		t.Error("expected true for enabled: false content")
	}
}

func TestExtractSupervisorKey(t *testing.T) {
	content := "supervisor:\n  enabled: true\n  runtime: claude\n  block_on_critical: false\n"
	if v := extractSupervisorKey(content, "runtime"); v != "claude" {
		t.Errorf("expected 'claude'; got %q", v)
	}
	if v := extractSupervisorKey(content, "block_on_critical"); v != "false" {
		t.Errorf("expected 'false'; got %q", v)
	}
	if v := extractSupervisorKey(content, "nonexistent"); v != "" {
		t.Errorf("expected '' for nonexistent key; got %q", v)
	}
}

func TestReplaceEnabledLine(t *testing.T) {
	content := "supervisor:\n  enabled: false\n  runtime: claude\n"
	result := replaceEnabledLine(content, "true")
	if !strings.Contains(result, "enabled: true") {
		t.Errorf("expected enabled: true; got:\n%s", result)
	}
	if strings.Contains(result, "enabled: false") {
		t.Errorf("expected no 'enabled: false'; got:\n%s", result)
	}
}

func TestSetSupervisorKey_Replaces(t *testing.T) {
	content := "supervisor:\n  enabled: true\n  runtime: claude\n"
	result := setSupervisorKey(content, "runtime", "codex")
	if !strings.Contains(result, "runtime: codex") {
		t.Errorf("expected runtime: codex; got:\n%s", result)
	}
}

func TestSetSupervisorKey_Inserts(t *testing.T) {
	content := "supervisor:\n  enabled: true\n"
	result := setSupervisorKey(content, "model", "haiku")
	if !strings.Contains(result, "model: haiku") {
		t.Errorf("expected model: haiku; got:\n%s", result)
	}
}

// ---- pending with existing acks ---------------------------------------------

func TestPending_AckedFindingExcluded(t *testing.T) {
	cfg := newCfg(t, "pending")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "supervisor:\n  enabled: true\n")
	writeFindings(t, cfg, "proj", []string{
		escalationFinding("2026-06-01T12:00:00Z", "surface_to_operator", "first"),
		escalationFinding("2026-06-01T12:00:01Z", "surface_to_operator", "second"),
	})

	// Ack the first finding.
	fid1 := deriveFindingID("2026-06-01T12:00:00Z", "proj", 1)
	ackFile := ackFilePath(cfg.HomeDir)
	rec := AckRecord{
		TS:        "2026-06-01T13:00:00Z",
		FindingID: fid1,
		Project:   "proj",
		User:      "testuser",
		Note:      "",
	}
	b, _ := json.Marshal(rec)
	if err := os.MkdirAll(filepath.Dir(ackFile), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(ackFile, append(b, '\n'), 0644); err != nil {
		t.Fatalf("write ack: %v", err)
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run pending: %v", err)
	}
	if res.PendingCount != 1 {
		t.Errorf("expected PendingCount=1 (first acked); got %d", res.PendingCount)
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "second") {
		t.Errorf("expected second finding in output; got: %q", out)
	}
}

// ---- project resolution error -----------------------------------------------

func TestMissingProjectPath_Error(t *testing.T) {
	cfg := newCfg(t, "status")
	// Project set but no .project-path file.
	cfg.Project = "nonexistent"

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for missing project")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("expected 'missing' in error; got: %v", err)
	}
}

func TestNoProject_NoInfer_Error(t *testing.T) {
	cfg := newCfg(t, "status")
	// No project set, empty agent-control dir, cwd won't match.

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error when project cannot be inferred")
	}
	if !strings.Contains(err.Error(), "infer project") {
		t.Errorf("expected 'infer project' in error; got: %v", err)
	}
}

// ---- atomic write -----------------------------------------------------------

func TestAtomicWriteFile_WritesAndRenames(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	if err := atomicWriteFile(path, []byte("hello\n"), 0644); err != nil {
		t.Fatalf("atomicWriteFile: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "hello\n" {
		t.Errorf("expected 'hello\\n'; got %q", data)
	}

	// No temp file should remain.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".yakos-supervise-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

// ---- appendAck round-trip ---------------------------------------------------

func TestAppendAck_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	ackFile := filepath.Join(dir, ".yakos-state", "supervisor-acks.ndjson")

	rec := AckRecord{
		TS:        "2026-06-01T12:00:00Z",
		FindingID: "f-2026-06-01T12-00-00Z-proj-1",
		Project:   "proj",
		User:      "testuser",
		Note:      "test note",
	}
	if err := appendAck(ackFile, rec); err != nil {
		t.Fatalf("appendAck: %v", err)
	}

	data, err := os.ReadFile(ackFile)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var got AckRecord
	if err := json.Unmarshal(bytes.TrimRight(data, "\n"), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.FindingID != rec.FindingID {
		t.Errorf("FindingID: got %q; want %q", got.FindingID, rec.FindingID)
	}
	if got.Note != rec.Note {
		t.Errorf("Note: got %q; want %q", got.Note, rec.Note)
	}
}
