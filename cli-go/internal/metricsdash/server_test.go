package metricsdash_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/metrics"
	"github.com/bakw00ds/yakos/internal/metricsdash"
)

// ---- test infrastructure ---------------------------------------------------

// newTestServer builds a metricsdash.Server backed by httptest.NewServer.
// projectDir may be "" for an empty history state.
func newTestServer(t *testing.T, projectDir string) (*httptest.Server, string) {
	t.Helper()
	stateDir := t.TempDir()
	tok, err := metricsdash.LoadOrCreateMetricsToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateMetricsToken: %v", err)
	}
	srv := metricsdash.New(metricsdash.Config{
		Token:      tok,
		ProjectDir: projectDir,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, tok
}

// newTestServerAllProjects builds a metricsdash.Server with AllProjects enabled.
func newTestServerAllProjects(t *testing.T, projectDir, homeDir string) (*httptest.Server, string) {
	t.Helper()
	stateDir := t.TempDir()
	tok, err := metricsdash.LoadOrCreateMetricsToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateMetricsToken: %v", err)
	}
	srv := metricsdash.New(metricsdash.Config{
		Token:       tok,
		ProjectDir:  projectDir,
		AllProjects: true,
		HomeDir:     homeDir,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, tok
}

func get(t *testing.T, url, tok string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response, dst interface{}) {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func drainClose(resp *http.Response) {
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

// writeFixtureHistory writes N snapshots to a temp project dir and returns
// the project dir path.
func writeFixtureHistory(t *testing.T, snaps []metrics.Snapshot) string {
	t.Helper()
	dir := t.TempDir()
	for _, s := range snaps {
		if err := metrics.AppendSnapshot(dir, s); err != nil {
			t.Fatalf("AppendSnapshot: %v", err)
		}
	}
	return dir
}

func floatPtr(f float64) *float64 { return &f }

func makeSnap(commit, branch string, ts time.Time, cost *float64) metrics.Snapshot {
	snap := metrics.Snapshot{
		Schema:  "yakos.metrics/v1",
		Commit:  commit,
		Branch:  branch,
		Ts:      ts,
		Trigger: "manual",
	}
	snap.Metrics.Efficiency.TotalCostUSD = cost
	return snap
}

// ---- static assets ---------------------------------------------------------

func TestIndex_Served(t *testing.T) {
	ts, _ := newTestServer(t, "")
	resp := get(t, ts.URL+"/", "")
	defer drainClose(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type=%q; want text/html", ct)
	}
}

func TestIndex_NotFoundOnSubpath(t *testing.T) {
	ts, _ := newTestServer(t, "")
	resp := get(t, ts.URL+"/nonexistent", "")
	defer drainClose(resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status=%d; want 404", resp.StatusCode)
	}
}

// ---- auth ------------------------------------------------------------------

func TestAllAPIEndpoints_RequireAuth(t *testing.T) {
	ts, _ := newTestServer(t, "")
	endpoints := []string{
		"/api/metrics/snapshot",
		"/api/metrics/history",
		"/api/metrics/trend",
		"/api/metrics/compare",
	}
	for _, ep := range endpoints {
		t.Run(ep, func(t *testing.T) {
			resp := get(t, ts.URL+ep, "")
			defer drainClose(resp)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("unauthed %s: status=%d; want 401", ep, resp.StatusCode)
			}
		})
	}
}

func TestAllAPIEndpoints_RejectBadToken(t *testing.T) {
	ts, _ := newTestServer(t, "")
	endpoints := []string{
		"/api/metrics/snapshot",
		"/api/metrics/history",
		"/api/metrics/trend",
		"/api/metrics/compare",
	}
	badTok := strings.Repeat("b", 64)
	for _, ep := range endpoints {
		t.Run(ep, func(t *testing.T) {
			resp := get(t, ts.URL+ep, badTok)
			defer drainClose(resp)
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("bad-token %s: status=%d; want 403", ep, resp.StatusCode)
			}
		})
	}
}

func TestAuth_BearerCaseInsensitive(t *testing.T) {
	ts, tok := newTestServer(t, "")
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/metrics/snapshot", nil)
	req.Header.Set("Authorization", "BEARER "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer drainClose(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200 (BEARER case-insensitive)", resp.StatusCode)
	}
}

func TestAuth_NoBearerPrefix_Is401(t *testing.T) {
	ts, tok := newTestServer(t, "")
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/metrics/snapshot", nil)
	req.Header.Set("Authorization", tok) // no "Bearer " prefix
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer drainClose(resp)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d; want 401 when Bearer prefix missing", resp.StatusCode)
	}
}

// ---- GET /api/metrics/snapshot ---------------------------------------------

func TestSnapshot_EmptyHistory_Returns200(t *testing.T) {
	ts, tok := newTestServer(t, "")
	resp := get(t, ts.URL+"/api/metrics/snapshot", tok)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200 for empty history", resp.StatusCode)
	}
	var result map[string]interface{}
	decodeJSON(t, resp, &result)
	if _, ok := result["message"]; !ok {
		t.Error("empty history should return {message: ...} not a crash")
	}
}

func TestSnapshot_WithHistory_ReturnsLatest(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	earlier := now.Add(-24 * time.Hour)
	projectDir := writeFixtureHistory(t, []metrics.Snapshot{
		makeSnap("aaa111", "main", earlier, floatPtr(1.50)),
		makeSnap("bbb222", "main", now, floatPtr(2.75)),
	})

	ts, tok := newTestServer(t, projectDir)
	resp := get(t, ts.URL+"/api/metrics/snapshot", tok)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200", resp.StatusCode)
	}

	var snap struct {
		Commit  string `json:"commit"`
		Metrics struct {
			Efficiency struct {
				TotalCostUSD *float64 `json:"total_cost_usd"`
			} `json:"efficiency"`
		} `json:"metrics"`
	}
	decodeJSON(t, resp, &snap)

	if !strings.HasPrefix(snap.Commit, "bbb222") {
		t.Errorf("commit=%q; want bbb222 (latest)", snap.Commit)
	}
	if snap.Metrics.Efficiency.TotalCostUSD == nil || *snap.Metrics.Efficiency.TotalCostUSD != 2.75 {
		t.Errorf("total_cost_usd=%v; want 2.75", snap.Metrics.Efficiency.TotalCostUSD)
	}
}

