package perfdash_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/perfdash"
)

// ---- test infrastructure ---------------------------------------------------

// newTestServer builds a perfdash.Server backed by httptest.NewServer.
// Returns the test server and the bearer token.
func newTestServer(t *testing.T, workDir string) (*httptest.Server, string) {
	t.Helper()
	stateDir := t.TempDir()
	tok, err := perfdash.LoadOrCreatePerfToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreatePerfToken: %v", err)
	}
	if workDir == "" {
		workDir = t.TempDir() // empty — no log files
	}
	srv := perfdash.New(perfdash.Config{
		Token:   tok,
		WorkDir: workDir,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, tok
}

// get issues a GET to url with the given bearer token (empty = no header).
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

// decodeJSON reads and JSON-decodes resp.Body into dst.
func decodeJSON(t *testing.T, resp *http.Response, dst interface{}) {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

// drainClose reads and discards resp.Body then closes it.
func drainClose(resp *http.Response) {
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

// writeDispatchLog writes a minimal dispatch-log.ndjson to dir with n events.
func writeDispatchLog(t *testing.T, dir string, events []string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "dispatch-log.ndjson")
	content := strings.Join(events, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	return path
}

// sampleEvent builds a dispatch_finished JSON line with the given fields.
func sampleEvent(ts, agent, runtime string, exitCode int, durationS float64, costUSD float64) string {
	inTok := int64(costUSD / 3.0 * 1_000_000)
	return fmt.Sprintf(
		`{"type":"dispatch_finished","ts":%q,"agent":%q,"runtime":%q,"project":"test-project","exit_code":%d,"duration_s":%f,"est_input_tokens":%d,"est_output_tokens":100}`,
		ts, agent, runtime, exitCode, durationS, inTok,
	)
}

func nowISO() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func hoursAgoISO(h int) string {
	return time.Now().UTC().Add(-time.Duration(h) * time.Hour).Format(time.RFC3339)
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

func TestAppJS_Served(t *testing.T) {
	ts, _ := newTestServer(t, "")
	resp := get(t, ts.URL+"/app.js", "")
	defer drainClose(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/javascript") {
		t.Errorf("Content-Type=%q; want application/javascript", ct)
	}
}

func TestCSS_Served(t *testing.T) {
	ts, _ := newTestServer(t, "")
	resp := get(t, ts.URL+"/styles.css", "")
	defer drainClose(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/css") {
		t.Errorf("Content-Type=%q; want text/css", ct)
	}
}

// ---- auth ------------------------------------------------------------------

func TestSummary_RequiresAuth(t *testing.T) {
	ts, _ := newTestServer(t, "")
	resp := get(t, ts.URL+"/api/perf/summary", "")
	defer drainClose(resp)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d; want 401", resp.StatusCode)
	}
}

func TestSummary_RejectsBadToken(t *testing.T) {
	ts, _ := newTestServer(t, "")
	resp := get(t, ts.URL+"/api/perf/summary", strings.Repeat("a", 64))
	defer drainClose(resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status=%d; want 403", resp.StatusCode)
	}
}

func TestTimeseries_RequiresAuth(t *testing.T) {
	ts, _ := newTestServer(t, "")
	resp := get(t, ts.URL+"/api/perf/timeseries", "")
	defer drainClose(resp)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d; want 401", resp.StatusCode)
	}
}

func TestByAxis_RequiresAuth(t *testing.T) {
	ts, _ := newTestServer(t, "")
	resp := get(t, ts.URL+"/api/perf/by_axis", "")
	defer drainClose(resp)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d; want 401", resp.StatusCode)
	}
}

func TestRecent_RequiresAuth(t *testing.T) {
	ts, _ := newTestServer(t, "")
	resp := get(t, ts.URL+"/api/perf/recent", "")
	defer drainClose(resp)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d; want 401", resp.StatusCode)
	}
}

