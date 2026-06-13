// Package selfupdate replaces the running yakos binary in-place from a
// GitHub release asset.
//
// Security model:
//   - HTTPS only; TLS verification on by default.
//   - GitHub repo is pinned in the code (repoOwner/repoName constants) and
//     never taken from user input.
//   - HTTP redirects are followed only when the destination uses HTTPS and
//     its host is on the allowlist (github.com, objects.githubusercontent.com,
//     releases.githubusercontent.com, codeload.github.com); any non-HTTPS
//     redirect or redirect to an unlisted host causes the download to abort.
//   - The release tag is validated against a strict regex before use in
//     any URL or filename, preventing path traversal.
//   - Every downloaded binary is SHA-256 verified against the release's
//     checksums.txt before the old binary is touched.
//   - The temp file is written into the same directory as the target binary
//     (same filesystem) so os.Rename is atomic.
//   - Permissions on the temp file are 0755 before rename.
//   - The temp file is always removed on failure; no partial binary is left
//     at the live path.
//
// Windows note: Windows does not allow renaming over a running .exe.  The
// implementation falls back to the "rename self out of the way first"
// pattern: the old binary is moved to <path>.old, then the new binary is
// written to the original path, and a best-effort removal of .old is
// attempted.  The caller's process continues using the original (old) image
// until it exits; the new binary is available at the original path
// immediately after the swap.
package selfupdate

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	repoOwner       = "bakw00ds"
	repoName        = "yakos"
	defaultTimeout  = 15 * time.Second
	githubAPIBase   = "https://api.github.com"
	githubRawBase   = "https://github.com"
	maxAssetBytes   = 256 << 20 // 256 MiB safety cap
	maxChecksumSize = 4 << 20   // 4 MiB

	// downloadTimeout is the http.Client.Timeout for the binary asset download
	// client.  It is deliberately 0 (no per-client timeout) so that a large
	// binary on a slow link is not killed by the client timeout.
	// The effective bound on a download is:
	//   (a) the caller's context deadline — runUpdateBinary sets 10 minutes, and
	//   (b) the 256 MiB maxAssetBytes LimitReader cap.
	// Metadata fetches (API, checksums.txt) use BuildDefaultClient whose
	// client.Timeout is 15 s, so widening the context does not make those hang.
	downloadTimeout = 0
)

// allowedRedirectHosts is the allowlist for HTTP redirect destinations.
var allowedRedirectHosts = []string{
	"github.com",
	"objects.githubusercontent.com",
	"releases.githubusercontent.com",
	"codeload.github.com",
}

// tagRe validates a release tag before it is interpolated into any URL or
// filename.  It accepts the forms "v1.2.3", "v1.2.3.4", "1.2.3", "1.2.3.4".
var tagRe = regexp.MustCompile(`^v?[0-9]+(\.[0-9]+){1,3}$`)

// releaseResponse is the subset of the GitHub releases API we need.
type releaseResponse struct {
	TagName string `json:"tag_name"`
}

// Result describes what happened after Apply returns successfully.
type Result struct {
	// OldVersion is the version string of the binary that was replaced (or the
	// running version when AlreadyUpToDate is true).
	OldVersion string

	// NewVersion is the tag of the release that was applied.
	NewVersion string

	// ExePath is the absolute path of the binary that was replaced.
	ExePath string

	// AlreadyUpToDate is true when the running version is >= the latest release
	// and Force was not set.
	AlreadyUpToDate bool
}

// Opts controls an Apply call.
type Opts struct {
	// CurrentVersion is the version string of the running binary, typically
	// version.Version (the ldflags-injected value, without " (go)" suffix).
	// If empty, the binary is always considered out-of-date (safe default that
	// makes the update proceed).
	CurrentVersion string

	// Force skips the version comparison and applies the latest release even
	// when the running version already matches.
	Force bool

	// DryRun fetches the latest tag and compares versions but does NOT
	// download or write anything.
	DryRun bool

	// HTTPClient is used for metadata fetches (GitHub API, checksums.txt).
	// If nil, a default client with the redirect allowlist policy and a
	// 15 s timeout is used.  Injected in tests.
	HTTPClient *http.Client

	// DownloadClient is used for the binary asset download only.
	// If nil, a client with no overall timeout is used (the caller's
	// context deadline governs instead; large binaries need the headroom).
	// Injected in tests so requests reach the test server.
	DownloadClient *http.Client

	// ExeResolver returns the absolute path to the running binary.
	// Defaults to os.Executable + filepath.EvalSymlinks.  Injected in tests.
	ExeResolver func() (string, error)

	// Writer receives informational output.  Defaults to os.Stdout.
	Writer io.Writer
}

