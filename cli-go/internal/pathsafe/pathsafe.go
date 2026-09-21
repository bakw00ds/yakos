// Package pathsafe holds small, dependency-free path-validation helpers
// shared across daemon transports, so a fix landed in one place is landed
// everywhere a value with the same shape and threat model is accepted.
//
// Extracted from internal/supervise's original validateProjectSlug/
// ErrInvalidProject (M3, security-review-2026-09-14.md) after round-2
// review R13 found the identical unvalidated-slug traversal one package
// over in yakos.status.read (both JSON-RPC and gRPC): the M3 fix was never
// swept for siblings that join a caller-supplied "project" value onto
// $HOME the same way. internal/supervise now delegates here too, so all
// three call sites (supervise.run/.ack, status.read x2) share one
// validator instead of three copies that can individually drift.
package pathsafe

import (
	"errors"
	"path/filepath"
	"strings"
)

// ErrInvalidProjectSlug is returned by ValidateProjectSlug when project is a
// path-traversal or absolute-path payload rather than a plain single-segment
// slug.
var ErrInvalidProjectSlug = errors.New("pathsafe: invalid project: must be a plain slug (no path separators or '..')")

// ValidateProjectSlug rejects a project value that could escape the caller's
// intended base directory (typically $HOME/agent-control/<project>).
//
// SECURITY (M3/L8/R13, security-review-2026-09-14.md +
// s2-daemon-security-review-2026-09-21.md): callers that accept a "project"
// field from an MCP/JSON-RPC/gRPC caller and then join it onto a base
// directory unmodified (filepath.Join(base, project, ...)) are vulnerable
// to a value like "../../../tmp/x" (or an absolute path) reading/writing
// files under an arbitrary directory instead of the caller's own project
// directory. "project" is always meant to be a single path segment (e.g.
// "yakos"), so any path separator or ".." is rejected outright rather than
// attempting to lexically normalize and re-validate containment (see
// R21/M3's residual-lexical-only note: this is a denylist on the INPUT
// shape, not a containment check on the resolved path — callers whose base
// directory itself could be attacker-influenced still need a post-join
// containment assert; project slugs from a trusted base directory do not).
func ValidateProjectSlug(project string) error {
	if project == "" {
		return nil
	}
	if filepath.IsAbs(project) || strings.ContainsAny(project, "/\\") || strings.Contains(project, "..") {
		return ErrInvalidProjectSlug
	}
	return nil
}
