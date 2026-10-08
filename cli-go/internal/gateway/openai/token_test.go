package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/modelreg"
)

func modelsStatus(t *testing.T, srv *Server, bearer string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Host, req.RemoteAddr = srv.addr, "127.0.0.1:50000"
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	return rr.Code
}

// The endpoint reads its token file on every request, so a rotation revokes the
// old token at once, and a file that is missing, loose, symlinked or malformed
// admits nobody.
func TestEndpointTokenIsReadPerRequestAndFailsClosed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	tok, err := LoadOrCreateToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{
		Addr: "127.0.0.1:7898", Token: FileToken(dir),
		Service: dispatch.NewService(dispatch.ServiceConfig{}), Transcripts: consoleui.NewTranscripts(t.TempDir()),
		Registry: func(string) (*modelreg.Registry, error) { return modelreg.Load(modelreg.Options{}) },
		SignedIn: func(context.Context, string) bool { return false },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := modelsStatus(t, srv, tok); got != http.StatusOK {
		t.Fatalf("the minted token: %d", got)
	}
	if got := modelsStatus(t, srv, ""); got != http.StatusUnauthorized {
		t.Fatalf("no token: %d", got)
	}

	next, err := RotateToken(dir)
	if err != nil || next == tok {
		t.Fatalf("RotateToken = %q, %v", next, err)
	}
	if got := modelsStatus(t, srv, tok); got != http.StatusUnauthorized {
		t.Errorf("the rotated-out token still works on the running endpoint: %d", got)
	}
	if got := modelsStatus(t, srv, next); got != http.StatusOK {
		t.Errorf("the new token: %d", got)
	}

	// No file, a short body, and (on POSIX) a loose mode or a symlink all fail closed.
	p := TokenPath(dir)
	if runtime.GOOS != "windows" {
		if err := os.Chmod(p, 0o644); err != nil {
			t.Fatal(err)
		}
		if got := modelsStatus(t, srv, next); got != http.StatusUnauthorized {
			t.Errorf("a world-readable token file was trusted: %d", got)
		}
		if err := os.Chmod(p, 0o600); err != nil {
			t.Fatal(err)
		}
		other := filepath.Join(t.TempDir(), "elsewhere")
		if err := os.WriteFile(other, []byte(next+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_ = os.Remove(p)
		if err := os.Symlink(other, p); err != nil {
			t.Fatal(err)
		}
		if got := modelsStatus(t, srv, next); got != http.StatusUnauthorized {
			t.Errorf("a symlinked token file was followed: %d", got)
		}
	}
	_ = os.Remove(p)
	if err := os.WriteFile(p, []byte("short\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, offered := range []string{"short", "", next} {
		if got := modelsStatus(t, srv, offered); got != http.StatusUnauthorized {
			t.Errorf("a short token file admitted %q: %d", offered, got)
		}
	}
	_ = os.Remove(p)
	if got := modelsStatus(t, srv, next); got != http.StatusUnauthorized {
		t.Errorf("a missing token file admitted a request: %d", got)
	}
}