func TestSnapshot_ReturnsJSON(t *testing.T) {
	ts, tok := newTestServer(t, "")
	resp := get(t, ts.URL+"/api/metrics/snapshot", tok)
	defer drainClose(resp)
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type=%q; want application/json", ct)
	}
}

// ---- GET /api/metrics/history ----------------------------------------------

func TestHistory_EmptyHistory_Returns200WithEmptyArray(t *testing.T) {
	ts, tok := newTestServer(t, "")
	resp := get(t, ts.URL+"/api/metrics/history", tok)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200", resp.StatusCode)
	}
	var result []interface{}
	decodeJSON(t, resp, &result)
	if result == nil {
		t.Error("history should return [] not null for empty store")
	}
}

func TestHistory_ReturnsHeaders(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	projectDir := writeFixtureHistory(t, []metrics.Snapshot{
		makeSnap("ccc333", "main", now.Add(-time.Hour), nil),
		makeSnap("ddd444", "feat/x", now, nil),
	})

	ts, tok := newTestServer(t, projectDir)
	resp := get(t, ts.URL+"/api/metrics/history", tok)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200", resp.StatusCode)
	}

	var headers []struct {
		Commit string `json:"commit"`
		Ts     string `json:"ts"`
		Branch string `json:"branch"`
	}
	decodeJSON(t, resp, &headers)

	if len(headers) != 2 {
		t.Fatalf("got %d headers; want 2", len(headers))
	}
	// Check fields are populated
	if headers[0].Commit == "" {
		t.Error("header[0].commit should not be empty")
	}
	if headers[0].Ts == "" {
		t.Error("header[0].ts should not be empty")
	}
}

// ---- GET /api/metrics/trend ------------------------------------------------

func TestTrend_EmptyHistory_Returns200WithEmptyArray(t *testing.T) {
	ts, tok := newTestServer(t, "")
	resp := get(t, ts.URL+"/api/metrics/trend", tok)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200", resp.StatusCode)
	}
	var result []interface{}
	decodeJSON(t, resp, &result)
	if result == nil {
		t.Error("trend should return [] not null for empty history")
	}
}

