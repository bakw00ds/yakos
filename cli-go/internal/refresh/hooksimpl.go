package refresh

// hooksimpl.go — the `--hooks-impl bash|go|hybrid` switch (K-87 A-3).
//
// The merged settings.json registers each hook as a command. By default that
// command is the bash script (${CLAUDE_PROJECT_DIR}/scripts/hooks/<name>.sh).
// The switch rewrites the TEMPLATE side of the merge before it runs, so the
// existing merge phases stay the single place that decides add/replace/keep:
//
//	bash   — template untouched (byte-identical to pre-A-3 behavior)
//	go     — every hook command becomes `yakos hook run <name>`
//	hybrid — only registry GoReady hooks become `<yakos> hook run <name>`
//
// Binary reference: the Go-form command embeds the ABSOLUTE path of the
// running yakos binary (os.Executable, symlinks evaluated), not a bare
// `yakos`. Claude Code launched from a GUI/IDE may not have yakos on PATH;
// a missing command exits 127, which Claude Code treats as non-blocking, so
// gates such as secret-scan and budget-guard would silently stop enforcing.
// The tradeoff: settings.json changes when the binary moves; the next
// refresh rewrites the command in place.
//
// The switch is Go-only. cli/lib/refresh.sh (bash refresh) always registers
// the bash scripts.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/bakw00ds/yakos/internal/hooks/registry"
)

// HooksImpl selects which hook implementation settings.json wires up.
type HooksImpl string

const (
	HooksImplBash   HooksImpl = "bash"
	HooksImplGo     HooksImpl = "go"
	HooksImplHybrid HooksImpl = "hybrid"

	// hooksImplYAMLKey is the top-level key persisted in <project>/.yakos.yml.
	hooksImplYAMLKey = "hooks_impl"
)

// ParseHooksImpl validates s. The empty string is not valid here; callers
// handle "unset" themselves.
func ParseHooksImpl(s string) (HooksImpl, error) {
	switch HooksImpl(s) {
	case HooksImplBash, HooksImplGo, HooksImplHybrid:
		return HooksImpl(s), nil
	}
	return "", fmt.Errorf("invalid hooks impl %q (want bash, go, or hybrid)", s)
}

// GoReadyAllowlist returns the hooks `--hooks-impl hybrid` moves to Go:
// every registry entry marked GoReady (parity-verified by the A-1 work and
// tests/run-hook-parity.sh). The registry is the single source of truth;
// there is no second list to keep in sync. Sorted by name
// (rule:cache-stability).
func GoReadyAllowlist() []string { return goReadyHooks() }

// goReadyHooks lists registry names with GoReady set. A variable so tests can
// simulate registry states.
var goReadyHooks = func() []string {
	var out []string
	for _, e := range registry.All() {
		if e.GoReady {
			out = append(out, e.Name)
		}
	}
	return out
}

// registeredHooks lists the hook names registered in the Go registry. It is a
// variable so tests can simulate an unregistered hook.
var registeredHooks = func() []string { return registry.Names() }

// goCommand renders the Go-form hook command for a hook name.
//
// The command pins the Go tier with an explicit `--impl go`. Without it the
// runner falls back to YAKOS_HOOKS, whose default is bash mode: a Go-form
// command run with the variable unset would look for lib/hooks-user/<name>.sh,
// find nothing, and exit 0, silently disabling every gate (fail-open).
func goCommand(bin, name string) string {
	return shellQuote(bin) + " hook run --impl go " + name
}

// shellQuote single-quotes s only when it contains characters a shell would
// treat specially, so ordinary paths stay readable.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"'$`\\&;|<>(){}[]*?!#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// isGoCommand reports whether command is a `yakos hook run <name>` form.
func isGoCommand(command string) bool {
	_, ok := goHookName(command)
	return ok
}

