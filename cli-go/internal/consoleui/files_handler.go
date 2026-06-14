package consoleui

// files_handler.go — Phase 7 (IDE): /api/files/* endpoints.
//
// Read endpoints (RoleRead), both jailed to Config.WorkspaceRoot:
//
//	GET /api/files/tree?dir=<relpath>     — JSON file tree for a directory
//	GET /api/files/content?path=<relpath> — file content (UTF-8 or base64);
//	                                        returns "version" field (SHA-256 of
//	                                        raw bytes) for optimistic concurrency
//	                                        on subsequent writes.
//
// Write endpoint (RoleDispatch), jailed to Config.WorkspaceRoot:
//
//	POST /api/files/write                 — atomic write; optimistic concurrency
//	                                        via version echo.
//
// # Path jail
//
// Every request path goes through jailPath before any OS call:
//
//  1. Reject absolute paths and paths containing ".." segments.
//  2. Join with WorkspaceRoot and filepath.Clean.
//  3. Resolve symlinks with filepath.EvalSymlinks; re-assert the prefix.
//  4. Reject ".git/" internals (content + tree both refuse to descend).
//
// On any violation a generic 400/403 is returned — no path detail leaked.
//
// # Secret filtering
//
// All endpoints refuse secret-matched files.
// The tree endpoint omits secret-matched files entirely (they do not appear
// in entries). The content endpoint refuses with 403.
// The write endpoint refuses with 403 — operators must not persist secrets
// through the IDE write surface.
//
// Deny-set layers (evaluated against lowercased basename):
//
//  1. Exact basename: id_rsa, id_dsa, id_ecdsa, id_ed25519, .npmrc,
//     .netrc, .pgpass, .htpasswd.
//  2. Extension: .pem, .key, .p12, .pfx, .crt, .cer, .der, .keystore,
//     .jks, .asc, .gpg.
//  3. Substring: ".env" anywhere in the basename (covers .env, .env.local,
//     .envrc, .env_file), and "credentials" anywhere in the basename.
//
// # Caps
//
// - Tree: 2000 entries per directory, max depth 10, and a global total-node
//   budget of 10 000 across the entire recursive traversal.  A `truncated`
//   flag is set when any cap fires.
// - Content: 5 MiB hard cap → 413 with a clear message.
// - Write: 5 MiB hard cap on content → 413; request body LimitReader.
//
// # Write semantics (optimistic concurrency)
//
// POST /api/files/write accepts {path, content, version}:
//   - version = "" : create-new / force-write. Allowed whether or not the
//     file exists. Use this to create a new file or to force-overwrite when
//     the caller intentionally does not track version state.
//   - version != "": must match the SHA-256 hex of the current on-disk
//     content. Mismatch → 409 (no clobber; caller must reload).
//
// The write is atomic: content is written to a temp file in the same directory
// then renamed over the target. On rename failure the temp file is cleaned up.
//
// Parent directory creation: for v1, the parent directory must already exist.
// Writing to a non-existent parent directory path returns 400.  This prevents
// surprise directory creation from the IDE surface.
//
// File mode: 0644 on create; existing file mode is preserved on update.
//
// # Idempotency
//
// GET endpoints are idempotent by HTTP semantics.
// POST /api/files/write: callers MUST supply an Idempotency-Key header to
// make retries safe when the version is "". When version is non-empty and
// the write succeeds, a retry with the same version will 409 (the new
// on-disk hash won't match), which is safe — the data is already written.
// The caller should treat a 409 on retry as a successful first write and
// re-read the version.
//
// # Rate limiting
//
// Inherits the console's default edge rate-limit class (shared with all
// /api/* routes via the edge middleware in server.go).  No override.
import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/bakw00ds/yakos/internal/netid"
)

// maxFileContentBytes is the hard size cap for /api/files/content and
// the content field of /api/files/write requests.
const maxFileContentBytes = 5 << 20 // 5 MiB

// maxWriteRequestBodyBytes caps the raw HTTP request body for POST /api/files/write.
// Slightly larger than maxFileContentBytes to account for JSON framing overhead.
const maxWriteRequestBodyBytes = maxFileContentBytes + 4096

// fileContentHash returns the SHA-256 hex digest of data.
// Used as the version stamp for optimistic concurrency on file writes
// (mirrors contentHash in flows_handler.go but operates on raw file bytes).
func fileContentHash(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

// maxTreeEntriesPerDir is the maximum number of entries returned per directory
// in /api/files/tree.  When exceeded, the response carries truncated=true.
const maxTreeEntriesPerDir = 2000

// maxTreeDepth is the maximum recursion depth for /api/files/tree.
// When exceeded, deeper directories are included as leaf entries (not
// expanded), and truncated=true is set.
const maxTreeDepth = 10

// maxTreeTotalNodes is the global budget of tree entries (files + dirs)
// across the entire recursive traversal.  Prevents pathological wide+deep
// trees from spiking memory or latency.  truncated=true is set when hit.
const maxTreeTotalNodes = 10_000

// skipDirs is the set of directory names that are always skipped in tree
// traversal (never listed, never descended).
var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
}

