// export_test.go exposes internal helpers for the grpcserver_test package.
package grpcserver

import (
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// NewDispatchServiceForTest constructs a minimal dispatch.Service for use in
// grpcserver tests that do not exercise the dispatch path.  Tests that only
// exercise kanban, cost, status, or refresh do not need a working YakosRoot;
// the service is only injected to satisfy the non-nil requirement on Config.
func NewDispatchServiceForTest(workspaceRoot string, bus *wsbus.Bus) *dispatch.Service {
	return dispatch.NewService(dispatch.ServiceConfig{
		WorkspaceRoot: workspaceRoot,
		Bus:           bus,
		// YakosRoot intentionally empty: dispatch tests must supply their own.
	})
}