func TestAuth_BearerCaseInsensitive(t *testing.T) {
	ts, tok := newTestServer(t, "")
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/perf/summary", nil)
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

func TestAuth_NoBearerPrefix(t *testing.T) {
	ts, tok := newTestServer(t, "")
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/perf/summary", nil)
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

// ---- GET /api/perf/summary -------------------------------------------------

func TestSummary_EmptyLog(t *testing.T) {
	ts, tok := newTestServer(t, "")
	resp := get(t, ts.URL+"/api/perf/summary", tok)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200", resp.StatusCode)
	}
	var result struct {
		TotalDispatches int64 `json:"total_dispatches"`
	}
	decodeJSON(t, resp, &result)
	if result.TotalDispatches != 0 {
		t.Errorf("total_dispatches=%d; want 0 for empty log", result.TotalDispatches)
	}
}

func TestSummary_WithEvents(t *testing.T) {
	dir := t.TempDir()
	writeDispatchLog(t, dir, []string{
		sampleEvent(nowISO(), "backend", "claude", 0, 120.0, 0.05),
		sampleEvent(nowISO(), "frontend", "claude", 0, 60.0, 0.02),
		sampleEvent(nowISO(), "backend", "gemini", 1, 30.0, 0.01),
	})

	ts, tok := newTestServer(t, dir)
	resp := get(t, ts.URL+"/api/perf/summary?window=24h", tok)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200", resp.StatusCode)
	}

	var result struct {
		TotalDispatches int64   `json:"total_dispatches"`
		TotalCostUSD    float64 `json:"total_cost_usd"`
		AvgLatencyMs    int64   `json:"avg_latency_ms"`
		P50LatencyMs    int64   `json:"p50_latency_ms"`
		P95LatencyMs    int64   `json:"p95_latency_ms"`
		TopAgents       []struct {
			Key        string `json:"key"`
			Dispatches int64  `json:"dispatches"`
		} `json:"top_agents"`
		TopRuntimes []struct {
			Key        string `json:"key"`
			Dispatches int64  `json:"dispatches"`
		} `json:"top_runtimes"`
	}
	decodeJSON(t, resp, &result)

	if result.TotalDispatches != 3 {
		t.Errorf("total_dispatches=%d; want 3", result.TotalDispatches)
	}
	if len(result.TopAgents) == 0 {
		t.Error("top_agents should not be empty")
	}
	if len(result.TopRuntimes) == 0 {
		t.Error("top_runtimes should not be empty")
	}
	// Avg latency should be positive (we have durations).
	if result.AvgLatencyMs == 0 {
		t.Error("avg_latency_ms should be > 0")
	}
	if result.P95LatencyMs == 0 {
		t.Error("p95_latency_ms should be > 0")
	}
}

func TestSummary_TopAgentsOrdering(t *testing.T) {
	dir := t.TempDir()
	writeDispatchLog(t, dir, []string{
		sampleEvent(nowISO(), "backend", "claude", 0, 10, 0.01),
		sampleEvent(nowISO(), "backend", "claude", 0, 10, 0.01),
		sampleEvent(nowISO(), "backend", "claude", 0, 10, 0.01),
		sampleEvent(nowISO(), "frontend", "claude", 0, 10, 0.01),
	})
	ts, tok := newTestServer(t, dir)
	resp := get(t, ts.URL+"/api/perf/summary?window=24h", tok)
	var result struct {
		TopAgents []struct{ Key string `json:"key"` } `json:"top_agents"`
	}
	decodeJSON(t, resp, &result)
	if len(result.TopAgents) < 2 {
		t.Fatalf("expected at least 2 top_agents; got %d", len(result.TopAgents))
	}
	if result.TopAgents[0].Key != "backend" {
		t.Errorf("top_agents[0]=%q; want backend (most dispatches)", result.TopAgents[0].Key)
	}
}