// secretExactBasenames is the deny-set of exact lowercased basenames.
// These are common extensionless private-key / credential files.
var secretExactBasenames = map[string]bool{
	"id_rsa":     true,
	"id_dsa":     true,
	"id_ecdsa":   true,
	"id_ed25519": true,
	".npmrc":     true,
	".netrc":     true,
	".pgpass":    true,
	".htpasswd":  true,
}

// secretExtensions is the deny-set of lowercased file extensions.
var secretExtensions = map[string]bool{
	".pem":      true,
	".key":      true,
	".p12":      true,
	".pfx":      true,
	".crt":      true,
	".cer":      true,
	".der":      true,
	".keystore": true,
	".jks":      true,
	".asc":      true,
	".gpg":      true,
}

// isSecretFile reports whether the given filename (base only, any case) matches
// the secret deny-set.  Used by both the tree and content endpoints.
//
// Layers (short-circuit on first match):
//  1. Exact lowercased basename in secretExactBasenames.
//  2. Lowercased extension in secretExtensions.
//  3. Lowercased basename contains ".env" (covers .env, .env.local, .envrc,
//     .env_file) or "credentials".
func isSecretFile(name string) bool {
	lower := strings.ToLower(name)

	// Layer 1: exact basename.
	if secretExactBasenames[lower] {
		return true
	}

	// Layer 2: extension.
	ext := strings.ToLower(filepath.Ext(lower))
	if ext != "" && secretExtensions[ext] {
		return true
	}

	// Layer 3: substrings.
	if strings.Contains(lower, ".env") {
		return true
	}
	if strings.Contains(lower, "credentials") {
		return true
	}

	return false
}

// filesHandlers holds the /api/files/* handler set.
// workspaceRoot is the absolute, cleaned, symlink-resolved root that all
// paths are jailed to.  It is set once at construction time.
type filesHandlers struct {
	// workspaceRoot is the jailed root (absolute, clean, real path).
	// An empty string means the workspace feature is disabled; both
	// handlers will return 503 Service Unavailable.
	//
	// IMPORTANT: workspaceRoot is the symlink-resolved path of the
	// configured root (via filepath.EvalSymlinks at construction time).
	// This ensures that jailPath comparisons are always against the real
	// path, not a path that may have symlinks (e.g. /var → /private/var
	// on macOS).  Without this, EvalSymlinks on a subdirectory would return
	// a /private/var/... path that does not share the /var/... prefix.
	workspaceRoot string
}

// newFilesHandlers constructs a filesHandlers rooted at workspaceRoot.
// If workspaceRoot is empty, the handlers return 503 on every call.
// The workspaceRoot is passed through filepath.EvalSymlinks so that the
// jailed prefix comparison is always against the real (symlink-free) path.
//
// Fail-safe: if EvalSymlinks returns a non-not-exist error (e.g. permission
// denied), the jail cannot operate correctly against the unresolved root, so
// workspaceRoot is set to "" and handlers will return 503. A slog.Warn is
// emitted.  Only a genuine not-exist error is tolerated (path will 404 per
// request) because the workspace dir may not exist yet at startup.
func newFilesHandlers(workspaceRoot string) *filesHandlers {
	if workspaceRoot == "" {
		return &filesHandlers{workspaceRoot: ""}
	}
	// Resolve symlinks in the root itself so jailPath comparisons work on
	// platforms where temp/workspace paths pass through symlinks (macOS
	// /var → /private/var is the canonical example).
	realRoot, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		if os.IsNotExist(err) {
			// Workspace dir doesn't exist yet; use the cleaned path.
			// Per-request handlers will 404 when they try to stat.
			return &filesHandlers{workspaceRoot: filepath.Clean(workspaceRoot)}
		}
		// Any other error (permission denied, etc.): operating on an unresolved
		// root would break the jail prefix invariant.  Fail safe: disable the
		// file API and log a warning so the operator can investigate.
		slog.Warn("consoleui/files: EvalSymlinks on workspace root failed; file API disabled",
			"workspace_root", workspaceRoot, "err", err)
		return &filesHandlers{workspaceRoot: ""}
	}
	return &filesHandlers{workspaceRoot: realRoot}
}

