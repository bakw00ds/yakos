package metrics

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// fakeDispatchRunner implements dispatchRunner for tests.
// responses maps "<agent>:<task-prefix>" → output string.
// errors maps "<agent>" → error to return.
type fakeDispatchRunner struct {
	// responses maps agent name → response string to return on success.
	responses map[string]string
	// errs maps agent name → error to return instead of responses.
	errs map[string]error
	// called records which agents were dispatched (agent name → call count).
	called map[string]int
}

func newFakeDispatchRunner() *fakeDispatchRunner {
	return &fakeDispatchRunner{
		responses: make(map[string]string),
		errs:      make(map[string]error),
		called:    make(map[string]int),
	}
}

func (f *fakeDispatchRunner) Dispatch(agent, _ string) (string, error) {
	f.called[agent]++
	if err, ok := f.errs[agent]; ok {
		return "", err
	}
	if resp, ok := f.responses[agent]; ok {
		return resp, nil
	}
	return "", fmt.Errorf("fake: no response configured for agent %q", agent)
}

// --- extractJSON ---

func TestExtractJSON_Object(t *testing.T) {
	raw := `Some prose before. {"findings_by_severity":{"P0":1,"P1":2}} trailing text.`
	got := extractJSON(raw)
	want := `{"findings_by_severity":{"P0":1,"P1":2}}`
	if got != want {
		t.Errorf("extractJSON: got %q; want %q", got, want)
	}
}

func TestExtractJSON_NoJSON(t *testing.T) {
	got := extractJSON("no json here at all")
	if got != "" {
		t.Errorf("extractJSON: expected empty; got %q", got)
	}
}

func TestExtractJSON_EmptyString(t *testing.T) {
	got := extractJSON("")
	if got != "" {
		t.Errorf("extractJSON: expected empty; got %q", got)
	}
}

func TestExtractJSON_JsonOnlyNoWrapper(t *testing.T) {
	raw := `{"findings_by_severity":{"P0":0,"P1":0,"P2":3,"P3":1}}`
	got := extractJSON(raw)
	if got != raw {
		t.Errorf("extractJSON: got %q; want %q", got, raw)
	}
}

func TestExtractJSON_NestedObjects(t *testing.T) {
	raw := `Here is the result: {"a":{"b":{"c":42}}}`
	got := extractJSON(raw)
	want := `{"a":{"b":{"c":42}}}`
	if got != want {
		t.Errorf("extractJSON: got %q; want %q", got, want)
	}
}

func TestExtractJSON_StringContainingBraces(t *testing.T) {
	// String value containing braces should not confuse depth tracking.
	raw := `{"key":"value with {braces} inside","count":5}`
	got := extractJSON(raw)
	if got != raw {
		t.Errorf("extractJSON string-with-braces: got %q; want %q", got, raw)
	}
}

// --- [S] collector: well-formed JSON tally → fields populated ---

func TestCollectCodeReviewFindings_WellFormed(t *testing.T) {
	snap := makeTestSnap()
	dr := newFakeDispatchRunner()
	dr.responses["code-reviewer"] = `{"findings_by_severity":{"P0":0,"P1":2,"P2":5,"P3":10}}`

	collectCodeReviewFindings(dr, snap)

	if snap.ToolStatus["code-review"] != "ok" {
		t.Errorf("status: got %q; want ok", snap.ToolStatus["code-review"])
	}
	if snap.Metrics.CodeQuality.ReviewFindingsBySeverity == nil {
		t.Fatal("ReviewFindingsBySeverity should not be nil on success")
	}
	if snap.Metrics.CodeQuality.ReviewFindingsBySeverity["P0"] != 0 {
		t.Errorf("P0: got %d; want 0", snap.Metrics.CodeQuality.ReviewFindingsBySeverity["P0"])
	}
	if snap.Metrics.CodeQuality.ReviewFindingsBySeverity["P1"] != 2 {
		t.Errorf("P1: got %d; want 2", snap.Metrics.CodeQuality.ReviewFindingsBySeverity["P1"])
	}
	if snap.Metrics.CodeQuality.ReviewFindingsBySeverity["P2"] != 5 {
		t.Errorf("P2: got %d; want 5", snap.Metrics.CodeQuality.ReviewFindingsBySeverity["P2"])
	}
	if snap.Metrics.CodeQuality.ReviewFindingsBySeverity["P3"] != 10 {
		t.Errorf("P3: got %d; want 10", snap.Metrics.CodeQuality.ReviewFindingsBySeverity["P3"])
	}
}

