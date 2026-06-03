// Package jsonrpc socketpath.go — workspace-hash socket path resolution.
//
// Per phase2 design §3:
//
//	Linux:   $XDG_RUNTIME_DIR/yakos/<hash>.sock
//	         fallback: /tmp/yakos-<uid>/<hash>.sock
//	macOS:   $TMPDIR/yakos/<hash>.sock
//	         (XDG_RUNTIME_DIR unset on stock macOS)
//	Windows: \\.\pipe\yakos-<uid>-<hash>
//
// <hash> = first 16 hex chars of SHA-256 of the absolute workspace root path.
// On macOS/Windows the path is lowercased before hashing for case-insensitivity.
package jsonrpc

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// SocketPath returns the platform-appropriate socket/pipe path for workspaceRoot.
// workspaceRoot must be an absolute path; it is cleaned before hashing.
func SocketPath(workspaceRoot string) string {
	h := workspaceHash(workspaceRoot)
	switch runtime.GOOS {
	case "windows":
		uid := os.Getenv("USERNAME") // best-effort uid on Windows
		return fmt.Sprintf(`\\.\pipe\yakos-%s-%s`, uid, h)
	case "darwin":
		tmpDir := os.Getenv("TMPDIR")
		if tmpDir == "" {
			tmpDir = "/tmp"
		}
		return filepath.Join(tmpDir, "yakos", h+".sock")
	default: // linux + others
		if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
			return filepath.Join(xdg, "yakos", h+".sock")
		}
		uid := fmt.Sprintf("%d", os.Getuid())
		return filepath.Join("/tmp", "yakos-"+uid, h+".sock")
	}
}

// PIDPath returns the path for the daemon PID file (mirrors SocketPath convention).
func PIDPath(workspaceRoot string) string {
	h := workspaceHash(workspaceRoot)
	switch runtime.GOOS {
	case "windows":
		uid := os.Getenv("USERNAME")
		return filepath.Join(os.Getenv("TEMP"), fmt.Sprintf("yakos-%s-%s.pid", uid, h))
	case "darwin":
		tmpDir := os.Getenv("TMPDIR")
		if tmpDir == "" {
			tmpDir = "/tmp"
		}
		return filepath.Join(tmpDir, "yakos", h+".pid")
	default:
		if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
			return filepath.Join(xdg, "yakos", h+".pid")
		}
		uid := fmt.Sprintf("%d", os.Getuid())
		return filepath.Join("/tmp", "yakos-"+uid, h+".pid")
	}
}

// workspaceHash returns the first 16 hex chars of SHA-256(canonicalized path).
// macOS and Windows lowercase the path first for case-insensitive filesystems.
func workspaceHash(workspaceRoot string) string {
	path := filepath.Clean(workspaceRoot)
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	sum := sha256.Sum256([]byte(path))
	return fmt.Sprintf("%x", sum[:8]) // 8 bytes = 16 hex chars
}

// socketDir returns the directory portion of a socket path.
func socketDir(path string) string {
	return filepath.Dir(path)
}
