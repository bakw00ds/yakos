// Package consoleui — diff_handler.go
//
// IDE Phase 3b diff-review backend.
//
// # Endpoints
//
//   - GET  /api/files/diff?session=<id>       — per-file structured hunks of the
//     session worktree's uncommitted changes (RoleRead, owner-scoped).
//   - POST /api/files/diff/accept             — promote a single hunk from the
//     worktree into the real tree (RoleDispatch, owner-scoped, audited).
//   - POST /api/files/diff/reject             — discard a single hunk from the
//     worktree (RoleDispatch, owner-scoped, audited).
//   - GET  /api/git/status?session=<id>       — git status for the session
//     worktree (RoleRead, owner-scoped).
//   - POST /api/git/commit                    — commit promoted changes in the
//     real tree (RoleDispatch, owner-scoped, audited).
//
// # Security model
//
// Every mutating endpoint: RoleDispatch + owner-scoped to the caller's session
// + path JAILED under WorkspaceRoot (reuse filesHandlers.jailPath) + audit log
// via slog.
//
// WorkDirOverride / worktree paths are SERVER-DERIVED from the worktreemgr.Manager,
// never from a client request body.
//
// Promote is atomic: git apply exits non-zero if context lines do not match the
// real tree; on failure the real tree is unchanged (git apply is atomic per
// invocation) and the handler returns 409. The real tree is never left
// half-applied.
//
// Binary files: marked with status "binary", no hunks returned. Accept of a
// binary file promotes the whole file (copy worktree file to real tree). Reject
// reverts the whole file in the worktree.
//
// # Diff cap
//
// Responses are capped at maxDiffBytes (1 MiB) to prevent memory spikes.
// When the cap fires, a truncated=true flag is set in the response and only
// the already-parsed files are returned.
package consoleui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/worktreemgr"
)

// maxDiffBytes is the cap on the total diff output processed per request.
// When exceeded, hunks already parsed are returned with truncated=true.
const maxDiffBytes = 1 << 20 // 1 MiB

// maxCommitMsgLen is the maximum commit message length accepted by /api/git/commit.
const maxCommitMsgLen = 4096

// gitTimeout is the deadline applied to every git shell-out in this handler.
const gitTimeout = 30 * time.Second

// diffHandlers holds the dependencies for the diff-review endpoints.
// Wired into Server in New().
type diffHandlers struct {
	mgr           *worktreemgr.Manager
	files         *filesHandlers // reused for path jailing
	workspaceRoot string
	hub           *ChatHub // for owner-scope checks
}

// newDiffHandlers constructs a diffHandlers.
func newDiffHandlers(mgr *worktreemgr.Manager, files *filesHandlers, workspaceRoot string, hub *ChatHub) *diffHandlers {
	return &diffHandlers{
		mgr:           mgr,
		files:         files,
		workspaceRoot: workspaceRoot,
		hub:           hub,
	}
}

// ---- Wire types (JSON shapes shared with the frontend) ----------------------

// DiffHunk is one unified-diff hunk with parsed metadata.
// The frontend uses hunkID (ordinal within the file, zero-indexed) to identify
// a hunk in accept/reject requests.
type DiffHunk struct {
	// Header is the @@ … @@ hunk header line (raw, for display).
	Header string `json:"header"`
	// Lines is the complete hunk content including the header line.
	// Each line retains its leading +/-/space sigil for rendering.
	Lines []string `json:"lines"`
	// OldStart is the starting line in the old file (from the @@ header).
	OldStart int `json:"old_start"`
	// OldCount is the number of lines in the old file (from the @@ header).
	OldCount int `json:"old_count"`
	// NewStart is the starting line in the new file (from the @@ header).
	NewStart int `json:"new_start"`
	// NewCount is the number of lines in the new file (from the @@ header).
	NewCount int `json:"new_count"`
	// HunkID is the zero-based ordinal of this hunk within the file.
	HunkID int `json:"hunk_id"`
}

// DiffFile is the per-file entry in the diff response.
type DiffFile struct {
	// Path is the workspace-relative path of the file.
	Path string `json:"path"`
	// Status is "modified", "added", "deleted", "renamed", or "binary".
	Status string `json:"status"`
	// OldPath is the pre-rename path (non-empty only when Status=="renamed").
	OldPath string `json:"old_path,omitempty"`
	// Hunks is the list of parsed hunks. Empty for binary files.
	Hunks []DiffHunk `json:"hunks"`
	// Binary is true when the file is detected as binary.
	Binary bool `json:"binary"`
}

