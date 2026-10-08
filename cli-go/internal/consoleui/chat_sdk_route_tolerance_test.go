package consoleui

import (
	"errors"
	"fmt"
	"testing"

	"github.com/bakw00ds/yakos/internal/dispatch"
)

func TestSDKPaneToleratesRouteError(t *testing.T) {
	unavail := fmt.Errorf("dispatch: %w", &dispatch.ExplicitRuntimeError{Runtime: "claude", Reason: "CLI not found on PATH"})
	disabled := &dispatch.ExplicitRuntimeError{Runtime: "claude", Reason: dispatch.DisabledByProjectReason}
	refused := &dispatch.RouteRefusedError{Class: "sensitive", Reason: "x", Cause: unavail}
	cases := []struct {
		name       string
		structured bool
		rt         string
		err        error
		want       bool
	}{
		{"sdk pane, claude unavailable", true, "claude", unavail, true},
		{"cli pane keeps failing closed", false, "claude", unavail, false},
		{"sensitive refusal stays", true, "claude", refused, false},
		{"project disable stays", true, "claude", disabled, false},
		{"other error stays", true, "claude", errors.New("boom"), false},
		{"non-claude runtime", true, "codex", unavail, false},
	}
	for _, c := range cases {
		if got := sdkPaneToleratesRouteError(c.structured, c.rt, c.err); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