func TestSummary_WindowFilter(t *testing.T) {
	dir := t.TempDir()
	writeDispatchLog(t, dir, []string{
		sampleEvent(nowISO(), "backend", "claude", 0, 10, 0.01),       // in window
		sampleEvent(hoursAgoISO(48), "backend", "claude", 0, 10, 0.01), // outside 24h
	})
	ts, tok := newTestServer(t, dir)
	resp := get(t, ts.URL+"/api/perf/summary?window=24h", tok)
	var result struct {
		TotalDispatches int64 `json:"total_dispatches"`
	}
	decodeJSON(t, resp, &result)
	if result.TotalDispatches != 1 {
		t.Errorf("total_dispatches=%d; want 1 (window filter should exclude old event)", result.TotalDispatches)
	}
}

func TestSummary_ReturnsJSON(t *testing.T) {
	ts, tok := newTestServer(t, "")
	resp := get(t, ts.URL+"/api/perf/summary", tok)
	defer drainClose(resp)
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type=%q; want application/json", ct)
	}
}

// ---- GET /api/perf/timeseries -----------------------------------------------

func TestTimeseries_EmptyLog(t *testing.T) {
	ts, tok := newTestServer(t, "")
	resp := get(t, ts.URL+"/api/perf/timeseries?window=24h&bucket=hour&metric=dispatches", tok)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200", resp.StatusCode)
	}
	var result []interface{}
	decodeJSON(t, resp, &result)
	if result == nil {
		t.Error("timeseries should return [] not null for empty log")
	}
}

func TestTimeseries_AllMetrics(t *testing.T) {
	dir := t.TempDir()
	writeDispatchLog(t, dir, []string{
		sampleEvent(nowISO(), "backend", "claude", 0, 60.0, 0.05),
	})
	ts, tok := newTestServer(t, dir)

	for _, metric := range []string{"dispatches", "cost", "latency"} {
		t.Run(metric, func(t *testing.T) {
			resp := get(t, ts.URL+"/api/perf/timeseries?window=24h&bucket=hour&metric="+metric, tok)
			if resp.StatusCode != http.StatusOK {
				t.Errorf("metric=%s: status=%d; want 200", metric, resp.StatusCode)
			}
			drainClose(resp)
		})
	}
}

func TestTimeseries_InvalidMetric(t *testing.T) {
	ts, tok := newTestServer(t, "")
	resp := get(t, ts.URL+"/api/perf/timeseries?metric=bogus", tok)
	defer drainClose(resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d; want 400 for invalid metric", resp.StatusCode)
	}
}

func TestTimeseries_BucketHour(t *testing.T) {
	dir := t.TempDir()
	writeDispatchLog(t, dir, []string{
		sampleEvent(nowISO(), "backend", "claude", 0, 10, 0.01),
		sampleEvent(hoursAgoISO(1), "backend", "claude", 0, 10, 0.01),
	})
	ts, tok := newTestServer(t, dir)
	resp := get(t, ts.URL+"/api/perf/timeseries?window=6h&bucket=hour&metric=dispatches", tok)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200", resp.StatusCode)
	}
	var points []struct {
		Ts    string  `json:"ts"`
		Value float64 `json:"value"`
	}
	decodeJSON(t, resp, &points)
	// At least 2 buckets worth of coverage.
	if len(points) < 2 {
		t.Errorf("expected at least 2 buckets for 6h window hourly; got %d", len(points))
	}
}

func TestTimeseries_BucketDay(t *testing.T) {
	ts, tok := newTestServer(t, "")
	resp := get(t, ts.URL+"/api/perf/timeseries?window=7d&bucket=day&metric=dispatches", tok)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200", resp.StatusCode)
	}
	var points []interface{}
	decodeJSON(t, resp, &points)
	if len(points) < 7 {
		t.Errorf("expected at least 7 daily buckets; got %d", len(points))
	}
}