func TestCollectSecurityReviewFindings_WellFormed(t *testing.T) {
	snap := makeTestSnap()
	dr := newFakeDispatchRunner()
	dr.responses["security-reviewer"] = `{"findings_by_severity":{"P0":1,"P1":3,"P2":2,"P3":0}}`

	collectSecurityReviewFindings(dr, snap)

	if snap.ToolStatus["security-review"] != "ok" {
		t.Errorf("status: got %q; want ok", snap.ToolStatus["security-review"])
	}
	if snap.Metrics.Security.SecurityReviewFindingsBySeverity == nil {
		t.Fatal("SecurityReviewFindingsBySeverity should not be nil on success")
	}
	if snap.Metrics.Security.SecurityReviewFindingsBySeverity["P0"] != 1 {
		t.Errorf("P0: got %d; want 1", snap.Metrics.Security.SecurityReviewFindingsBySeverity["P0"])
	}
}

// --- dispatch error → nil + status "dispatch-failed" ---

func TestCollectCodeReviewFindings_DispatchError(t *testing.T) {
	snap := makeTestSnap()
	dr := newFakeDispatchRunner()
	dr.errs["code-reviewer"] = fmt.Errorf("yakos dispatch: no runtime configured")

	collectCodeReviewFindings(dr, snap)

	if snap.ToolStatus["code-review"] != "dispatch-failed" {
		t.Errorf("status: got %q; want dispatch-failed", snap.ToolStatus["code-review"])
	}
	if snap.Metrics.CodeQuality.ReviewFindingsBySeverity != nil {
		t.Error("ReviewFindingsBySeverity should be nil on dispatch error")
	}
}

func TestCollectSecurityReviewFindings_DispatchError(t *testing.T) {
	snap := makeTestSnap()
	dr := newFakeDispatchRunner()
	dr.errs["security-reviewer"] = fmt.Errorf("yakos dispatch: no runtime configured")

	collectSecurityReviewFindings(dr, snap)

	if snap.ToolStatus["security-review"] != "dispatch-failed" {
		t.Errorf("status: got %q; want dispatch-failed", snap.ToolStatus["security-review"])
	}
	if snap.Metrics.Security.SecurityReviewFindingsBySeverity != nil {
		t.Error("SecurityReviewFindingsBySeverity should be nil on dispatch error")
	}
}

// --- JSON wrapped in prose → extracted + populated ---

func TestCollectCodeReviewFindings_JSONWrappedInProse(t *testing.T) {
	snap := makeTestSnap()
	dr := newFakeDispatchRunner()
	// Agent responded with prose wrapping the JSON.
	dr.responses["code-reviewer"] = `I reviewed the codebase. Here are my findings:

{"findings_by_severity":{"P0":0,"P1":1,"P2":7,"P3":3}}

Let me know if you need more detail.`

	collectCodeReviewFindings(dr, snap)

	if snap.ToolStatus["code-review"] != "ok" {
		t.Errorf("status: got %q; want ok", snap.ToolStatus["code-review"])
	}
	if snap.Metrics.CodeQuality.ReviewFindingsBySeverity == nil {
		t.Fatal("ReviewFindingsBySeverity should not be nil when JSON is embedded in prose")
	}
	if snap.Metrics.CodeQuality.ReviewFindingsBySeverity["P1"] != 1 {
		t.Errorf("P1: got %d; want 1", snap.Metrics.CodeQuality.ReviewFindingsBySeverity["P1"])
	}
	if snap.Metrics.CodeQuality.ReviewFindingsBySeverity["P2"] != 7 {
		t.Errorf("P2: got %d; want 7", snap.Metrics.CodeQuality.ReviewFindingsBySeverity["P2"])
	}
}

