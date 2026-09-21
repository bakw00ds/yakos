package mcpserver

// refresh_internal_test.go — internal (white-box) tests for the M2 fix
// (round 1) and its round-2 follow-ups (R5, R19): yakos.refresh must
// default to a safe, project-scoped, read-only run, requiring explicit
// opt-in for both writing (apply:true) and reaching every project under
// $HOME/agent-control (scope:"all"). See security-review-2026-09-14.md M2
// and work/current/reports/s2-daemon-security-review-2026-09-21.md R5/R19.
//
// This uses the unexported refreshArgs/handleRefresh directly rather than
// exercising the full handleRefresh -> refresh.Run(scope:"all") path, since
// scope:"all" calls refresh.CollectProjects(os.Getenv("HOME")) directly
// (not test-injectable), which would make a test scan the real
// developer/CI machine's home directory. The apply/scope decoding and
// defaulting is what's under test; it's exercised precisely, without
// touching that unrelated code path.

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// TestRefreshArgs_OmittedApplyDefaultsFalse is the core M2/R5 regression:
// omitting "apply" entirely must decode to false (Go's bool zero value),
// which refresh.ResolveApply then correctly treats as dry-run — no *bool
// pointer machinery is needed anymore because the wire field is named
// "apply", not "dryRun": the zero value IS the safe value by construction.
func TestRefreshArgs_OmittedApplyDefaultsFalse(t *testing.T) {
	var p refreshArgs
	if err := json.Unmarshal([]byte(`{}`), &p); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if p.Apply {
		t.Fatal("refreshArgs{} decoded Apply = true; want false (omitting apply must be safe/dry-run)")
	}
}

// TestRefreshArgs_ExplicitApplyTrueDecodes verifies the opt-in write path
// still works: an explicit apply:true must decode to true.
func TestRefreshArgs_ExplicitApplyTrueDecodes(t *testing.T) {
	var p refreshArgs
	if err := json.Unmarshal([]byte(`{"apply":true}`), &p); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !p.Apply {
		t.Fatal(`refreshArgs{"apply":true} decoded Apply = false; want true`)
	}
}

// TestHandleRefresh_RejectsInvalidScope verifies scope is validated against
// the {"", "project", "all"} enum rather than silently accepted (and, worse,
// silently treated as "all" by some future refactor).
func TestHandleRefresh_RejectsInvalidScope(t *testing.T) {
	cfg := Config{YakosRoot: "/does/not/matter/for/this/test"}
	res := handleRefresh(context.Background(), cfg, json.RawMessage(`{"scope":"everything"}`))
	if !res.IsError {
		t.Fatal(`handleRefresh({"scope":"everything"}): want an error result for an invalid scope value`)
	}
}

// TestHandleRefresh_OmittedScopeStaysProjectScoped is the R19 regression:
// omitting scope must resolve to the current WorkspaceRoot only, never
// refresh.CollectProjects's every-project-under-$HOME sweep. Verified
// indirectly: cfg.YakosRoot is set to a nonexistent path so refresh.Run
// fails fast (this test asserts only that it does NOT hang/scan a real
// home directory, i.e. it must return quickly and deterministically).
func TestHandleRefresh_OmittedScopeStaysProjectScoped(t *testing.T) {
	cfg := Config{YakosRoot: "/does/not/exist/yakos-root", WorkspaceRoot: "/does/not/exist/workspace"}
	done := make(chan ToolsCallResult, 1)
	go func() {
		done <- handleRefresh(context.Background(), cfg, json.RawMessage(`{}`))
	}()
	select {
	case res := <-done:
		// A nonexistent YakosRoot/WorkspaceRoot should surface as an error
		// result quickly; the point of this test is that handleRefresh
		// returns at all without falling into CollectProjects's real-$HOME
		// walk (which this synthetic YakosRoot makes trivially fail-fast
		// instead of silently succeeding against the real machine).
		_ = res
	case <-time.After(5 * time.Second):
		t.Fatal("handleRefresh({}) did not return promptly — suspect it scoped to CollectProjects(real $HOME) despite scope being omitted (R19 regression)")
	}
}

// TestHandleRefresh_OmittedApplyDoesNotWrite is the round-2 review N7
// regression. The implementer's report claimed one *_OmittedApplyDoesNotWrite
// test "per transport", but no such test existed for MCP: the two tests
// above only prove refreshArgs decodes Apply to the expected bool — neither
// calls refresh.ResolveApply or refresh.Run, so an inverted ResolveApply
// (`return apply` instead of `return !apply`) would leave every test in
// this file green while MCP silently applied on every default call.
//
// This drives handleRefresh end-to-end — the same shared
// refresh.ResolveApply helper the JSON-RPC (internal/serve), gRPC
// (internal/grpcserver), and REST (internal/restapi) transports' own
// *_OmittedApplyDoesNotWrite tests exercise — and asserts on
// refresh.Run's own "[DRY RUN]" marker (internal/refresh/refresh.go) plus
// zero new entries under an isolated, scratch WorkspaceRoot (never the
// real repo). Reverting refresh.ResolveApply to `return apply` makes
// every subtest here fail.
func TestHandleRefresh_OmittedApplyDoesNotWrite(t *testing.T) {
	cases := []struct {
		name string
		args json.RawMessage
	}{
		{"apply omitted", json.RawMessage(`{}`)},
		{"apply explicit false", json.RawMessage(`{"apply":false}`)},
		{"apply zero-value via null args", json.RawMessage(`null`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			yakosRoot := t.TempDir()
			workspaceRoot := t.TempDir()
			cfg := Config{YakosRoot: yakosRoot, WorkspaceRoot: workspaceRoot}

			res := handleRefresh(context.Background(), cfg, tc.args)
			if res.IsError {
				t.Fatalf("handleRefresh(%s): unexpected error result: %+v", tc.args, res)
			}
			if len(res.Content) == 0 {
				t.Fatalf("handleRefresh(%s): empty content", tc.args)
			}
			out := res.Content[0].Text
			if !strings.Contains(out, "[DRY RUN]") {
				t.Errorf("handleRefresh(%s): output = %q; want a dry-run report ([DRY RUN] marker) — omitting/falsifying apply must never write (N7 regression)", tc.args, out)
			}

			// Nothing should have been written under the scratch WorkspaceRoot —
			// the concrete blast-radius check the grpc/JSON-RPC/REST siblings
			// also make.
			entries, _ := os.ReadDir(workspaceRoot)
			if len(entries) != 0 {
				t.Errorf("handleRefresh(%s): workspaceRoot has %d new entries after a dry-run call: %v (N7 regression: dry-run wrote files)", tc.args, len(entries), entries)
			}
		})
	}
}