func TestTrend_ReturnsPoints(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	projectDir := writeFixtureHistory(t, []metrics.Snapshot{
		makeSnap("e1", "main", now.Add(-2*time.Hour), floatPtr(1.0)),
		makeSnap("e2", "main", now.Add(-time.Hour), floatPtr(2.0)),
		makeSnap("e3", "main", now, floatPtr(3.0)),
	})

	ts, tok := newTestServer(t, projectDir)
	resp := get(t, ts.URL+"/api/metrics/trend?metric=efficiency.total_cost_usd&last=10", tok)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200", resp.StatusCode)
	}

	var points []struct {
		Commit string   `json:"commit"`
		Value  *float64 `json:"value"`
	}
	decodeJSON(t, resp, &points)

	if len(points) != 3 {
		t.Fatalf("got %d points; want 3", len(points))
	}
	if points[0].Value == nil || *points[0].Value != 1.0 {
		t.Errorf("points[0].value=%v; want 1.0", points[0].Value)
	}
}

func TestTrend_LastN_Limits(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	var snaps []metrics.Snapshot
	for i := 0; i < 20; i++ {
		snaps = append(snaps, makeSnap("sha"+string(rune('a'+i)), "main",
			now.Add(-time.Duration(20-i)*time.Hour), floatPtr(float64(i))))
	}
	projectDir := writeFixtureHistory(t, snaps)

	ts, tok := newTestServer(t, projectDir)
	resp := get(t, ts.URL+"/api/metrics/trend?last=5", tok)
	var points []interface{}
	decodeJSON(t, resp, &points)
	if len(points) != 5 {
		t.Errorf("got %d points with last=5; want 5", len(points))
	}
}

func TestTrend_DefaultMetric(t *testing.T) {
	ts, tok := newTestServer(t, "")
	resp := get(t, ts.URL+"/api/metrics/trend", tok)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200 with default metric", resp.StatusCode)
	}
	drainClose(resp)
}

// ---- GET /api/metrics/compare ----------------------------------------------

func TestCompare_MissingParams_Returns400(t *testing.T) {
	ts, tok := newTestServer(t, "")
	resp := get(t, ts.URL+"/api/metrics/compare", tok)
	defer drainClose(resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d; want 400 for missing a/b params", resp.StatusCode)
	}
}

func TestCompare_UnknownSHA_Returns404(t *testing.T) {
	projectDir := writeFixtureHistory(t, []metrics.Snapshot{
		makeSnap("abc123", "main", time.Now().UTC(), nil),
	})
	ts, tok := newTestServer(t, projectDir)
	resp := get(t, ts.URL+"/api/metrics/compare?a=abc123&b=zzz999", tok)
	defer drainClose(resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status=%d; want 404 for unknown sha", resp.StatusCode)
	}
}

func TestCompare_MatchingSnapshotsReturnsAAndB(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	projectDir := writeFixtureHistory(t, []metrics.Snapshot{
		makeSnap("f1111111", "main", now.Add(-time.Hour), floatPtr(1.0)),
		makeSnap("f2222222", "main", now, floatPtr(2.0)),
	})

	ts, tok := newTestServer(t, projectDir)
	resp := get(t, ts.URL+"/api/metrics/compare?a=f1111111&b=f2222222", tok)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200", resp.StatusCode)
	}

	var diff struct {
		A struct {
			Commit string `json:"commit"`
		} `json:"a"`
		B struct {
			Commit string `json:"commit"`
		} `json:"b"`
	}
	decodeJSON(t, resp, &diff)

	if !strings.HasPrefix(diff.A.Commit, "f1111111") {
		t.Errorf("diff.a.commit=%q; want f1111111", diff.A.Commit)
	}
	if !strings.HasPrefix(diff.B.Commit, "f2222222") {
		t.Errorf("diff.b.commit=%q; want f2222222", diff.B.Commit)
	}
}

// ---- read-only enforcement -------------------------------------------------

