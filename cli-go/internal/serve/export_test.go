// export_test.go exposes internal functions for the _test package.
package serve

import (
	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/jsonrpc"
	"github.com/bakw00ds/yakos/internal/perfdash"
	"path/filepath"
)

// RegisterMethodsForTest exposes registerMethods for test packages that
// need to build a server inline (using net.Pipe) rather than a real socket.
func RegisterMethodsForTest(srv *jsonrpc.Server, cfg Config) {
	registerMethods(srv, cfg)
}

// BuildConsoleCfgForTest constructs the consoleui.Config that Run() would pass
// to consoleui.New, using the same field assignments as the real path.
// This lets tests assert that all required fields — especially WorkspaceRoot —
// are wired from serve.Config to consoleui.Config without starting a full daemon.
func BuildConsoleCfgForTest(cfg Config) consoleui.Config {
	workDir := perfdash.DefaultWorkDir(cfg.WorkspaceRoot)
	perfStateDir := perfdash.DefaultStateDir()
	kanbanPath := filepath.Join(cfg.WorkspaceRoot, "work", "current", "kanban.md")
	return consoleui.Config{
		Addr:            cfg.consoleBind(),
		KanbanBoardPath: kanbanPath,
		KanbanProject:   filepath.Base(cfg.WorkspaceRoot),
		MetricsProjectDir: cfg.WorkspaceRoot,
		// PerfWorkDir mirrors the fix in Run(): points at the dispatch state dir
		// (~/.yakos-state / $YAKOS_DISPATCH_LOG) so the Performance tab reads
		// from the same location the daemon writes dispatch-log.ndjson.
		PerfWorkDir:   perfStateDir,
		WorkDir:       workDir,
		StateDir:      cfg.consoleStateDir(),
		WorkspaceRoot: cfg.WorkspaceRoot,
		YakosRoot:     cfg.YakosRoot,
		// Bus, DispatchService, WorkflowEngine, Token omitted — not
		// constructed by this helper (requires live services); the fields
		// under test here are the static config derivations.
	}
}

// ---- Phase 6c: console-bind test exports ------------------------------------

// IsNonLoopbackForTest exposes mtls.IsNonLoopback for tests in the serve_test
// package.  It is a thin wrapper so tests can verify the predicate that
// controls the mTLS path without importing internal/mtls directly.
func IsNonLoopbackForTest(addr string) bool {
	return isNonLoopbackBind(addr)
}

// IsWildcardBindForTest exposes isWildcardBind for tests.
func IsWildcardBindForTest(addr string) bool {
	return isWildcardBind(addr)
}

// NormalizeExternalHostsForTest exposes normalizeExternalHosts for tests.
func NormalizeExternalHostsForTest(hosts []string, bindAddr string) []string {
	return normalizeExternalHosts(hosts, bindAddr)
}

// ConsoleBind is the exported view of Config.consoleBind() logic for tests.
// It mirrors the priority: consoleBind > consoleAddr > default.
func ConsoleBind(consoleAddr, consoleBind string) string {
	cfg := Config{
		ConsoleAddr: consoleAddr,
		ConsoleBind: consoleBind,
	}
	return cfg.consoleBind()
}
