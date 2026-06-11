package metrics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- stat helpers ---

func TestMedian(t *testing.T) {
	m, ok := Median([]float64{3, 1, 2})
	if !ok || m != 2.0 {
		t.Errorf("Median([3,1,2]) = %v,%v; want 2.0,true", m, ok)
	}
	m, ok = Median([]float64{1, 2, 3, 4})
	if !ok || m != 2.5 {
		t.Errorf("Median([1,2,3,4]) = %v,%v; want 2.5,true", m, ok)
	}
	_, ok = Median(nil)
	if ok {
		t.Error("Median(nil) should return ok=false")
	}
}

func TestMean(t *testing.T) {
	m, ok := Mean([]float64{1, 2, 3})
	if !ok || m != 2.0 {
		t.Errorf("Mean([1,2,3]) = %v,%v; want 2.0,true", m, ok)
	}
	_, ok = Mean(nil)
	if ok {
		t.Error("Mean(nil) should return ok=false")
	}
}

func TestRate(t *testing.T) {
	r, ok := Rate(3, 4)
	if !ok || r != 0.75 {
		t.Errorf("Rate(3,4) = %v,%v; want 0.75,true", r, ok)
	}
	_, ok = Rate(1, 0)
	if ok {
		t.Error("Rate(1,0) should return ok=false (div-by-zero)")
	}
}

func TestPercentile(t *testing.T) {
	p, ok := Percentile([]float64{1, 2, 3, 4, 5}, 50)
	if !ok || p != 3.0 {
		t.Errorf("Percentile([1-5], 50) = %v,%v; want 3.0,true", p, ok)
	}
	_, ok = Percentile(nil, 50)
	if ok {
		t.Error("Percentile(nil, 50) should return ok=false")
	}
}

// --- null != 0 invariant ---

func TestNullVsZeroInvariant(t *testing.T) {
	// secret_scan_hits = 0 (measured, empty gitleaks output)
	hits := intPtr(0)
	// coverage_pct = nil (tool absent)
	var cov *float64

	snap := Snapshot{
		Schema:   schemaVersion,
		Ts:       time.Now().UTC(),
		Commit:   "abc123",
		Branch:   "main",
		Trigger:  "manual",
		Profiles: []string{"go-backend"},
		Metrics: Metrics{
			Security: SecurityMetrics{SecretScanHits: hits},
			Test:     TestMetrics{CoveragePct: cov},
		},
		ToolStatus: map[string]string{},
	}

	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	s := string(data)
	if !strings.Contains(s, `"secret_scan_hits":0`) {
		t.Errorf("expected secret_scan_hits:0 in JSON; got %s", s)
	}
	if !strings.Contains(s, `"coverage_pct":null`) {
		t.Errorf("expected coverage_pct:null in JSON; got %s", s)
	}
}

// --- store ---

func TestAppendAndReadHistory(t *testing.T) {
	dir := t.TempDir()

	snap := Snapshot{
		Schema:     schemaVersion,
		Ts:         time.Now().UTC(),
		Commit:     "deadbeef",
		Branch:     "main",
		Trigger:    "manual",
		Profiles:   []string{"go-backend"},
		ToolStatus: map[string]string{"go-test": "ok"},
	}

	if err := AppendSnapshot(dir, snap); err != nil {
		t.Fatalf("AppendSnapshot: %v", err)
	}
	// Append a second one.
	snap2 := snap
	snap2.Commit = "cafebabe"
	if err := AppendSnapshot(dir, snap2); err != nil {
		t.Fatalf("AppendSnapshot 2: %v", err)
	}

	snaps, err := ReadHistory(dir)
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	if len(snaps) != 2 {
		t.Fatalf("expected 2 snapshots; got %d", len(snaps))
	}
	if snaps[0].Commit != "deadbeef" {
		t.Errorf("first snapshot commit: got %q; want deadbeef", snaps[0].Commit)
	}
	if snaps[1].Commit != "cafebabe" {
		t.Errorf("second snapshot commit: got %q; want cafebabe", snaps[1].Commit)
	}
}

func TestReadHistory_NotExist(t *testing.T) {
	snaps, err := ReadHistory(t.TempDir())
	if err != nil {
		t.Fatalf("ReadHistory on empty dir: %v", err)
	}
	if len(snaps) != 0 {
		t.Errorf("expected 0 snapshots; got %d", len(snaps))
	}
}

// --- analyzer list - missing tool path ---