// LatestRelease queries the GitHub releases API and returns the latest release
// tag (e.g. "v1.2.3.4").
func LatestRelease(ctx context.Context, client *http.Client) (string, error) {
	if client == nil {
		client = BuildDefaultClient()
	}

	apiURL := fmt.Sprintf("%s/repos/%s/%s/releases/latest", githubAPIBase, repoOwner, repoName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("selfupdate: build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("selfupdate: GitHub API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		return "", fmt.Errorf("selfupdate: GitHub API rate-limited (HTTP 403); try again later")
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("selfupdate: GitHub API returned HTTP %d", resp.StatusCode)
	}

	var rel releaseResponse
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", fmt.Errorf("selfupdate: decode GitHub response: %w", err)
	}
	if rel.TagName == "" {
		return "", fmt.Errorf("selfupdate: GitHub response missing tag_name")
	}
	if !tagRe.MatchString(rel.TagName) {
		return "", fmt.Errorf("selfupdate: invalid tag_name %q from GitHub API", rel.TagName)
	}
	return rel.TagName, nil
}

// Apply is the top-level entry point.  It fetches the latest release, checks
// whether an update is needed, downloads + verifies the binary, and atomically
// replaces the running executable.
func Apply(ctx context.Context, opts Opts) (*Result, error) {
	if opts.Writer == nil {
		opts.Writer = os.Stdout
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = BuildDefaultClient()
	}
	if opts.ExeResolver == nil {
		opts.ExeResolver = defaultExeResolver
	}

	// 1. Fetch latest release tag.
	tag, err := LatestRelease(ctx, opts.HTTPClient)
	if err != nil {
		return nil, err
	}

	// 2. Compare to running version.
	current := strings.TrimPrefix(strings.TrimSpace(opts.CurrentVersion), "v")
	latest := strings.TrimPrefix(tag, "v")

	result := &Result{
		OldVersion: opts.CurrentVersion,
		NewVersion: tag,
	}

	if !opts.Force && current != "" {
		newer, err := isNewer(latest, current)
		if err != nil {
			// Non-fatal: if we can't compare, proceed with the update.
			fmt.Fprintf(opts.Writer, "warning: version compare failed (%v); proceeding with update\n", err)
		} else if !newer {
			result.AlreadyUpToDate = true
			fmt.Fprintf(opts.Writer, "already up to date (v%s is the latest)\n", current)
			return result, nil
		}
	}

	if opts.DryRun {
		fmt.Fprintf(opts.Writer, "[dry-run] would update %s → %s\n", opts.CurrentVersion, tag)
		return result, nil
	}

	// 3. Resolve running binary path.
	exePath, err := opts.ExeResolver()
	if err != nil {
		return nil, fmt.Errorf("selfupdate: resolve executable: %w", err)
	}
	result.ExePath = exePath

	// 4. Build asset name.
	assetName, err := buildAssetName(tag)
	if err != nil {
		return nil, err
	}

	// 5. Download checksums.txt.
	checksumsURL := fmt.Sprintf(
		"%s/%s/%s/releases/download/%s/checksums.txt",
		githubRawBase, repoOwner, repoName, tag,
	)
	checksums, err := fetchChecksums(ctx, opts.HTTPClient, checksumsURL)
	if err != nil {
		return nil, fmt.Errorf("selfupdate: fetch checksums.txt: %w", err)
	}

	// 6. Look up expected hash for our asset.
	expectedHash, ok := checksums[assetName]
	if !ok {
		return nil, fmt.Errorf("selfupdate: no checksum entry for %q in checksums.txt — aborting", assetName)
	}

	// 7. Download binary.
	// Use a dedicated long-timeout client for the binary asset fetch.
	// The 15 s timeout on the metadata client is appropriate for small
	// JSON/checksum fetches but would cut off a large binary on a slow link.
	// When HTTPClient is injected (tests) we use it for the download too so
	// test servers remain reachable; in production HTTPClient is nil here and
	// we fall through to buildDownloadClient().
	assetURL := fmt.Sprintf(
		"%s/%s/%s/releases/download/%s/%s",
		githubRawBase, repoOwner, repoName, tag, assetName,
	)
	dlClient := opts.DownloadClient
	if dlClient == nil {
		// Production path: no overall timeout; context deadline governs.
		dlClient = buildDownloadClient()
	}
	assetBytes, err := fetchBinaryAsset(ctx, dlClient, assetURL)
	if err != nil {
		return nil, fmt.Errorf("selfupdate: download binary: %w", err)
	}

	// 8. Verify SHA-256.
	// TODO(security): detached signature verification — tracked as follow-up.
	// The checksums.txt file itself is not signed; a future release should
	// verify a detached GPG/cosign signature over checksums.txt before
	// trusting the hashes within it.
	if err := verifySHA256(assetBytes, expectedHash); err != nil {
		return nil, err // already prefixed
	}

	// 9. Atomic replace.
	if err := atomicReplace(exePath, assetBytes); err != nil {
		return nil, fmt.Errorf("selfupdate: replace binary: %w", err)
	}

	fmt.Fprintf(opts.Writer, "updated %s → %s\n", opts.CurrentVersion, tag)
	fmt.Fprintf(opts.Writer, "restart yakos to use the new version\n")
	return result, nil
}

