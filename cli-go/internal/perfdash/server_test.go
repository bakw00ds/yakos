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

// sampleEvent builds a legacy, estimate-only dispatch_finished JSON line: size
// estimates and no usage object, the shape of every bash-written row. sizeHint
// only scales est_input_tokens. The dashboard never prices or counts size
// estimates (K-136), so these rows carry no dollars and no tokens whatever
// sizeHint is.
func sampleEvent(ts, agent, runtime string, exitCode int, durationS float64, sizeHint float64) string {
	inTok := int64(sizeHint / 3.0 * 1_000_000)
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
		TopAgents []struct {
			Key string `json:"key"`
		} `json:"top_agents"`
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
		sampleEvent(nowISO(), "backend", "claude", 0, 10, 0.01),        // in window
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
	want := filepath.FromSlash("work/current")
	if !strings.HasSuffix(dir, want) {
		t.Errorf("DefaultWorkDir=%q; should end in %q", dir, want)
	}
}

func TestDefaultStateDir(t *testing.T) {
	dir := perfdash.DefaultStateDir()
	if dir == "" {
		t.Error("DefaultStateDir should not be empty")
	}
}

// TestDefaultStateDir_EnvOverride verifies that YAKOS_DISPATCH_LOG overrides
// the default ~/.yakos-state, matching the precedence used by the dispatch
// writer (dispatch/events.go dispatchLogPath) so read and write agree.
func TestDefaultStateDir_EnvOverride(t *testing.T) {
	want := t.TempDir()
	t.Setenv("YAKOS_DISPATCH_LOG", want)
	got := perfdash.DefaultStateDir()
	if got != want {
		t.Errorf("DefaultStateDir with YAKOS_DISPATCH_LOG=%q: got %q; want %q", want, got, want)
	}
}

// ---- HandlerNoToken (session-console mount) ---------------------------------
// These tests verify the session-mode path: mounted via HandlerNoToken() the
// API endpoints respond 200 without any Authorization header, mirroring what
// the networked session console does (auth is enforced at the outer edge).

func newTestServerNoToken(t *testing.T, workDir string) *httptest.Server {
	t.Helper()
	stateDir := t.TempDir()
	tok, err := perfdash.LoadOrCreatePerfToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreatePerfToken: %v", err)
	}
	if workDir == "" {
		workDir = t.TempDir()
	}
	srv := perfdash.New(perfdash.Config{
		Token:   tok,
		WorkDir: workDir,
	})
	ts := httptest.NewServer(srv.HandlerNoToken())
	t.Cleanup(ts.Close)
	return ts
}

func TestHandlerNoToken_SummaryServedWithoutBearer(t *testing.T) {
	ts := newTestServerNoToken(t, "")
	resp := get(t, ts.URL+"/api/perf/summary", "") // no token
	defer drainClose(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("HandlerNoToken summary: status=%d; want 200 (no bearer required)", resp.StatusCode)
	}
}

