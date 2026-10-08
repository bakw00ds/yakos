package anthropic

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTokenFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if _, err := ReadToken(dir); err == nil {
		t.Fatal("ReadToken invented a token")
	}
	tok, err := LoadOrCreateToken(dir)
	if err != nil || !validToken(tok) {
		t.Fatalf("LoadOrCreateToken = %q, %v", tok, err)
	}
	again, err := LoadOrCreateToken(dir)
	if err != nil || again != tok {
		t.Fatalf("second load = %q, %v; want the same token", again, err)
	}
	if got, err := ReadToken(dir); err != nil || got != tok {
		t.Fatalf("ReadToken = %q, %v", got, err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	fi, err := os.Stat(TokenPath(dir))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode %v, %v; want 0600", fi.Mode(), err)
	}
	di, _ := os.Stat(dir)
	if di.Mode().Perm() != 0o700 {
		t.Errorf("state dir mode %v, want 0700", di.Mode().Perm())
	}
	// A token file other users can read is exposed: never trusted, replaced.
	if err := os.Chmod(TokenPath(dir), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadToken(dir); err == nil {
		t.Error("ReadToken trusted a world-readable token file")
	}
	fresh, err := LoadOrCreateToken(dir)
	if err != nil || fresh == tok {
		t.Errorf("exposed token kept: %q, %v", fresh, err)
	}
	// A symlinked token file is refused, not followed.
	other := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(other, []byte(tok+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(TokenPath(dir))
	if err := os.Symlink(other, TokenPath(dir)); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadToken(dir); err == nil {
		t.Error("ReadToken followed a symlink")
	}
}

func TestRotateToken(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	first, err := RotateToken(dir)
	if err != nil || !validToken(first) {
		t.Fatalf("RotateToken = %q, %v", first, err)
	}
	second, err := RotateToken(dir)
	if err != nil || second == first {
		t.Fatalf("second RotateToken = %q, %v; want a different token", second, err)
	}
	if got, err := ReadToken(dir); err != nil || got != second {
		t.Fatalf("ReadToken = %q, %v; want the rotated token", got, err)
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(TokenPath(dir)); err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v, %v; want 0600", fi.Mode(), err)
		}
	}
	// A loose or symlinked file is replaced, not trusted.
	_ = os.Remove(TokenPath(dir))
	other := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(other, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, TokenPath(dir)); err == nil {
		if _, err := RotateToken(dir); err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(other); string(b) != "x" {
			t.Error("RotateToken wrote through a symlink")
		}
	}
}

// An unset or short configured token must match nothing, whichever path serves
// the request. With DeferToken and no SetToken, Handler() once let an empty
// offered token equal the empty configured one (sha256 of "" on both sides) and
// reached the upstream with the operator's key (sec-355d I1).
func TestHandlerFailsClosedWithoutAUsableToken(t *testing.T) {
	for name, configured := range map[string]string{"unset": "", "short": "short-token"} {
		t.Run(name, func(t *testing.T) {
			up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) { w.WriteHeader(http.StatusOK) })
			u, _ := url.Parse(up.srv.URL)
			addr := freeAddr(t)
			srv, err := New(Config{Addr: addr, DeferToken: true, baseURL: u, badTokenGap: -1, APIKey: "FAKE-key-for-test"})
			if err != nil {
				t.Fatal(err)
			}
			if configured != "" {
				srv.SetToken(configured)
			}
			offers := []map[string]string{
				{TokenHeader: ""},
				{TokenHeader: " "},
				{"Authorization": "Bearer  "},
				{"Authorization": "Bearer " + configured, TokenHeader: configured},
				{},
			}
			for i, hdr := range offers {
				req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"m","max_tokens":1,"messages":[]}`))
				req.Host = addr
				req.Header.Set("Content-Type", "application/json")
				for k, v := range hdr {
					req.Header[http.CanonicalHeaderKey(k)] = []string{v}
				}
				rr := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rr, req)
				if rr.Code != http.StatusUnauthorized {
					t.Errorf("offer %d: status %d, want 401", i, rr.Code)
				}
			}
			if n := len(up.hits()); n != 0 {
				t.Fatalf("%d requests reached the upstream, want 0", n)
			}
		})
	}

	// Control: the same request path reaches the upstream once a real token is set,
	// so the 401s above are the token check and not a Host or route refusal.
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) { w.WriteHeader(http.StatusOK) })
	u, _ := url.Parse(up.srv.URL)
	addr := freeAddr(t)
	srv, err := New(Config{Addr: addr, DeferToken: true, baseURL: u, badTokenGap: -1, APIKey: "FAKE-key-for-test"})
	if err != nil {
		t.Fatal(err)
	}
	srv.SetToken(testToken)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"m","max_tokens":1,"messages":[]}`))
	req.Host = addr
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(TokenHeader, testToken)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code == http.StatusUnauthorized || len(up.hits()) != 1 {
		t.Fatalf("control: status %d, upstream hits %d; want the request admitted", rr.Code, len(up.hits()))
	}
}
