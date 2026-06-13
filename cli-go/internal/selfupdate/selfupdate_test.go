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

// discardWriter returns a fresh buffer that discards output (avoids os.Stdout noise).
// Each caller gets its own buffer so tests cannot accidentally share state.
func discardWriter() *bytes.Buffer {
	return &bytes.Buffer{}
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

	testClient := clientForServer(srv)
	res, err := selfupdate.Apply(context.Background(), selfupdate.Opts{
		CurrentVersion: "v1.0.0.0",
		HTTPClient:     testClient,
		DownloadClient: testClient,
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

	testClient := clientForServer(srv)
	res, err := selfupdate.Apply(context.Background(), selfupdate.Opts{
		CurrentVersion: fakeTag, // same as the server's latest
		HTTPClient:     testClient,
		DownloadClient: testClient,
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

	testClient := clientForServer(srv)
	_, err := selfupdate.Apply(context.Background(), selfupdate.Opts{
		CurrentVersion: "v1.0.0.0",
		HTTPClient:     testClient,
		DownloadClient: testClient,
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

	testClient := clientForServer(srv)
	res, err := selfupdate.Apply(context.Background(), selfupdate.Opts{
		CurrentVersion: "v1.0.0.0",
		DryRun:         true,
		HTTPClient:     testClient,
		DownloadClient: testClient,
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

	testClient := clientForServer(srv)
	res, err := selfupdate.Apply(context.Background(), selfupdate.Opts{
		CurrentVersion: fakeTag, // same version as server's latest
		Force:          true,
		HTTPClient:     testClient,
		DownloadClient: testClient,
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

// productionClientForServer returns a client that uses the REAL production
// CheckRedirect policy from BuildDefaultClient (scheme + host allowlist check)
// but routes initial requests to the given test server via hostRewriteTransport.
// Redirect targets are NOT rewritten — they hit the raw URL as issued by the
// test server, so the CheckRedirect policy sees the actual redirect destination.
func productionClientForServer(srv *httptest.Server) *http.Client {
	prod := selfupdate.BuildDefaultClient()
	prod.Transport = &hostRewriteTransport{
		base:   http.DefaultTransport,
		target: srv.URL,
	}
	return prod
}

// TestRedirectToDisallowedHostRejected verifies the redirect allowlist using
// the REAL production client so that any policy regression is caught here.
//
// Three sub-cases:
//  1. Redirect to a completely disallowed host via http:// — scheme check fires.
//  2. Redirect to an allowlisted host via https:// but a different disallowed
//     host component — host check fires (HIGH-1: scheme alone is insufficient).
//  3. Redirect from https → http on an allowlisted host (github.com) — scheme
//     check fires (HIGH-1 regression guard: downgrade must be rejected).
func TestRedirectToDisallowedHostRejected(t *testing.T) {
	cases := []struct {
		name        string
		redirectURL string
		wantErr     string // substring expected in the error
	}{
		{
			name:        "non_https_to_disallowed_host",
			redirectURL: "http://evil.example.com/payload",
			wantErr:     "non-HTTPS",
		},
		{
			name: "https_to_disallowed_host",
			// https:// scheme passes the scheme check; host check must reject it.
			redirectURL: "https://evil.example.com/payload",
			wantErr:     "disallowed host",
		},
		{
			name: "https_to_http_downgrade_on_allowlisted_host",
			// github.com IS on the allowlist, but http:// scheme must be rejected.
			redirectURL: "http://github.com/legit-looking-path",
			wantErr:     "non-HTTPS",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/repos/bakw00ds/yakos/releases/latest", func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, tc.redirectURL, http.StatusFound)
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			// Use the production client so the real CheckRedirect policy
			// is what's under test (HIGH-2).
			client := productionClientForServer(srv)

			_, err := selfupdate.LatestRelease(context.Background(), client)
			if err == nil {
				t.Fatalf("expected redirect rejection for %q, got nil", tc.redirectURL)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
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

	testClient := clientForServer(srv)
	exePath := makeTempExe(t, fakeBinaryOldContent)
	_, err := selfupdate.Apply(context.Background(), selfupdate.Opts{
		CurrentVersion: "v1.0.0.0",
		HTTPClient:     testClient,
		DownloadClient: testClient,
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

			testClient := clientForServer(srv)
			res, err := selfupdate.Apply(context.Background(), selfupdate.Opts{
				CurrentVersion: "v" + tc.base,
				DryRun:         true, // never write anything
				HTTPClient:     testClient,
				DownloadClient: testClient,
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

// TestVersionParseOverflow verifies that a version component that overflows
// uint64 is rejected with an error rather than silently wrapping.
// Before the strconv.ParseUint fix this would have silently accepted the value.
func TestVersionParseOverflow(t *testing.T) {
	// v99999999999999999999 is far beyond uint64 max (18446744073709551615).
	assetBytes := []byte(fakeBinaryContent)
	srv := newFakeServer(t, "v1.0.0.0", assetBytes, "")
	exePath := makeTempExe(t, fakeBinaryOldContent)

	testClient := clientForServer(srv)
	// The server returns v1.0.0.0; we pass an overflowing current version.
	// parseVersion should return an error; Apply should emit a warning and
	// proceed (the existing behavior on version-compare failure is to proceed
	// with the update, not abort — see Apply source).  The important thing is
	// that no panic or silent truncation occurs, and the warning is emitted so
	// a future silent-skip regression is caught here.
	buf := &bytes.Buffer{}
	_, err := selfupdate.Apply(context.Background(), selfupdate.Opts{
		CurrentVersion: "v99999999999999999999.0.0.0",
		DryRun:         true,
		HTTPClient:     testClient,
		DownloadClient: testClient,
		ExeResolver:    func() (string, error) { return exePath, nil },
		Writer:         buf,
	})
	// Apply warns and proceeds on version-compare failure — err should be nil.
	if err != nil {
		t.Fatalf("Apply should proceed on unparseable current version, got: %v", err)
	}
	if !strings.Contains(buf.String(), "warning") {
		t.Errorf("expected a warning line in output on overflow, got: %q", buf.String())
	}
}
