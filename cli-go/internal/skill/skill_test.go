// Package skill_test covers the Go port of cli/lib/skill.sh (rank 26).
//
// Test scenarios (18+ total):
//
//	(a) candidates: empty file → "(no pending candidates)"
//	(b) candidates: parses slug/confidence/evidence from candidates.md
//	(c) candidates: missing candidates file → "(no pending candidates)"
//	(d) candidates: --review prints M2 advisory
//	(e) promote: slug not found → error
//	(f) promote: successful promotion writes SKILL.md with correct layout
//	(g) promote: strips candidate from file after promote
//	(h) promote: appends to promotion log on success
//	(i) promote: repeat-rejection (≥3 graveyard hits) prompts; abort on "N"
//	(j) promote: repeat-rejection; proceeds on "y"
//	(k) promote: validate failure reverts SKILL.md
//	(l) reject: slug not found → error
//	(m) reject: appends to graveyard with fingerprint
//	(n) reject: strips candidate from file
//	(o) reject: appends promotion log with action="rejected"
//	(p) defer: writes to skill-deferrals.ndjson
//	(q) defer: appends promotion log with action="deferred"
//	(r) stats: no log → "(no promotion log yet)"
//	(s) stats: tallies proposed/promoted/rejected/deferred from NDJSON
//	(t) stats: calibration warning when rate <5% over 100 proposals
//	(u) stats: calibration warning when rate >40% over 100 proposals
//	(v) stats: info note when <20 proposals
//	(w) extractCandidate + stripCandidate round-trip correctness
//	(x) evidenceFingerprint determinism + distinct slugs same evidence → same FP
//	(y) graveyardCount: matches by slug OR fingerprint
//	(z) unknown subcommand → error
package skill

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- helpers ----------------------------------------------------------------