func TestTimeseries_DefaultMetric(t *testing.T) {
	ts, tok := newTestServer(t, "")
	// No metric param → defaults to dispatches.
	resp := get(t, ts.URL+"/api/perf/timeseries?window=24h&bucket=hour", tok)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200", resp.StatusCode)
	}
	drainClose(resp)
}

func TestTimeseries_ReturnsJSON(t *testing.T) {
	ts, tok := newTestServer(t, "")
	resp := get(t, ts.URL+"/api/perf/timeseries", tok)
	defer drainClose(resp)
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type=%q; want application/json", ct)
	}
}

// ---- GET /api/perf/by_axis --------------------------------------------------

func TestByAxis_AllAxes(t *testing.T) {
	dir := t.TempDir()
	writeDispatchLog(t, dir, []string{
		sampleEvent(nowISO(), "backend", "claude", 0, 60.0, 0.05),
		sampleEvent(nowISO(), "frontend", "gemini", 0, 30.0, 0.02),
	})
	ts, tok := newTestServer(t, dir)

	for _, axis := range []string{"agent", "runtime", "project", "day"} {
		t.Run(axis, func(t *testing.T) {
			resp := get(t, ts.URL+"/api/perf/by_axis?axis="+axis+"&window=24h", tok)
			if resp.StatusCode != http.StatusOK {
				t.Errorf("axis=%s: status=%d; want 200", axis, resp.StatusCode)
			}
			drainClose(resp)
		})
	}
}

func TestByAxis_InvalidAxis(t *testing.T) {
	ts, tok := newTestServer(t, "")
	resp := get(t, ts.URL+"/api/perf/by_axis?axis=bogus", tok)
	defer drainClose(resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d; want 400 for invalid axis", resp.StatusCode)
	}
}

func TestByAxis_EmptyLog(t *testing.T) {
	ts, tok := newTestServer(t, "")
	resp := get(t, ts.URL+"/api/perf/by_axis?axis=agent", tok)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200", resp.StatusCode)
	}
	var result []interface{}
	decodeJSON(t, resp, &result)
	if result == nil {
		t.Error("by_axis should return [] not null for empty log")
	}
}

func TestByAxis_RowShape(t *testing.T) {
	dir := t.TempDir()
	writeDispatchLog(t, dir, []string{
		sampleEvent(nowISO(), "backend", "claude", 0, 60.0, 0.05),
		sampleEvent(nowISO(), "backend", "claude", 0, 120.0, 0.08),
	})
	ts, tok := newTestServer(t, dir)
	resp := get(t, ts.URL+"/api/perf/by_axis?axis=agent&window=24h", tok)
	var rows []struct {
		Key          string  `json:"key"`
		Dispatches   int64   `json:"dispatches"`
		CostUSD      float64 `json:"cost_usd"`
		AvgLatencyMs int64   `json:"avg_latency_ms"`
		P95LatencyMs int64   `json:"p95_latency_ms"`
	}
	decodeJSON(t, resp, &rows)
	if len(rows) != 1 {
		t.Fatalf("rows=%d; want 1 (only 'backend')", len(rows))
	}
	if rows[0].Key != "backend" {
		t.Errorf("key=%q; want backend", rows[0].Key)
	}
	if rows[0].Dispatches != 2 {
		t.Errorf("dispatches=%d; want 2", rows[0].Dispatches)
	}
	if rows[0].AvgLatencyMs == 0 {
		t.Error("avg_latency_ms should be > 0")
	}
	if rows[0].P95LatencyMs == 0 {
		t.Error("p95_latency_ms should be > 0")
	}
}

func TestByAxis_ReturnsJSON(t *testing.T) {
	ts, tok := newTestServer(t, "")
	resp := get(t, ts.URL+"/api/perf/by_axis", tok)
	defer drainClose(resp)
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type=%q; want application/json", ct)
	}
}

// ---- GET /api/perf/recent ---------------------------------------------------