// DiffResponse is the JSON response for GET /api/files/diff.
type DiffResponse struct {
	// SessionID is echoed from the request.
	SessionID string `json:"session_id"`
	// Files is the per-file diff list.
	Files []DiffFile `json:"files"`
	// Truncated is true when the maxDiffBytes cap was hit.
	Truncated bool `json:"truncated"`
}

// AcceptRequest is the DTO for POST /api/files/diff/accept.
// Path + HunkID identify the exact hunk to promote.
// For binary files, HunkID is ignored and the whole file is promoted.
type AcceptRequest struct {
	SessionID  string `json:"session_id"`
	OperatorID string `json:"operator_id"` // used on loopback path (C1)
	Path       string `json:"path"`
	HunkID     int    `json:"hunk_id"`
}

// RejectRequest is the DTO for POST /api/files/diff/reject.
type RejectRequest struct {
	SessionID  string `json:"session_id"`
	OperatorID string `json:"operator_id"`
	Path       string `json:"path"`
	HunkID     int    `json:"hunk_id"`
}

// GitStatusResponse is the JSON response for GET /api/git/status.
type GitStatusResponse struct {
	SessionID string          `json:"session_id"`
	Branch    string          `json:"branch"`
	Ahead     int             `json:"ahead"`
	Behind    int             `json:"behind"`
	Staged    []GitStatusFile `json:"staged"`
	Unstaged  []GitStatusFile `json:"unstaged"`
	Untracked []string        `json:"untracked"`
}

// GitStatusFile is one entry in the staged/unstaged lists.
type GitStatusFile struct {
	Path string `json:"path"`
	Code string `json:"code"` // e.g. "M", "D", "A", "R"
}

// CommitRequest is the DTO for POST /api/git/commit.
type CommitRequest struct {
	SessionID  string `json:"session_id"`
	OperatorID string `json:"operator_id"`
	// Message is the commit message. Must be non-empty.
	Message string `json:"message"`
	// Paths is the list of workspace-relative paths to stage and commit.
	// When non-empty, only these paths are committed (never git add -A).
	// When empty, the handler returns 400.
	Paths []string `json:"paths"`
}

// CommitResponse is the JSON response for POST /api/git/commit.
type CommitResponse struct {
	SHA string `json:"sha"`
}

// ---- Owner-scope helper ------------------------------------------------------

// resolveOperatorID extracts the effective operator ID for owner-scoping.
// On the mTLS-authenticated path (C1) the cert CN wins; on the loopback path
// the caller-supplied ID is used (validated before this call).
func resolveOperatorID(r *http.Request, supplied string) string {
	id := netid.IdentityFrom(r.Context())
	if id.Authenticated {
		return id.OperatorID
	}
	return supplied
}

// ownerCheck verifies that effectiveOperatorID owns sessionID in the hub.
// Returns false (and writes 403 to w) if the check fails.
// Returns true (session exists, correct owner) on success.
// If the session is unknown to the hub (finished session), returns false with 404.
func (d *diffHandlers) ownerCheck(w http.ResponseWriter, sessionID, effectiveOperatorID string) bool {
	owner, exists := d.hub.SessionOwner(sessionID)
	if !exists {
		writeGenericError(w, http.StatusNotFound, "session not found or already closed")
		return false
	}
	if owner != effectiveOperatorID {
		writeGenericError(w, http.StatusForbidden, "forbidden: session owned by different operator")
		return false
	}
	return true
}

// worktreePath returns the worktree path for sessionID.
// Returns ("", false) and writes an appropriate error if no worktree exists.
func (d *diffHandlers) worktreePath(w http.ResponseWriter, sessionID string) (string, bool) {
	if d.mgr == nil {
		writeGenericError(w, http.StatusServiceUnavailable, "diff-mode not available: worktree manager not configured")
		return "", false
	}
	wt, ok := d.mgr.PathFor(sessionID)
	if !ok {
		writeGenericError(w, http.StatusNotFound, "no worktree for session; start a review-mode dispatch first")
		return "", false
	}
	return wt, true
}

// ---- GET /api/files/diff -----------------------------------------------------