// TestReadOnly_NoEndpointMutatesHistory verifies that a round of authed
// requests leaves the history file byte-identical to before.
func TestReadOnly_NoEndpointMutatesHistory(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	projectDir := writeFixtureHistory(t, []metrics.Snapshot{
		makeSnap("g1abc", "main", now, floatPtr(1.23)),
	})

	histPath := filepath.Join(projectDir, ".yakos", "metrics", "history.ndjson")
	before, err := os.ReadFile(histPath)
	if err != nil {
		t.Fatalf("read history before: %v", err)
	}

	ts, tok := newTestServer(t, projectDir)

	endpoints := []string{
		"/api/metrics/snapshot",
		"/api/metrics/history",
		"/api/metrics/trend",
		"/api/metrics/compare?a=g1abc&b=g1abc",
	}
	for _, ep := range endpoints {
		resp := get(t, ts.URL+ep, tok)
		drainClose(resp)
	}

	after, err := os.ReadFile(histPath)
	if err != nil {
		t.Fatalf("read history after: %v", err)
	}
	if string(before) != string(after) {
		t.Error("history file was mutated by read-only serve endpoints")
	}
}

// TestNoWritePaths_PostRejected verifies that POST to API endpoints is rejected
// (GET-only routes via Go 1.22+ mux pattern matching).
func TestNoWritePaths_PostRejected(t *testing.T) {
	ts, tok := newTestServer(t, "")
	endpoints := []string{
		"/api/metrics/snapshot",
		"/api/metrics/history",
		"/api/metrics/trend",
		"/api/metrics/compare",
	}
	for _, ep := range endpoints {
		t.Run(ep, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodPost, ts.URL+ep, nil)
			req.Header.Set("Authorization", "Bearer "+tok)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("POST %s: %v", ep, err)
			}
			drainClose(resp)
			if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
				t.Errorf("POST %s: status=%d; expected non-2xx (endpoint is GET-only)", ep, resp.StatusCode)
			}
		})
	}
}

// ---- --host validation -----------------------------------------------------

func TestValidateAddr_LoopbackAccepted(t *testing.T) {
	// IPv6 loopback requires bracket notation: [::1]:port.
	addrs := []string{"127.0.0.1:7896", "[::1]:7896", "127.0.0.1"}
	for _, a := range addrs {
		if err := metricsdash.ValidateAddr(a); err != nil {
			t.Errorf("ValidateAddr(%q) = %v; want nil (loopback)", a, err)
		}
	}
}

func TestValidateAddr_NonLoopbackRefused(t *testing.T) {
	addrs := []string{"0.0.0.0:7896", "0.0.0.0", "192.168.1.1:7896"}
	for _, a := range addrs {
		if err := metricsdash.ValidateAddr(a); err == nil {
			t.Errorf("ValidateAddr(%q) = nil; want error for non-loopback", a)
		}
	}
}

// ---- token management ------------------------------------------------------

func TestLoadOrCreateMetricsToken_CreatesOnMissing(t *testing.T) {
	stateDir := t.TempDir()
	tok, err := metricsdash.LoadOrCreateMetricsToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateMetricsToken: %v", err)
	}
	if len(tok) != 64 {
		t.Errorf("token length=%d; want 64", len(tok))
	}
}

func TestLoadOrCreateMetricsToken_Idempotent(t *testing.T) {
	stateDir := t.TempDir()
	tok1, err := metricsdash.LoadOrCreateMetricsToken(stateDir)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	tok2, err := metricsdash.LoadOrCreateMetricsToken(stateDir)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if tok1 != tok2 {
		t.Error("LoadOrCreateMetricsToken should return the same token on repeated calls")
	}
}

func TestMetricsTokenFilePath(t *testing.T) {
	stateDir := t.TempDir()
	path := metricsdash.MetricsTokenFilePath(stateDir)
	if !strings.HasSuffix(path, "metrics-token") {
		t.Errorf("path=%q; should end in metrics-token", path)
	}
}

// ---- cross-project rollup --------------------------------------------------

func TestProjects_AllProjects_Returns200(t *testing.T) {
	homeDir := t.TempDir()
	ts, tok := newTestServerAllProjects(t, "", homeDir)
	resp := get(t, ts.URL+"/api/metrics/projects", tok)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200", resp.StatusCode)
	}
	var result []interface{}
	decodeJSON(t, resp, &result)
	if result == nil {
		t.Error("projects should return [] not null for empty agent-control")
	}
}

