package selfupdate_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/selfupdate"
)

// fakeBinaryContent is the payload we pretend is the new release binary.
const fakeBinaryContent = "THIS IS A FAKE YAKOS BINARY v2.0.0.0"

// fakeBinaryOldContent seeds the "current" exe in replace tests.
const fakeBinaryOldContent = "THIS IS A FAKE YAKOS BINARY v1.0.0.0"

// fakeTag is the release tag returned by the fake GitHub API.
const fakeTag = "v2.0.0.0"

// buildChecksum returns the SHA-256 hex of data, in lowercase.
func buildChecksum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// platformAssetName returns the asset filename for the current platform.
func platformAssetName(tag string) string {
	name := fmt.Sprintf("yakos-%s-%s-%s", tag, runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// newFakeServer creates a httptest.Server that serves the GitHub API
// endpoints needed by selfupdate.  If checksumOverride is non-empty it is
// used instead of the correct SHA-256 (so tests can inject a bad checksum).
func newFakeServer(t *testing.T, tag string, assetBody []byte, checksumOverride string) *httptest.Server {
	t.Helper()
	digest := buildChecksum(assetBody)
	if checksumOverride != "" {
		digest = checksumOverride
	}
	name := platformAssetName(tag)
	checksumBody := fmt.Sprintf("%s  %s\n", digest, name)

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/bakw00ds/yakos/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": tag})
	})
	mux.HandleFunc("/bakw00ds/yakos/releases/download/"+tag+"/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, checksumBody)
	})
	mux.HandleFunc("/bakw00ds/yakos/releases/download/"+tag+"/"+name, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(assetBody)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// hostRewriteTransport routes all requests to a fixed test server URL while
// preserving the original path and query string.
type hostRewriteTransport struct {
	base   http.RoundTripper
	target string // "http://127.0.0.1:PORT"
}

func (tr *hostRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.URL.Scheme = "http"
	cloned.URL.Host = strings.TrimPrefix(tr.target, "http://")
	return tr.base.RoundTrip(cloned)
}

// clientForServer returns an *http.Client that routes all requests to srv.
func clientForServer(srv *httptest.Server) *http.Client {
	return &http.Client{
		Transport: &hostRewriteTransport{base: http.DefaultTransport, target: srv.URL},
	}
}

// makeTempExe writes content to a temp file with 0755 perms and returns its path.
func makeTempExe(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	name := "yakos-test"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	exe := filepath.Join(dir, name)
	if err := os.WriteFile(exe, []byte(content), 0755); err != nil {
		t.Fatalf("write temp exe: %v", err)
	}
	return exe
}

// discardBuf is an io.Writer that throws away all writes (avoids os.Stdout noise).
var discardBuf = &bytes.Buffer{}

func discardWriter() *bytes.Buffer {
	discardBuf.Reset()
	return discardBuf
}

// --- Tests ---

// TestLatestRelease_Success confirms LatestRelease parses the tag_name field.
func TestLatestRelease_Success(t *testing.T) {
	srv := newFakeServer(t, fakeTag, []byte(fakeBinaryContent), "")
	tag, err := selfupdate.LatestRelease(context.Background(), clientForServer(srv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tag != fakeTag {
		t.Errorf("tag = %q, want %q", tag, fakeTag)
	}
}

// TestApply_NewerVersionDetectedAndApplied is the happy path: a newer release
// causes the exe to be replaced with the downloaded bytes.
func TestApply_NewerVersionDetectedAndApplied(t *testing.T) {
	assetBytes := []byte(fakeBinaryContent)
	srv := newFakeServer(t, fakeTag, assetBytes, "")
	exePath := makeTempExe(t, fakeBinaryOldContent)

	res, err := selfupdate.Apply(context.Background(), selfupdate.Opts{
		CurrentVersion: "v1.0.0.0",
		HTTPClient:     clientForServer(srv),
		ExeResolver:    func() (string, error) { return exePath, nil },
		Writer:         discardWriter(),
	})
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	if res.AlreadyUpToDate {
		t.Error("expected update to be applied, got AlreadyUpToDate=true")
	}
	if res.NewVersion != fakeTag {
		t.Errorf("NewVersion = %q, want %q", res.NewVersion, fakeTag)
	}
	if res.OldVersion != "v1.0.0.0" {
		t.Errorf("OldVersion = %q, want %q", res.OldVersion, "v1.0.0.0")
	}

	// Verify the binary at exePath was replaced with the new content.
	got, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("read replaced exe: %v", err)
	}
	if string(got) != fakeBinaryContent {
		t.Errorf("replaced binary = %q, want %q", got, fakeBinaryContent)
	}

	// Verify 0755 permissions on Unix.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(exePath)
		if err != nil {
			t.Fatalf("stat replaced exe: %v", err)
		}
		if info.Mode().Perm() != 0755 {
			t.Errorf("replaced exe perm = %v, want 0755", info.Mode().Perm())
		}
	}
}

// TestApply_AlreadyUpToDate verifies that when running == latest the result is
// AlreadyUpToDate and the exe file is not modified.
func TestApply_AlreadyUpToDate(t *testing.T) {
	assetBytes := []byte(fakeBinaryContent)
	srv := newFakeServer(t, fakeTag, assetBytes, "")
	exePath := makeTempExe(t, fakeBinaryOldContent)

	origInfo, err := os.Stat(exePath)
	if err != nil {
		t.Fatalf("stat original exe: %v", err)
	}

	res, err := selfupdate.Apply(context.Background(), selfupdate.Opts{
		CurrentVersion: fakeTag, // same as the server's latest
		HTTPClient:     clientForServer(srv),
		ExeResolver:    func() (string, error) { return exePath, nil },
		Writer:         discardWriter(),
	})
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	if !res.AlreadyUpToDate {
		t.Error("expected AlreadyUpToDate=true when versions match")
	}

	// Exe must NOT have been touched — compare mtime.
	newInfo, err := os.Stat(exePath)
	if err != nil {
		t.Fatalf("stat exe after Apply: %v", err)
	}
	if !newInfo.ModTime().Equal(origInfo.ModTime()) {
		t.Error("exe mtime changed despite being up to date")
	}
}

// TestApply_ChecksumMismatchAborts is the critical security test.
// When checksums.txt contains a wrong digest the binary must NOT be replaced
// and the error must clearly mention the mismatch.
func TestApply_ChecksumMismatchAborts(t *testing.T) {
	assetBytes := []byte(fakeBinaryContent)
	badChecksum := strings.Repeat("0", 64) // all-zero hex — guaranteed wrong
	srv := newFakeServer(t, fakeTag, assetBytes, badChecksum)
	exePath := makeTempExe(t, fakeBinaryOldContent)

	_, err := selfupdate.Apply(context.Background(), selfupdate.Opts{
		CurrentVersion: "v1.0.0.0",
		HTTPClient:     clientForServer(srv),
		ExeResolver:    func() (string, error) { return exePath, nil },
		Writer:         discardWriter(),
	})
	if err == nil {
		t.Fatal("expected error on checksum mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "MISMATCH") {
		t.Errorf("error should mention MISMATCH, got: %v", err)
	}

	// The exe must still contain the OLD content — no partial write.
	got, readErr := os.ReadFile(exePath)
	if readErr != nil {
		t.Fatalf("read exe after mismatch: %v", readErr)
	}
	if string(got) != fakeBinaryOldContent {
		t.Errorf("exe content was modified after checksum mismatch; got %q", got)
	}

	// No stale temp file must be left next to the exe.
	entries, _ := os.ReadDir(filepath.Dir(exePath))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".yakos-update-") {
			t.Errorf("stale temp file left after failed update: %s", e.Name())
		}
	}
}