// buildAssetName returns the release asset filename for the current platform.
// Tag must already be validated by tagRe.
func buildAssetName(tag string) (string, error) {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	// Map GOARCH to the naming used in the release workflow.
	archName := goarch // amd64, arm64 are used verbatim

	name := fmt.Sprintf("yakos-%s-%s-%s", tag, goos, archName)
	if goos == "windows" {
		name += ".exe"
	}
	return name, nil
}

// fetchChecksums downloads and parses a checksums.txt file, returning a map
// from filename to lowercase hex SHA-256 digest.
func fetchChecksums(ctx context.Context, client *http.Client, rawURL string) (map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxChecksumSize))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	return parseChecksums(string(body))
}

// parseChecksums parses the "<sha256>  <filename>" format written by
// sha256sum / openssl dgst, returning a filename→digest map.
func parseChecksums(text string) (map[string]string, error) {
	result := make(map[string]string)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Format: <64-hex-chars>  <filename>  (two spaces)
		// Also accept single space for tolerance.
		parts := strings.Fields(line)
		if len(parts) != 2 {
			return nil, fmt.Errorf("malformed checksums.txt line: %q", line)
		}
		hash := strings.ToLower(parts[0])
		file := parts[1]
		if len(hash) != 64 {
			return nil, fmt.Errorf("invalid SHA-256 hex length in checksums.txt: %q", hash)
		}
		result[file] = hash
	}
	if len(result) == 0 {
		return nil, errors.New("checksums.txt is empty or unreadable")
	}
	return result, nil
}

// fetchBinaryAsset downloads the asset and returns the raw bytes.
func fetchBinaryAsset(ctx context.Context, client *http.Client, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d fetching asset", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAssetBytes))
	if err != nil {
		return nil, fmt.Errorf("read asset body: %w", err)
	}
	return data, nil
}

// verifySHA256 returns an error (with a clear security message) when the
// computed digest of data does not match expected (lowercase hex).
func verifySHA256(data []byte, expected string) error {
	h := sha256.Sum256(data)
	actual := hex.EncodeToString(h[:])
	if actual != strings.ToLower(expected) {
		return fmt.Errorf(
			"selfupdate: checksum MISMATCH — download corrupt or tampered "+
				"(expected %s, got %s) — aborting; binary NOT replaced",
			expected, actual,
		)
	}
	return nil
}

// atomicReplace writes newBytes to a temp file in the same directory as
// exePath, then renames it over exePath.  On Unix this is atomic even while
// the old binary is running (the process holds an open fd to the old inode;
// the rename swaps the directory entry only).
//
// On Windows, os.Rename fails with "Access is denied" when the target is a
// running .exe.  The fallback renames the old exe to exePath+".old" first,
// then writes the new binary to exePath, and attempts to remove .old
// immediately.  Stale .old files (from a crash mid-replace) are cleaned up
// on the next invocation by removeStaleWindowsOld.
func atomicReplace(exePath string, newBytes []byte) error {
	// Proactively clean up any stale .old file from a previous Windows swap.
	if runtime.GOOS == "windows" {
		_ = os.Remove(exePath + ".old")
	}

	dir := filepath.Dir(exePath)
	tmp, err := os.CreateTemp(dir, ".yakos-update-*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()

	// Ensure temp is removed on any error path.
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(newBytes); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write new binary to temp: %w", err)
	}
	if err := tmp.Chmod(0755); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if runtime.GOOS == "windows" {
		// Windows: rename old binary out of the way first.
		oldPath := exePath + ".old"
		if err := os.Rename(exePath, oldPath); err != nil {
			return fmt.Errorf("rename old binary on Windows: %w", err)
		}
		if err := os.Rename(tmpPath, exePath); err != nil {
			// Attempt to restore the old binary; best effort.
			_ = os.Rename(oldPath, exePath)
			// Include oldPath in the error so the operator can recover
			// manually if the restore also fails.
			return fmt.Errorf("rename new binary on Windows (old binary is at %s): %w", oldPath, err)
		}
		// Best-effort remove the .old; may fail if something holds the file.
		_ = os.Remove(oldPath)
		success = true
		return nil
	}

	// Unix: atomic rename (the old inode is kept alive by the running process).
	if err := os.Rename(tmpPath, exePath); err != nil {
		return fmt.Errorf("rename temp to %s: %w", exePath, err)
	}
	success = true
	return nil
}

