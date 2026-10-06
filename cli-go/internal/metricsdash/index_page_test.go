package metricsdash_test

// index_page_test.go - runs testdata/render_check.js under Node.js to check what
// dist/index.html (the console's Cost tab) actually renders: tokens first,
// dollars only for API-billed runtimes, every interpolated value escaped, and a
// log directory that is missing or unreadable never breaks the page.
//
// The harness reads the page's inline <script> from the file, runs it against a
// tiny fake DOM and canned API responses, and asserts on the result; a syntax
// error in the page fails every scenario. Skipped when node is not on PATH, like
// the console's app.js smoke test, so machines without Node are not broken.

import (
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestIndex_ServesTokensFirstCostTab guards the embedded page: the bytes the
// server hands out are the K-136 page (a Tokens card first, the per-runtime
// section, the live_cost fetch), not a stale build.
func TestIndex_ServesTokensFirstCostTab(t *testing.T) {
	ts, _ := newTestServer(t, "")
	resp := get(t, ts.URL+"/", "")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d; want 200", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	page := string(raw)
	for _, want := range []string{
		`id="card-tokens"`,
		`id="runtime-section"`,
		`api/metrics/live_cost`,
		`API-billed runs only`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("served page lacks %q", want)
		}
	}
	// Tokens is the first card, ahead of the dollar card.
	if i, j := strings.Index(page, `id="card-tokens"`), strings.Index(page, `id="card-cost"`); i < 0 || j < 0 || i > j {
		t.Errorf("the Tokens card (at %d) must come before the Total Cost card (at %d)", i, j)
	}
}

func TestIndexPage_Behaviour(t *testing.T) {
	nodeBin, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH - skipping the Cost tab page-behaviour test")
	}
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed - cannot resolve the source directory")
	}
	dir := filepath.Dir(thisFile)
	script := filepath.Join(dir, "testdata", "render_check.js")
	page := filepath.Join(dir, "dist", "index.html")

	scenarios := []struct{ name, what string }{
		{"subscription_only", "tokens card and per-kind split, API-equivalent shown muted, no USD column"},
		{"codex_only", "a tokens-only log renders no dollar figure anywhere"},
		{"api_billed", "the USD header and its cells appear together; codex shows a dash"},
		{"hostile_runtime", "a hostile runtime name and non-numeric counts are escaped, never raw"},
		{"log_dir_not_configured", "a {message} live_cost payload leaves the page working with dashes"},
		{"live_cost_fails", "a failing live_cost call leaves the page working with dashes"},
		{"older_server", "a live_cost payload without the token fields is treated as unavailable"},
		{"malformed_runtimes", "junk rows in the runtimes list are skipped, the page keeps working"},
		{"runtimes_not_an_array", "a non-array runtimes value is treated as no rows"},
		{"existing_cards", "the pre-existing snapshot cards still render"},
	}
	for _, sc := range scenarios {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			out, err := exec.Command(nodeBin, script, page, sc.name).CombinedOutput()
			if err != nil {
				t.Fatalf("%s (%s): %v\n%s", sc.name, sc.what, err, out)
			}
		})
	}
}
