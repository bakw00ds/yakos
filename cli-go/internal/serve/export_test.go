// export_test.go exposes internal functions for the _test package.
package serve

import "github.com/bakw00ds/yakos/internal/jsonrpc"

// RegisterMethodsForTest exposes registerMethods for test packages that
// need to build a server inline (using net.Pipe) rather than a real socket.
func RegisterMethodsForTest(srv *jsonrpc.Server, cfg Config) {
	registerMethods(srv, cfg)
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
