// skill_parity_test.go — Phase 1 parity tests for `yakos skill`.
//
// Design notes:
//
//  1. All tests call skill.Run directly with temp-dir paths; no bash runtime
//     or real ~/.yakos-state directory is required.
//
//  2. The bash skill.sh manages skill candidates: list (candidates), promote
//     (candidate → SKILL.md), reject (graveyard), defer (N cycles), stats.
//
//  3. Parity is verified behaviourally (output shape, filesystem effects, error
//     conditions) rather than byte-for-byte.
//
// Critical scenarios:
//
//	(a) candidates: missing file → advisory
//	(b) candidates: parses slug, confidence, evidence count
//	(c) promote: slug not in file → error
//	(d) promote: successful → SKILL.md exists with correct header
//	(e) promote: strips promoted candidate from file
//	(f) promote: repeat-rejection warning shown (≥3 graveyard hits)
//	(g) reject: slug not in file → error
//	(h) reject: appends to graveyard NDJSON
//	(i) reject: strips candidate from file
//	(j) defer: writes skill-deferrals.ndjson with correct cycle fields
//	(k) stats: no log → advisory
//	(l) stats: tallies proposed/promoted/rejected/deferred
//	(m) unknown subcommand → error with hint
//	(n) help text contains key phrases
//	(o) skill entry in portedCommands
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/skill"
)

// ---- helpers ----------------------------------------------------------------