// jailPath resolves relpath against h.workspaceRoot and asserts that the
// resulting absolute path is strictly inside the workspace.
//
// Returns the cleaned, symlink-resolved absolute path on success.
// Returns ("", false) — with no path details — on any violation.
//
// Violation conditions:
//   - workspaceRoot is empty (feature disabled).
//   - relpath is absolute (starts with "/").
//   - relpath contains ".." after filepath.Clean (traversal attempt).
//   - filepath.EvalSymlinks resolves to a path outside workspaceRoot.
//   - The resolved path is ".git" or inside ".git/".
//
// IMPORTANT: for paths that do not yet exist (new-file writes), EvalSymlinks
// on the full candidate fails with not-exist and this function returns the
// UNRESOLVED lexical candidate.  That is safe for READ paths (the subsequent
// os.Stat/os.ReadFile will 404), but is NOT safe for WRITE paths — a
// symlinked parent directory whose EvalSymlinks-resolved real location is
// outside the jail would pass the lexical isUnderRoot check.
//
// Use jailPathForCreate for write operations on new files.  It separately
// resolves the parent directory (which must already exist) and verifies the
// real parent is inside the jail, then returns realParent+base so all
// filesystem operations use the resolved path.
func (h *filesHandlers) jailPath(relpath string) (string, bool) {
	if h.workspaceRoot == "" {
		return "", false
	}
	// Reject absolute paths early.
	if filepath.IsAbs(relpath) {
		return "", false
	}

	// Build candidate path.
	candidate := filepath.Join(h.workspaceRoot, relpath)
	candidate = filepath.Clean(candidate)

	// Prefix-check before symlink resolution (fast rejection).
	if !isUnderRoot(candidate, h.workspaceRoot) {
		return "", false
	}

	// Reject .git internals on the candidate (pre-symlink) path.
	// This check runs before EvalSymlinks so that non-existent paths inside
	// .git/ (e.g. a new file the caller wants to create) are also rejected.
	// The post-symlink check below covers the case where a symlink resolves
	// to a .git path.
	if isGitPath(candidate, h.workspaceRoot) {
		return "", false
	}

	// Resolve symlinks: reject if the real path escapes the workspace.
	real, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		// Path does not exist or is not accessible.
		// For not-exist: return the unresolved candidate so callers can
		// distinguish "not found" from "escape" and produce a proper 404.
		// For any other error (permission denied etc.): refuse.
		if os.IsNotExist(err) {
			return candidate, true
		}
		return "", false
	}
	if !isUnderRoot(real, h.workspaceRoot) {
		return "", false
	}

	// Reject .git internals on the symlink-resolved path (belt-and-suspenders:
	// a symlink inside the jail could resolve to a .git path).
	if isGitPath(real, h.workspaceRoot) {
		return "", false
	}

	return real, true
}

// jailPathForCreate is the write-safe variant of jailPath for paths that may
// not yet exist.  It separately resolves the parent directory via
// filepath.EvalSymlinks (the parent MUST already exist per the v1 no-mkdir
// rule) and verifies that the real parent is inside the workspace jail.  The
// returned absPath is realParent+base — fully symlink-resolved — so all
// subsequent filesystem operations (WriteFile, Rename) act on the real
// location, never on an unresolved lexical candidate.
//
// This closes the symlinked-parent escape: a dir inside the workspace that
// is itself a symlink to an outside location would pass jailPath's lexical
// isUnderRoot check on the candidate but its EvalSymlinks-resolved real path
// would fail isUnderRoot here.
//
// Returns the real absolute path on success (parent resolved + basename appended).
// Returns ("", errJailParentNotExist) when the parent directory does not exist.
// Returns ("", errJailEscape) on any jail/secret/.git violation.
// Returns ("", other error) on unexpected OS errors.
//
// Secret and .git checks are performed against both the lexical candidate
// (fast rejection of obvious names) and the resolved real path (defence
// against symlink rename tricks — e.g. a symlink that makes "safe.txt"
// resolve into ".git/config" can't bypass the per-name checks because we
// check basename of the final resolved path too).
var errJailEscape = fmt.Errorf("path outside workspace jail")
var errJailParentNotExist = fmt.Errorf("parent directory does not exist")

