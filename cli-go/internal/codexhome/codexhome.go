// Package codexhome resolves which CODEX_HOME a yakOS-spawned codex process
// runs under.
//
// yakOS keeps its own codex login in a profile directory,
// ~/.yakos-state/codex-home, created by `yakos auth login codex`. Sharing the
// operator's ~/.codex between their interactive codex and yakOS dispatches
// lets two processes refresh and rewrite one auth.json (openai/codex#48465
// describes a login call overwriting the shared file), which can sign the
// operator out of the other. yakOS therefore never calls the app-server
// account/login method and never writes to ~/.codex.
//
// Until the profile exists, dispatch keeps using whatever codex would have
// used, so nothing breaks for an operator who has not run the login command.
package codexhome

import (
	"os"
	"path/filepath"
)

const (
	// ProfileDirName is the profile directory name inside ~/.yakos-state.
	ProfileDirName = "codex-home"
	authFileName   = "auth.json"
	stateDirName   = ".yakos-state"
)

// ProfileDir returns the yakOS-owned profile directory for the given home
// directory. It is derived from home alone, never from YAKOS_DISPATCH_LOG
// (see statepath.TrustedDir for why).
func ProfileDir(home string) string {
	if home == "" {
		return ""
	}
	return filepath.Join(home, stateDirName, ProfileDirName)
}

// ProfileHasAuth reports whether the yakOS profile holds a codex login.
func ProfileHasAuth(home string) bool {
	dir := ProfileDir(home)
	if dir == "" {
		return false
	}
	fi, err := os.Stat(filepath.Join(dir, authFileName))
	return err == nil && fi.Mode().IsRegular()
}

// HooksFileName is the user-level hooks file codex reads from its CODEX_HOME.
const HooksFileName = "hooks.json"

// ProfileHasHooks reports whether `yakos hooks install --harness codex` has
// put a hooks entry into the yakOS profile. A link or any other non-regular
// entry counts as present: the caller must judge it untrusted (a silent "not
// installed" would hide a swapped file), see hooksinstall.CodexHooksTrusted.
func ProfileHasHooks(home string) bool {
	dir := ProfileDir(home)
	if dir == "" {
		return false
	}
	_, err := os.Lstat(filepath.Join(dir, HooksFileName))
	return err == nil
}

// Effective returns the CODEX_HOME dispatch should set and whether it is the
// yakOS profile. Order:
//
//  1. The yakOS profile, once it holds a login (isolated is true). It wins
//     over an ambient CODEX_HOME so a project-supplied environment cannot
//     point codex at a directory of its own.
//     The profile is also used, without a login of its own, when it holds the
//     installed hooks file and OPENAI_API_KEY is set: codex then authenticates
//     with the key and still loads the hooks (K-145).
//  2. $CODEX_HOME when set (isolated is false).
//  3. "" with isolated false: codex's own default, ~/.codex.
func Effective(home string, getenv func(string) string) (dir string, isolated bool) {
	if ProfileHasAuth(home) {
		return ProfileDir(home), true
	}
	if getenv != nil && getenv("OPENAI_API_KEY") != "" && ProfileHasHooks(home) {
		return ProfileDir(home), true
	}
	if getenv != nil {
		if v := getenv("CODEX_HOME"); v != "" {
			return v, false
		}
	}
	return "", false
}

// AuthDir returns the directory whose auth.json decides whether codex is
// logged in for a dispatch: Effective's directory, or ~/.codex.
func AuthDir(home string, getenv func(string) string) string {
	if dir, _ := Effective(home, getenv); dir != "" {
		return dir
	}
	return filepath.Join(home, ".codex")
}
