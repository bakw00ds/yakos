package consoleui

// files_handler.go — Phase 7 (IDE): /api/files/* endpoints.
//
// Two read-only endpoints, both requiring RoleRead, both jailed to
// Config.WorkspaceRoot:
//
//	GET /api/files/tree?dir=<relpath>    — JSON file tree for a directory
//	GET /api/files/content?path=<relpath> — file content (UTF-8 or base64)
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
// Both endpoints omit / refuse files matching the secret deny-set.
// The tree endpoint omits secret-matched files entirely (they do not appear
// in entries). The content endpoint refuses with 403.
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
// - Content: 2 MiB hard cap → 413 with a clear message.
//
// # Idempotency
//
// Both endpoints are GET (read-only); idempotency is guaranteed by HTTP
// semantics and the absence of any state mutation.
//
// # Rate limiting
//
// Inherits the console's default edge rate-limit class (shared with all
// /api/* routes via the edge middleware in server.go).  No override.
import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// maxFileContentBytes is the hard size cap for /api/files/content.
const maxFileContentBytes = 2 << 20 // 2 MiB

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

	// Reject .git internals.
	if isGitPath(real, h.workspaceRoot) {
		return "", false
	}

	return real, true
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
}

// handleFilesContent serves GET /api/files/content?path=<relpath>.
//
// - Hard size cap: 2 MiB → 413.
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
		writeGenericError(w, http.StatusRequestEntityTooLarge, "file too large (max 2 MiB)")
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