func (h *filesHandlers) jailPathForCreate(relpath string) (string, error) {
	if h.workspaceRoot == "" {
		return "", errJailEscape
	}
	if filepath.IsAbs(relpath) {
		return "", errJailEscape
	}

	// Lexical candidate — used for early-reject checks only.
	candidate := filepath.Clean(filepath.Join(h.workspaceRoot, relpath))
	if !isUnderRoot(candidate, h.workspaceRoot) {
		return "", errJailEscape
	}
	if isGitPath(candidate, h.workspaceRoot) {
		return "", errJailEscape
	}

	// Secret check on the candidate basename (fast path, no symlink follow).
	baseName := filepath.Base(candidate)
	if isSecretFile(baseName) {
		return "", errJailEscape
	}

	// Resolve the PARENT directory through EvalSymlinks.
	// The parent must already exist (v1: no mkdir), so EvalSymlinks must succeed.
	candidateParent := filepath.Dir(candidate)
	realParent, err := filepath.EvalSymlinks(candidateParent)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errJailParentNotExist
		}
		// Permission denied or other error: refuse.
		return "", errJailEscape
	}

	// Assert the real parent is inside the workspace.
	// This is the critical check: if candidateParent was a symlink pointing
	// outside the workspace, realParent will not share the workspaceRoot prefix.
	if !isUnderRoot(realParent, h.workspaceRoot) {
		return "", errJailEscape
	}

	// Reject real parent inside .git (belt-and-suspenders against symlink tricks).
	if isGitPath(realParent, h.workspaceRoot) {
		return "", errJailEscape
	}

	// Build the fully-resolved target path: realParent + basename.
	// All filesystem ops (WriteFile, Rename) must use this path.
	realTarget := filepath.Join(realParent, baseName)

	// Secret check on the resolved basename (symlink rename defence: a symlink
	// named "safe.go" could resolve to ".env" if the parent dir has been
	// manipulated; baseName here is the name component of the original request
	// which is already validated, but we re-check for defence in depth).
	if isSecretFile(filepath.Base(realTarget)) {
		return "", errJailEscape
	}

	// Re-verify the assembled resolved path is still inside the workspace
	// (realParent+baseName should trivially be inside since realParent is
	// inside and baseName contains no separators, but be explicit).
	if !isUnderRoot(realTarget, h.workspaceRoot) {
		return "", errJailEscape
	}

	return realTarget, nil
}

// isUnderRoot reports whether absPath is equal to root or is strictly inside
// root (i.e. has root + separator as a prefix).
// Both paths must already be filepath.Clean'd.
func isUnderRoot(absPath, root string) bool {
	if absPath == root {
		return true
	}
	return strings.HasPrefix(absPath, root+string(filepath.Separator))
}

// isGitPath reports whether absPath is the .git directory or inside it.
func isGitPath(absPath, root string) bool {
	gitDir := filepath.Join(root, ".git")
	return absPath == gitDir ||
		strings.HasPrefix(absPath, gitDir+string(filepath.Separator))
}

// ---- /api/files/tree ---------------------------------------------------------

// fileTreeEntry is one entry in the /api/files/tree JSON response.
type fileTreeEntry struct {
	// Name is the base name of the entry.
	Name string `json:"name"`
	// Path is the workspace-relative path (forward-slash separated).
	Path string `json:"path"`
	// Type is "file" or "dir".
	Type string `json:"type"`
	// Size is the file size in bytes (0 for directories).
	Size int64 `json:"size"`
	// Children holds the entries of a "dir" entry when the recursive
	// traversal has expanded this directory (depth < maxTreeDepth and
	// total-node budget not exhausted).  Nil when the dir was not expanded.
	Children []fileTreeEntry `json:"children,omitempty"`
}

// fileTreeResponse is the JSON response for GET /api/files/tree.
type fileTreeResponse struct {
	// Dir is the workspace-relative directory path that was listed.
	Dir string `json:"dir"`
	// Entries is the list of entries in the directory.
	Entries []fileTreeEntry `json:"entries"`
	// Truncated is true when any cap (per-dir, depth, or total-node budget)
	// was hit during the traversal.
	Truncated bool `json:"truncated"`
}