// defaultExeResolver returns the absolute, symlink-resolved path to the
// running binary.
func defaultExeResolver() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// tlsTransport returns an *http.Transport with TLS 1.2 as the minimum
// accepted version.  This is shared by both the metadata and download clients.
func tlsTransport() *http.Transport {
	return &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}
}

// BuildDefaultClient returns an *http.Client that enforces the redirect
// allowlist (HTTPS scheme + host), pins TLS 1.2 minimum, and sets a
// conservative 15 s timeout suitable for small metadata fetches
// (API responses, checksums.txt).  It is exported so tests can compose
// the real policy with a test-server transport.
func BuildDefaultClient() *http.Client {
	return &http.Client{
		Timeout:   defaultTimeout,
		Transport: tlsTransport(),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return checkRedirectHost(req.URL)
		},
	}
}

// buildDownloadClient returns an *http.Client for the binary asset download.
// client.Timeout is 0 (no per-client timeout); the effective download bound is
// the caller's context deadline (10 min in runUpdateBinary) plus the 256 MiB
// LimitReader in fetchBinaryAsset.  The same redirect policy and TLS 1.2
// minimum apply as the metadata client.
func buildDownloadClient() *http.Client {
	return &http.Client{
		Timeout:   downloadTimeout,
		Transport: tlsTransport(),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return checkRedirectHost(req.URL)
		},
	}
}

// checkRedirectHost returns an error when the redirect destination uses a
// non-HTTPS scheme or when the host is not on the allowlist.
// Both checks are required: an https→http downgrade to an allowlisted host
// would otherwise slip through.
func checkRedirectHost(u *url.URL) error {
	if strings.ToLower(u.Scheme) != "https" {
		return fmt.Errorf("selfupdate: redirect to non-HTTPS URL %q rejected", u.String())
	}
	host := strings.ToLower(u.Hostname())
	for _, allowed := range allowedRedirectHosts {
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return nil
		}
	}
	return fmt.Errorf("selfupdate: redirect to disallowed host %q rejected", host)
}

// isNewer reports whether candidate is strictly newer than base using the
// X.Y.Z.W versioning scheme (up to 4 dot-separated numeric components).
// A shorter version string is right-padded with zeros for comparison.
func isNewer(candidate, base string) (bool, error) {
	cv, err := parseVersion(candidate)
	if err != nil {
		return false, fmt.Errorf("candidate %q: %w", candidate, err)
	}
	bv, err := parseVersion(base)
	if err != nil {
		return false, fmt.Errorf("base %q: %w", base, err)
	}
	for i := range cv {
		if cv[i] > bv[i] {
			return true, nil
		}
		if cv[i] < bv[i] {
			return false, nil
		}
	}
	return false, nil // equal
}

// parseVersion splits a version string (optionally prefixed with "v") into a
// 4-element int slice, right-padding with zeros.
func parseVersion(s string) ([4]uint64, error) {
	s = strings.TrimPrefix(s, "v")
	parts := strings.SplitN(s, ".", 5)
	if len(parts) > 4 {
		return [4]uint64{}, fmt.Errorf("version %q: too many components (max 4)", s)
	}
	var v [4]uint64
	for i, p := range parts {
		if p == "" {
			return v, fmt.Errorf("version %q: empty component at index %d", s, i)
		}
		n, err := parseUint64(p)
		if err != nil {
			return v, fmt.Errorf("version %q component %q: %w", s, p, err)
		}
		v[i] = n
	}
	return v, nil
}

// parseUint64 parses a decimal string as uint64.
// strconv.ParseUint is used to avoid silent overflow on large values.
func parseUint64(s string) (uint64, error) {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, err
	}
	return n, nil
}
