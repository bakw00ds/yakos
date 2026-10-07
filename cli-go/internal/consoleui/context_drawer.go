package consoleui

import (
	_ "embed"
	"net/http"
)

//go:embed dist/context-drawer.js
var contextDrawerJS []byte

// handleContextDrawerJS serves /context-drawer.js (token-exempt static asset,
// like /app.js).
func (s *Server) handleContextDrawerJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	_, _ = w.Write(contextDrawerJS)
}