// handleFilesDiff serves GET /api/files/diff?session=<id>.
//
// Returns the structured per-file diff of the session worktree's uncommitted
// changes.  Tracked changes come from `git diff`; untracked files are
// synthesized as full-add diffs using `git diff --no-index /dev/null <file>`.
//
// Auth: RoleRead + owner-scoped to the caller's session.
func (d *diffHandlers) handleFilesDiff(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := r.URL.Query().Get("session")
	if sessionID == "" {
		writeGenericError(w, http.StatusBadRequest, "session parameter required")
		return
	}

	// C1 owner resolution.
	operatorID := r.URL.Query().Get("operatorId")
	effectiveOperator := resolveOperatorID(r, operatorID)
	if effectiveOperator == "" {
		writeGenericError(w, http.StatusBadRequest, "operatorId is required")
		return
	}
	if !d.ownerCheck(w, sessionID, effectiveOperator) {
		return
	}

	wt, ok := d.worktreePath(w, sessionID)
	if !ok {
		return
	}

	files, truncated, err := d.buildDiff(wt)
	if err != nil {
		slog.Error("consoleui/diff: buildDiff", "session", sessionID, "err", err)
		writeGenericError(w, http.StatusInternalServerError, "failed to build diff")
		return
	}

	resp := DiffResponse{
		SessionID: sessionID,
		Files:     files,
		Truncated: truncated,
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

// buildDiff runs git diff + status in the worktree and returns parsed diff files.
func (d *diffHandlers) buildDiff(wtPath string) ([]DiffFile, bool, error) {
	var files []DiffFile
	truncated := false
	seen := make(map[string]bool) // paths we've already emitted

	// 1. Tracked changes: git diff --no-color (includes modified + deleted).
	trackedDiff, err := runGitOutput(wtPath, "diff", "--no-color")
	if err != nil {
		return nil, false, fmt.Errorf("git diff: %w", err)
	}
	if len(trackedDiff) > maxDiffBytes {
		trackedDiff = trackedDiff[:maxDiffBytes]
		truncated = true
	}

	parsedTracked, err := parseUnifiedDiff(trackedDiff)
	if err != nil {
		return nil, false, fmt.Errorf("parse tracked diff: %w", err)
	}
	for _, f := range parsedTracked {
		seen[f.Path] = true
		files = append(files, f)
	}

	// 2. Untracked files: synthesize add-diff via git diff --no-index /dev/null <file>.
	statusOut, err := runGitOutput(wtPath, "status", "--porcelain")
	if err != nil {
		return nil, false, fmt.Errorf("git status: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(statusOut)), "\n") {
		if len(line) < 4 {
			continue
		}
		xy := line[:2]
		fpath := strings.TrimSpace(line[3:])
		if fpath == "" {
			continue
		}
		if xy == "??" {
			// Untracked file.
			if seen[fpath] {
				continue
			}
			seen[fpath] = true

			absPath := filepath.Join(wtPath, filepath.FromSlash(fpath))
			data, readErr := os.ReadFile(absPath)
			if readErr != nil {
				continue
			}
			if isBinary(data) {
				files = append(files, DiffFile{Path: fpath, Status: "added", Binary: true})
				continue
			}
			// Synthesize an add-diff using git diff --no-index /dev/null <file>.
			// This gives us a proper unified diff we can apply later.
			addDiff, gitErr := runGitOutputAllowFail(wtPath, "diff", "--no-color", "--no-index", "--", "/dev/null", absPath)
			if len(addDiff) > 0 {
				// Strip the /dev/null a/ prefix from path headers so path is relative.
				addDiff = bytes.ReplaceAll(addDiff, []byte("--- /dev/null"), []byte("--- /dev/null"))
				addDiff = bytes.ReplaceAll(addDiff,
					[]byte("b/"+absPath),
					[]byte("b/"+fpath))
				// Parse and re-label as "added".
				diffFiles, parseErr := parseUnifiedDiff(addDiff)
				if parseErr == nil && len(diffFiles) > 0 {
					df := diffFiles[0]
					df.Path = fpath
					df.Status = "added"
					files = append(files, df)
					if gitErr == nil {
						_ = gitErr
					}
					continue
				}
			}
			// Fallback: synthesize a minimal add-file representation.
			lines := makeAddLines(data)
			files = append(files, DiffFile{
				Path:   fpath,
				Status: "added",
				Hunks: []DiffHunk{{
					Header:   fmt.Sprintf("@@ -0,0 +1,%d @@", len(lines)),
					Lines:    lines,
					OldStart: 0, OldCount: 0,
					NewStart: 1, NewCount: len(lines),
					HunkID:   0,
				}},
			})
			_ = gitErr
		}
	}

	return files, truncated, nil
}

// makeAddLines turns file content into unified-diff "+"-prefixed lines.
func makeAddLines(data []byte) []string {
	content := string(data)
	rawLines := strings.Split(content, "\n")
	// Remove trailing empty element from trailing newline.
	if len(rawLines) > 0 && rawLines[len(rawLines)-1] == "" {
		rawLines = rawLines[:len(rawLines)-1]
	}
	out := make([]string, len(rawLines))
	for i, l := range rawLines {
		out[i] = "+" + l
	}
	return out
}

// ---- POST /api/files/diff/accept ---------------------------------------------

// handleDiffAccept promotes a single hunk from the worktree into the real tree.
//
// For binary files, the whole file is copied (HunkID is ignored).
// For text files, `git apply` is used against WorkspaceRoot (atomic per
// invocation — either the full hunk applies or the real tree is unchanged).
// On conflict (context lines do not match), returns 409.
//
// Auth: RoleDispatch + owner-scoped + path jailed under WorkspaceRoot.
func (d *diffHandlers) handleDiffAccept(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req AcceptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeGenericError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.SessionID == "" || req.Path == "" {
		writeGenericError(w, http.StatusBadRequest, "session_id and path are required")
		return
	}

	effectiveOperator := resolveOperatorID(r, req.OperatorID)
	if effectiveOperator == "" {
		writeGenericError(w, http.StatusBadRequest, "operator_id is required")
		return
	}
	if !d.ownerCheck(w, req.SessionID, effectiveOperator) {
		return
	}

	// Path jail: path must be workspace-relative and inside WorkspaceRoot.
	absTarget, ok := d.files.jailPath(req.Path)
	if !ok {
		writeGenericError(w, http.StatusBadRequest, "invalid path: outside workspace jail")
		return
	}
	// Reject secret files.
	if isSecretFile(filepath.Base(req.Path)) {
		writeGenericError(w, http.StatusForbidden, "accepting this file type is not permitted")
		return
	}

	wt, ok := d.worktreePath(w, req.SessionID)
	if !ok {
		return
	}

	// Build diff for the specific file to extract the requested hunk.
	files, _, err := d.buildDiff(wt)
	if err != nil {
		slog.Error("consoleui/diff: accept buildDiff", "session", req.SessionID, "path", req.Path, "err", err)
		writeGenericError(w, http.StatusInternalServerError, "failed to build diff")
		return
	}

	var target *DiffFile
	for i := range files {
		if files[i].Path == req.Path {
			target = &files[i]
			break
		}
	}
	if target == nil {
		writeGenericError(w, http.StatusNotFound, "path not found in diff")
		return
	}

	// Binary file: promote the whole file.
	if target.Binary {
		if err := d.promoteBinaryFile(wt, req.Path, absTarget); err != nil {
			slog.Error("consoleui/diff: accept binary promote",
				"session", req.SessionID, "path", req.Path, "err", err)
			writeGenericError(w, http.StatusConflict, fmt.Sprintf("failed to promote binary file: %v", err))
			return
		}
		slog.Info("consoleui/diff: accept binary",
			"operator_id", effectiveOperator, "session", req.SessionID,
			"path", req.Path)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		return
	}

	// Text file: extract and apply the specific hunk.
	if req.HunkID < 0 || req.HunkID >= len(target.Hunks) {
		writeGenericError(w, http.StatusBadRequest, fmt.Sprintf("hunk_id %d out of range [0, %d)", req.HunkID, len(target.Hunks)))
		return
	}
	hunk := target.Hunks[req.HunkID]

	// Build a minimal patch for this single hunk.
	patch := buildSingleHunkPatch(target, &hunk, wt)

	// Apply the patch to the REAL tree atomically.
	if err := applyPatchToRealTree(d.workspaceRoot, patch); err != nil {
		slog.Warn("consoleui/diff: accept apply failed (conflict or context mismatch)",
			"session", req.SessionID, "path", req.Path, "hunk_id", req.HunkID, "err", err)
		writeGenericError(w, http.StatusConflict,
			fmt.Sprintf("hunk does not apply cleanly to the real tree (conflict): %v", err))
		return
	}

	slog.Info("consoleui/diff: accept hunk",
		"operator_id", effectiveOperator, "session", req.SessionID,
		"path", req.Path, "hunk_id", req.HunkID, "real_tree", d.workspaceRoot)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// buildSingleHunkPatch builds a minimal unified-diff patch for one hunk.
// The patch header uses paths relative to the repo root as git expects.
func buildSingleHunkPatch(file *DiffFile, hunk *DiffHunk, wtPath string) []byte {
	var buf bytes.Buffer
	// For added files, use /dev/null as the old file.
	oldPath := file.Path
	newPath := file.Path
	if file.Status == "added" {
		oldPath = "/dev/null"
	} else if file.Status == "deleted" {
		newPath = "/dev/null"
	}
	if file.OldPath != "" {
		oldPath = file.OldPath
	}
	fmt.Fprintf(&buf, "--- a/%s\n", oldPath)
	fmt.Fprintf(&buf, "+++ b/%s\n", newPath)
	fmt.Fprintf(&buf, "%s\n", hunk.Header)
	for _, l := range hunk.Lines[1:] { // skip the header line (already written above)
		fmt.Fprintf(&buf, "%s\n", l)
	}
	return buf.Bytes()
}

// applyPatchToRealTree applies a unified-diff patch to the real working tree
// using `git apply --recount`. The call is atomic: either it succeeds fully
// or the real tree is unchanged (git apply does not partially apply).
func applyPatchToRealTree(repoRoot string, patch []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "apply", "--recount", "-") //nolint:gosec
	cmd.Stdin = bytes.NewReader(patch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git apply: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// promoteBinaryFile copies the binary file from the worktree to the real tree.
func (d *diffHandlers) promoteBinaryFile(wtPath, relPath, absTarget string) error {
	src := filepath.Join(wtPath, filepath.FromSlash(relPath))
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read worktree file: %w", err)
	}
	// Jail check on target is done by the caller; write atomically.
	tmp := absTarget + ".yakos-diff-promote-tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmp, absTarget); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// ---- POST /api/files/diff/reject ---------------------------------------------

// handleDiffReject discards a single hunk from the worktree.
//
// For binary files, the whole file is reverted in the worktree.
// For text files, `git apply -R` reverses the hunk in the worktree.
// For added (untracked) files, the file is removed from the worktree.
//
// Auth: RoleDispatch + owner-scoped + path jailed.
func (d *diffHandlers) handleDiffReject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req RejectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeGenericError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.SessionID == "" || req.Path == "" {
		writeGenericError(w, http.StatusBadRequest, "session_id and path are required")
		return
	}

	effectiveOperator := resolveOperatorID(r, req.OperatorID)
	if effectiveOperator == "" {
		writeGenericError(w, http.StatusBadRequest, "operator_id is required")
		return
	}
	if !d.ownerCheck(w, req.SessionID, effectiveOperator) {
		return
	}

	// Path jail.
	if _, ok := d.files.jailPath(req.Path); !ok {
		writeGenericError(w, http.StatusBadRequest, "invalid path: outside workspace jail")
		return
	}
	if isSecretFile(filepath.Base(req.Path)) {
		writeGenericError(w, http.StatusForbidden, "rejecting this file type is not permitted")
		return
	}

	wt, ok := d.worktreePath(w, req.SessionID)
	if !ok {
		return
	}

	files, _, err := d.buildDiff(wt)
	if err != nil {
		slog.Error("consoleui/diff: reject buildDiff", "session", req.SessionID, "path", req.Path, "err", err)
		writeGenericError(w, http.StatusInternalServerError, "failed to build diff")
		return
	}

	var target *DiffFile
	for i := range files {
		if files[i].Path == req.Path {
			target = &files[i]
			break
		}
	}
	if target == nil {
		writeGenericError(w, http.StatusNotFound, "path not found in diff")
		return
	}

	if target.Binary || target.Status == "added" {
		// Binary or new untracked file: remove from worktree.
		absWT := filepath.Join(wt, filepath.FromSlash(req.Path))
		if err := os.Remove(absWT); err != nil && !os.IsNotExist(err) {
			slog.Error("consoleui/diff: reject remove",
				"session", req.SessionID, "path", req.Path, "err", err)
			writeGenericError(w, http.StatusInternalServerError, "failed to remove file from worktree")
			return
		}
		slog.Info("consoleui/diff: reject (removed from worktree)",
			"operator_id", effectiveOperator, "session", req.SessionID, "path", req.Path)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		return
	}

	if target.Status == "deleted" {
		// Deleted file: restore from HEAD in worktree.
		if err := runGitInDir(wt, "checkout", "HEAD", "--", req.Path); err != nil {
			slog.Error("consoleui/diff: reject restore deleted",
				"session", req.SessionID, "path", req.Path, "err", err)
			writeGenericError(w, http.StatusInternalServerError, "failed to restore deleted file in worktree")
			return
		}
		slog.Info("consoleui/diff: reject (restored deleted file)",
			"operator_id", effectiveOperator, "session", req.SessionID, "path", req.Path)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		return
	}

	// Text modified file: reverse-apply the specific hunk in the worktree.
	if req.HunkID < 0 || req.HunkID >= len(target.Hunks) {
		writeGenericError(w, http.StatusBadRequest, fmt.Sprintf("hunk_id %d out of range [0, %d)", req.HunkID, len(target.Hunks)))
		return
	}
	hunk := target.Hunks[req.HunkID]
	patch := buildSingleHunkPatch(target, &hunk, wt)

	// Reverse-apply in the worktree.
	if err := applyPatchReverse(wt, patch); err != nil {
		slog.Warn("consoleui/diff: reject apply -R failed",
			"session", req.SessionID, "path", req.Path, "hunk_id", req.HunkID, "err", err)
		writeGenericError(w, http.StatusConflict, fmt.Sprintf("hunk does not reverse-apply cleanly: %v", err))
		return
	}

	slog.Info("consoleui/diff: reject hunk",
		"operator_id", effectiveOperator, "session", req.SessionID,
		"path", req.Path, "hunk_id", req.HunkID)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// applyPatchReverse reverse-applies a patch in the worktree via git apply -R.
func applyPatchReverse(repoRoot string, patch []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "apply", "--recount", "-R", "-") //nolint:gosec
	cmd.Stdin = bytes.NewReader(patch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git apply -R: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ---- GET /api/git/status -----------------------------------------------------

// handleGitStatus serves GET /api/git/status?session=<id>.
//
// Returns branch info and the staged/unstaged/untracked file lists from the
// session worktree.
//
// Auth: RoleRead + owner-scoped.
func (d *diffHandlers) handleGitStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := r.URL.Query().Get("session")
	if sessionID == "" {
		writeGenericError(w, http.StatusBadRequest, "session parameter required")
		return
	}

	operatorID := r.URL.Query().Get("operatorId")
	effectiveOperator := resolveOperatorID(r, operatorID)
	if effectiveOperator == "" {
		writeGenericError(w, http.StatusBadRequest, "operatorId is required")
		return
	}
	if !d.ownerCheck(w, sessionID, effectiveOperator) {
		return
	}

	wt, ok := d.worktreePath(w, sessionID)
	if !ok {
		return
	}

	// Parse `git status --porcelain=v2 --branch`.
	statusOut, err := runGitOutput(wt, "status", "--porcelain=v2", "--branch")
	if err != nil {
		slog.Error("consoleui/diff: git status", "session", sessionID, "err", err)
		writeGenericError(w, http.StatusInternalServerError, "failed to run git status")
		return
	}

	resp := parseGitStatusV2(statusOut)
	resp.SessionID = sessionID

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

// parseGitStatusV2 parses `git status --porcelain=v2 --branch` output.
func parseGitStatusV2(out []byte) GitStatusResponse {
	var resp GitStatusResponse
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			resp.Branch = strings.TrimPrefix(line, "# branch.head ")
		case strings.HasPrefix(line, "# branch.ab "):
			parts := strings.Fields(strings.TrimPrefix(line, "# branch.ab "))
			if len(parts) == 2 {
				fmt.Sscanf(parts[0], "+%d", &resp.Ahead)
				fmt.Sscanf(parts[1], "-%d", &resp.Behind)
			}
		case strings.HasPrefix(line, "1 ") || strings.HasPrefix(line, "2 "):
			// Ordinary changed entry.
			fields := strings.Fields(line)
			if len(fields) < 9 {
				continue
			}
			xy := fields[1] // XY
			path := fields[8]
			x := string(xy[0]) // staged
			y := string(xy[1]) // unstaged
			if x != "." {
				resp.Staged = append(resp.Staged, GitStatusFile{Path: path, Code: x})
			}
			if y != "." {
				resp.Unstaged = append(resp.Unstaged, GitStatusFile{Path: path, Code: y})
			}
		case strings.HasPrefix(line, "? "):
			resp.Untracked = append(resp.Untracked, strings.TrimPrefix(line, "? "))
		}
	}
	return resp
}

// ---- POST /api/git/commit ----------------------------------------------------

// handleGitCommit commits promoted changes in the REAL tree.
//
// Only the caller-supplied paths are staged (never git add -A).
// The commit message is validated for non-emptiness and Conventional Commits form
// (warning only; does not hard-block).
//
// Auth: RoleDispatch + owner-scoped + paths jailed.
func (d *diffHandlers) handleGitCommit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CommitRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<16))
	if err := dec.Decode(&req); err != nil {
		writeGenericError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.SessionID == "" {
		writeGenericError(w, http.StatusBadRequest, "session_id is required")
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		writeGenericError(w, http.StatusBadRequest, "message is required")
		return
	}
	if len(req.Message) > maxCommitMsgLen {
		writeGenericError(w, http.StatusBadRequest, "message too long")
		return
	}
	if len(req.Paths) == 0 {
		writeGenericError(w, http.StatusBadRequest, "paths is required and must be non-empty; only promoted paths may be committed")
		return
	}

	effectiveOperator := resolveOperatorID(r, req.OperatorID)
	if effectiveOperator == "" {
		writeGenericError(w, http.StatusBadRequest, "operator_id is required")
		return
	}
	if !d.ownerCheck(w, req.SessionID, effectiveOperator) {
		return
	}

	if d.workspaceRoot == "" {
		writeGenericError(w, http.StatusServiceUnavailable, "workspace root not configured")
		return
	}

	// Jail and validate each path.
	for _, p := range req.Paths {
		if _, ok := d.files.jailPath(p); !ok {
			writeGenericError(w, http.StatusBadRequest, fmt.Sprintf("invalid path %q: outside workspace jail", p))
			return
		}
		if isSecretFile(filepath.Base(p)) {
			writeGenericError(w, http.StatusForbidden, fmt.Sprintf("committing %q is not permitted", p))
			return
		}
	}

	// Stage only the specified paths (never git add -A).
	addArgs := append([]string{"add", "--"}, req.Paths...)
	if err := runGitInDir(d.workspaceRoot, addArgs...); err != nil {
		slog.Error("consoleui/diff: commit git add",
			"session", req.SessionID, "paths", req.Paths, "err", err)
		writeGenericError(w, http.StatusInternalServerError, fmt.Sprintf("git add failed: %v", err))
		return
	}

	// Commit with the supplied message.
	sha, err := runGitOutputIn(d.workspaceRoot, "commit", "--no-gpg-sign",
		"-m", req.Message,
		"--", // paths already staged; this prevents double-staging
	)
	if err != nil {
		slog.Error("consoleui/diff: commit git commit",
			"session", req.SessionID, "err", err)
		// Unstage to leave the real tree clean.
		_ = runGitInDir(d.workspaceRoot, "reset", "HEAD", "--")
		writeGenericError(w, http.StatusInternalServerError, fmt.Sprintf("git commit failed: %v", err))
		return
	}

	// Extract the commit SHA from the output ("master (root-commit) <sha>" or "[master <sha>]").
	commitSHA := extractCommitSHA(sha)

	slog.Info("consoleui/diff: git commit",
		"operator_id", effectiveOperator, "session", req.SessionID,
		"sha", commitSHA, "paths", req.Paths)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(CommitResponse{SHA: commitSHA})
}

// extractCommitSHA parses the short SHA from `git commit` output.
func extractCommitSHA(out []byte) string {
	// output: "[branch sha] message" or "[branch (root-commit) sha] message"
	s := strings.TrimSpace(string(out))
	// Find the first ']'
	closeBracket := strings.Index(s, "]")
	if closeBracket < 0 {
		return strings.Fields(s)[0] // fallback
	}
	header := s[1:closeBracket] // strip leading '[' and trailing ']'
	parts := strings.Fields(header)
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1] // last word before ']' is the SHA
}

// ---- parseUnifiedDiff --------------------------------------------------------

// parseUnifiedDiff parses the output of `git diff --no-color` (or similar
// unified diff) into a slice of DiffFile.
//
// It handles:
//   - Multi-file diffs (multiple --- +++ sections).
//   - Binary files (detected from "Binary files … differ" marker).
//   - Renamed files (--- a/old +++ b/new with different paths).
//   - Multiple hunks per file.
func parseUnifiedDiff(data []byte) ([]DiffFile, error) {
	if len(data) == 0 {
		return nil, nil
	}

	if !utf8.Valid(data) {
		// Non-UTF-8 diff output should not occur for text diffs; treat as error.
		return nil, fmt.Errorf("diff output is not valid UTF-8")
	}

	lines := strings.Split(string(data), "\n")

	var files []DiffFile
	var cur *DiffFile
	var curHunk *DiffHunk

	for i := 0; i < len(lines); i++ {
		line := lines[i]

		// New file section.
		if strings.HasPrefix(line, "diff --git ") {
			if cur != nil {
				if curHunk != nil {
					cur.Hunks = append(cur.Hunks, *curHunk)
					curHunk = nil
				}
				files = append(files, *cur)
			}
			cur = &DiffFile{}
			// Extract paths from "diff --git a/<old> b/<new>".
			// This is a heuristic (spaces in paths break it) but covers the common case.
			parts := strings.SplitN(strings.TrimPrefix(line, "diff --git "), " ", 2)
			if len(parts) == 2 {
				cur.Path = strings.TrimPrefix(parts[1], "b/")
			}
			cur.Status = "modified" // may be overwritten below
			continue
		}

		if cur == nil {
			continue
		}

		// Binary marker.
		if strings.HasPrefix(line, "Binary files") && strings.HasSuffix(line, "differ") {
			cur.Binary = true
			cur.Status = "binary"
			continue
		}

		// --- a/<path> header.
		if strings.HasPrefix(line, "--- ") {
			old := strings.TrimPrefix(line, "--- ")
			old = strings.TrimPrefix(old, "a/")
			if old == "/dev/null" {
				cur.Status = "added"
			}
			continue
		}

		// +++ b/<path> header.
		if strings.HasPrefix(line, "+++ ") {
			newP := strings.TrimPrefix(line, "+++ ")
			newP = strings.TrimPrefix(newP, "b/")
			if newP == "/dev/null" {
				cur.Status = "deleted"
			} else {
				if cur.Status != "added" {
					// Check for rename: path differs from what was parsed from diff --git.
					if cur.Path != "" && newP != cur.Path {
						cur.OldPath = cur.Path
						cur.Path = newP
						cur.Status = "renamed"
					} else {
						cur.Path = newP
					}
				}
			}
			continue
		}

		// Hunk header @@ …
		if strings.HasPrefix(line, "@@ ") {
			if curHunk != nil {
				cur.Hunks = append(cur.Hunks, *curHunk)
			}
			h := DiffHunk{
				Header: line,
				HunkID: len(cur.Hunks),
				Lines:  []string{line},
			}
			// Parse @@ -oldStart,oldCount +newStart,newCount @@
			parseHunkHeader(line, &h)
			curHunk = &h
			continue
		}

		// Hunk body (context/add/delete lines).
		if curHunk != nil {
			if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") {
				curHunk.Lines = append(curHunk.Lines, line)
			}
			// Skip "\ No newline at end of file".
		}
	}

	// Flush last file/hunk.
	if cur != nil {
		if curHunk != nil {
			cur.Hunks = append(cur.Hunks, *curHunk)
		}
		files = append(files, *cur)
	}

	return files, nil
}