func newTestConfig(t *testing.T) Config {
	t.Helper()
	home := t.TempDir()
	curDir := filepath.Join(home, "agent-control", "proj", "work", "current")
	if err := os.MkdirAll(curDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return Config{
		HomeDir:    home,
		ProjectDir: curDir,
		Writer:     &bytes.Buffer{},
		ErrWriter:  &bytes.Buffer{},
	}
}

func out(cfg Config) string    { return cfg.Writer.(*bytes.Buffer).String() }
func errOut(cfg Config) string { return cfg.ErrWriter.(*bytes.Buffer).String() }

// sampleCandidatesFile writes a well-formed candidates.md with two entries.
func sampleCandidatesFile(curDir string) string {
	path := filepath.Join(curDir, "skill-candidates.md")
	content := `# Skill candidates

## candidate: parallel-dispatch (2026-06-01)

**Confidence**: high
**Source evidence** (Cycle references):
- Cycle 10: lead dispatched three agents in parallel
- Cycle 15: noted another parallel pattern

Some description.

## candidate: audit-log-every-mutation (2026-06-02)

**Confidence**: medium
**Source evidence** (Cycle references):
- Cycle 20: mutation without log observed

Extra body text.
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		panic(err)
	}
	return path
}

// ---- scenario (a): empty candidates file ------------------------------------

func TestSkillParity_Candidates_EmptyFile(t *testing.T) {
	cfg := newTestConfig(t)
	// Write an empty candidates file.
	path := filepath.Join(cfg.ProjectDir, "skill-candidates.md")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg.Subcommand = "candidates"
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Candidates) != 0 {
		t.Errorf("expected 0 candidates; got %d", len(res.Candidates))
	}
	if !strings.Contains(out(cfg), "no pending candidates") {
		t.Errorf("expected 'no pending candidates'; got: %q", out(cfg))
	}
}

// ---- scenario (b): parse slug/confidence/evidence ---------------------------

func TestSkillParity_Candidates_ParsesEntries(t *testing.T) {
	cfg := newTestConfig(t)
	sampleCandidatesFile(cfg.ProjectDir)
	cfg.Subcommand = "candidates"
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Candidates) != 2 {
		t.Fatalf("expected 2 candidates; got %d: %+v", len(res.Candidates), res.Candidates)
	}

	c0 := res.Candidates[0]
	if c0.Slug != "parallel-dispatch" {
		t.Errorf("expected slug 'parallel-dispatch'; got %q", c0.Slug)
	}
	if c0.Confidence != "high" {
		t.Errorf("expected conf 'high'; got %q", c0.Confidence)
	}
	if c0.EvidenceCount != 2 {
		t.Errorf("expected 2 evidence items; got %d", c0.EvidenceCount)
	}

	c1 := res.Candidates[1]
	if c1.Slug != "audit-log-every-mutation" {
		t.Errorf("expected slug 'audit-log-every-mutation'; got %q", c1.Slug)
	}
	if c1.Confidence != "medium" {
		t.Errorf("expected conf 'medium'; got %q", c1.Confidence)
	}
	if c1.EvidenceCount != 1 {
		t.Errorf("expected 1 evidence item; got %d", c1.EvidenceCount)
	}

	o := out(cfg)
	if !strings.Contains(o, "parallel-dispatch") {
		t.Errorf("expected 'parallel-dispatch' in output; got: %q", o)
	}
	if !strings.Contains(o, "conf=high") {
		t.Errorf("expected 'conf=high' in output; got: %q", o)
	}
}

// ---- scenario (c): missing candidates file ----------------------------------

func TestSkillParity_Candidates_MissingFile(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.Subcommand = "candidates"
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Candidates) != 0 {
		t.Errorf("expected 0 candidates; got %d", len(res.Candidates))
	}
	if !strings.Contains(out(cfg), "no pending candidates") {
		t.Errorf("expected advisory; got: %q", out(cfg))
	}
}

// ---- scenario (d): --review prints M2 advisory ------------------------------

func TestSkillParity_Candidates_ReviewAdvisory(t *testing.T) {
	cfg := newTestConfig(t)
	sampleCandidatesFile(cfg.ProjectDir)
	cfg.Subcommand = "candidates"
	cfg.Review = true
	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(errOut(cfg), "M2") {
		t.Errorf("expected M2 advisory in stderr; got: %q", errOut(cfg))
	}
}

// ---- scenario (e): promote slug not found -----------------------------------

func TestSkillParity_Promote_SlugNotFound(t *testing.T) {
	cfg := newTestConfig(t)
	sampleCandidatesFile(cfg.ProjectDir)
	cfg.Subcommand = "promote"
	cfg.Slug = "no-such-slug"
	projectDir := t.TempDir()
	cfg.ProjectPath = projectDir

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for slug not found")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error; got: %v", err)
	}
}

// ---- scenario (f): successful promotion writes SKILL.md ---------------------

func TestSkillParity_Promote_WritesSkillMD(t *testing.T) {
	cfg := newTestConfig(t)
	sampleCandidatesFile(cfg.ProjectDir)
	projectDir := t.TempDir()
	cfg.Subcommand = "promote"
	cfg.Slug = "parallel-dispatch"
	cfg.ProjectPath = projectDir

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run promote: %v", err)
	}
	if res.PromotedSlug != "parallel-dispatch" {
		t.Errorf("expected PromotedSlug='parallel-dispatch'; got %q", res.PromotedSlug)
	}

	skillFile := filepath.Join(res.SkillDir, "SKILL.md")
	data, err := os.ReadFile(skillFile)
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "name: parallel-dispatch") {
		t.Errorf("SKILL.md missing name field; got:\n%s", content)
	}
	if !strings.Contains(content, "allowed-tools:") {
		t.Errorf("SKILL.md missing allowed-tools; got:\n%s", content)
	}
	if !strings.Contains(content, "# parallel-dispatch") {
		t.Errorf("SKILL.md missing h1 title; got:\n%s", content)
	}
}

// ---- scenario (g): promote strips candidate from file -----------------------

func TestSkillParity_Promote_StripsCandidateFromFile(t *testing.T) {
	cfg := newTestConfig(t)
	candidateFile := sampleCandidatesFile(cfg.ProjectDir)
	projectDir := t.TempDir()
	cfg.Subcommand = "promote"
	cfg.Slug = "parallel-dispatch"
	cfg.ProjectPath = projectDir

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run promote: %v", err)
	}

	remaining, err := os.ReadFile(candidateFile)
	if err != nil {
		t.Fatalf("read candidates file: %v", err)
	}
	if strings.Contains(string(remaining), "parallel-dispatch") {
		t.Errorf("expected parallel-dispatch stripped from candidates file; still present")
	}
	// Other candidate must still be there.
	if !strings.Contains(string(remaining), "audit-log-every-mutation") {
		t.Errorf("expected audit-log-every-mutation still in file; missing")
	}
}

// ---- scenario (h): promote appends to promotion log ------------------------

func TestSkillParity_Promote_AppendToLog(t *testing.T) {
	cfg := newTestConfig(t)
	sampleCandidatesFile(cfg.ProjectDir)
	projectDir := t.TempDir()
	cfg.Subcommand = "promote"
	cfg.Slug = "parallel-dispatch"
	cfg.ProjectPath = projectDir

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run promote: %v", err)
	}

	logPath := filepath.Join(cfg.HomeDir, ".yakos-state", "promotion-log.ndjson")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read promotion log: %v", err)
	}

	var found bool
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var rec map[string]interface{}
		if json.Unmarshal([]byte(line), &rec) == nil {
			if rec["action"] == "promoted" && rec["slug"] == "parallel-dispatch" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected promoted entry in log; log:\n%s", string(data))
	}
}

// ---- scenario (i): repeat-rejection prompts; abort on "N" ------------------

func TestSkillParity_Promote_RepeatRejection_Abort(t *testing.T) {
	cfg := newTestConfig(t)
	sampleCandidatesFile(cfg.ProjectDir)
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
		entry := GraveyardEntry{
			TS:                  "2026-01-01T00:00:00Z",
			Slug:                "parallel-dispatch",
			Reason:              "test",
			ByUser:              "testuser",
			EvidenceFingerprint: "aabbccddeeff",
		}
		b, _ := json.Marshal(entry)
		f, ferr := os.OpenFile(graveyardPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644) //nolint:gosec
		if ferr != nil {
			t.Fatalf("open graveyard: %v", ferr)
		}
		if _, werr := fmt.Fprintf(f, "%s\n", b); werr != nil {
			_ = f.Close()
			t.Fatalf("write graveyard: %v", werr)
		}
		_ = f.Close()
	}

	// PromptFn returns false (user said "N").
	cfg.PromptFn = func(prompt string) (bool, error) { return false, nil }

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	// Aborted: no SKILL.md should have been written.
	if res.PromotedSlug != "" {
		t.Errorf("expected no promotion after abort; got PromotedSlug=%q", res.PromotedSlug)
	}
	if !strings.Contains(errOut(cfg), "WARN") {
		t.Errorf("expected WARN in stderr; got: %q", errOut(cfg))
	}
}

// ---- scenario (j): repeat-rejection; proceeds on "y" ----------------------

func TestSkillParity_Promote_RepeatRejection_Proceed(t *testing.T) {
	cfg := newTestConfig(t)
	sampleCandidatesFile(cfg.ProjectDir)
	projectDir := t.TempDir()
	cfg.Subcommand = "promote"
	cfg.Slug = "parallel-dispatch"
	cfg.ProjectPath = projectDir

	// Seed graveyard with 3 entries.
	graveyardPath := filepath.Join(cfg.HomeDir, ".yakos-state", "skill-graveyard.ndjson")
	if err := os.MkdirAll(filepath.Dir(graveyardPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for i := 0; i < 3; i++ {
		entry := GraveyardEntry{TS: "2026-01-01T00:00:00Z", Slug: "parallel-dispatch", Reason: "test", ByUser: "u", EvidenceFingerprint: "aaa"}
		b, _ := json.Marshal(entry)
		f, ferr := os.OpenFile(graveyardPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644) //nolint:gosec
		if ferr != nil {
			t.Fatalf("open graveyard: %v", ferr)
		}
		if _, werr := fmt.Fprintf(f, "%s\n", b); werr != nil {
			_ = f.Close()
			t.Fatalf("write graveyard: %v", werr)
		}
		_ = f.Close()
	}

	// PromptFn returns true (user said "y").
	cfg.PromptFn = func(prompt string) (bool, error) { return true, nil }

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.PromotedSlug != "parallel-dispatch" {
		t.Errorf("expected promotion after 'y'; got PromotedSlug=%q", res.PromotedSlug)
	}
}

// ---- scenario (k): validate failure reverts SKILL.md -----------------------

func TestSkillParity_Promote_ValidateFailureReverts(t *testing.T) {
	cfg := newTestConfig(t)
	sampleCandidatesFile(cfg.ProjectDir)
	projectDir := t.TempDir()
	cfg.Subcommand = "promote"
	cfg.Slug = "parallel-dispatch"
	cfg.ProjectPath = projectDir
	cfg.ValidateFn = func(string) error { return fmt.Errorf("validation failed") }

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error when validate fails")
	}
	if !strings.Contains(err.Error(), "validation failed") {
		t.Errorf("expected validation failure in error; got: %v", err)
	}
	// SKILL.md dir should have been removed.
	skillDir := filepath.Join(projectDir, ".claude", "skills", "parallel-dispatch")
	if isDir(skillDir) {
		t.Errorf("expected skill dir removed on validate failure; still exists at %s", skillDir)
	}
}

// ---- scenario (l): reject slug not found ------------------------------------

func TestSkillParity_Reject_SlugNotFound(t *testing.T) {
	cfg := newTestConfig(t)
	sampleCandidatesFile(cfg.ProjectDir)
	cfg.Subcommand = "reject"
	cfg.Slug = "no-such-slug"

	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for slug not found")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found'; got: %v", err)
	}
}

// ---- scenario (m): reject appends to graveyard with fingerprint -------------

func TestSkillParity_Reject_AppendGraveyard(t *testing.T) {
	cfg := newTestConfig(t)
	sampleCandidatesFile(cfg.ProjectDir)
	cfg.Subcommand = "reject"
	cfg.Slug = "parallel-dispatch"
	cfg.Reason = "not useful enough"

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run reject: %v", err)
	}

	graveyardPath := filepath.Join(cfg.HomeDir, ".yakos-state", "skill-graveyard.ndjson")
	data, err := os.ReadFile(graveyardPath)
	if err != nil {
		t.Fatalf("read graveyard: %v", err)
	}

	var entry GraveyardEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &entry); err != nil {
		t.Fatalf("parse graveyard entry: %v", err)
	}
	if entry.Slug != "parallel-dispatch" {
		t.Errorf("expected slug='parallel-dispatch'; got %q", entry.Slug)
	}
	if entry.Reason != "not useful enough" {
		t.Errorf("expected reason='not useful enough'; got %q", entry.Reason)
	}
	if entry.EvidenceFingerprint == "" || entry.EvidenceFingerprint == "no-evidence" {
		t.Errorf("expected non-trivial fingerprint; got %q", entry.EvidenceFingerprint)
	}
}

// ---- scenario (n): reject strips candidate from file ------------------------

func TestSkillParity_Reject_StripsCandidate(t *testing.T) {
	cfg := newTestConfig(t)
	candidateFile := sampleCandidatesFile(cfg.ProjectDir)
	cfg.Subcommand = "reject"
	cfg.Slug = "parallel-dispatch"

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run reject: %v", err)
	}

	data, err := os.ReadFile(candidateFile)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(data), "parallel-dispatch") {
		t.Errorf("expected parallel-dispatch stripped; still present in:\n%s", string(data))
	}
	if !strings.Contains(string(data), "audit-log-every-mutation") {
		t.Errorf("expected other candidate still present")
	}
}

// ---- scenario (o): reject appends promotion log with action="rejected" ------

func TestSkillParity_Reject_AppendLog(t *testing.T) {
	cfg := newTestConfig(t)
	sampleCandidatesFile(cfg.ProjectDir)
	cfg.Subcommand = "reject"
	cfg.Slug = "parallel-dispatch"
	cfg.Reason = "low value"

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	logPath := filepath.Join(cfg.HomeDir, ".yakos-state", "promotion-log.ndjson")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	var found bool
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var rec map[string]interface{}
		if json.Unmarshal([]byte(line), &rec) == nil {
			if rec["action"] == "rejected" && rec["slug"] == "parallel-dispatch" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected rejected entry in log; log:\n%s", string(data))
	}
}

// ---- scenario (p): defer writes to skill-deferrals.ndjson ------------------

func TestSkillParity_Defer_WritesDeferrals(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.Subcommand = "defer"
	cfg.Slug = "parallel-dispatch"
	cfg.DeferCycles = 5

	// Write cycle-count.
	if err := os.WriteFile(filepath.Join(cfg.ProjectDir, ".cycle-count"), []byte("20"), 0644); err != nil {
		t.Fatalf("write cycle-count: %v", err)
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run defer: %v", err)
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
		t.Fatalf("parse deferrals entry: %v", err)
	}
	if rec["slug"] != "parallel-dispatch" {
		t.Errorf("expected slug='parallel-dispatch'; got %v", rec["slug"])
	}
	if int(rec["deferred_at_cycle"].(float64)) != 20 {
		t.Errorf("expected deferred_at_cycle=20; got %v", rec["deferred_at_cycle"])
	}
	if int(rec["defer_until_cycle"].(float64)) != 25 {
		t.Errorf("expected defer_until_cycle=25; got %v", rec["defer_until_cycle"])
	}
}

// ---- scenario (q): defer appends promotion log action="deferred" -----------

func TestSkillParity_Defer_AppendLog(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.Subcommand = "defer"
	cfg.Slug = "parallel-dispatch"
	cfg.DeferCycles = 3

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run defer: %v", err)
	}

	logPath := filepath.Join(cfg.HomeDir, ".yakos-state", "promotion-log.ndjson")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	var found bool
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var rec map[string]interface{}
		if json.Unmarshal([]byte(line), &rec) == nil {
			if rec["action"] == "deferred" && rec["slug"] == "parallel-dispatch" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected deferred entry in log; log:\n%s", string(data))
	}
}

// ---- scenario (r): stats no log → advisory ----------------------------------

func TestSkillParity_Stats_NoLog(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.Subcommand = "stats"

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run stats: %v", err)
	}
	if res.Stats == nil {
		t.Fatal("expected Stats != nil")
	}
	if !strings.Contains(out(cfg), "no promotion log") {
		t.Errorf("expected 'no promotion log'; got: %q", out(cfg))
	}
}

// ---- scenario (s): stats tallies from NDJSON --------------------------------

func TestSkillParity_Stats_TalliesLog(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.Subcommand = "stats"

	logPath := filepath.Join(cfg.HomeDir, ".yakos-state", "promotion-log.ndjson")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	lines := []string{
		`{"ts":"t","action":"proposed","slug":"a"}`,
		`{"ts":"t","action":"proposed","slug":"b"}`,
		`{"ts":"t","action":"proposed","slug":"c"}`,
		`{"ts":"t","action":"promoted","slug":"a"}`,
		`{"ts":"t","action":"rejected","slug":"b"}`,
		`{"ts":"t","action":"deferred","slug":"c"}`,
	}
	if err := os.WriteFile(logPath, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run stats: %v", err)
	}
	if res.Stats.Proposed != 3 {
		t.Errorf("expected Proposed=3; got %d", res.Stats.Proposed)
	}
	if res.Stats.Promoted != 1 {
		t.Errorf("expected Promoted=1; got %d", res.Stats.Promoted)
	}
	if res.Stats.Rejected != 1 {
		t.Errorf("expected Rejected=1; got %d", res.Stats.Rejected)
	}
	if res.Stats.Deferred != 1 {
		t.Errorf("expected Deferred=1; got %d", res.Stats.Deferred)
	}

	o := out(cfg)
	if !strings.Contains(o, "Proposed:  3") {
		t.Errorf("expected Proposed: 3 in output; got: %q", o)
	}
	if !strings.Contains(o, "Promoted:  1") {
		t.Errorf("expected Promoted: 1 in output; got: %q", o)
	}
}

// ---- scenario (t): calibration warning rate <5% ----------------------------

func TestSkillParity_Stats_CalibrationWarnLow(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.Subcommand = "stats"

	logPath := filepath.Join(cfg.HomeDir, ".yakos-state", "promotion-log.ndjson")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// 100 proposed, 2 promoted → rate=2% (<5%)
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&sb, `{"ts":"t","action":"proposed","slug":"s%d"}`+"\n", i)
	}
	sb.WriteString(`{"ts":"t","action":"promoted","slug":"s0"}` + "\n")
	sb.WriteString(`{"ts":"t","action":"promoted","slug":"s1"}` + "\n")
	if err := os.WriteFile(logPath, []byte(sb.String()), 0644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(errOut(cfg), "over-eager") {
		t.Errorf("expected over-eager warning; got: %q", errOut(cfg))
	}
}

// ---- scenario (u): calibration warning rate >40% ---------------------------

func TestSkillParity_Stats_CalibrationWarnHigh(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.Subcommand = "stats"

	logPath := filepath.Join(cfg.HomeDir, ".yakos-state", "promotion-log.ndjson")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// 100 proposed, 50 promoted → rate=50% (>40%)
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&sb, `{"ts":"t","action":"proposed","slug":"s%d"}`+"\n", i)
	}
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&sb, `{"ts":"t","action":"promoted","slug":"s%d"}`+"\n", i)
	}
	if err := os.WriteFile(logPath, []byte(sb.String()), 0644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(errOut(cfg), "under-skeptical") {
		t.Errorf("expected under-skeptical warning; got: %q", errOut(cfg))
	}
}

// ---- scenario (v): info note when <20 proposals -----------------------------

func TestSkillParity_Stats_SmallSampleInfo(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.Subcommand = "stats"

	logPath := filepath.Join(cfg.HomeDir, ".yakos-state", "promotion-log.ndjson")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// 5 proposed
	var sb strings.Builder
	for i := 0; i < 5; i++ {
		fmt.Fprintf(&sb, `{"ts":"t","action":"proposed","slug":"s%d"}`+"\n", i)
	}
	if err := os.WriteFile(logPath, []byte(sb.String()), 0644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(errOut(cfg), "sample size") {
		t.Errorf("expected sample-size info; got: %q", errOut(cfg))
	}
}

// ---- scenario (w): extractCandidate + stripCandidate round-trip -------------

func TestSkillParity_ExtractAndStrip_RoundTrip(t *testing.T) {
	content := `# Candidates

## candidate: foo (2026-01-01)

**Confidence**: high

Body of foo.

## candidate: bar (2026-01-02)

**Confidence**: low

Body of bar.
`
	fooBody := extractCandidate("foo", content)
	if !strings.Contains(fooBody, "foo") {
		t.Errorf("expected foo in extracted body; got: %q", fooBody)
	}
	if strings.Contains(fooBody, "bar") {
		t.Errorf("expected foo body to not contain bar; got: %q", fooBody)
	}

	stripped := stripCandidate("foo", content)
	if strings.Contains(stripped, "## candidate: foo") {
		t.Errorf("expected foo section stripped; got:\n%s", stripped)
	}
	if !strings.Contains(stripped, "## candidate: bar") {
		t.Errorf("expected bar section still present; got:\n%s", stripped)
	}
}

// ---- scenario (x): evidenceFingerprint determinism -------------------------

func TestSkillParity_EvidenceFingerprint_Deterministic(t *testing.T) {
	content := `## candidate: foo (2026-01-01)

**Source evidence** (Cycle references):
- Cycle 10: something
- Cycle 15: another
`
	fp1 := evidenceFingerprint("foo", content)
	fp2 := evidenceFingerprint("foo", content)
	if fp1 != fp2 {
		t.Errorf("fingerprint not deterministic: %q vs %q", fp1, fp2)
	}
	if fp1 == "" || fp1 == "no-evidence" {
		t.Errorf("expected non-trivial fingerprint; got %q", fp1)
	}
}

func TestSkillParity_EvidenceFingerprint_SameEvidenceSameFingerprint(t *testing.T) {
	content1 := `## candidate: cleanup-files (2026-01-01)

**Source evidence** (Cycle references):
- Cycle 10: observed
- Cycle 15: noted
`
	content2 := `## candidate: clean-up-files (2026-01-01)

**Source evidence** (Cycle references):
- Cycle 10: observed
- Cycle 15: noted
`
	fp1 := evidenceFingerprint("cleanup-files", content1)
	fp2 := evidenceFingerprint("clean-up-files", content2)
	if fp1 != fp2 {
		t.Errorf("expected same fingerprint for same evidence cycles; %q vs %q", fp1, fp2)
	}
}

// ---- scenario (y): graveyardCount matches slug OR fingerprint ---------------

func TestSkillParity_GraveyardCount_MatchesSlugOrFingerprint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "graveyard.ndjson")

	entries := []GraveyardEntry{
		{TS: "t", Slug: "foo", EvidenceFingerprint: "aaaaaa"},
		{TS: "t", Slug: "bar", EvidenceFingerprint: "bbbbbb"},
		{TS: "t", Slug: "baz", EvidenceFingerprint: "aaaaaa"}, // same FP as foo
	}
	for _, e := range entries {
		if err := appendGraveyard(path, e); err != nil {
			t.Fatalf("appendGraveyard: %v", err)
		}
	}

	// Count by slug "foo": 1 direct match.
	c := graveyardCount(path, "foo", "")
	if c != 1 {
		t.Errorf("expected 1 for slug=foo; got %d", c)
	}

	// Count by slug "foo" + fingerprint "aaaaaa": matches foo (slug) + baz (FP).
	c = graveyardCount(path, "foo", "aaaaaa")
	if c != 2 {
		t.Errorf("expected 2 for slug=foo + fp=aaaaaa; got %d", c)
	}

	// Count by slug "qux" + fingerprint "aaaaaa": matches foo+baz by FP.
	c = graveyardCount(path, "qux", "aaaaaa")
	if c != 2 {
		t.Errorf("expected 2 for slug=qux + fp=aaaaaa (FP match); got %d", c)
	}
}

// ---- scenario (z): unknown subcommand → error --------------------------------

func TestSkillParity_UnknownSubcommand_Error(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.Subcommand = "bogus"
	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("expected 'unknown subcommand' in error; got: %v", err)
	}
	if !strings.Contains(err.Error(), "yakos skill help") {
		t.Errorf("expected help hint in error; got: %v", err)
	}
}
