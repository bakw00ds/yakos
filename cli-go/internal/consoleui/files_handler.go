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
// The content endpoint refuses to serve files matching secret patterns
// (*.pem, *.key, *credentials*, *.env*).  The tree endpoint lists them
// (so the editor can show them as greyed-out) but does NOT serve their
// content.  This is documented in the API contract.
//
// # Caps
//
// - Tree: 2000 entries per directory, max depth 10.  A `truncated` flag
//   is set when either cap fires.
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
	"strings"
	"unicode/utf8"
)

// maxFileContentBytes is the hard size cap for /api/files/content.
const maxFileContentBytes = 2 << 20 // 2 MiB

// maxTreeEntriesPerDir is the maximum number of entries returned per directory
// in /api/files/tree.  When exceeded, the response carries truncated=true.
const maxTreeEntriesPerDir = 2000

// maxTreeDepth is the maximum recursion depth for /api/files/tree.
// When exceeded, deeper directories are listed as entries but not expanded,
// and truncated=true is set.
const maxTreeDepth = 10

// skipDirs is the set of directory names that are always skipped in tree
// traversal (never descend, never list contents).
var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
}

// secretPatterns are glob-style suffix/substring checks for the content
// endpoint.  Files matching any pattern get 403 (secret content refused).
// The tree endpoint still lists them; only content is refused.
//
// Pattern rules (evaluated in order):
//   - suffix match for extension-based patterns (*.pem, *.key)
//   - substring match for name-based patterns (*credentials*, *.env*)
var secretPatterns = []struct {
	suffix    string
	substring string
}{
	{suffix: ".pem"},
	{suffix: ".key"},
	{suffix: ".env"},
	{substring: "credentials"},
	{substring: ".env."},
}

// isSecretFile reports whether the given filename (base only) matches any
// secret pattern.  Used by the content endpoint to refuse serving secrets.
func isSecretFile(name string) bool {
	lower := strings.ToLower(name)
	for _, p := range secretPatterns {
		if p.suffix != "" && strings.HasSuffix(lower, p.suffix) {
			return true
		}
		if p.substring != "" && strings.Contains(lower, p.substring) {
			return true
		}
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
func newFilesHandlers(workspaceRoot string) *filesHandlers {
	if workspaceRoot == "" {
		return &filesHandlers{workspaceRoot: ""}
	}
	// Resolve symlinks in the root itself so jailPath comparisons work on
	// platforms where temp/workspace paths pass through symlinks (macOS
	// /var → /private/var is the canonical example).
	realRoot, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		// If the root doesn't exist yet, use the cleaned path and let
		// the per-request handlers return 404/503 naturally.
		realRoot = filepath.Clean(workspaceRoot)
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
	// Reject empty to avoid resolving to workspaceRoot itself unintentionally.
	// (Callers pass "." for the tree root case.)

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
		// Path does not exist or is not accessible; treat as not-found for
		// tree/content handlers (caller will stat and 404 appropriately).
		// We still jail: if the error is "not exist" we allow the caller to
		// 404 cleanly. For any other error (permission denied etc.) refuse.
		if os.IsNotExist(err) {
			// Return the un-resolved candidate so callers can distinguish
			// "not found" from "escape" — the is-not-found branch below in
			// handlers will produce a proper 404 rather than a silent 403.
			// We still verify the prefix on the unresolved path above.
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
	// Children is non-nil only for "dir" entries when the tree was built
	// recursively.  Currently always nil (flat per-directory listing).
	Children []fileTreeEntry `json:"children,omitempty"`
}

// fileTreeResponse is the JSON response for GET /api/files/tree.
type fileTreeResponse struct {
	// Dir is the workspace-relative directory path that was listed.
	Dir string `json:"dir"`
	// Entries is the list of entries in the directory.
	Entries []fileTreeEntry `json:"entries"`
	// Truncated is true when the entry cap or depth cap was hit.
	Truncated bool `json:"truncated"`
}

// handleFilesTree serves GET /api/files/tree?dir=<relpath>.
//
// - Default dir is "." (workspace root).
// - Skips .git/ and node_modules/.
// - Caps at maxTreeEntriesPerDir entries.
// - Sorts: directories first, then alphabetically.
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

	entries, truncated := h.readDirEntries(absDir, 0)

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
// depth is the current recursion depth (0 = top-level).
// Returns entries and a truncated flag.
func (h *filesHandlers) readDirEntries(absDir string, depth int) ([]fileTreeEntry, bool) {
	rawEntries, err := os.ReadDir(absDir)
	if err != nil {
		slog.Error("consoleui/files: readdir", "dir", "...", "err", err)
		return nil, false
	}

	truncated := false
	if len(rawEntries) > maxTreeEntriesPerDir {
		rawEntries = rawEntries[:maxTreeEntriesPerDir]
		truncated = true
	}

	var dirs []fileTreeEntry
	var files []fileTreeEntry

	for _, de := range rawEntries {
		name := de.Name()

		if de.IsDir() {
			// Skip always-ignored dirs.
			if skipDirs[name] {
				continue
			}
			entPath := h.toRelPath(filepath.Join(absDir, name))
			entry := fileTreeEntry{
				Name: name,
				Path: entPath,
				Type: "dir",
			}
			// Recurse if not too deep.
			if depth+1 < maxTreeDepth {
				children, childTrunc := h.readDirEntries(filepath.Join(absDir, name), depth+1)
				entry.Children = children
				if childTrunc {
					truncated = true
				}
			} else {
				// At max depth: mark truncated so the caller knows.
				truncated = true
			}
			dirs = append(dirs, entry)
		} else {
			// Regular file (or symlink treated as file — we list it, content
			// endpoint will jail-resolve it).
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

	// Re-check secret on the real basename after resolution (symlink could change name).
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