func TestCollectSecurityReviewFindings_JSONWrappedInProse(t *testing.T) {
	snap := makeTestSnap()
	dr := newFakeDispatchRunner()
	dr.responses["security-reviewer"] = `Security review complete.
{"findings_by_severity":{"P0":2,"P1":0,"P2":1,"P3":4}}
End of review.`

	collectSecurityReviewFindings(dr, snap)

	if snap.ToolStatus["security-review"] != "ok" {
		t.Errorf("status: got %q; want ok", snap.ToolStatus["security-review"])
	}
	if snap.Metrics.Security.SecurityReviewFindingsBySeverity == nil {
		t.Fatal("SecurityReviewFindingsBySeverity should not be nil")
	}
	if snap.Metrics.Security.SecurityReviewFindingsBySeverity["P0"] != 2 {
		t.Errorf("P0: got %d; want 2", snap.Metrics.Security.SecurityReviewFindingsBySeverity["P0"])
	}
}

// --- unparseable garbage → nil + status "unparseable" ---

func TestCollectCodeReviewFindings_UnparseableGarbage(t *testing.T) {
	snap := makeTestSnap()
	dr := newFakeDispatchRunner()
	dr.responses["code-reviewer"] = "this is not json at all, just garbage text"

	collectCodeReviewFindings(dr, snap)

	if snap.ToolStatus["code-review"] != "unparseable" {
		t.Errorf("status: got %q; want unparseable", snap.ToolStatus["code-review"])
	}
	if snap.Metrics.CodeQuality.ReviewFindingsBySeverity != nil {
		t.Error("ReviewFindingsBySeverity should be nil on unparseable output")
	}
}

func TestCollectSecurityReviewFindings_UnparseableGarbage(t *testing.T) {
	snap := makeTestSnap()
	dr := newFakeDispatchRunner()
	dr.responses["security-reviewer"] = "!!not json!!"

	collectSecurityReviewFindings(dr, snap)

	if snap.ToolStatus["security-review"] != "unparseable" {
		t.Errorf("status: got %q; want unparseable", snap.ToolStatus["security-review"])
	}
	if snap.Metrics.Security.SecurityReviewFindingsBySeverity != nil {
		t.Error("SecurityReviewFindingsBySeverity should be nil on unparseable output")
	}
}

func TestCollectCodeReviewFindings_ValidJSONWrongShape(t *testing.T) {
	// Valid JSON but missing the expected key → unparseable.
	snap := makeTestSnap()
	dr := newFakeDispatchRunner()
	dr.responses["code-reviewer"] = `{"wrong_key": {"P0": 1}}`

	collectCodeReviewFindings(dr, snap)

	// findings_by_severity key is absent → nil map → unparseable.
	if snap.ToolStatus["code-review"] != "unparseable" {
		t.Errorf("status: got %q; want unparseable", snap.ToolStatus["code-review"])
	}
	if snap.Metrics.CodeQuality.ReviewFindingsBySeverity != nil {
		t.Error("ReviewFindingsBySeverity should be nil for wrong-shape JSON")
	}
}

// --- --deep off → [S] collectors not invoked at all + Snapshot.Deep==false ---

func TestRunDeepCollectors_NotCalledWhenDeepOff(t *testing.T) {
	dir := t.TempDir()
	dr := newFakeDispatchRunner()
	dr.responses["code-reviewer"] = `{"findings_by_severity":{"P0":0,"P1":0,"P2":0,"P3":0}}`
	dr.responses["security-reviewer"] = `{"findings_by_severity":{"P0":0,"P1":0,"P2":0,"P3":0}}`

	runner := &fakeGitRunner{
		responses: map[string]string{
			"rev-parse HEAD":              "abc001",
			"rev-parse --abbrev-ref HEAD": "main",
			"tag --list":                  "",
		},
	}

	var buf strings.Builder
	cfg := Config{
		Subcommand:     "collect",
		Trigger:        "manual",
		NoWrite:        true,
		SkipAnalyzers:  true,
		Deep:           false, // --deep is OFF
		ProjectDir:     dir,
		StateDir:       dir,
		HomeDir:        dir,
		GitRunner:      runner,
		DispatchRunner: dr,
		Now:            func() time.Time { return time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC) },
		Writer:         &buf,
		ErrWriter:      &buf,
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Snapshot.Deep must be false.
	if res.Snapshot.Deep {
		t.Error("Snapshot.Deep should be false when --deep not set")
	}

	// [S] collectors must NOT have been invoked.
	if dr.called["code-reviewer"] > 0 {
		t.Errorf("code-reviewer should not have been dispatched; called %d times", dr.called["code-reviewer"])
	}
	if dr.called["security-reviewer"] > 0 {
		t.Errorf("security-reviewer should not have been dispatched; called %d times", dr.called["security-reviewer"])
	}

	// [S] fields must be nil.
	if res.Snapshot.Metrics.CodeQuality.ReviewFindingsBySeverity != nil {
		t.Error("ReviewFindingsBySeverity should be nil when --deep off")
	}
	if res.Snapshot.Metrics.Security.SecurityReviewFindingsBySeverity != nil {
		t.Error("SecurityReviewFindingsBySeverity should be nil when --deep off")
	}
}