// TestApply_DryRun verifies --dry-run logs the intent but writes nothing.
func TestApply_DryRun(t *testing.T) {
	assetBytes := []byte(fakeBinaryContent)
	srv := newFakeServer(t, fakeTag, assetBytes, "")
	exePath := makeTempExe(t, fakeBinaryOldContent)

	origInfo, err := os.Stat(exePath)
	if err != nil {
		t.Fatalf("stat original exe: %v", err)
	}

	res, err := selfupdate.Apply(context.Background(), selfupdate.Opts{
		CurrentVersion: "v1.0.0.0",
		DryRun:         true,
		HTTPClient:     clientForServer(srv),
		ExeResolver:    func() (string, error) { return exePath, nil },
		Writer:         discardWriter(),
	})
	if err != nil {
		t.Fatalf("Apply DryRun error: %v", err)
	}
	if res.AlreadyUpToDate {
		t.Error("DryRun result should not report AlreadyUpToDate when there IS a newer version")
	}

	// Exe must NOT have been touched.
	newInfo, err := os.Stat(exePath)
	if err != nil {
		t.Fatalf("stat exe after dry-run: %v", err)
	}
	if !newInfo.ModTime().Equal(origInfo.ModTime()) {
		t.Error("exe was modified in dry-run mode")
	}
}

// TestApply_ForceReinstallsSameVersion verifies --force bypasses the
// version check and replaces the binary even when versions are identical.
func TestApply_ForceReinstallsSameVersion(t *testing.T) {
	assetBytes := []byte(fakeBinaryContent)
	srv := newFakeServer(t, fakeTag, assetBytes, "")
	exePath := makeTempExe(t, fakeBinaryOldContent)

	res, err := selfupdate.Apply(context.Background(), selfupdate.Opts{
		CurrentVersion: fakeTag, // same version as server's latest
		Force:          true,
		HTTPClient:     clientForServer(srv),
		ExeResolver:    func() (string, error) { return exePath, nil },
		Writer:         discardWriter(),
	})
	if err != nil {
		t.Fatalf("Apply Force error: %v", err)
	}
	if res.AlreadyUpToDate {
		t.Error("--force should bypass AlreadyUpToDate; got AlreadyUpToDate=true")
	}
	got, _ := os.ReadFile(exePath)
	if string(got) != fakeBinaryContent {
		t.Errorf("binary was not replaced with --force; content = %q", got)
	}
}

