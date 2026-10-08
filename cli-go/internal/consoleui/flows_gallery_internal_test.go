package consoleui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The gallery module is served same-origin as a static, token-exempt asset
// and never assigns server data through innerHTML (K-152).
func TestFlowsGalleryAsset(t *testing.T) {
	if strings.Contains(string(flowsGalleryJS), "innerHTML") {
		t.Error("flows-gallery.js must not use innerHTML")
	}
	r := httptest.NewRequest(http.MethodGet, "/flows-gallery.js", nil)
	if !isStaticAsset(r) {
		t.Error("/flows-gallery.js must be token-exempt like the other static assets")
	}
	srv := &Server{}
	rec := httptest.NewRecorder()
	srv.handleFlowsGalleryJS(rec, r)
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/javascript") {
		t.Errorf("served %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}
