package selfupdate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestCheckRedirectHost(t *testing.T) {
	cases := []struct {
		url     string
		wantErr string // "" = allowed
	}{
		{"https://github.com/x", ""},
		{"https://codeload.github.com/x", ""},
		{"https://release-assets.githubusercontent.com/x", ""},
		{"https://objects.githubusercontent.com/x", ""},
		{"https://releases.githubusercontent.com/x", ""},
		{"https://Release-Assets.GitHubUserContent.com/x", ""},
		{"https://release-assets.githubusercontent.com:443/x", ""},
		{"https://evil.github.com/x", "disallowed host"},
		{"https://api.github.com/x", "disallowed host"},
		{"https://githubusercontent.com/x", "disallowed host"},
		{"https://githubusercontent.com.evil.com/x", "disallowed host"},
		{"https://xgithubusercontent.com/x", "disallowed host"},
		{"https://a.b.githubusercontent.com/x", "disallowed host"},
		{"https://release-assets.githubusercontent.com./x", "disallowed host"},
		{"https://-bad.githubusercontent.com/x", "disallowed host"},
		{"https://xn--githubusercontent-9zb.com/x", "disallowed host"},
		{"https://xn--gthub-5qa.githubusercontent.com.evil.com/x", "disallowed host"},
		{"https://release-assets.githubusercontent.com:8443/x", "non-default port"},
		{"https://user:pw@release-assets.githubusercontent.com/x", "userinfo"},
		{"https://release-assets.githubusercontent.com@evil.com/x", "userinfo"},
		{"https://evil.com@github.com/x", "userinfo"},
		{"http://release-assets.githubusercontent.com/x", "non-HTTPS"},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			u, err := url.Parse(tc.url)
			if err != nil {
				t.Fatal(err)
			}
			err = checkRedirectHost(u)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("want allowed, got %v", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("want rejection %q, got nil", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("error %q lacks %q", err, tc.wantErr)
			}
		})
	}
}

func TestCheckRedirectCap(t *testing.T) {
	u, _ := url.Parse("https://github.com/x")
	via := make([]*http.Request, maxRedirects)
	if err := checkRedirect(&http.Request{URL: u}, via); err == nil {
		t.Fatal("expected redirect cap error")
	}
	if err := checkRedirect(&http.Request{URL: u}, via[:maxRedirects-1]); err != nil {
		t.Fatal(err)
	}
}

// rewriteTransport sends every request (including redirect hops) to one test
// server while leaving req.URL untouched for CheckRedirect.
type rewriteTransport struct{ target string }

func (rt rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c := req.Clone(req.Context())
	c.URL.Scheme = "http"
	c.URL.Host = strings.TrimPrefix(rt.target, "http://")
	return http.DefaultTransport.RoundTrip(c)
}

const goodHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// TestChecksumsFollowReleaseAssetsRedirect replays the real GitHub chain:
// github.com/.../checksums.txt -> 302 -> release-assets.githubusercontent.com.
func TestChecksumsFollowReleaseAssetsRedirect(t *testing.T) {
	for _, tc := range []struct {
		name, loc string
		wantErr   bool
	}{
		{"release_assets", "https://release-assets.githubusercontent.com/blob?x=1", false},
		{"lookalike", "https://release-assets.githubusercontent.com.evil.com/blob", true},
		{"deep_subdomain", "https://a.b.githubusercontent.com/blob", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/bakw00ds/yakos/releases/download/v9.9.9.9/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, tc.loc, http.StatusFound)
			})
			mux.HandleFunc("/blob", func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(goodHash + "  yakos-v9.9.9.9-linux-amd64\n"))
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()
			c := BuildDefaultClient()
			c.Transport = rewriteTransport{target: srv.URL}
			m, err := fetchChecksums(context.Background(), c, "https://github.com/bakw00ds/yakos/releases/download/v9.9.9.9/checksums.txt")
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "disallowed host") {
					t.Fatalf("want disallowed host error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if m["yakos-v9.9.9.9-linux-amd64"] != goodHash {
				t.Fatalf("unexpected checksums: %v", m)
			}
		})
	}
}