func TestRunAnalyzerList_MissingTool(t *testing.T) {
	dir := t.TempDir()
	queue := []analyzer{
		{
			Name: "bogus-tool-xyz",
			Tool: "bogus-tool-that-does-not-exist-xyz",
			Args: []string{"--version"},
			Apply: func(out string, runErr error, m *Metrics) error {
				t.Error("Apply should not be called for missing tool")
				return nil
			},
		},
	}
	m := &Metrics{}
	status := make(map[string]string)
	runAnalyzerList(dir, queue, m, status)

	if status["bogus-tool-xyz"] != "tool-missing" {
		t.Errorf("expected tool-missing; got %q", status["bogus-tool-xyz"])
	}
	// Metrics should remain nil (not set).
	if m.Test.CoveragePct != nil {
		t.Error("coverage_pct should be nil when tool missing")
	}
}

// --- gitRunner fake ---

type fakeGitRunner struct {
	responses map[string]string
}

func (f *fakeGitRunner) Run(dir string, args ...string) (string, error) {
	key := strings.Join(args, " ")
	if v, ok := f.responses[key]; ok {
		return v, nil
	}
	return "", os.ErrNotExist
}

func TestGetGitInfo_Fake(t *testing.T) {
	runner := &fakeGitRunner{
		responses: map[string]string{
			"rev-parse HEAD":              "abcdef1234567890",
			"rev-parse --abbrev-ref HEAD": "feat/test-branch",
		},
	}
	info := getGitInfo(runner, "/tmp")
	if info.Commit != "abcdef1234567890" {
		t.Errorf("Commit: got %q; want abcdef1234567890", info.Commit)
	}
	if info.Branch != "feat/test-branch" {
		t.Errorf("Branch: got %q; want feat/test-branch", info.Branch)
	}
}

// --- ParseArgs ---

func TestParseArgs_Collect(t *testing.T) {
	cfg, err := ParseArgs([]string{"collect", "--trigger", "ci", "--no-write"}, "/tmp")
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if cfg.Subcommand != "collect" {
		t.Errorf("Subcommand: got %q; want collect", cfg.Subcommand)
	}
	if cfg.Trigger != "ci" {
		t.Errorf("Trigger: got %q; want ci", cfg.Trigger)
	}
	if !cfg.NoWrite {
		t.Error("NoWrite should be true")
	}
}

func TestParseArgs_Compare(t *testing.T) {
	cfg, err := ParseArgs([]string{"compare", "abc123", "def456"}, "/tmp")
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if cfg.ShaA != "abc123" || cfg.ShaB != "def456" {
		t.Errorf("ShaA=%q ShaB=%q; want abc123,def456", cfg.ShaA, cfg.ShaB)
	}
}

func TestParseArgs_Compare_MissingArgs(t *testing.T) {
	_, err := ParseArgs([]string{"compare", "abc123"}, "/tmp")
	if err == nil {
		t.Error("expected error for missing shaB")
	}
}

func TestParseArgs_UnknownSubcommand(t *testing.T) {
	_, err := ParseArgs([]string{"bogus"}, "/tmp")
	if err == nil {
		t.Error("expected error for unknown subcommand")
	}
}

func TestParseArgs_Help(t *testing.T) {
	cfg, err := ParseArgs([]string{"help"}, "/tmp")
	if err != nil {
		t.Fatalf("ParseArgs help: %v", err)
	}
	if cfg.Subcommand != "help" {
		t.Errorf("Subcommand: got %q; want help", cfg.Subcommand)
	}
}

// --- collect via Run --

func TestRun_CollectNoWrite(t *testing.T) {
	dir := t.TempDir()
	// Create a minimal git-like repo.
	runner := &fakeGitRunner{
		responses: map[string]string{
			"rev-parse HEAD":              "testsha001",
			"rev-parse --abbrev-ref HEAD": "main",
			"tag --list":                  "",
		},
	}

	var buf strings.Builder
	cfg := Config{
		Subcommand:    "collect",
		Trigger:       "manual",
		NoWrite:       true,
		SkipAnalyzers: true,
		ProjectDir:    dir,
		StateDir:      dir,
		HomeDir:       dir,
		GitRunner:     runner,
		Now:           func() time.Time { return time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC) },
		Writer:        &buf,
		ErrWriter:     &buf,
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run collect: %v", err)
	}
	if res.Snapshot == nil {
		t.Fatal("expected snapshot in result")
	}
	if res.Snapshot.Commit != "testsha001" {
		t.Errorf("Commit: got %q; want testsha001", res.Snapshot.Commit)
	}

	// Verify history was NOT written (--no-write).
	histPath := HistoryPath(dir)
	if _, err := os.Stat(histPath); !os.IsNotExist(err) {
		t.Error("history.ndjson should not exist with --no-write")
	}
}

