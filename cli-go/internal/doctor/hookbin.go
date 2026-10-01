package doctor

import (
	"encoding/json"
	"github.com/bakw00ds/yakos/internal/binver"
	"github.com/bakw00ds/yakos/internal/hookguard"
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
	command, _ = stripHookGuard(command)
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

// stripHookGuard removes the fail-closed wrapper `yakos refresh` puts around
// an enforcing Go hook, so the rest parses as a plain Go hook command.
// guarded reports whether a wrapper was present.
func stripHookGuard(command string) (plain string, guarded bool) {
	if p, ok := hookguard.Strip(command); ok {
		return p, true
	}
	return command, false
}

// hookImplMix is the per-implementation tally of a project's registered hooks.
type hookImplMix struct {
	Go      []string // hook names running on `yakos hook run --impl go`
	Guarded []string // subset of Go that falls back to the bash twin if the binary is missing
	Bash    []string // hook names running as scripts/hooks/<name>.sh
}

// readHookImplMix classifies every hook command in the project's
// .claude/settings.json. Unreadable or malformed settings yield an empty mix.
func readHookImplMix(projectPath string) hookImplMix {
	var mix hookImplMix
	data, err := os.ReadFile(filepath.Join(projectPath, ".claude", "settings.json")) //nolint:gosec
	if err != nil {
		return mix
	}
	var parsed struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if json.Unmarshal(data, &parsed) != nil {
		return mix
	}
	goSeen, guardSeen, bashSeen := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, entries := range parsed.Hooks {
		for _, e := range entries {
			for _, h := range e.Hooks {
				plain, guarded := stripHookGuard(h.Command)
				if w, ok := hookBinaryWords(plain); ok && len(w) >= 4 && w[1] == "hook" && w[2] == "run" {
					name := w[len(w)-1]
					if !goSeen[name] {
						goSeen[name] = true
						mix.Go = append(mix.Go, name)
					}
					if guarded && !guardSeen[name] {
						guardSeen[name] = true
						mix.Guarded = append(mix.Guarded, name)
					}
					continue
				}
				p := "/" + strings.ReplaceAll(h.Command, "\\", "/")
				if i := strings.Index(p, "/scripts/hooks/"); i >= 0 {
					name := strings.TrimSuffix(filepath.Base(p[i:]), ".sh")
					if !bashSeen[name] {
						bashSeen[name] = true
						mix.Bash = append(mix.Bash, name)
					}
				}
			}
		}
	}
	sort.Strings(mix.Go)
	sort.Strings(mix.Guarded)
	sort.Strings(mix.Bash)
	return mix
}

// hookBinaryPaths returns the sorted, de-duplicated absolute yakos binary
// paths pinned by the project's hook commands.
func hookBinaryPaths(projectPath string) []string {
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
	var out []string
	for _, entries := range parsed.Hooks {
		for _, e := range entries {
			for _, h := range e.Hooks {
				if bin, ok := hookCommandBinary(h.Command); ok && !seen[bin] {
					seen[bin] = true
					out = append(out, bin)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// staleHookBinaries returns "<path> (<version or reason>)" for every pinned
// binary that exists but cannot run `hook run --impl` (older than
// binver.MinHookRun). Such a binary reads "--impl" as a hook name, prints
// "unknown hook" and exits 0, so every Go hook pinned to it fails open.
// Missing binaries are reported by missingHookBinaries instead.
func staleHookBinaries(projectPath string) []string {
	var out []string
	for _, bin := range hookBinaryPaths(projectPath) {
		if !isExecutableFile(bin) {
			continue
		}
		if ok, detail := binver.SupportsHookRun(bin); !ok {
			out = append(out, bin+" ("+detail+")")
		}
	}
	return out
}

// HookBinaryProblems lists, one line each, what is wrong with the yakos
// binaries a project's hooks pin: missing or non-executable, or too old to
// understand `hook run --impl`. Empty when all is well. Used by `yakos start`.
func HookBinaryProblems(projectPath string) []string {
	var out []string
	for _, m := range missingHookBinaries(projectPath) {
		out = append(out, "hook binary "+m+" is missing or not executable; enforcing hooks fall back to bash, the rest stop running. Run 'yakos refresh'.")
	}
	for _, s := range staleHookBinaries(projectPath) {
		out = append(out, "hook binary "+s+" is too old for `hook run --impl` (needs "+binver.MinHookRun+"); its Go hooks silently do nothing. Install a current yakos and run 'yakos refresh'.")
	}
	return out
}
