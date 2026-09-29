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
//	hybrid — only hooks in goReadyAllowlist become `yakos hook run <name>`
//
// Binary reference: the command uses the bare name `yakos`, resolved via PATH
// when Claude Code runs the hook. No other generated settings command embeds
// an absolute binary path, and a bare name keeps settings.json stable across
// reinstalls (rule:cache-stability: same inputs, same bytes).
//
// The switch is Go-only. cli/lib/refresh.sh (bash refresh) always registers
// the bash scripts.

import (
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

// goReadyAllowlist is the set of hooks `--hooks-impl hybrid` moves to the Go
// implementation. Names are hook names without the .sh extension.
//
// Source of truth: the parity matrix in
// work/current/reports/s6-a1-hooks-translator-2026-09-23.md ("GoReady"
// hooks). Widen this list only when that matrix marks another hook
// parity-verified. Keep it sorted (rule:cache-stability).
var goReadyAllowlist = []string{
	"cycle-counter",
	"mailbox-mirror",
	"session-end-check",
	"task-dependency-gate",
	"team-lifecycle",
}

// GoReadyAllowlist returns a copy of the hybrid allowlist.
func GoReadyAllowlist() []string {
	out := make([]string, len(goReadyAllowlist))
	copy(out, goReadyAllowlist)
	return out
}

// registeredHooks lists the hook names registered in the Go registry. It is a
// variable so tests can simulate an unregistered hook.
var registeredHooks = func() []string { return registry.Names() }

// goCommand renders the Go-form hook command for a hook name.
func goCommand(name string) string { return "yakos hook run " + name }

// isGoCommand reports whether command is a `yakos hook run <name>` form.
func isGoCommand(command string) bool {
	_, ok := goHookName(command)
	return ok
}

// goHookName extracts <name> from `[<path>/]yakos hook run <name>`.
func goHookName(command string) (string, bool) {
	f := strings.Fields(command)
	if len(f) != 4 || f[1] != "hook" || f[2] != "run" {
		return "", false
	}
	if f[0] != "yakos" && !strings.HasSuffix(f[0], "/yakos") {
		return "", false
	}
	return f[3], true
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
		return GoReadyAllowlist()
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
func applyHooksImpl(tmpl map[string]any, impl HooksImpl) {
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
					h["command"] = goCommand(n)
				}
			}
		}
	}
}

// ---- persistence in <project>/.yakos.yml -----------------------------------

var hooksImplLineRe = regexp.MustCompile(`(?m)^` + hooksImplYAMLKey + `:[ \t]*([^\n#]*?)[ \t]*(#[^\n]*)?$`)

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
	v := strings.Trim(strings.TrimSpace(string(m[1])), `"'`)
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
	line := hooksImplYAMLKey + ": " + string(impl)
	var out []byte
	if loc := hooksImplLineRe.FindIndex(data); loc != nil {
		out = append(append(append([]byte{}, data[:loc[0]]...), line...), data[loc[1]:]...)
	} else {
		out = append([]byte{}, data...)
		if len(out) > 0 && out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
		out = append(out, line+"\n"...)
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
