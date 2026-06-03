// export_test.go exposes internal functions for the _test package.
package serve

import "github.com/bakw00ds/yakos/internal/jsonrpc"

// RegisterMethodsForTest exposes registerMethods for test packages that
// need to build a server inline (using net.Pipe) rather than a real socket.
func RegisterMethodsForTest(srv *jsonrpc.Server, cfg Config) {
	registerMethods(srv, cfg)
}