// posixWords splits command like a POSIX shell for the subset shellQuote
// emits: unquoted runs, single-quoted runs, and backslash escapes outside
// quotes (so the `'\”` idiom decodes to a literal single quote). It
// returns ok=false on anything else (double quotes, unterminated quotes),
// so unknown command shapes are never mistaken for Go hook commands.
func posixWords(command string) ([]string, bool) {
	var words []string
	var cur strings.Builder
	inWord := false
	rs := []rune(command)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case c == '\'':
			inWord = true
			i++
			for i < len(rs) && rs[i] != '\'' {
				cur.WriteRune(rs[i])
				i++
			}
			if i >= len(rs) {
				return nil, false
			}
		case c == '\\':
			i++
			if i >= len(rs) {
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

// goHookName extracts <name> from `<path>/yakos hook run --impl go <name>`
// (the path may be shell-quoted, or the bare word `yakos`). The legacy
// flag-less form `<path>/yakos hook run <name>` is also recognized so a
// project refreshed before the flag existed is migrated in place.
func goHookName(command string) (string, bool) {
	w, ok := posixWords(command)
	if !ok || len(w) < 4 || w[1] != "hook" || w[2] != "run" {
		return "", false
	}
	name := w[3]
	switch len(w) {
	case 4:
	case 6:
		if w[3] != "--impl" || w[4] != "go" {
			return "", false
		}
		name = w[5]
	default:
		return "", false
	}
	if name == "" || strings.HasPrefix(name, "-") {
		return "", false
	}
	base := filepath.Base(strings.ReplaceAll(w[0], "\\", "/"))
	if base != "yakos" && base != "yakos.exe" {
		return "", false
	}
	return name, true
}

// hookNameFromTemplateCommand returns the hook name (no .sh) for a bash-form
// template command, or "" when the command is not a scripts/hooks script.
func hookNameFromTemplateCommand(command string) string {
	n := canonicalHookName(command)
	if !strings.HasSuffix(n, ".sh") {
		return ""
	}
	return strings.TrimSuffix(n, ".sh")
}

// templateHookNames returns the sorted, de-duplicated hook names in tmpl.
func templateHookNames(tmpl map[string]any) []string {
	seen := map[string]bool{}
	for _, entries := range hooksMap(tmpl) {
		for _, rawEntry := range asList(entries) {
			for _, rawH := range asList(hooksList(toMap(rawEntry))) {
				if n := hookNameFromTemplateCommand(commandOf(toMap(rawH))); n != "" {
					seen[n] = true
				}
			}
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// goNamesFor returns the hook names that impl moves to Go, given the hook
// names present in the template.
func goNamesFor(impl HooksImpl, present []string) []string {
	switch impl {
	case HooksImplGo:
		return present
	case HooksImplHybrid:
		return goReadyHooks()
	}
	return nil
}

// ValidateHooksImpl fails closed: for go/hybrid, every hook that would be
// registered as `yakos hook run <name>` must exist in the Go registry.
// It never falls back to bash silently.
func ValidateHooksImpl(impl HooksImpl, tmpl map[string]any) error {
	names := goNamesFor(impl, templateHookNames(tmpl))
	if len(names) == 0 {
		return nil
	}
	reg := map[string]bool{}
	for _, n := range registeredHooks() {
		reg[n] = true
	}
	var missing []string
	for _, n := range names {
		if !reg[n] {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("--hooks-impl %s: no Go implementation registered for hook(s): %s (see `yakos hook list`); refusing rather than falling back to bash",
			impl, strings.Join(missing, ", "))
	}
	return nil
}

// applyHooksImpl rewrites template hook commands in place per impl. Call
// ValidateHooksImpl first. Hook ordering and everything but the "command"
// string is untouched.
func applyHooksImpl(tmpl map[string]any, impl HooksImpl, bin string) {
	names := goNamesFor(impl, templateHookNames(tmpl))
	if len(names) == 0 {
		return
	}
	goSet := map[string]bool{}
	for _, n := range names {
		goSet[n] = true
	}
	for _, entries := range hooksMap(tmpl) {
		for _, rawEntry := range asList(entries) {
			for _, rawH := range asList(hooksList(toMap(rawEntry))) {
				h := toMap(rawH)
				if n := hookNameFromTemplateCommand(commandOf(h)); n != "" && goSet[n] {
					h["command"] = goCommand(bin, n)
				}
			}
		}
	}
}

// ---- persistence in <project>/.yakos.yml -----------------------------------

// Groups: 1 = spacing after the colon, 2 = value, 3+ = trailing spacing,
// inline comment, and CR of a CRLF line (all preserved on rewrite).
var hooksImplLineRe = regexp.MustCompile(`(?m)^` + hooksImplYAMLKey + `:([ \t]*)([^\n#\r]*?)[ \t]*(#[^\n\r]*)?\r?$`)

// ReadPersistedHooksImpl returns the project's persisted impl. found is false
// when .yakos.yml or the key is absent. An unparseable value is an error, not
// a silent default.
func ReadPersistedHooksImpl(projPath string) (impl HooksImpl, found bool, err error) {
	data, err := os.ReadFile(filepath.Join(projPath, ".yakos.yml")) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	m := hooksImplLineRe.FindSubmatch(data)
	if m == nil {
		return "", false, nil
	}
	v := strings.Trim(strings.TrimSpace(string(m[2])), `"'`)
	impl, perr := ParseHooksImpl(v)
	if perr != nil {
		return "", false, fmt.Errorf("%s: %s: %w", filepath.Join(projPath, ".yakos.yml"), hooksImplYAMLKey, perr)
	}
	return impl, true, nil
}

// PersistHooksImpl writes hooks_impl into <project>/.yakos.yml with a
// text-level edit so existing content, comments, and formatting survive.
// It creates the file when absent, and is a no-op when the value is already
// current.
func PersistHooksImpl(projPath string, impl HooksImpl) error {
	path := filepath.Join(projPath, ".yakos.yml")
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	eol := "\n"
	if bytes.Contains(data, []byte("\r\n")) {
		eol = "\r\n"
	}
	var out []byte
	if loc := hooksImplLineRe.FindSubmatchIndex(data); loc != nil {
		// Replace only the value; spacing, inline comment, and line ending stay.
		out = append(append(append([]byte{}, data[:loc[4]]...), string(impl)...), data[loc[5]:]...)
	} else {
		out = append([]byte{}, data...)
		if len(out) > 0 && out[len(out)-1] != '\n' {
			out = append(out, eol...)
		}
		out = append(out, hooksImplYAMLKey+": "+string(impl)+eol...)
	}
	if string(out) == string(data) {
		return nil
	}
	tmp, err := os.CreateTemp(projPath, ".yakos-refresh-tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, werr := tmp.Write(out); werr != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return werr
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil { //nolint:gosec
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

// nonGoReadyIn returns the template hooks that `go` mode moves to Go even
// though the registry does not mark them GoReady (sorted).
func nonGoReadyIn(tmpl map[string]any) []string {
	ready := map[string]bool{}
	for _, n := range goReadyHooks() {
		ready[n] = true
	}
	var out []string
	for _, n := range templateHookNames(tmpl) {
		if !ready[n] {
			out = append(out, n)
		}
	}
	return out
}

// runningBinary resolves the absolute, symlink-evaluated path of the
// running yakos binary. A variable so tests can substitute a fixed path.
var runningBinary = func() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return filepath.Abs(exe)
}

// ephemeralBinary reports whether bin lives somewhere unlikely to survive:
// the OS temp dir or a git-worktree-style directory.
func ephemeralBinary(bin string) bool {
	slash := filepath.ToSlash(bin)
	tmp := filepath.ToSlash(os.TempDir())
	if r, err := filepath.EvalSymlinks(os.TempDir()); err == nil {
		if strings.HasPrefix(slash, filepath.ToSlash(r)+"/") {
			return true
		}
	}
	for _, t := range []string{strings.TrimRight(tmp, "/"), "/tmp", "/private/tmp", "/var/tmp", "/private/var/tmp"} {
		if strings.HasPrefix(slash, t+"/") {
			return true
		}
	}
	return strings.Contains(slash, "-wt-") || strings.Contains(slash, "/worktrees/")
}
