package restapi

// refresh_handler_test.go — tests for handleRefreshRun (round-2 review R5).
// This handler is not yet wired to a route (see its doc comment), but R5
// flagged it as inheriting the M2 zero-value bug pre-emptively: it must not
// ship with "apply" defaulting to true the day someone routes it. In-package
// (not restapi_test) so it can call the unexported handler and Server field
// directly.
//
// Uses fully isolated t.TempDir()s for YakosRoot/WorkspaceRoot — never the
// real repo — so a pre-fix run (which would apply changes) cannot write
// outside the test's own throwaway directory.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestHandleRefreshRun_OmittedApplyDoesNotWrite is the R5 regression: an
// entirely empty request body (Content-Length: 0, so req stays its Go zero
// value) must resolve to dry-run, not apply.
func TestHandleRefreshRun_OmittedApplyDoesNotWrite(t *testing.T) {
	yakosRoot := t.TempDir()
	workspaceRoot := t.TempDir()

	s := New(Config{YakosRoot: yakosRoot, WorkspaceRoot: workspaceRoot})

	req := httptest.NewRequest(http.MethodPost, "/v1/refresh", nil)
	rec := httptest.NewRecorder()
	s.handleRefreshRun(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleRefreshRun with empty body: status = %d; body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if body != "" && !strings.Contains(body, "DRY RUN") {
		t.Errorf("handleRefreshRun with empty body: response = %q; want a dry-run report — omitting apply must never write (R5 regression)", body)
	}

	entries, _ := os.ReadDir(workspaceRoot)
	if len(entries) != 0 {
		t.Errorf("workspaceRoot has %d new entries after an omitted-apply refresh run: %v (R5 regression: dry-run wrote files)", len(entries), entries)
	}
}

// TestHandleRefreshRun_InvalidScopeRejected verifies scope is validated
// against the {"", "project", "all"} enum.
func TestHandleRefreshRun_InvalidScopeRejected(t *testing.T) {
	s := New(Config{YakosRoot: t.TempDir(), WorkspaceRoot: t.TempDir()})

	req := httptest.NewRequest(http.MethodPost, "/v1/refresh", strings.NewReader(`{"scope":"everything"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleRefreshRun(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleRefreshRun with invalid scope: status = %d; want %d", rec.Code, http.StatusBadRequest)
	}
}