func newSkillConfig(t *testing.T) skill.Config {
	t.Helper()
	home := t.TempDir()
	curDir := filepath.Join(home, "agent-control", "proj", "work", "current")
	if err := os.MkdirAll(curDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return skill.Config{
		HomeDir:    home,
		ProjectDir: curDir,
		Writer:     &bytes.Buffer{},
		ErrWriter:  &bytes.Buffer{},
	}
}

func skillOut(cfg skill.Config) string    { return cfg.Writer.(*bytes.Buffer).String() }
func skillErrOut(cfg skill.Config) string { return cfg.ErrWriter.(*bytes.Buffer).String() }

func writeSkillCandidates(t *testing.T, curDir string) string {
	t.Helper()
	path := filepath.Join(curDir, "skill-candidates.md")
	content := `# Skill candidates

## candidate: parallel-dispatch (2026-06-01)

**Confidence**: high
**Source evidence** (Cycle references):
- Cycle 10: lead dispatched three agents simultaneously
- Cycle 15: another parallel pattern observed

## candidate: audit-log-every-mutation (2026-06-02)

**Confidence**: medium
**Source evidence** (Cycle references):
- Cycle 20: mutation without audit log observed
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write candidates: %v", err)
	}
	return path
}

// ---- scenario (a): candidates: missing file ---------------------------------

func TestSkillParity_Candidates_MissingFile(t *testing.T) {
	cfg := newSkillConfig(t)
	cfg.Subcommand = "candidates"
	if _, err := skill.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(skillOut(cfg), "no pending candidates") {
		t.Errorf("expected advisory; got: %q", skillOut(cfg))
	}
}

// ---- scenario (b): candidates parses entries --------------------------------

func TestSkillParity_Candidates_ParsesEntries(t *testing.T) {
	cfg := newSkillConfig(t)
	writeSkillCandidates(t, cfg.ProjectDir)
	cfg.Subcommand = "candidates"
	res, err := skill.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Candidates) != 2 {
		t.Fatalf("expected 2 candidates; got %d", len(res.Candidates))
	}
	o := skillOut(cfg)
	if !strings.Contains(o, "parallel-dispatch") {
		t.Errorf("expected 'parallel-dispatch' in output; got: %q", o)
	}
	if !strings.Contains(o, "conf=high") {
		t.Errorf("expected 'conf=high' in output; got: %q", o)
	}
	if !strings.Contains(o, "evidence=2") {
		t.Errorf("expected 'evidence=2' in output; got: %q", o)
	}
}

// ---- scenario (c): promote: slug not found ----------------------------------

func TestSkillParity_Promote_SlugNotFound(t *testing.T) {
	cfg := newSkillConfig(t)
	writeSkillCandidates(t, cfg.ProjectDir)
	cfg.Subcommand = "promote"
	cfg.Slug = "nonexistent-slug"
	cfg.ProjectPath = t.TempDir()

	_, err := skill.Run(cfg)
	if err == nil {
		t.Fatal("expected error for slug not found")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (d): promote: SKILL.md written with correct header ------------

func TestSkillParity_Promote_WritesSkillMD(t *testing.T) {
	cfg := newSkillConfig(t)
	writeSkillCandidates(t, cfg.ProjectDir)
	projectDir := t.TempDir()
	cfg.Subcommand = "promote"
	cfg.Slug = "parallel-dispatch"
	cfg.ProjectPath = projectDir

	res, err := skill.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.PromotedSlug != "parallel-dispatch" {
		t.Errorf("expected PromotedSlug='parallel-dispatch'; got %q", res.PromotedSlug)
	}

	skillFile := filepath.Join(res.SkillDir, "SKILL.md")
	data, readErr := os.ReadFile(skillFile)
	if readErr != nil {
		t.Fatalf("read SKILL.md: %v", readErr)
	}
	content := string(data)
	for _, phrase := range []string{"name: parallel-dispatch", "allowed-tools:", "# parallel-dispatch"} {
		if !strings.Contains(content, phrase) {
			t.Errorf("SKILL.md missing %q; got:\n%s", phrase, content)
		}
	}
}

// ---- scenario (e): promote: strips candidate from file ----------------------

func TestSkillParity_Promote_StripsCandidateFromFile(t *testing.T) {
	cfg := newSkillConfig(t)
	candidateFile := writeSkillCandidates(t, cfg.ProjectDir)
	projectDir := t.TempDir()
	cfg.Subcommand = "promote"
	cfg.Slug = "parallel-dispatch"
	cfg.ProjectPath = projectDir

	if _, err := skill.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(candidateFile)
	if err != nil {
		t.Fatalf("read candidates file: %v", err)
	}
	if strings.Contains(string(data), "parallel-dispatch") {
		t.Errorf("expected parallel-dispatch stripped; still present")
	}
	if !strings.Contains(string(data), "audit-log-every-mutation") {
		t.Errorf("expected audit-log-every-mutation still present; missing")
	}
}

// ---- scenario (f): promote: repeat-rejection warning shown ------------------

func TestSkillParity_Promote_RepeatRejectionWarning(t *testing.T) {
	cfg := newSkillConfig(t)
	writeSkillCandidates(t, cfg.ProjectDir)
	projectDir := t.TempDir()
	cfg.Subcommand = "promote"
	cfg.Slug = "parallel-dispatch"
	cfg.ProjectPath = projectDir

	// Seed graveyard with 3 entries for this slug.
	graveyardPath := filepath.Join(cfg.HomeDir, ".yakos-state", "skill-graveyard.ndjson")
	if err := os.MkdirAll(filepath.Dir(graveyardPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for i := 0; i < 3; i++ {
		entry := map[string]string{
			"ts": "2026-01-01T00:00:00Z", "slug": "parallel-dispatch",
			"reason": "test", "by_user": "u", "evidence_fingerprint": "aaa",
		}
		b, _ := json.Marshal(entry)
		f, ferr := os.OpenFile(graveyardPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644) //nolint:gosec
		if ferr != nil {
			t.Fatalf("open graveyard: %v", ferr)
		}
		if _, err := fmt.Fprintf(f, "%s\n", b); err != nil {
			_ = f.Close()
			t.Fatalf("write graveyard: %v", err)
		}
		_ = f.Close()
	}

	// Abort on warning.
	cfg.PromptFn = func(string) (bool, error) { return false, nil }
	if _, err := skill.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(skillErrOut(cfg), "WARN") {
		t.Errorf("expected WARN in stderr; got: %q", skillErrOut(cfg))
	}
}

// ---- scenario (g): reject: slug not found -----------------------------------

func TestSkillParity_Reject_SlugNotFound(t *testing.T) {
	cfg := newSkillConfig(t)
	writeSkillCandidates(t, cfg.ProjectDir)
	cfg.Subcommand = "reject"
	cfg.Slug = "no-such-slug"

	_, err := skill.Run(cfg)
	if err == nil {
		t.Fatal("expected error for slug not found")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (h): reject: appends to graveyard NDJSON ----------------------

func TestSkillParity_Reject_AppendGraveyard(t *testing.T) {
	cfg := newSkillConfig(t)
	writeSkillCandidates(t, cfg.ProjectDir)
	cfg.Subcommand = "reject"
	cfg.Slug = "parallel-dispatch"
	cfg.Reason = "parity test reason"

	if _, err := skill.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	graveyardPath := filepath.Join(cfg.HomeDir, ".yakos-state", "skill-graveyard.ndjson")
	data, err := os.ReadFile(graveyardPath)
	if err != nil {
		t.Fatalf("read graveyard: %v", err)
	}

	var rec map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &rec); err != nil {
		t.Fatalf("parse graveyard: %v", err)
	}
	if rec["slug"] != "parallel-dispatch" {
		t.Errorf("expected slug='parallel-dispatch'; got %q", rec["slug"])
	}
	if rec["reason"] != "parity test reason" {
		t.Errorf("expected reason='parity test reason'; got %q", rec["reason"])
	}
	if rec["evidence_fingerprint"] == "" {
		t.Errorf("expected non-empty evidence_fingerprint")
	}
}

// ---- scenario (i): reject: strips candidate from file -----------------------

func TestSkillParity_Reject_StripsCandidateFromFile(t *testing.T) {
	cfg := newSkillConfig(t)
	candidateFile := writeSkillCandidates(t, cfg.ProjectDir)
	cfg.Subcommand = "reject"
	cfg.Slug = "parallel-dispatch"

	if _, err := skill.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(candidateFile)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(data), "parallel-dispatch") {
		t.Errorf("expected parallel-dispatch stripped; still present")
	}
	if !strings.Contains(string(data), "audit-log-every-mutation") {
		t.Errorf("expected audit-log-every-mutation still present; missing")
	}
}

// ---- scenario (j): defer: writes deferrals NDJSON with cycle fields ---------

func TestSkillParity_Defer_WritesDeferrals(t *testing.T) {
	cfg := newSkillConfig(t)
	cfg.Subcommand = "defer"
	cfg.Slug = "parallel-dispatch"
	cfg.DeferCycles = 5

	if err := os.WriteFile(filepath.Join(cfg.ProjectDir, ".cycle-count"), []byte("10"), 0644); err != nil {
		t.Fatalf("write cycle-count: %v", err)
	}

	res, err := skill.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.DeferredSlug != "parallel-dispatch" {
		t.Errorf("expected DeferredSlug='parallel-dispatch'; got %q", res.DeferredSlug)
	}

	deferralsPath := filepath.Join(cfg.ProjectDir, "skill-deferrals.ndjson")
	data, err := os.ReadFile(deferralsPath)
	if err != nil {
		t.Fatalf("read deferrals: %v", err)
	}
	var rec map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &rec); err != nil {
		t.Fatalf("parse deferrals: %v", err)
	}
	if int(rec["deferred_at_cycle"].(float64)) != 10 {
		t.Errorf("expected deferred_at_cycle=10; got %v", rec["deferred_at_cycle"])
	}
	if int(rec["defer_until_cycle"].(float64)) != 15 {
		t.Errorf("expected defer_until_cycle=15; got %v", rec["defer_until_cycle"])
	}
}

// ---- scenario (k): stats: no log → advisory ---------------------------------

func TestSkillParity_Stats_NoLog(t *testing.T) {
	cfg := newSkillConfig(t)
	cfg.Subcommand = "stats"

	if _, err := skill.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(skillOut(cfg), "no promotion log") {
		t.Errorf("expected advisory; got: %q", skillOut(cfg))
	}
}

// ---- scenario (l): stats: tallies correctly ---------------------------------

func TestSkillParity_Stats_Tallies(t *testing.T) {
	cfg := newSkillConfig(t)
	cfg.Subcommand = "stats"

	logPath := filepath.Join(cfg.HomeDir, ".yakos-state", "promotion-log.ndjson")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	lines := []string{
		`{"ts":"t","action":"proposed","slug":"a"}`,
		`{"ts":"t","action":"proposed","slug":"b"}`,
		`{"ts":"t","action":"promoted","slug":"a"}`,
		`{"ts":"t","action":"rejected","slug":"b"}`,
	}
	if err := os.WriteFile(logPath, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	res, err := skill.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Stats.Proposed != 2 {
		t.Errorf("expected Proposed=2; got %d", res.Stats.Proposed)
	}
	if res.Stats.Promoted != 1 {
		t.Errorf("expected Promoted=1; got %d", res.Stats.Promoted)
	}
	if res.Stats.Rejected != 1 {
		t.Errorf("expected Rejected=1; got %d", res.Stats.Rejected)
	}
	o := skillOut(cfg)
	if !strings.Contains(o, "Proposed:  2") {
		t.Errorf("expected 'Proposed:  2' in output; got: %q", o)
	}
}

// ---- scenario (m): unknown subcommand ---------------------------------------

func TestSkillParity_UnknownSubcommand_Error(t *testing.T) {
	cfg := newSkillConfig(t)
	cfg.Subcommand = "bogus"
	_, err := skill.Run(cfg)
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "yakos skill help") {
		t.Errorf("expected help hint; got: %v", err)
	}
}

// ---- scenario (n): help text key phrases ------------------------------------

func TestSkillParity_HelpText_KeyPhrases(t *testing.T) {
	var buf bytes.Buffer
	skill.PrintHelp(&buf)
	help := buf.String()

	phrases := []string{
		"yakos skill",
		"candidates",
		"promote",
		"reject",
		"defer",
		"stats",
		"--global",
		"--reason",
		"librarian",
	}
	for _, p := range phrases {
		if !strings.Contains(help, p) {
			t.Errorf("help text missing phrase %q", p)
		}
	}
}

// ---- scenario (o): portedCommands entry -------------------------------------

func TestSkillParity_PortedCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "skill" {
			return
		}
	}
	t.Error("expected 'skill' in portedCommands; not found")
}