// --- --deep on with all-failing dispatch → snapshot still written, [S] fields nil, exit 0 ---

func TestRunDeepCollectors_AllFailingDispatch_SnapshotStillWritten(t *testing.T) {
	dir := t.TempDir()
	dr := newFakeDispatchRunner()
	dr.errs["code-reviewer"] = fmt.Errorf("no runtime configured")
	dr.errs["security-reviewer"] = fmt.Errorf("no runtime configured")

	runner := &fakeGitRunner{
		responses: map[string]string{
			"rev-parse HEAD":              "abc002",
			"rev-parse --abbrev-ref HEAD": "main",
			"tag --list":                  "",
		},
	}

	var buf strings.Builder
	cfg := Config{
		Subcommand:     "collect",
		Trigger:        "manual",
		NoWrite:        false, // write to disk
		SkipAnalyzers:  true,
		Deep:           true, // --deep ON, but dispatch fails
		ProjectDir:     dir,
		StateDir:       dir,
		HomeDir:        dir,
		GitRunner:      runner,
		DispatchRunner: dr,
		Now:            func() time.Time { return time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC) },
		Writer:         &buf,
		ErrWriter:      &buf,
	}

	res, err := Run(cfg)
	// Must NOT return an error — dispatch failure is non-fatal.
	if err != nil {
		t.Fatalf("Run should not error on dispatch failure; got: %v", err)
	}

	// Snapshot.Deep must be true (--deep was set, even though collectors failed).
	if !res.Snapshot.Deep {
		t.Error("Snapshot.Deep should be true when --deep is set")
	}

	// [S] fields must be nil (dispatch failed).
	if res.Snapshot.Metrics.CodeQuality.ReviewFindingsBySeverity != nil {
		t.Error("ReviewFindingsBySeverity should be nil on dispatch failure")
	}
	if res.Snapshot.Metrics.Security.SecurityReviewFindingsBySeverity != nil {
		t.Error("SecurityReviewFindingsBySeverity should be nil on dispatch failure")
	}

	// Status entries must record the failure.
	if res.Snapshot.ToolStatus["code-review"] != "dispatch-failed" {
		t.Errorf("code-review status: got %q; want dispatch-failed", res.Snapshot.ToolStatus["code-review"])
	}
	if res.Snapshot.ToolStatus["security-review"] != "dispatch-failed" {
		t.Errorf("security-review status: got %q; want dispatch-failed", res.Snapshot.ToolStatus["security-review"])
	}

	// Snapshot must have been written to disk.
	snaps, readErr := ReadHistory(dir)
	if readErr != nil {
		t.Fatalf("ReadHistory: %v", readErr)
	}
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot in history; got %d", len(snaps))
	}
	if snaps[0].Commit != "abc002" {
		t.Errorf("snapshot commit: got %q; want abc002", snaps[0].Commit)
	}
}

// --- --deep on, all succeeding → fields populated ---