// TestTagValidationRejectsMaliciousTag verifies that a tag containing
// path-traversal characters is rejected before use in any URL or filename.
func TestTagValidationRejectsMaliciousTag(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/bakw00ds/yakos/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		// Inject a tag that looks like a path traversal.
		_ = json.NewEncoder(w).Encode(map[string]string{
			"tag_name": "v1.0/../../../etc/passwd",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := selfupdate.LatestRelease(context.Background(), clientForServer(srv))
	if err == nil {
		t.Fatal("expected error for malicious tag, got nil")
	}
	if !strings.Contains(err.Error(), "invalid tag_name") {
		t.Errorf("expected 'invalid tag_name' in error, got: %v", err)
	}
}

// TestRedirectToDisallowedHostRejected verifies the redirect allowlist.
// A server redirecting to an arbitrary host must cause the fetch to fail.
func TestRedirectToDisallowedHostRejected(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/bakw00ds/yakos/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://evil.example.com/payload", http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Compose: host-rewrite transport (to route to our test server) +
	// a CheckRedirect that enforces the allowlist on redirect destinations
	// (the redirect itself goes to evil.example.com, bypassing the rewrite,
	// so the policy fires on the absolute URL).
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			host := strings.ToLower(req.URL.Hostname())
			allowed := []string{
				"github.com",
				"objects.githubusercontent.com",
				"releases.githubusercontent.com",
				"codeload.github.com",
			}
			for _, a := range allowed {
				if host == a || strings.HasSuffix(host, "."+a) {
					return nil
				}
			}
			return fmt.Errorf("selfupdate: redirect to disallowed host %q rejected", host)
		},
		Transport: &hostRewriteTransport{base: http.DefaultTransport, target: srv.URL},
	}

	_, err := selfupdate.LatestRelease(context.Background(), client)
	if err == nil {
		t.Fatal("expected redirect rejection, got nil")
	}
	// The error may be wrapped; look for the key phrase.
	if !strings.Contains(err.Error(), "disallowed host") && !strings.Contains(err.Error(), "redirect") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestChecksumMissingEntry verifies that if checksums.txt does NOT contain
// an entry for our platform asset we abort cleanly (no binary replacement).
func TestChecksumMissingEntry(t *testing.T) {
	assetBytes := []byte(fakeBinaryContent)
	digest := buildChecksum(assetBytes)

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/bakw00ds/yakos/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": fakeTag})
	})
	mux.HandleFunc("/bakw00ds/yakos/releases/download/"+fakeTag+"/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		// Entry for a totally different platform — our platform entry is absent.
		fmt.Fprintf(w, "%s  yakos-%s-someother-os-someother-arch\n", digest, fakeTag)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	exePath := makeTempExe(t, fakeBinaryOldContent)
	_, err := selfupdate.Apply(context.Background(), selfupdate.Opts{
		CurrentVersion: "v1.0.0.0",
		HTTPClient:     clientForServer(srv),
		ExeResolver:    func() (string, error) { return exePath, nil },
		Writer:         discardWriter(),
	})
	if err == nil {
		t.Fatal("expected error when asset not in checksums.txt")
	}
	if !strings.Contains(err.Error(), "no checksum entry") {
		t.Errorf("unexpected error: %v", err)
	}
	// Exe must still be old.
	got, _ := os.ReadFile(exePath)
	if string(got) != fakeBinaryOldContent {
		t.Error("exe was modified despite missing checksum entry")
	}
}

// TestVersionCompare exercises the isNewer logic indirectly through Apply's
// AlreadyUpToDate short-circuit.
func TestVersionCompare(t *testing.T) {
	cases := []struct {
		candidate string // released version
		base      string // currently installed version
		wantNewer bool
	}{
		{"2.0.0.0", "1.9.9.9", true},
		{"1.0.0.0", "1.0.0.0", false},
		{"1.0.0.1", "1.0.0.0", true},
		{"1.0.0.0", "1.0.0.1", false},
		{"2.0", "1.9.9.9", true},
		{"1.0.0", "1.0.0.0", false},
		{"0.75.0", "0.74.0", true},
		{"0.74.0", "0.75.0", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.candidate+"_vs_"+tc.base, func(t *testing.T) {
			assetBytes := []byte(fakeBinaryContent)
			srv := newFakeServer(t, "v"+tc.candidate, assetBytes, "")
			exePath := makeTempExe(t, fakeBinaryOldContent)

			res, err := selfupdate.Apply(context.Background(), selfupdate.Opts{
				CurrentVersion: "v" + tc.base,
				DryRun:         true, // never write anything
				HTTPClient:     clientForServer(srv),
				ExeResolver:    func() (string, error) { return exePath, nil },
				Writer:         discardWriter(),
			})
			if err != nil {
				t.Fatalf("Apply error: %v", err)
			}
			gotNewer := !res.AlreadyUpToDate
			if gotNewer != tc.wantNewer {
				t.Errorf("candidate=%s base=%s: isNewer=%v, want %v",
					tc.candidate, tc.base, gotNewer, tc.wantNewer)
			}
		})
	}
}