func TestProjects_EndpointAbsentWithoutFlag(t *testing.T) {
	ts, tok := newTestServer(t, "")
	resp := get(t, ts.URL+"/api/metrics/projects", tok)
	defer drainClose(resp)
	// Without --all-projects the route is not registered → 404 or 405.
	if resp.StatusCode == http.StatusOK {
		t.Error("projects endpoint should not be available without --all-projects flag")
	}
}

func TestProjects_DiscoveryReturnsProjectEntries(t *testing.T) {
	homeDir := t.TempDir()
	// Set up a fake agent-control/<proj>/.project-path pointing to a project
	// with a history file.
	projDir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	if err := metrics.AppendSnapshot(projDir, makeSnap("abc123", "main", now, floatPtr(0.42))); err != nil {
		t.Fatalf("AppendSnapshot: %v", err)
	}
	acDir := filepath.Join(homeDir, "agent-control", "my-project")
	if err := os.MkdirAll(acDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(acDir, ".project-path"), []byte(projDir+"\n"), 0644); err != nil {
		t.Fatalf("write .project-path: %v", err)
	}

	ts, tok := newTestServerAllProjects(t, "", homeDir)
	resp := get(t, ts.URL+"/api/metrics/projects", tok)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200", resp.StatusCode)
	}

	var projects []struct {
		Project       string `json:"project"`
		SnapshotCount int    `json:"snapshot_count"`
		Latest        *struct {
			Commit string `json:"commit"`
		} `json:"latest"`
	}
	decodeJSON(t, resp, &projects)

	if len(projects) != 1 {
		t.Fatalf("got %d projects; want 1", len(projects))
	}
	if projects[0].Project != "my-project" {
		t.Errorf("project=%q; want my-project", projects[0].Project)
	}
	if projects[0].SnapshotCount != 1 {
		t.Errorf("snapshot_count=%d; want 1", projects[0].SnapshotCount)
	}
	if projects[0].Latest == nil || !strings.HasPrefix(projects[0].Latest.Commit, "abc123") {
		t.Errorf("latest.commit=%v; want abc123", projects[0].Latest)
	}
}

// ---- analytics unit tests --------------------------------------------------

func TestHistoryHeaders_Empty(t *testing.T) {
	headers := metricsdash.HistoryHeaders(nil)
	if len(headers) != 0 {
		t.Errorf("HistoryHeaders(nil) = %d items; want 0", len(headers))
	}
}

func TestHistoryHeaders_PopulatesFields(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	snaps := []metrics.Snapshot{
		makeSnap("sha1", "main", now, nil),
		makeSnap("sha2", "dev", now.Add(time.Hour), nil),
	}
	headers := metricsdash.HistoryHeaders(snaps)
	if len(headers) != 2 {
		t.Fatalf("got %d headers; want 2", len(headers))
	}
	if headers[0].Commit != "sha1" {
		t.Errorf("commit=%q; want sha1", headers[0].Commit)
	}
	if headers[1].Branch != "dev" {
		t.Errorf("branch=%q; want dev", headers[1].Branch)
	}
}

func TestLatestSnapshot_Nil(t *testing.T) {
	if got := metricsdash.LatestSnapshot(nil); got != nil {
		t.Errorf("LatestSnapshot(nil) = %v; want nil", got)
	}
}

func TestLatestSnapshot_ReturnsLast(t *testing.T) {
	now := time.Now().UTC()
	snaps := []metrics.Snapshot{
		makeSnap("s1", "main", now, nil),
		makeSnap("s2", "main", now.Add(time.Hour), nil),
	}
	got := metricsdash.LatestSnapshot(snaps)
	if got == nil || got.Commit != "s2" {
		t.Errorf("LatestSnapshot = %v; want commit s2", got)
	}
}

func TestTrendSeries_Empty(t *testing.T) {
	pts := metricsdash.TrendSeries(nil, "efficiency.total_cost_usd", 10, "")
	if len(pts) != 0 {
		t.Errorf("TrendSeries(nil) = %d points; want 0", len(pts))
	}
}

func TestTrendSeries_LastNLimit(t *testing.T) {
	now := time.Now().UTC()
	var snaps []metrics.Snapshot
	for i := 0; i < 15; i++ {
		snaps = append(snaps, makeSnap("s", "main", now.Add(time.Duration(i)*time.Hour), floatPtr(float64(i))))
	}
	pts := metricsdash.TrendSeries(snaps, "efficiency.total_cost_usd", 5, "")
	if len(pts) != 5 {
		t.Errorf("TrendSeries with lastN=5 returned %d points; want 5", len(pts))
	}
}