func TestHandlerNoToken_AllEndpointsReachable(t *testing.T) {
	ts := newTestServerNoToken(t, "")
	endpoints := []string{
		"/api/perf/summary",
		"/api/perf/timeseries?window=24h&bucket=hour&metric=dispatches",
		"/api/perf/by_axis?axis=agent",
		"/api/perf/recent",
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

func TestHandlerNoToken_IndexAndStaticsServed(t *testing.T) {
	ts := newTestServerNoToken(t, "")
	paths := []struct {
		path  string
		ctPfx string
	}{
		{"/", "text/html"},
		{"/app.js", "application/javascript"},
		{"/styles.css", "text/css"},
	}
	for _, tc := range paths {
		t.Run(tc.path, func(t *testing.T) {
			resp := get(t, ts.URL+tc.path, "")
			defer drainClose(resp)
			if resp.StatusCode != http.StatusOK {
				t.Errorf("HandlerNoToken %s: status=%d; want 200", tc.path, resp.StatusCode)
			}
			ct := resp.Header.Get("Content-Type")
			if !strings.HasPrefix(ct, tc.ctPfx) {
				t.Errorf("Content-Type=%q; want prefix %q", ct, tc.ctPfx)
			}
		})
	}
}

func TestHandlerNoToken_InvalidAxisReturns400(t *testing.T) {
	ts := newTestServerNoToken(t, "")
	resp := get(t, ts.URL+"/api/perf/by_axis?axis=bogus", "")
	defer drainClose(resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("HandlerNoToken by_axis bogus: status=%d; want 400", resp.StatusCode)
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

// ---- tokens and dollars (K-136) ---------------------------------------------

// ledgerLine builds a dispatch_finished line the way the Go dispatcher writes it
// after K-136: a usage object and a billing field (omitted when billing is ""
// to build a legacy line). apiEquiv, when > 0, is the api_equivalent_usd a
// subscription run reports. Large est_* values ride along so a test fails if
// the dashboard ever counts them.
func ledgerLine(ts, agent, runtime, billing string, in, out, cacheRead, cacheCreation int64, usageCost, apiEquiv float64) string {
	line := fmt.Sprintf(
		`{"type":"dispatch_finished","ts":%q,"agent":%q,"runtime":%q,"project":"test-project","exit_code":0,"duration_s":2,"est_input_tokens":4000000,"est_output_tokens":4000000`,
		ts, agent, runtime,
	)
	if billing != "" {
		line += fmt.Sprintf(`,"billing":%q`, billing)
	}
	line += fmt.Sprintf(
		`,"usage":{"input_tokens":%d,"output_tokens":%d,"cache_read":%d,"cache_creation":%d,"duration_ms":2000,"total_cost_usd":%g}`,
		in, out, cacheRead, cacheCreation, usageCost,
	)
	if apiEquiv > 0 {
		line += fmt.Sprintf(`,"api_equivalent_usd":%g`, apiEquiv)
	}
	return line + "}"
}

// mixedLedgerLog is a log with every kind of row: an api-billed claude run, a
// subscription claude run with an api-equivalent, a codex and an agy run on
// subscriptions, a legacy Go row with a usage cost and no billing field, and a
// legacy estimate-only row.
//
//	spend:        0.30 (api) + 0.20 (legacy) = 0.50
//	api-equiv:    0.42 (subscription claude), never spend
//	tokens:       1000 + 53540 + 15552 + 12864 + 100 = 83056
//	per kind:     input 22720, output 912, cache_read 57424, cache_creation 2000
func mixedLedgerLog(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	now := nowISO()
	writeDispatchLog(t, dir, []string{
		ledgerLine(now, "w-api", "claude", "api", 900, 100, 0, 0, 0.30, 0),
		ledgerLine(now, "w-sub", "claude", "subscription", 1200, 340, 50000, 2000, 0, 0.42),
		ledgerLine(now, "w-codex", "codex", "subscription", 7707, 421, 7424, 0, 0, 0),
		ledgerLine(now, "w-agy", "agy", "subscription", 12863, 1, 0, 0, 0, 0),
		ledgerLine(now, "w-legacy", "claude", "", 50, 50, 0, 0, 0.20, 0),
		sampleEvent(now, "w-est", "gemini", 0, 5, 3.0),
	})
	return dir
}

// keysOf decodes a JSON object into its raw members.
func keysOf(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode object: %v\n%s", err, raw)
	}
	return m
}

func hasKeys(t *testing.T, what string, m map[string]json.RawMessage, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			t.Errorf("%s: missing key %q", what, k)
		}
	}
}

func feq(a, b float64) bool { return a-b < 1e-9 && b-a < 1e-9 }

// An estimate-only log reports no dollars and no tokens. Before K-136 this log
// was priced from its est_* fields at a hard-coded rate.
func TestSummary_EstimateOnlyLogCostsNothing(t *testing.T) {
	dir := t.TempDir()
	writeDispatchLog(t, dir, []string{
		sampleEvent(nowISO(), "backend", "claude", 0, 60.0, 0.05),
		sampleEvent(nowISO(), "frontend", "codex", 0, 30.0, 3.0),
	})
	ts, tok := newTestServer(t, dir)

	resp := get(t, ts.URL+"/api/perf/summary?window=24h", tok)
	var s struct {
		TotalDispatches int64   `json:"total_dispatches"`
		TotalCostUSD    float64 `json:"total_cost_usd"`
		TopAgents       []struct {
			CostUSD float64 `json:"cost_usd"`
		} `json:"top_agents"`
	}
	decodeJSON(t, resp, &s)
	if s.TotalDispatches != 2 {
		t.Fatalf("total_dispatches=%d; want 2", s.TotalDispatches)
	}
	if s.TotalCostUSD != 0 {
		t.Errorf("total_cost_usd=%v; want 0 (estimates are not dollars)", s.TotalCostUSD)
	}
	for _, a := range s.TopAgents {
		if a.CostUSD != 0 {
			t.Errorf("top agent cost_usd=%v; want 0", a.CostUSD)
		}
	}

	resp = get(t, ts.URL+"/api/perf/recent?limit=10", tok)
	var recent []struct {
		CostUSD float64 `json:"cost_usd"`
	}
	decodeJSON(t, resp, &recent)
	for _, r := range recent {
		if r.CostUSD != 0 {
			t.Errorf("recent cost_usd=%v; want 0", r.CostUSD)
		}
	}

	resp = get(t, ts.URL+"/api/perf/timeseries?window=24h&bucket=hour&metric=cost", tok)
	var points []struct {
		Value float64 `json:"value"`
	}
	decodeJSON(t, resp, &points)
	for _, p := range points {
		if p.Value != 0 {
			t.Errorf("cost timeseries value=%v; want 0", p.Value)
		}
	}
}

func TestSummary_TokensAndAPISpend(t *testing.T) {
	ts, tok := newTestServer(t, mixedLedgerLog(t))

	resp := get(t, ts.URL+"/api/perf/summary?window=24h", tok)
	defer func() { _ = resp.Body.Close() }()
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Every key that existed before K-136 is still there.
	top := keysOf(t, raw)
	hasKeys(t, "summary", top, "total_dispatches", "total_cost_usd", "avg_latency_ms",
		"p50_latency_ms", "p95_latency_ms", "top_agents", "top_runtimes",
		// ...and the additive ones.
		"total_tokens", "token_detail", "api_equivalent_usd")

	var s struct {
		TotalDispatches  int64   `json:"total_dispatches"`
		TotalCostUSD     float64 `json:"total_cost_usd"`
		TotalTokens      int64   `json:"total_tokens"`
		APIEquivalentUSD float64 `json:"api_equivalent_usd"`
		TokenDetail      struct {
			Input         int64 `json:"input"`
			Output        int64 `json:"output"`
			CacheRead     int64 `json:"cache_read"`
			CacheCreation int64 `json:"cache_creation"`
		} `json:"token_detail"`
		TopRuntimes []struct {
			Key         string  `json:"key"`
			Dispatches  int64   `json:"dispatches"`
			CostUSD     float64 `json:"cost_usd"`
			Tokens      int64   `json:"tokens"`
			TokenDetail struct {
				Input int64 `json:"input"`
			} `json:"token_detail"`
		} `json:"top_runtimes"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if s.TotalDispatches != 6 {
		t.Errorf("total_dispatches=%d; want 6", s.TotalDispatches)
	}
	if !feq(s.TotalCostUSD, 0.50) {
		t.Errorf("total_cost_usd=%v; want 0.50 (api 0.30 + legacy 0.20; subscription rows add none)", s.TotalCostUSD)
	}
	if s.TotalTokens != 83056 {
		t.Errorf("total_tokens=%d; want 83056 (no est_* in it)", s.TotalTokens)
	}
	d := s.TokenDetail
	if d.Input != 22720 || d.Output != 912 || d.CacheRead != 57424 || d.CacheCreation != 2000 {
		t.Errorf("token_detail=%+v; want input 22720 output 912 cache_read 57424 cache_creation 2000", d)
	}
	if !feq(s.APIEquivalentUSD, 0.42) {
		t.Errorf("api_equivalent_usd=%v; want 0.42", s.APIEquivalentUSD)
	}

	byKey := map[string]int{}
	for i, r := range s.TopRuntimes {
		byKey[r.Key] = i
	}
	cl, ok := byKey["claude"]
	if !ok {
		t.Fatalf("top_runtimes lacks claude: %+v", s.TopRuntimes)
	}
	if r := s.TopRuntimes[cl]; r.Dispatches != 3 || r.Tokens != 54640 || !feq(r.CostUSD, 0.50) {
		t.Errorf("claude top runtime: %+v; want 3 dispatches, 54640 tokens, $0.50", r)
	}
	if cx, ok := byKey["codex"]; !ok {
		t.Errorf("top_runtimes lacks codex: %+v", s.TopRuntimes)
	} else if r := s.TopRuntimes[cx]; r.Tokens != 15552 || r.CostUSD != 0 || r.TokenDetail.Input != 7707 {
		t.Errorf("codex top runtime: %+v; want 15552 tokens, no dollars, input 7707 as logged", r)
	}

	// Existing keys of a top item are still there.
	var items struct {
		TopAgents []json.RawMessage `json:"top_agents"`
	}
	if err := json.Unmarshal(raw, &items); err != nil || len(items.TopAgents) == 0 {
		t.Fatalf("top_agents: %v", err)
	}
	hasKeys(t, "top agent", keysOf(t, items.TopAgents[0]), "key", "dispatches", "cost_usd", "tokens", "token_detail")
}

func TestByAxis_TokensAndAPIEquivalent(t *testing.T) {
	ts, tok := newTestServer(t, mixedLedgerLog(t))

	resp := get(t, ts.URL+"/api/perf/by_axis?axis=runtime&window=24h", tok)
	defer func() { _ = resp.Body.Close() }()
	var raws []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raws); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(raws) != 4 {
		t.Fatalf("rows=%d; want 4 runtimes (claude, agy, codex, gemini)", len(raws))
	}

	type row struct {
		Key              string  `json:"key"`
		Dispatches       int64   `json:"dispatches"`
		CostUSD          float64 `json:"cost_usd"`
		Tokens           int64   `json:"tokens"`
		APIEquivalentUSD float64 `json:"api_equivalent_usd"`
		TokenDetail      struct {
			CacheRead     int64 `json:"cache_read"`
			CacheCreation int64 `json:"cache_creation"`
		} `json:"token_detail"`
	}
	rows := map[string]row{}
	for _, raw := range raws {
		var r row
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatalf("decode row: %v", err)
		}
		rows[r.Key] = r
		keys := keysOf(t, raw)
		hasKeys(t, "by_axis row "+r.Key, keys, "key", "dispatches", "cost_usd",
			"avg_latency_ms", "p95_latency_ms", "tokens", "token_detail")
		// api_equivalent_usd is omitted when there is none.
		_, has := keys["api_equivalent_usd"]
		if want := r.Key == "claude"; has != want {
			t.Errorf("by_axis row %s: api_equivalent_usd present=%v; want %v", r.Key, has, want)
		}
	}

	if r := rows["claude"]; r.Dispatches != 3 || r.Tokens != 54640 || !feq(r.CostUSD, 0.50) || !feq(r.APIEquivalentUSD, 0.42) {
		t.Errorf("claude row: %+v; want 3 dispatches, 54640 tokens, $0.50 spend, $0.42 api-equivalent", r)
	}
	if r := rows["codex"]; r.Tokens != 15552 || r.CostUSD != 0 || r.TokenDetail.CacheRead != 7424 {
		t.Errorf("codex row: %+v; want 15552 tokens, no dollars, cache_read 7424", r)
	}
	if r := rows["agy"]; r.Tokens != 12864 || r.CostUSD != 0 {
		t.Errorf("agy row: %+v; want 12864 tokens and no dollars", r)
	}
	if r := rows["gemini"]; r.Tokens != 0 || r.CostUSD != 0 {
		t.Errorf("estimate-only gemini row: %+v; want no tokens and no dollars", r)
	}
}

func TestRecent_TokensBillingAndAPIEquivalent(t *testing.T) {
	ts, tok := newTestServer(t, mixedLedgerLog(t))

	resp := get(t, ts.URL+"/api/perf/recent?limit=50", tok)
	defer func() { _ = resp.Body.Close() }()
	var raws []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raws); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(raws) != 6 {
		t.Fatalf("rows=%d; want 6", len(raws))
	}

	type row struct {
		Agent            string  `json:"agent"`
		Billing          string  `json:"billing"`
		CostUSD          float64 `json:"cost_usd"`
		Tokens           int64   `json:"tokens"`
		APIEquivalentUSD float64 `json:"api_equivalent_usd"`
	}
	rows := map[string]row{}
	for _, raw := range raws {
		var r row
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatalf("decode row: %v", err)
		}
		rows[r.Agent] = r
		keys := keysOf(t, raw)
		hasKeys(t, "recent row "+r.Agent, keys, "ts", "agent", "runtime", "project",
			"exit_code", "duration_s", "cost_usd", "latency_ms", "tokens", "token_detail")
		// billing is omitted on rows written before K-136.
		_, hasBilling := keys["billing"]
		if want := r.Agent != "w-legacy" && r.Agent != "w-est"; hasBilling != want {
			t.Errorf("recent row %s: billing present=%v; want %v", r.Agent, hasBilling, want)
		}
	}

	if r := rows["w-api"]; r.Billing != "api" || !feq(r.CostUSD, 0.30) || r.Tokens != 1000 {
		t.Errorf("w-api: %+v", r)
	}
	if r := rows["w-sub"]; r.Billing != "subscription" || r.CostUSD != 0 || r.Tokens != 53540 || !feq(r.APIEquivalentUSD, 0.42) {
		t.Errorf("w-sub: %+v; want subscription, no dollars, 53540 tokens, $0.42 api-equivalent", r)
	}
	if r := rows["w-codex"]; r.Billing != "subscription" || r.CostUSD != 0 || r.Tokens != 15552 {
		t.Errorf("w-codex: %+v", r)
	}
	if r := rows["w-legacy"]; r.Billing != "" || !feq(r.CostUSD, 0.20) || r.Tokens != 100 {
		t.Errorf("w-legacy: %+v; want no billing, $0.20 (legacy usage cost still counts), 100 tokens", r)
	}
	if r := rows["w-est"]; r.CostUSD != 0 || r.Tokens != 0 {
		t.Errorf("w-est: %+v; want no dollars and no tokens", r)
	}
}

func TestTimeseries_TokensMetric(t *testing.T) {
	ts, tok := newTestServer(t, mixedLedgerLog(t))

	type point struct {
		Ts    string  `json:"ts"`
		Value float64 `json:"value"`
	}
	sum := func(metric string) float64 {
		resp := get(t, ts.URL+"/api/perf/timeseries?window=24h&bucket=hour&metric="+metric, tok)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("metric=%s: status=%d; want 200", metric, resp.StatusCode)
		}
		var pts []point
		decodeJSON(t, resp, &pts)
		var total float64
		for _, p := range pts {
			total += p.Value
		}
		return total
	}

	if got := sum("tokens"); got != 83056 {
		t.Errorf("tokens series sums to %v; want 83056", got)
	}
	// The cost series is API spend only: 0.30 api + 0.20 legacy.
	if got := sum("cost"); !feq(got, 0.50) {
		t.Errorf("cost series sums to %v; want 0.50", got)
	}
	if got := sum("dispatches"); got != 6 {
		t.Errorf("dispatches series sums to %v; want 6", got)
	}
}

func TestTimeseries_InvalidMetricNamesTokens(t *testing.T) {
	ts, tok := newTestServer(t, "")
	resp := get(t, ts.URL+"/api/perf/timeseries?metric=bogus", tok)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d; want 400", resp.StatusCode)
	}
	var body struct {
		Error string `json:"error"`
	}
	decodeJSON(t, resp, &body)
	if body.Error != "metric must be cost|latency|dispatches|tokens" {
		t.Errorf("error=%q", body.Error)
	}
}

func TestHandlerNoToken_TokensMetricAndNewKeys(t *testing.T) {
	ts := newTestServerNoToken(t, mixedLedgerLog(t))

	resp := get(t, ts.URL+"/api/perf/timeseries?window=24h&bucket=hour&metric=tokens", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tokens timeseries without bearer: status=%d; want 200", resp.StatusCode)
	}
	var pts []struct {
		Value float64 `json:"value"`
	}
	decodeJSON(t, resp, &pts)
	var total float64
	for _, p := range pts {
		total += p.Value
	}
	if total != 83056 {
		t.Errorf("tokens series sums to %v; want 83056", total)
	}

	resp = get(t, ts.URL+"/api/perf/summary?window=24h", "")
	var s struct {
		TotalTokens int64 `json:"total_tokens"`
	}
	decodeJSON(t, resp, &s)
	if s.TotalTokens != 83056 {
		t.Errorf("session-mounted summary total_tokens=%d; want 83056", s.TotalTokens)
	}
}

// The served page starts tokens-first: Tokens is the default chart metric, and
// the Cost (USD) chart option and card start hidden until a window has API
// spend (the behaviour itself is exercised by TestDashboardRendering).
func TestIndex_TokensFirstDefaults(t *testing.T) {
	ts, _ := newTestServer(t, "")

	resp := get(t, ts.URL+"/", "")
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	html := string(body)
	if !strings.Contains(html, `<option value="tokens" selected>`) {
		t.Error("index.html: tokens is not the default chart metric")
	}
	if !strings.Contains(html, `id="metric-opt-cost" hidden disabled`) {
		t.Error("index.html: the Cost (USD) chart option should start hidden and disabled")
	}
	if !strings.Contains(html, `<div class="card hidden" id="card-cost-wrap">`) {
		t.Error("index.html: the cost card should start hidden")
	}
}