func TestRunDeepCollectors_AllSucceeding(t *testing.T) {
	dir := t.TempDir()
	dr := newFakeDispatchRunner()
	dr.responses["code-reviewer"] = `{"findings_by_severity":{"P0":0,"P1":1,"P2":4,"P3":9}}`
	dr.responses["security-reviewer"] = `{"findings_by_severity":{"P0":0,"P1":2,"P2":1,"P3":0}}`

	runner := &fakeGitRunner{
		responses: map[string]string{
			"rev-parse HEAD":              "abc003",
			"rev-parse --abbrev-ref HEAD": "main",
			"tag --list":                  "",
		},
	}

	var buf strings.Builder
	cfg := Config{
		Subcommand:     "collect",
		Trigger:        "release",
		NoWrite:        true,
		SkipAnalyzers:  true,
		Deep:           true,
		ProjectDir:     dir,
		StateDir:       dir,
		HomeDir:        dir,
		GitRunner:      runner,
		DispatchRunner: dr,
		Now:            func() time.Time { return time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC) },
		Writer:         &buf,
		ErrWriter:      &buf,
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !res.Snapshot.Deep {
		t.Error("Snapshot.Deep should be true")
	}

	cq := res.Snapshot.Metrics.CodeQuality.ReviewFindingsBySeverity
	if cq == nil {
		t.Fatal("ReviewFindingsBySeverity should not be nil")
	}
	if cq["P1"] != 1 {
		t.Errorf("P1: got %d; want 1", cq["P1"])
	}
	if cq["P2"] != 4 {
		t.Errorf("P2: got %d; want 4", cq["P2"])
	}

	sec := res.Snapshot.Metrics.Security.SecurityReviewFindingsBySeverity
	if sec == nil {
		t.Fatal("SecurityReviewFindingsBySeverity should not be nil")
	}
	if sec["P1"] != 2 {
		t.Errorf("P1: got %d; want 2", sec["P1"])
	}

	if res.Snapshot.ToolStatus["code-review"] != "ok" {
		t.Errorf("code-review status: got %q; want ok", res.Snapshot.ToolStatus["code-review"])
	}
	if res.Snapshot.ToolStatus["security-review"] != "ok" {
		t.Errorf("security-review status: got %q; want ok", res.Snapshot.ToolStatus["security-review"])
	}
}

// --- ParseArgs --deep flag ---

func TestParseArgs_Collect_Deep(t *testing.T) {
	cfg, err := ParseArgs([]string{"collect", "--deep", "--no-write"}, "/tmp")
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if !cfg.Deep {
		t.Error("Deep should be true when --deep flag is passed")
	}
	if !cfg.NoWrite {
		t.Error("NoWrite should be true")
	}
}

func TestParseArgs_Collect_NoDeepByDefault(t *testing.T) {
	cfg, err := ParseArgs([]string{"collect", "--no-write"}, "/tmp")
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if cfg.Deep {
		t.Error("Deep should be false by default")
	}
}

// --- nil dispatch runner → safe degradation ---

func TestRunDeepCollectors_NilRunner(t *testing.T) {
	snap := makeTestSnap()
	runDeepCollectors(nil, snap)

	if !snap.Deep {
		t.Error("Snapshot.Deep should still be true even when runner is nil")
	}
	if snap.ToolStatus["code-review"] != "dispatch-failed" {
		t.Errorf("code-review status: got %q; want dispatch-failed", snap.ToolStatus["code-review"])
	}
	if snap.ToolStatus["security-review"] != "dispatch-failed" {
		t.Errorf("security-review status: got %q; want dispatch-failed", snap.ToolStatus["security-review"])
	}
	if snap.Metrics.CodeQuality.ReviewFindingsBySeverity != nil {
		t.Error("ReviewFindingsBySeverity should be nil with nil runner")
	}
	if snap.Metrics.Security.SecurityReviewFindingsBySeverity != nil {
		t.Error("SecurityReviewFindingsBySeverity should be nil with nil runner")
	}
}

// --- schema: [S] fields default to nil ---

func TestDeepSchemaFields_NullWhenNotMeasured(t *testing.T) {
	m := &Metrics{}
	if m.CodeQuality.ReviewFindingsBySeverity != nil {
		t.Error("ReviewFindingsBySeverity should default to nil")
	}
	if m.Security.SecurityReviewFindingsBySeverity != nil {
		t.Error("SecurityReviewFindingsBySeverity should default to nil")
	}
}

// --- helpers ---

func makeTestSnap() *Snapshot {
	s := newSnapshot(
		time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC),
		"testcommit", "main", "manual", nil,
	)
	return &s
}