// handleFilesTree serves GET /api/files/tree?dir=<relpath>&depth=<n>.
//
//   - Default dir is "." (workspace root).
//   - Default depth is 1 (immediate children only; dirs have no pre-populated
//     children in the response — the frontend lazy-loads on expand).
//   - depth is clamped to [1, maxTreeDepth].
//   - Skips .git/ and node_modules/.
//   - Omits secret-matched files entirely (same deny-set as content endpoint).
//   - Caps at maxTreeEntriesPerDir entries per dir, maxTreeDepth depth, and
//     maxTreeTotalNodes total nodes across the traversal.
//   - Sorts: directories first, then alphabetically within each group.
func (h *filesHandlers) handleFilesTree(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.workspaceRoot == "" {
		writeGenericError(w, http.StatusServiceUnavailable, "workspace root not configured")
		return
	}

	reldir := r.URL.Query().Get("dir")
	if reldir == "" {
		reldir = "."
	}

	// Parse optional depth param; default 1, clamp to [1, maxTreeDepth].
	maxDepth := 1
	if depthStr := r.URL.Query().Get("depth"); depthStr != "" {
		if n, err := strconv.Atoi(depthStr); err == nil {
			maxDepth = n
		}
	}
	if maxDepth < 1 {
		maxDepth = 1
	}
	if maxDepth > maxTreeDepth {
		maxDepth = maxTreeDepth
	}

	absDir, ok := h.jailPath(reldir)
	if !ok {
		writeGenericError(w, http.StatusBadRequest, "invalid directory path")
		return
	}

	// Stat to check it exists and is a directory.
	fi, err := os.Stat(absDir)
	if err != nil {
		if os.IsNotExist(err) {
			writeGenericError(w, http.StatusNotFound, "directory not found")
			return
		}
		slog.Error("consoleui/files: tree stat", "err", err)
		writeGenericError(w, http.StatusInternalServerError, "failed to stat directory")
		return
	}
	if !fi.IsDir() {
		writeGenericError(w, http.StatusBadRequest, "path is not a directory")
		return
	}

	// totalNodes is a shared counter across the recursive traversal.
	// Passed by pointer so every readDirEntries call deducts from the same budget.
	totalNodes := maxTreeTotalNodes
	entries, truncated := h.readDirEntries(absDir, 0, maxDepth, &totalNodes)

	// Build workspace-relative path for the response "dir" field.
	dirRel := h.toRelPath(absDir)

	resp := fileTreeResponse{
		Dir:       dirRel,
		Entries:   entries,
		Truncated: truncated,
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

// readDirEntries reads one directory level and builds the entry list.
// depth is the current recursion depth (0 = top-level called from handler).
// maxDepth is the maximum depth to recurse; when depth >= maxDepth the function
// emits dir entries without expanding their children (lazy-load sentinel).
// totalNodes is a pointer to the global node budget; each file/dir consumes 1.
// Returns entries and a truncated flag.
func (h *filesHandlers) readDirEntries(absDir string, depth int, maxDepth int, totalNodes *int) ([]fileTreeEntry, bool) {
	rawEntries, err := os.ReadDir(absDir)
	if err != nil {
		slog.Error("consoleui/files: readdir", "err", err)
		return nil, false
	}

	truncated := false

	// Per-directory cap.
	if len(rawEntries) > maxTreeEntriesPerDir {
		rawEntries = rawEntries[:maxTreeEntriesPerDir]
		truncated = true
	}

	var dirs []fileTreeEntry
	var files []fileTreeEntry

	for _, de := range rawEntries {
		name := de.Name()

		if de.IsDir() {
			// Skip always-ignored dirs (no entry at all).
			if skipDirs[name] {
				continue
			}

			// Deduct from global budget.
			if *totalNodes <= 0 {
				truncated = true
				break
			}
			*totalNodes--

			entPath := h.toRelPath(filepath.Join(absDir, name))
			entry := fileTreeEntry{
				Name: name,
				Path: entPath,
				Type: "dir",
			}
			// Recurse if not at max depth and budget remains.
			// When depth >= maxDepth, emit a dir entry without children so the
			// frontend can lazy-load on expand.
			if depth+1 < maxDepth && *totalNodes > 0 {
				children, childTrunc := h.readDirEntries(filepath.Join(absDir, name), depth+1, maxDepth, totalNodes)
				entry.Children = children
				if childTrunc {
					truncated = true
				}
			} else if depth+1 >= maxDepth {
				// Reached requested depth cap — children omitted for lazy loading.
				// Do NOT mark the response truncated: this is expected, not an error.
			} else if *totalNodes <= 0 {
				// Global budget exhausted.
				truncated = true
			}
			dirs = append(dirs, entry)
		} else {
			// Skip secret-matched files: they are omitted from the tree
			// entirely (same deny-set as the content endpoint).
			if isSecretFile(name) {
				continue
			}

			// Deduct from global budget.
			if *totalNodes <= 0 {
				truncated = true
				break
			}
			*totalNodes--

			fi, err := de.Info()
			if err != nil {
				continue // Skip unreadable entries silently.
			}
			entPath := h.toRelPath(filepath.Join(absDir, name))
			files = append(files, fileTreeEntry{
				Name: name,
				Path: entPath,
				Type: "file",
				Size: fi.Size(),
			})
		}
	}

	// Sort each group alphabetically, then concat dirs-first.
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name < dirs[j].Name })
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })

	result := make([]fileTreeEntry, 0, len(dirs)+len(files))
	result = append(result, dirs...)
	result = append(result, files...)

	return result, truncated
}