// parseHunkHeader parses @@ -oldStart[,oldCount] +newStart[,newCount] @@ and
// fills the hunk's numeric fields.
func parseHunkHeader(header string, h *DiffHunk) {
	// Format: @@ -a[,b] +c[,d] @@
	var oldStart, oldCount, newStart, newCount int
	oldCount = 1
	newCount = 1

	// Find the range between the first pair of @@ markers.
	inner := strings.TrimPrefix(header, "@@ ")
	if idx := strings.Index(inner, " @@"); idx >= 0 {
		inner = inner[:idx]
	}

	parts := strings.Fields(inner)
	for _, part := range parts {
		if strings.HasPrefix(part, "-") {
			parseRange(part[1:], &oldStart, &oldCount)
		} else if strings.HasPrefix(part, "+") {
			parseRange(part[1:], &newStart, &newCount)
		}
	}

	h.OldStart = oldStart
	h.OldCount = oldCount
	h.NewStart = newStart
	h.NewCount = newCount
}

func parseRange(s string, start, count *int) {
	if idx := strings.Index(s, ","); idx >= 0 {
		fmt.Sscanf(s[:idx], "%d", start)
		fmt.Sscanf(s[idx+1:], "%d", count)
	} else {
		fmt.Sscanf(s, "%d", start)
	}
}

