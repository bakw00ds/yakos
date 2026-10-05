// Package routerpolicy reads the owner-only router policy file,
// ~/.yakos-state/router-policy.yml.
//
// The file is the one place an operator LOOSENS a routing default, so it is
// trusted only when it lives in the user's own state directory and passes the
// same checks as the budget policy (internal/budget/policy.go): a regular file
// (not a symlink), owned by the current user, not group or world writable.
// A project .yakos.yml can never enable anything here; it may only tighten.
// On Windows the owner and mode checks do not apply (as for the budget policy);
// the file must still be a regular, non-symlink file under the user's profile.
//
// Today the file carries one key:
//
//	allow_unsandboxed_runtimes: [codex, agy]
//
// Runtimes named there may be dispatched without their sandbox (codex
// --dangerously-bypass-approvals-and-sandbox, agy without --sandbox). Every
// other runtime, and every runtime when the file is missing or untrusted, keeps
// its sandbox flags. The router (P1) will add its rules to the same File;
// unknown keys are ignored so a policy written for a newer yakOS never fails
// this check.
package routerpolicy

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/bakw00ds/yakos/internal/statepath"
)

// FileName is the router policy file name inside the state directory.
const FileName = "router-policy.yml"

// maxPolicyBytes caps how much of the file is read. A policy is a few lines;
// anything larger is not a policy this loader should parse.
const maxPolicyBytes = 1 << 20

// File is the parsed router policy.
type File struct {
	// AllowUnsandboxedRuntimes lists runtime names (codex, agy) the operator
	// allows to run without their sandbox. Entries are matched exactly and
	// case-insensitively; there are no wildcards.
	AllowUnsandboxedRuntimes []string `yaml:"allow_unsandboxed_runtimes,omitempty"`
}

// ErrUntrusted marks a policy file ignored for being a symlink, not a regular
// file, or having the wrong owner or mode.
var ErrUntrusted = errors.New("router policy ignored")

// StateDir returns the directory the policy is read from: $HOME/.yakos-state.
// It is NOT statepath.Dir(): YAKOS_DISPATCH_LOG can be set by a project, and
// the policy loosens a security default (K-129). Empty means no home
// directory, so the policy cannot be read and nothing is allowed.
func StateDir() string { return statepath.TrustedDir() }

// Path returns the policy file path inside stateDir.
func Path(stateDir string) string { return filepath.Join(stateDir, FileName) }

// afterLstat runs in Load between the Lstat that vets the path and the Open that
// reads it. It does nothing in production; a test swaps the file there, the way
// a racing process would, to prove the descriptor check below catches it.
var afterLstat = func(path string) {}

// checkInfo applies the budget policy trust rules to an already-stat'ed file.
func checkInfo(path string, fi os.FileInfo) error {
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s is a symlink", ErrUntrusted, path)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%w: %s is not a regular file", ErrUntrusted, path)
	}
	if !statepath.OwnedByCurrentUser(fi) {
		return fmt.Errorf("%w: %s is owned by another user", ErrUntrusted, path)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%w: %s is group or world writable (chmod 600)", ErrUntrusted, path)
	}
	return nil
}

// Load reads the policy from stateDir. A missing file is an empty policy with
// no error. An untrusted, oversized or unparsable file yields an empty policy
// plus an error the caller should report; the empty policy allows nothing, so
// a bad file fails closed.
func Load(stateDir string) (File, error) {
	if stateDir == "" {
		return File{}, nil
	}
	path := Path(stateDir)
	lfi, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return File{}, nil
		}
		return File{}, err
	}
	if err := checkInfo(path, lfi); err != nil {
		return File{}, err
	}
	afterLstat(path)
	f, err := os.Open(path) //nolint:gosec // state-dir file, checked above
	if err != nil {
		return File{}, err
	}
	defer func() { _ = f.Close() }()
	// Re-check the descriptor actually being read: the path may have been
	// swapped for a symlink or another user's file after the Lstat above.
	ffi, err := f.Stat()
	if err != nil {
		return File{}, err
	}
	if !os.SameFile(lfi, ffi) {
		return File{}, fmt.Errorf("%w: %s changed while being read", ErrUntrusted, path)
	}
	if err := checkInfo(path, ffi); err != nil {
		return File{}, err
	}
	data, err := io.ReadAll(io.LimitReader(f, maxPolicyBytes+1))
	if err != nil {
		return File{}, err
	}
	if len(data) > maxPolicyBytes {
		return File{}, fmt.Errorf("%w: %s is larger than %d bytes", ErrUntrusted, path, maxPolicyBytes)
	}
	var p File
	if err := yaml.Unmarshal(data, &p); err != nil {
		return File{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return p, nil
}

// AllowsUnsandboxed reports whether the trusted policy in stateDir lists
// runtimeName in allow_unsandboxed_runtimes. Any error loading the policy is
// returned alongside false so the caller can say why bypass is not active.
func AllowsUnsandboxed(stateDir, runtimeName string) (bool, error) {
	p, err := Load(stateDir)
	if err != nil {
		return false, err
	}
	want := strings.ToLower(strings.TrimSpace(runtimeName))
	if want == "" {
		return false, nil
	}
	for _, r := range p.AllowUnsandboxedRuntimes {
		if strings.ToLower(strings.TrimSpace(r)) == want {
			return true, nil
		}
	}
	return false, nil
}