// toRelPath converts an absolute path inside the workspace to a
// workspace-relative path using forward slashes.
func (h *filesHandlers) toRelPath(abs string) string {
	rel, err := filepath.Rel(h.workspaceRoot, abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(rel)
}

// ---- /api/files/content ------------------------------------------------------

// fileContentResponse is the JSON response for GET /api/files/content.
type fileContentResponse struct {
	// Path is the workspace-relative path.
	Path string `json:"path"`
	// Encoding is "utf-8" or "base64".
	Encoding string `json:"encoding"`
	// Content is the file content, either as a UTF-8 string or a base64 string.
	Content string `json:"content"`
	// Size is the file size in bytes.
	Size int64 `json:"size"`
	// Language is a Monaco language ID derived from the file extension.
	// Empty string means "plaintext".
	Language string `json:"language,omitempty"`
	// Version is the SHA-256 hex digest of the raw file bytes.
	// Clients MUST echo this value in the "version" field of POST /api/files/write
	// to enable optimistic concurrency (stale-write protection).
	// The hash is computed over raw bytes regardless of encoding (UTF-8 or base64).
	Version string `json:"version"`
}

// handleFilesContent serves GET /api/files/content?path=<relpath>.
//
// - Hard size cap: 5 MiB → 413.
// - Binary detection: NUL byte or invalid UTF-8 → base64 encoding.
// - Secret patterns: refused with a generic 403.
func (h *filesHandlers) handleFilesContent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.workspaceRoot == "" {
		writeGenericError(w, http.StatusServiceUnavailable, "workspace root not configured")
		return
	}

	relpath := r.URL.Query().Get("path")
	if relpath == "" {
		writeGenericError(w, http.StatusBadRequest, "path parameter required")
		return
	}

	// Reject secret patterns by filename before the jail (fast path; no info leak).
	baseName := filepath.Base(relpath)
	if isSecretFile(baseName) {
		writeGenericError(w, http.StatusForbidden, "access to this file type is not permitted")
		return
	}

	absPath, ok := h.jailPath(relpath)
	if !ok {
		writeGenericError(w, http.StatusBadRequest, "invalid file path")
		return
	}

	// Re-check secret on the real basename after resolution (symlink could rename).
	if isSecretFile(filepath.Base(absPath)) {
		writeGenericError(w, http.StatusForbidden, "access to this file type is not permitted")
		return
	}

	fi, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			writeGenericError(w, http.StatusNotFound, "file not found")
			return
		}
		slog.Error("consoleui/files: content stat", "err", err)
		writeGenericError(w, http.StatusInternalServerError, "failed to stat file")
		return
	}

	if fi.IsDir() {
		writeGenericError(w, http.StatusBadRequest, "path is a directory; use /api/files/tree")
		return
	}

	// Hard size cap.
	if fi.Size() > maxFileContentBytes {
		writeGenericError(w, http.StatusRequestEntityTooLarge, "file too large (max 5 MiB)")
		return
	}

	data, err := os.ReadFile(absPath) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			writeGenericError(w, http.StatusNotFound, "file not found")
			return
		}
		slog.Error("consoleui/files: content read", "err", err)
		writeGenericError(w, http.StatusInternalServerError, "failed to read file")
		return
	}

	// Detect encoding: binary (NUL byte or invalid UTF-8) → base64.
	var encoding, content string
	if isBinary(data) {
		encoding = "base64"
		content = base64.StdEncoding.EncodeToString(data)
	} else {
		encoding = "utf-8"
		content = string(data)
	}

	relForResponse := h.toRelPath(absPath)

	resp := fileContentResponse{
		Path:     relForResponse,
		Encoding: encoding,
		Content:  content,
		Size:     fi.Size(),
		Language: languageFromExt(filepath.Ext(absPath)),
		Version:  fileContentHash(data), // hash over raw bytes, not the encoded form
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

// isBinary reports whether data appears to be binary content.
// Detection strategy:
//  1. Any NUL byte → binary.
//  2. Invalid UTF-8 sequence → binary.
func isBinary(data []byte) bool {
	if bytes.IndexByte(data, 0) >= 0 {
		return true
	}
	return !utf8.Valid(data)
}

// ---- /api/files/write -------------------------------------------------------

// fileWriteRequest is the DTO for POST /api/files/write.
// Bound from the request body; never bound directly to a domain struct.
type fileWriteRequest struct {
	// Path is the workspace-relative path to write.
	Path string `json:"path"`
	// Content is the UTF-8 text content to write.
	// NOTE: this endpoint only accepts UTF-8 text.  Binary files read via
	// GET /api/files/content are returned with encoding="base64"; the client
	// must decode the base64 back to raw bytes and re-encode as UTF-8 text
	// before POSTing, or use a different mechanism.  Submitting raw base64
	// strings as content will save the base64 text literally on disk.
	Content string `json:"content"`
	// Version is the last-seen version stamp (SHA-256 hex of previous content).
	// If empty, the write is treated as a create/force-write (no stale check).
	// If non-empty, must match the current on-disk SHA-256 or a 409 is returned.
	Version string `json:"version"`
}

// fileWriteResponse is the wire response for a successful POST /api/files/write.
type fileWriteResponse struct {
	// Path is the workspace-relative path that was written.
	Path string `json:"path"`
	// Version is the SHA-256 hex digest of the newly written content.
	// The client must use this value on the next write to avoid 409 Conflict.
	Version string `json:"version"`
	// Size is the number of bytes written.
	Size int64 `json:"size"`
}

// handleFilesWrite serves POST /api/files/write.
//
// Security model:
//   - Role: RoleDispatch (checked here in addition to the route wrapper).
//   - Jail: existing files go through jailPath (full EvalSymlinks on target).
//     New files go through jailPathForCreate (EvalSymlinks on parent dir,
//     which closes the symlinked-parent escape described below).
//   - Symlinked-parent escape closed: a directory inside the workspace that
//     is itself a symlink to an outside location passes jailPath's lexical
//     isUnderRoot check on the unresolved candidate, but jailPathForCreate
//     resolves the parent with EvalSymlinks and asserts the real parent is
//     inside the jail before any write touches the filesystem.
//   - Secrets: isSecretFile → 403.
//   - .git internals: rejected by both jail functions.
//   - WorkspaceRoot unset → 503.
//   - Content > 5 MiB → 413.
//   - Optimistic concurrency: version != "" → compare with on-disk SHA-256.
//     Mismatch → 409. version == "" → force-write (create or overwrite).
//   - Parent dir must exist (v1: no mkdir). Non-existent parent → 400.
//   - Atomic temp-rename write in the real (symlink-resolved) parent directory.
//   - File mode: 0644 on create; preserve existing mode on update.
//
// Idempotency: POST /api/files/write is NOT idempotent by default. Callers
// should supply an Idempotency-Key header when using version="" to avoid
// double-creation. When version is echoed from a prior read, the 409-on-retry
// semantics guarantee that a duplicate write is safe (data already persisted).
func (h *filesHandlers) handleFilesWrite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Per-handler role check: write requires RoleDispatch.
	// Fires only when resolver middleware has run (Resolved==true).
	// Loopback tests using srv.Handler() bypass this (Resolved=false → no-op).
	if id := netid.IdentityFrom(r.Context()); id.Resolved && !id.Role.Allows(netid.RoleDispatch) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if h.workspaceRoot == "" {
		writeGenericError(w, http.StatusServiceUnavailable, "workspace root not configured")
		return
	}

	// Cap and read the request body.
	body, err := io.ReadAll(io.LimitReader(r.Body, int64(maxWriteRequestBodyBytes)+1))
	if err != nil {
		writeGenericError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	if len(body) > maxWriteRequestBodyBytes {
		writeGenericError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}

	var req fileWriteRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeGenericError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if req.Path == "" {
		writeGenericError(w, http.StatusBadRequest, "path is required")
		return
	}

	// Reject secret patterns by filename before the jail (fast path; no info leak).
	baseName := filepath.Base(req.Path)
	if isSecretFile(baseName) {
		writeGenericError(w, http.StatusForbidden, "writing this file type is not permitted")
		return
	}

	// Content size cap (over the raw UTF-8 string bytes).
	contentBytes := []byte(req.Content)
	if len(contentBytes) > maxFileContentBytes {
		writeGenericError(w, http.StatusRequestEntityTooLarge, "content too large (max 5 MiB)")
		return
	}

	// --- Phase 1: determine whether the target file already exists.
	//
	// We use jailPath first; if the target exists it returns the fully
	// EvalSymlinks-resolved real path.  If the target does not exist,
	// jailPath returns an unresolved lexical candidate — which is NOT safe
	// for writes (symlinked parent dirs would pass the lexical jail check but
	// land outside the workspace).  For the not-exist case we switch to
	// jailPathForCreate which resolves the parent directory independently.
	jailedPath, ok := h.jailPath(req.Path)
	if !ok {
		writeGenericError(w, http.StatusBadRequest, "invalid file path")
		return
	}

	// Attempt to stat the jailed path to decide which branch we're on.
	fi, statErr := os.Stat(jailedPath)
	fileExists := statErr == nil && !fi.IsDir()

	var absPath string // the fully-resolved path all filesystem ops will use

	if fileExists {
		// Target exists: jailPath already returned the EvalSymlinks-resolved
		// path (EvalSymlinks succeeded).  Use it directly.
		absPath = jailedPath
	} else if statErr != nil && !os.IsNotExist(statErr) {
		// Unexpected stat error (e.g. permission denied).
		slog.Error("consoleui/files: write stat target", "err", statErr)
		writeGenericError(w, http.StatusInternalServerError, "failed to stat target path")
		return
	} else if fi != nil && fi.IsDir() {
		writeGenericError(w, http.StatusBadRequest, "path is a directory")
		return
	} else {
		// Target does not exist: use jailPathForCreate to resolve the parent
		// directory via EvalSymlinks and verify the real parent is inside the
		// workspace jail.  This closes the symlinked-parent escape.
		realTarget, createErr := h.jailPathForCreate(req.Path)
		switch createErr {
		case nil:
			absPath = realTarget
		case errJailParentNotExist:
			writeGenericError(w, http.StatusBadRequest, "parent directory does not exist")
			return
		default:
			writeGenericError(w, http.StatusBadRequest, "invalid file path")
			return
		}
	}

	// Re-check secret on the resolved basename after jail (defence against
	// symlink rename tricks where a link name looks safe but resolves to a
	// secret name — e.g. parent/.env_link → secret.
	if isSecretFile(filepath.Base(absPath)) {
		writeGenericError(w, http.StatusForbidden, "writing this file type is not permitted")
		return
	}

	// Optimistic concurrency: read existing content and compare versions.
	var existingData []byte
	if fileExists {
		var readErr error
		existingData, readErr = os.ReadFile(absPath) //nolint:gosec
		if readErr != nil {
			if os.IsNotExist(readErr) {
				// File disappeared between stat and read (TOCTOU): treat as
				// version conflict so the caller reloads.
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error": "file changed on disk; reload before saving",
				})
				return
			}
			slog.Error("consoleui/files: write read existing", "err", readErr)
			writeGenericError(w, http.StatusInternalServerError, "failed to check existing version")
			return
		}
	}

	if req.Version != "" && fileExists {
		diskVersion := fileContentHash(existingData)
		if diskVersion != req.Version {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "file changed on disk; reload before saving",
			})
			return
		}
	}
	// If version == "": force-write — no stale check, allowed for both new and existing files.
	// If version != "" and !fileExists: file was deleted since last read; treat as version mismatch.
	if req.Version != "" && !fileExists {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "file changed on disk; reload before saving",
		})
		return
	}

	// Determine file mode: preserve existing mode on update, use 0644 for new files.
	fileMode := os.FileMode(0644)
	if fileExists {
		if fstat, err := os.Stat(absPath); err == nil {
			fileMode = fstat.Mode().Perm()
		}
	}

	// Atomic write: temp file in the RESOLVED parent dir + rename.
	// Using the resolved parent (not the lexical candidate) ensures the temp
	// file and the final rename both land in the validated real directory.
	tmpPath := absPath + ".yakos-write-tmp"
	if err := os.WriteFile(tmpPath, contentBytes, fileMode); err != nil { //nolint:gosec
		slog.Error("consoleui/files: write tmp", "err", err)
		writeGenericError(w, http.StatusInternalServerError, "failed to write file")
		return
	}
	if err := os.Rename(tmpPath, absPath); err != nil {
		_ = os.Remove(tmpPath)
		slog.Error("consoleui/files: write rename", "err", err)
		writeGenericError(w, http.StatusInternalServerError, "failed to save file")
		return
	}

	newVersion := fileContentHash(contentBytes)
	relForResponse := h.toRelPath(absPath)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(fileWriteResponse{
		Path:    relForResponse,
		Version: newVersion,
		Size:    int64(len(contentBytes)),
	})
}

// languageFromExt maps a file extension (including the leading dot) to a
// Monaco editor language ID.  Returns "" (plaintext) for unknown extensions.
func languageFromExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".go":
		return "go"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".ts", ".mts", ".cts":
		return "typescript"
	case ".jsx":
		return "javascriptreact"
	case ".tsx":
		return "typescriptreact"
	case ".md", ".markdown":
		return "markdown"
	case ".yaml", ".yml":
		return "yaml"
	case ".json", ".jsonc":
		return "json"
	case ".sh", ".bash", ".zsh":
		return "shell"
	case ".py":
		return "python"
	case ".rb":
		return "ruby"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".c":
		return "c"
	case ".cpp", ".cc", ".cxx":
		return "cpp"
	case ".cs":
		return "csharp"
	case ".html", ".htm":
		return "html"
	case ".css":
		return "css"
	case ".scss":
		return "scss"
	case ".sql":
		return "sql"
	case ".xml":
		return "xml"
	case ".toml":
		return "toml"
	case ".dockerfile":
		return "dockerfile"
	case ".proto":
		return "protobuf"
	default:
		return ""
	}
}