func TestRecent_EmptyLog(t *testing.T) {
	ts, tok := newTestServer(t, "")
	resp := get(t, ts.URL+"/api/perf/recent", tok)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200", resp.StatusCode)
	}
	var result []interface{}
	decodeJSON(t, resp, &result)
	if result == nil {
		t.Error("recent should return [] not null for empty log")
	}
}

func TestRecent_ReturnsRows(t *testing.T) {
	dir := t.TempDir()
	writeDispatchLog(t, dir, []string{
		sampleEvent(nowISO(), "backend", "claude", 0, 60.0, 0.05),
		sampleEvent(nowISO(), "frontend", "gemini", 1, 30.0, 0.02),
		sampleEvent(nowISO(), "qa", "claude", 0, 10.0, 0.01),
	})
	ts, tok := newTestServer(t, dir)
	resp := get(t, ts.URL+"/api/perf/recent?limit=50", tok)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d; want 200", resp.StatusCode)
	}
	var rows []struct {
		Agent    string  `json:"agent"`
		Runtime  string  `json:"runtime"`
		ExitCode int     `json:"exit_code"`
		CostUSD  float64 `json:"cost_usd"`
	}
	decodeJSON(t, resp, &rows)
	if len(rows) != 3 {
		t.Errorf("rows=%d; want 3", len(rows))
	}
}

func TestRecent_LimitParam(t *testing.T) {
	dir := t.TempDir()
	events := make([]string, 10)
	for i := range events {
		events[i] = sampleEvent(nowISO(), fmt.Sprintf("agent%d", i), "claude", 0, 10.0, 0.01)
	}
	writeDispatchLog(t, dir, events)
	ts, tok := newTestServer(t, dir)
	resp := get(t, ts.URL+"/api/perf/recent?limit=3", tok)
	var rows []interface{}
	decodeJSON(t, resp, &rows)
	if len(rows) != 3 {
		t.Errorf("rows=%d; want 3 (limit=3)", len(rows))
	}
}

func TestRecent_RowShape(t *testing.T) {
	dir := t.TempDir()
	writeDispatchLog(t, dir, []string{
		sampleEvent(nowISO(), "backend", "claude", 0, 90.0, 0.05),
	})
	ts, tok := newTestServer(t, dir)
	resp := get(t, ts.URL+"/api/perf/recent?limit=1", tok)
	var rows []struct {
		Ts        string  `json:"ts"`
		Agent     string  `json:"agent"`
		Runtime   string  `json:"runtime"`
		Project   string  `json:"project"`
		ExitCode  int     `json:"exit_code"`
		DurationS float64 `json:"duration_s"`
		CostUSD   float64 `json:"cost_usd"`
		LatencyMs int64   `json:"latency_ms"`
	}
	decodeJSON(t, resp, &rows)
	if len(rows) != 1 {
		t.Fatalf("rows=%d; want 1", len(rows))
	}
	r := rows[0]
	if r.Agent != "backend" {
		t.Errorf("agent=%q; want backend", r.Agent)
	}
	if r.Runtime != "claude" {
		t.Errorf("runtime=%q; want claude", r.Runtime)
	}
	if r.Project != "test-project" {
		t.Errorf("project=%q; want test-project", r.Project)
	}
	if r.ExitCode != 0 {
		t.Errorf("exit_code=%d; want 0", r.ExitCode)
	}
	if r.LatencyMs == 0 {
		t.Error("latency_ms should be > 0")
	}
	if r.Ts == "" {
		t.Error("ts should not be empty")
	}
}

func TestRecent_ReturnsJSON(t *testing.T) {
	ts, tok := newTestServer(t, "")
	resp := get(t, ts.URL+"/api/perf/recent", tok)
	defer drainClose(resp)
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type=%q; want application/json", ct)
	}
}

// ---- read-only enforcement --------------------------------------------------
// The dashboard must not expose any write path. Verify all methods are GET-only.