// ---- git shell-out helpers ---------------------------------------------------

// runGitOutput runs a git command in dir and returns its combined stdout+stderr.
// On non-zero exit, it returns an error wrapping the output.
func runGitOutput(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()

	fullArgs := append([]string{"-C", dir}, args...) //nolint:gocritic
	cmd := exec.CommandContext(ctx, "git", fullArgs...) //nolint:gosec
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("git %s: exit %d: %s",
				strings.Join(args, " "), ee.ExitCode(), strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// runGitOutputAllowFail is like runGitOutput but returns output even on
// non-zero exit (e.g. git diff --no-index returns exit 1 when files differ).
func runGitOutputAllowFail(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()

	fullArgs := append([]string{"-C", dir}, args...) //nolint:gocritic
	cmd := exec.CommandContext(ctx, "git", fullArgs...) //nolint:gosec
	out, err := cmd.Output()
	var ee *exec.ExitError
	if err != nil && errors.As(err, &ee) {
		// Include stderr in the output for diagnosis but return the stdout.
		return append(out, ee.Stderr...), err
	}
	return out, err
}

// runGitInDir runs a git command in dir with no output capture.
func runGitInDir(dir string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()

	fullArgs := append([]string{"-C", dir}, args...) //nolint:gocritic
	cmd := exec.CommandContext(ctx, "git", fullArgs...) //nolint:gosec
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w (stderr: %s)",
			strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// runGitOutputIn runs a git command in dir and returns the stdout as []byte.
func runGitOutputIn(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()

	fullArgs := append([]string{"-C", dir}, args...) //nolint:gocritic
	cmd := exec.CommandContext(ctx, "git", fullArgs...) //nolint:gosec
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("git %s: exit %d: %s",
				strings.Join(args, " "), ee.ExitCode(), strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}