func TestRun_CollectAndReport(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeGitRunner{
		responses: map[string]string{
			"rev-parse HEAD":              "deadbeef001",
			"rev-parse --abbrev-ref HEAD": "main",
			"tag --list":                  "",
		},
	}

	// Collect with a snapshot that has null coverage but 0 secret_scan_hits.
	var collectBuf strings.Builder
	collectCfg := Config{
		Subcommand:    "collect",
		Trigger:       "manual",
		NoWrite:       false,
		SkipAnalyzers: true,
		ProjectDir:    dir,
		StateDir:      dir,
		HomeDir:       dir,
		GitRunner:     runner,
		Now:           func() time.Time { return time.Date(2026, 6, 11, 1, 0, 0, 0, time.UTC) },
		Writer:        &collectBuf,
		ErrWriter:     &collectBuf,
	}
	_, err := Run(collectCfg)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	// Manually inject a snapshot with null coverage + 0 secret hits to test
	// the null≠0 invariant in the stored file.
	snap := Snapshot{
		Schema:   schemaVersion,
		Ts:       time.Date(2026, 6, 11, 2, 0, 0, 0, time.UTC),
		Commit:   "nulltest001",
		Branch:   "main",
		Trigger:  "manual",
		Profiles: []string{"go-backend"},
		Metrics: Metrics{
			Security: SecurityMetrics{SecretScanHits: intPtr(0)},
			Test:     TestMetrics{CoveragePct: nil},
		},
		ToolStatus: map[string]string{"go-test": "tool-missing", "gitleaks": "ok"},
	}
	if err := AppendSnapshot(dir, snap); err != nil {
		t.Fatalf("AppendSnapshot: %v", err)
	}

	// Verify the null≠0 invariant in the file.
	histPath := HistoryPath(dir)
	data, err := os.ReadFile(histPath) //nolint:gosec
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	// Find the line with nulltest001.
	found := false
	for _, line := range lines {
		if strings.Contains(line, "nulltest001") {
			found = true
			if !strings.Contains(line, `"secret_scan_hits":0`) {
				t.Errorf("expected secret_scan_hits:0 in %s", line)
			}
			if !strings.Contains(line, `"coverage_pct":null`) {
				t.Errorf("expected coverage_pct:null in %s", line)
			}
		}
	}
	if !found {
		t.Error("nulltest001 snapshot not found in history")
	}

	// Run report.
	var reportBuf strings.Builder
	reportCfg := Config{
		Subcommand: "report",
		ProjectDir: dir,
		HomeDir:    dir,
		Writer:     &reportBuf,
		ErrWriter:  &reportBuf,
	}
	_, err = Run(reportCfg)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	report := reportBuf.String()
	if !strings.Contains(report, "yakos metrics report") {
		t.Errorf("report output missing header: %s", report)
	}
}

// --- hookBypass collector ---

func TestCollectHookBypass_Empty(t *testing.T) {
	m := &Metrics{}
	collectHookBypass(t.TempDir(), time.Now(), m)
	if m.Dispatch.HookBypassCount != nil {
		t.Errorf("expected nil hook bypass count for missing file; got %v", *m.Dispatch.HookBypassCount)
	}
}

func TestCollectHookBypass_ActiveEntry(t *testing.T) {
	dir := t.TempDir()
	workDir := filepath.Join(dir, "work", "current")
	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatal(err)
	}

	future := time.Now().Add(24 * time.Hour).Format(time.RFC3339)
	content := `# Hook bypass

## Active entries

## bypass:test-id-1
**Expires:** ` + future + `
**Reason:** testing

`
	if err := os.WriteFile(filepath.Join(workDir, "hook-bypass.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	m := &Metrics{}
	collectHookBypass(dir, time.Now(), m)
	if m.Dispatch.HookBypassCount == nil {
		t.Fatal("expected hook bypass count; got nil")
	}
	if *m.Dispatch.HookBypassCount != 1 {
		t.Errorf("expected 1 active bypass; got %d", *m.Dispatch.HookBypassCount)
	}
}

func TestCollectHookBypass_ExpiredEntry(t *testing.T) {
	dir := t.TempDir()
	workDir := filepath.Join(dir, "work", "current")
	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatal(err)
	}

	past := time.Now().Add(-24 * time.Hour).Format(time.RFC3339)
	content := `# Hook bypass

## Active entries

## bypass:test-expired
**Expires:** ` + past + `
**Reason:** testing

`
	if err := os.WriteFile(filepath.Join(workDir, "hook-bypass.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	m := &Metrics{}
	collectHookBypass(dir, time.Now(), m)
	if m.Dispatch.HookBypassCount == nil {
		t.Fatal("expected hook bypass count; got nil")
	}
	if *m.Dispatch.HookBypassCount != 0 {
		t.Errorf("expected 0 active bypasses (all expired); got %d", *m.Dispatch.HookBypassCount)
	}
}
