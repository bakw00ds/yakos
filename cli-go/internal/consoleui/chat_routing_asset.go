package consoleui

import (
	_ "embed"
	"net/http"
)

// chatRoutingJS is the console's routing UI module (K-148): @prefix parsing, the
// registry-driven selects, the route chip and the handoff banner. app.js stays
// small and calls into it through window.YakChatRouting.
//
//go:embed dist/chat-routing.js
var chatRoutingJS []byte

// handleChatRoutingJS serves GET /chat-routing.js. Token-exempt like /app.js
// (listed in isStaticAsset): it carries no secret and loads before the token.
func (s *Server) handleChatRoutingJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	_, _ = w.Write(chatRoutingJS)
}

// modelsJS is the Models & Providers tab (K-153), served at /models.js. Like
// chat-routing.js it is a plain script that app.js reaches through
// window.YakModels; it carries no secret and loads before the token.
//
//go:embed dist/models.js
var modelsJS []byte

// modelsWriteJS is the tab's write controls (K-175), served at /models_write.js.
// It draws nothing unless the overview says can_write.
//
//go:embed dist/models_write.js
var modelsWriteJS []byte

func (s *Server) handleModelsWriteJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	_, _ = w.Write(modelsWriteJS)
}

func (s *Server) handleModelsJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	_, _ = w.Write(modelsJS)
}
