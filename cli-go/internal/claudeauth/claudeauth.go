// Package claudeauth provides a shared helper for detecting whether Claude Code
// is authenticated. Both the start preflight (start.checkRuntimeAuth) and the
// auth status command (auth.checkAuth) use this to stay in sync.
//
// Detection order (first match wins → authed):
//
//  1. ANTHROPIC_API_KEY env var is non-empty.
//  2. $HOME/.claude/auth.json exists (legacy path).
//  3. $HOME/.claude.json exists, parses as JSON, and has a non-null, non-empty
//     "oauthAccount" object (current Claude Code OAuth path).
//  4. $HOME/.claude/credentials.json exists (forward-compat path).
package claudeauth

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// IsAuthed returns true when Claude Code appears to be authenticated.
//
// homeDir is the user's home directory (pass os.UserHomeDir() or an injected
// value for tests). apiKey is the value of ANTHROPIC_API_KEY (pass
// os.Getenv("ANTHROPIC_API_KEY") or an injected value for tests).
func IsAuthed(homeDir, apiKey string) bool {
	// 1. API key env var.
	if apiKey != "" {
		return true
	}

	if homeDir == "" {
		return false
	}

	// 2. Legacy ~/.claude/auth.json.
	if fileExists(filepath.Join(homeDir, ".claude", "auth.json")) {
		return true
	}

	// 3. Current OAuth path: ~/.claude.json must exist, parse as JSON, and
	//    have a non-null, non-empty "oauthAccount" object.
	//    NOTE: ~/.claude.json also exists for unauthenticated installs (it
	//    stores onboarding/config state). Mere existence is NOT enough —
	//    we must confirm oauthAccount is present and non-null.
	if hasOAuthAccount(filepath.Join(homeDir, ".claude.json")) {
		return true
	}

	// 4. Forward-compat ~/.claude/credentials.json.
	if fileExists(filepath.Join(homeDir, ".claude", "credentials.json")) {
		return true
	}

	return false
}

// hasOAuthAccount returns true when path is a readable JSON file whose
// top-level "oauthAccount" key is a non-null, non-empty JSON object.
// Any read or parse error returns false.
func hasOAuthAccount(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}

	// Decode only the top-level keys to avoid reading the full file into
	// a large struct. We use a raw map so any additional keys are ignored.
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return false
	}

	raw, ok := top["oauthAccount"]
	if !ok {
		return false
	}

	// Null literal → not authed.
	if string(raw) == "null" {
		return false
	}

	// Decode as a generic object and verify it has at least one key.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		// Not an object (e.g. a string or number) — treat as not authed.
		return false
	}

	// Empty object {} → not authed (onboarding placeholder).
	return len(obj) > 0
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