func TestNoWritePaths_PostRejected(t *testing.T) {
	ts, tok := newTestServer(t, "")
	endpoints := []string{
		"/api/perf/summary",
		"/api/perf/timeseries",
		"/api/perf/by_axis",
		"/api/perf/recent",
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
			// The Go 1.22+ mux only registers GET patterns, so POST returns 405.
			if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
				t.Errorf("POST %s: status=%d; expected non-2xx (endpoint is GET-only)", ep, resp.StatusCode)
			}
		})
	}
}

// ---- token management -------------------------------------------------------

func TestLoadOrCreatePerfToken_CreatesOnMissing(t *testing.T) {
	stateDir := t.TempDir()
	tok, err := perfdash.LoadOrCreatePerfToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreatePerfToken: %v", err)
	}
	if len(tok) != 64 {
		t.Errorf("token length=%d; want 64", len(tok))
	}
}

func TestLoadOrCreatePerfToken_Idempotent(t *testing.T) {
	stateDir := t.TempDir()
	tok1, err := perfdash.LoadOrCreatePerfToken(stateDir)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	tok2, err := perfdash.LoadOrCreatePerfToken(stateDir)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if tok1 != tok2 {
		t.Error("LoadOrCreatePerfToken should return the same token on repeated calls")
	}
}

func TestRotatePerfToken_ChangesToken(t *testing.T) {
	stateDir := t.TempDir()
	tok1, err := perfdash.LoadOrCreatePerfToken(stateDir)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	tok2, err := perfdash.RotatePerfToken(stateDir)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if tok1 == tok2 {
		t.Error("RotatePerfToken should produce a different token")
	}
}

func TestRotatePerfToken_NewTokenIsLoaded(t *testing.T) {
	stateDir := t.TempDir()
	_, _ = perfdash.LoadOrCreatePerfToken(stateDir)
	rotated, err := perfdash.RotatePerfToken(stateDir)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	loaded, err := perfdash.LoadOrCreatePerfToken(stateDir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if rotated != loaded {
		t.Error("after RotatePerfToken, LoadOrCreatePerfToken should return the new token")
	}
}

func TestPerfTokenFilePath(t *testing.T) {
	stateDir := t.TempDir()
	path := perfdash.PerfTokenFilePath(stateDir)
	if path == "" {
		t.Error("PerfTokenFilePath should not be empty")
	}
	if !strings.HasSuffix(path, "perf-token") {
		t.Errorf("path=%q; should end in perf-token", path)
	}
}

// ---- DefaultWorkDir / DefaultStateDir helpers --------------------------------

func TestDefaultWorkDir(t *testing.T) {
	root := "/some/workspace"
	dir := perfdash.DefaultWorkDir(root)
	if !strings.HasSuffix(dir, "work/current") {
		t.Errorf("DefaultWorkDir=%q; should end in work/current", dir)
	}
}

func TestDefaultStateDir(t *testing.T) {
	dir := perfdash.DefaultStateDir()
	if dir == "" {
		t.Error("DefaultStateDir should not be empty")
	}
}

// ---- concurrent access -----------------------------------------------------

func TestSummary_ConcurrentRequests(t *testing.T) {
	dir := t.TempDir()
	writeDispatchLog(t, dir, []string{
		sampleEvent(nowISO(), "backend", "claude", 0, 10.0, 0.01),
	})
	ts, tok := newTestServer(t, dir)

	const n = 20
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			resp, err := http.Get(ts.URL + "/api/perf/summary?window=24h") //nolint:noctx
			if err != nil {
				errs <- err
				return
			}
			req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/perf/summary", nil)
			req.Header.Set("Authorization", "Bearer "+tok)
			resp, err = http.DefaultClient.Do(req)
			if err != nil {
				errs <- err
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			errs <- nil
		}()
	}
	for i := 0; i < n; i++ {
		// Only report the first error; most will succeed.
		<-errs
	}
}