func TestCompareSnapshots_ByPrefix(t *testing.T) {
	now := time.Now().UTC()
	snaps := []metrics.Snapshot{
		makeSnap("aabbcc", "main", now, nil),
		makeSnap("ddeeff", "main", now.Add(time.Hour), nil),
	}
	a, b := metricsdash.CompareSnapshots(snaps, "aabb", "ddee")
	if a == nil || !strings.HasPrefix(a.Commit, "aabb") {
		t.Errorf("a.commit=%v; want aabbcc", a)
	}
	if b == nil || !strings.HasPrefix(b.Commit, "ddee") {
		t.Errorf("b.commit=%v; want ddeeff", b)
	}
}

func TestCompareSnapshots_NotFound(t *testing.T) {
	now := time.Now().UTC()
	snaps := []metrics.Snapshot{makeSnap("aabbcc", "main", now, nil)}
	a, b := metricsdash.CompareSnapshots(snaps, "aabb", "zzz")
	if a == nil {
		t.Error("a should be found")
	}
	if b != nil {
		t.Error("b should be nil for unknown sha")
	}
}

// ---- security nit fixes (post-review) --------------------------------------

// TestAPIReturnsRawBranch verifies that the JSON API returns branch names
// verbatim (including special characters like <script>). Escaping is the
// SPA's responsibility; the Go API must not alter the stored data.
func TestAPIReturnsRawBranch(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	dangerousBranch := `feat/<script>alert(1)</script>`
	projectDir := writeFixtureHistory(t, []metrics.Snapshot{
		makeSnap("xss111", dangerousBranch, now, nil),
	})

	ts, tok := newTestServer(t, projectDir)

	// /api/metrics/snapshot should return the raw branch string.
	resp := get(t, ts.URL+"/api/metrics/snapshot", tok)
	var snap struct {
		Branch string `json:"branch"`
	}
	decodeJSON(t, resp, &snap)
	if snap.Branch != dangerousBranch {
		t.Errorf("snapshot branch=%q; want raw %q (API must not alter stored data)", snap.Branch, dangerousBranch)
	}

	// /api/metrics/history should also return it verbatim.
	resp2 := get(t, ts.URL+"/api/metrics/history", tok)
	var headers []struct {
		Branch string `json:"branch"`
	}
	decodeJSON(t, resp2, &headers)
	if len(headers) != 1 || headers[0].Branch != dangerousBranch {
		t.Errorf("history[0].branch=%q; want raw %q", headers[0].Branch, dangerousBranch)
	}
}

// TestPortBound_ZeroRejected verifies that --port 0 is refused with a clear
// error (port 0 is technically valid for "pick any port" but not appropriate
// for a user-facing dashboard where the URL must be known up front).
func TestPortBound_ZeroRejected(t *testing.T) {
	_, err := metrics.ParseArgs([]string{"serve", "--port", "0"}, "/tmp")
	if err == nil {
		t.Error("ParseArgs(serve --port 0) should return error; port 0 out of [1,65535]")
	}
}

// TestPortBound_65536Rejected verifies that --port 65536 is refused.
func TestPortBound_65536Rejected(t *testing.T) {
	_, err := metrics.ParseArgs([]string{"serve", "--port", "65536"}, "/tmp")
	if err == nil {
		t.Error("ParseArgs(serve --port 65536) should return error; out of [1,65535]")
	}
}

// TestPortBound_ValidPort verifies that a valid port is accepted.
func TestPortBound_ValidPort(t *testing.T) {
	cfg, err := metrics.ParseArgs([]string{"serve", "--port", "8080"}, "/tmp")
	if err != nil {
		t.Errorf("ParseArgs(serve --port 8080) unexpected error: %v", err)
	}
	if cfg.ServePort != 8080 {
		t.Errorf("ServePort=%d; want 8080", cfg.ServePort)
	}
}

// TestValidateAddr_HostnameRefused verifies that a bare hostname is now
// refused outright (fix 4: honest early check).
func TestValidateAddr_HostnameRefused(t *testing.T) {
	hostnames := []string{"localhost", "localhost:7896", "myhost.local:9000"}
	for _, h := range hostnames {
		if err := metricsdash.ValidateAddr(h); err == nil {
			t.Errorf("ValidateAddr(%q) = nil; want error — hostnames must be refused", h)
		}
	}
}

