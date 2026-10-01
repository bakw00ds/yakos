// Package hookguard builds and recognizes the shell wrapper `yakos refresh`
// puts around a Go hook command that enforces something (K-118).
//
// Why a wrapper: Claude Code treats any hook exit other than 2 as
// non-blocking. A bare `<abs>/yakos hook run --impl go <name>` therefore fails
// open when the binary is missing (127), is a directory (126), is empty (1,
// or 0 under zsh), or dies on a signal (137, 139, 134). The wrapper:
//
//   - runs the Go hook only when the path is a non-empty executable regular
//     file, and otherwise execs the bash twin refresh deployed;
//   - passes the Go hook's exit code through only when it is 0 or 2, and maps
//     anything else (a crash, an incompatible binary) to a block with a
//     reason on stderr.
//
// It is plain POSIX sh with builtins only, so it works under sh, bash 3.2 and
// zsh, and never consults PATH.
package hookguard

import (
	"regexp"
	"strings"
)

// ShellQuote single-quotes s only when it contains characters a shell would
// treat specially, so ordinary paths stay readable.
func ShellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"'$`\\&;|<>(){}[]*?!#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Plain is the unguarded Go hook command.
func Plain(bin, name string) string {
	return ShellQuote(bin) + " hook run --impl go " + name
}

// Build is the guarded command for hook name run by binary bin.
func Build(bin, name string) string {
	q := ShellQuote(bin)
	return "if [ -f " + q + " ] && [ -x " + q + " ] && [ -s " + q + " ]; then " +
		Plain(bin, name) + `; rc=$?; ` +
		`if [ "$rc" -eq 0 ] || [ "$rc" -eq 2 ]; then exit "$rc"; fi; ` +
		"echo \"" + name + ": yakos exited $rc (crash or an incompatible binary); this hook enforces a security control, so the call is blocked. Run 'yakos doctor', then 'yakos refresh'.\" >&2; exit 2; " +
		`else exec "${CLAUDE_PROJECT_DIR}/scripts/hooks/` + name + `.sh"; fi`
}

var guardRe = regexp.MustCompile(`^if \[ -f .* \] && \[ -x .* \] && \[ -s .* \]; then (.*) hook run --impl go (\S+); rc=\$\?; `)

// Strip returns the plain Go command inside a guarded command. ok is true
// only when command is EXACTLY what Build emits for some binary and hook, so
// unknown shapes are never mistaken for a guarded hook.
func Strip(command string) (plain string, ok bool) {
	if !strings.HasPrefix(command, "if [ -f ") {
		return "", false
	}
	m := guardRe.FindStringSubmatch(command)
	if m == nil {
		return "", false
	}
	// m[1] is the (possibly quoted) binary word. Unquote it, rebuild the
	// command and compare, so only an exact Build() output passes.
	binWord, name := m[1], m[2]
	for _, bin := range candidates(binWord) {
		if Build(bin, name) == command {
			return binWord + " hook run --impl go " + name, true
		}
	}
	return "", false
}

// candidates unquotes binWord the way ShellQuote quotes: a bare word, or
// '...' with each embedded quote written as '\”.
func candidates(binWord string) []string {
	if len(binWord) >= 2 && binWord[0] == '\'' && binWord[len(binWord)-1] == '\'' {
		inner := binWord[1 : len(binWord)-1]
		return []string{strings.ReplaceAll(inner, `'\''`, `'`)}
	}
	return []string{binWord}
}
