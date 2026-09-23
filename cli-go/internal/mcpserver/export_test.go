package mcpserver

// export_test.go — test-only exports for the mcpserver package.
//
// Compiled only during tests. Exposes the parts of HTTPServer's internal
// http.Server wiring that round-1 security review findings R14 (Origin/Host
// check) and R24 (pre-auth ReadHeaderTimeout) need to assert on directly,
// without a live network round trip. Mirrors the pattern used by
// internal/consoleui/export_test.go.

import (
	"net/http"
	"time"
)

// ProtectedHandlerForTest returns the SAME handler Serve() uses in
// production — the mux wrapped with the Host-header (dashauth.RequireLocalHost)
// and Origin (rejectNonLoopbackOrigin) DNS-rebinding defenses (R14).
//
// Handler() deliberately returns the unprotected mux instead (see its doc
// comment) because httptest.NewServer binds an ephemeral port that would
// never match cfg.Addr's configured port, which would make the Host check
// reject every test request. Tests that specifically exercise Host/Origin
// rejection use this accessor with a fixed cfg.Addr and drive it directly
// via httptest.NewRecorder, not httptest.NewServer.
func ProtectedHandlerForTest(s *HTTPServer) http.Handler {
	return s.httpSrv.Handler
}

// ReadHeaderTimeoutForTest exposes the configured ReadHeaderTimeout (R24).
func ReadHeaderTimeoutForTest(s *HTTPServer) time.Duration {
	return s.httpSrv.ReadHeaderTimeout
}