// ---- HandlerNoToken (session-console mount) ---------------------------------
// These tests verify the session-mode path: mounted via HandlerNoToken() the
// API endpoints respond 200 without any Authorization header, mirroring what
// the networked session console does (auth is enforced at the outer edge).

func newTestServerNoToken(t *testing.T, projectDir string) *httptest.Server {
	t.Helper()
	stateDir := t.TempDir()
	tok, err := metricsdash.LoadOrCreateMetricsToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateMetricsToken: %v", err)
	}
	srv := metricsdash.New(metricsdash.Config{
		Token:      tok,
		ProjectDir: projectDir,
	})
	ts := httptest.NewServer(srv.HandlerNoToken())
	t.Cleanup(ts.Close)
	return ts
}

func TestHandlerNoToken_SnapshotServedWithoutBearer(t *testing.T) {
	ts := newTestServerNoToken(t, "")
	resp := get(t, ts.URL+"/api/metrics/snapshot", "") // no token
	defer drainClose(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("HandlerNoToken snapshot: status=%d; want 200 (no bearer required)", resp.StatusCode)
	}
}

func TestHandlerNoToken_AllEndpointsReachable(t *testing.T) {
	ts := newTestServerNoToken(t, "")
	endpoints := []string{
		"/api/metrics/snapshot",
		"/api/metrics/history",
		"/api/metrics/trend",
	}
	for _, ep := range endpoints {
		t.Run(ep, func(t *testing.T) {
			resp := get(t, ts.URL+ep, "") // no token
			defer drainClose(resp)
			if resp.StatusCode != http.StatusOK {
				t.Errorf("HandlerNoToken %s: status=%d; want 200", ep, resp.StatusCode)
			}
		})
	}
}

func TestHandlerNoToken_IndexServed(t *testing.T) {
	ts := newTestServerNoToken(t, "")
	resp := get(t, ts.URL+"/", "")
	defer drainClose(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("HandlerNoToken /: status=%d; want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type=%q; want text/html", ct)
	}
}

// TestHandlerNoToken_CompareReturns400OnMissingParams verifies that the
// compare endpoint still validates its required query params even without auth.
func TestHandlerNoToken_CompareReturns400OnMissingParams(t *testing.T) {
	ts := newTestServerNoToken(t, "")
	resp := get(t, ts.URL+"/api/metrics/compare", "") // no token, no a/b params
	defer drainClose(resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("HandlerNoToken compare: status=%d; want 400 (missing params)", resp.StatusCode)
	}
}

// TestProjects_NoHistoryPath verifies that history_path is absent from the
// /api/metrics/projects JSON response (fix 3: info-leak removal).
func TestProjects_NoHistoryPath(t *testing.T) {
	homeDir := t.TempDir()
	projDir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	if err := metrics.AppendSnapshot(projDir, makeSnap("abc999", "main", now, nil)); err != nil {
		t.Fatalf("AppendSnapshot: %v", err)
	}
	acDir := filepath.Join(homeDir, "agent-control", "test-proj")
	if err := os.MkdirAll(acDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(acDir, ".project-path"), []byte(projDir+"\n"), 0644); err != nil {
		t.Fatalf("write .project-path: %v", err)
	}

	ts, tok := newTestServerAllProjects(t, "", homeDir)
	resp := get(t, ts.URL+"/api/metrics/projects", tok)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200", resp.StatusCode)
		drainClose(resp)
		return
	}

	// Decode as a slice of raw JSON objects so we can inspect all keys.
	var raw []map[string]json.RawMessage
	decodeJSON(t, resp, &raw)
	if len(raw) != 1 {
		t.Fatalf("got %d projects; want 1", len(raw))
	}
	if _, present := raw[0]["history_path"]; present {
		t.Error("history_path must not be present in /api/metrics/projects response (info leak)")
	}
	// Confirm expected fields are still there.
	for _, field := range []string{"project", "snapshot_count"} {
		if _, present := raw[0][field]; !present {
			t.Errorf("expected field %q missing from projects response", field)
		}
	}
}
