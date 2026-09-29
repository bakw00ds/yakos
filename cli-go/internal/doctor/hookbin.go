package doctor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// hookBinaryWords splits command like a POSIX shell for the subset
// `yakos refresh` emits: unquoted runs, single-quoted runs and backslash
// escapes outside quotes. Anything else (double quotes, unterminated
// quotes) reports ok=false so unknown shapes are never misread.
func hookBinaryWords(command string) ([]string, bool) {
	var words []string
	var cur strings.Builder
	inWord := false
	rs := []rune(command)
	for i := 0; i < len(rs); i++ {
		switch c := rs[i]; {
		case c == '\'':
			inWord = true
			for i++; i < len(rs) && rs[i] != '\''; i++ {
				cur.WriteRune(rs[i])
			}
			if i >= len(rs) {
				return nil, false
			}
		case c == '\\':
			if i++; i >= len(rs) {
				return nil, false
			}
			inWord = true
			cur.WriteRune(rs[i])
		case c == '"':
			return nil, false
		case c == ' ' || c == '\t':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			inWord = true
			cur.WriteRune(c)
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, true
}

// hookCommandBinary returns the absolute yakos binary path when command has
// the `<abs>/yakos hook run ...` form written by `refresh --hooks-impl
// go|hybrid`. Bare `yakos` (resolved through PATH) is not reported.
func hookCommandBinary(command string) (string, bool) {
	w, ok := hookBinaryWords(command)
	if !ok || len(w) < 4 || w[1] != "hook" || w[2] != "run" {
		return "", false
	}
	base := filepath.Base(strings.ReplaceAll(w[0], "\\", "/"))
	if base != "yakos" && base != "yakos.exe" {
		return "", false
	}
	if !filepath.IsAbs(w[0]) {
		return "", false
	}
	return w[0], true
}

// isExecutableFile reports whether p is a regular file the OS would run.
func isExecutableFile(p string) bool {
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() {
		return false
	}
	return runtime.GOOS == "windows" || fi.Mode().Perm()&0o111 != 0
}

// missingHookBinaries returns the sorted, de-duplicated absolute binary
// paths referenced by hook commands in the project's .claude/settings.json
// that do not point at an existing executable. A missing binary makes the
// hook exit 127, which Claude Code treats as non-blocking (silent
// fail-open). Unreadable or malformed settings yield nil.
func missingHookBinaries(projectPath string) []string {
	data, err := os.ReadFile(filepath.Join(projectPath, ".claude", "settings.json")) //nolint:gosec
	if err != nil {
		return nil
	}
	var parsed struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if json.Unmarshal(data, &parsed) != nil {
		return nil
	}
	seen := map[string]bool{}
	var missing []string
	for _, entries := range parsed.Hooks {
		for _, e := range entries {
			for _, h := range e.Hooks {
				bin, ok := hookCommandBinary(h.Command)
				if !ok || seen[bin] {
					continue
				}
				seen[bin] = true
				if !isExecutableFile(bin) {
					missing = append(missing, bin)
				}
			}
		}
	}
	sort.Strings(missing)
	return missing
}
