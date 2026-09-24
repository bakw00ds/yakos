package consoleui_test

import (
	"net/http/httptest"
	"testing"

	"github.com/bakw00ds/yakos/internal/buildinfo"
	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// TestXYakosBuildHeader_SetOnEveryResponse asserts the outermost middleware
// layer (withBuildIDHeader, applied in New()) stamps every response — even
// one the inner auth chain would otherwise reject — with the daemon's
// buildinfo.BuildID(). This is the mechanism a browser tab (or any client)
// uses to detect that the daemon behind an open console session was
// replaced. See work/current/reports/s6-structural-plan-2026-09-23.md §4.2.
func TestXYakosBuildHeader_SetOnEveryResponse(t *testing.T) {
	stateDir := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)

	srv := consoleui.MustNew(t, consoleui.Config{
		Token:             tok,
		KanbanBoardPath:   t.TempDir() + "/kanban.md",
		KanbanProject:     "test",
		MetricsProjectDir: t.TempDir(),
		PerfWorkDir:       t.TempDir(),
		Bus:               bus,
	})
	handler := srv.FullHandler()

	// "/" is a token-exempt static asset (isStaticAsset), so this request
	// only needs to clear the loopback Host check — default Addr is
	// 127.0.0.1:7890 (consoleui.DefaultAddr()).
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = consoleui.DefaultAddr()
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	got := rr.Header().Get("X-Yakos-Build")
	if got == "" {
		t.Fatal("X-Yakos-Build header missing from response")
	}
	if want := buildinfo.BuildID(); got != want {
		t.Errorf("X-Yakos-Build = %q; want %q", got, want)
	}
}

// TestXYakosBuildHeader_SetEvenOnUnauthorized asserts the header is present
// even on a rejected (non-static, unauthenticated) request — the header is
// applied outermost, before the auth gate, so staleness can be detected
// regardless of auth outcome.
func TestXYakosBuildHeader_SetEvenOnUnauthorized(t *testing.T) {
	stateDir := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)

	srv := consoleui.MustNew(t, consoleui.Config{
		Token:             tok,
		KanbanBoardPath:   t.TempDir() + "/kanban.md",
		KanbanProject:     "test",
		MetricsProjectDir: t.TempDir(),
		PerfWorkDir:       t.TempDir(),
		Bus:               bus,
	})
	handler := srv.FullHandler()

	// A non-static, mutating-looking path with no bearer token: expect a
	// 401/403-class rejection, but the header must still be set.
	req := httptest.NewRequest("GET", "/api/presence", nil)
	req.Host = consoleui.DefaultAddr()
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code == 200 {
		t.Fatalf("expected an auth rejection for an unauthenticated non-static request, got 200")
	}
	if got := rr.Header().Get("X-Yakos-Build"); got == "" {
		t.Error("X-Yakos-Build header missing from a rejected response")
	}
}
