package doctor

import (
	"path/filepath"
	"strings"

	"github.com/bakw00ds/yakos/internal/codexhome"
	"github.com/bakw00ds/yakos/internal/routerpolicy"
)

// checkRuntimeIsolation reports how the third-party harnesses are isolated from
// the operator's own sessions (K-133):
//
//   - codex: whether dispatch runs under the yakOS-owned CODEX_HOME profile
//     (~/.yakos-state/codex-home) or shares the operator's ~/.codex. Sharing one
//     auth.json between a yakOS dispatch and an interactive codex risks one
//     refresh invalidating the other (openai/codex#48465). Silent when codex is
//     not installed.
//   - the owner-only router policy: warns when it lets codex or agy run without
//     their sandbox, and when it exists but was ignored (wrong owner, group or
//     world writable, symlink, malformed), so an operator who edited it knows
//     why the sandbox stayed on.
//
// Everything is read-only and local. The section prints nothing on a machine
// with no codex and no policy file.
func (r *runner) checkRuntimeIsolation() {
	var lines []func()

	if _, err := r.lookPath("codex"); err == nil && r.env("OPENAI_API_KEY") == "" {
		profile := codexhome.ProfileDir(r.home)
		if _, isolated := codexhome.Effective(r.home, r.env); isolated {
			lines = append(lines, func() {
				r.ok(SectionRuntimeIsolation, "codex: dispatch runs under the yakOS-owned profile %s", profile)
			})
		} else {
			lines = append(lines, func() {
				r.info(SectionRuntimeIsolation,
					"codex: dispatch shares %s with your interactive codex. Run 'yakos auth login codex' to give yakOS its own login profile (%s)",
					filepath.Join(r.home, ".codex"), profile)
			})
		}
	}

	pol, perr := routerpolicy.Load(filepath.Join(r.home, ".yakos-state"))
	switch {
	case perr != nil: // Load reports a missing file as an empty policy, so this is a real problem
		lines = append(lines, func() {
			r.warn(SectionRuntimeIsolation, "router policy ignored, codex and agy stay sandboxed: %v", perr)
		})
	case len(pol.AllowUnsandboxedRuntimes) > 0:
		var harnesses []string
		for _, name := range pol.AllowUnsandboxedRuntimes {
			switch strings.ToLower(strings.TrimSpace(name)) {
			case "codex", "agy":
				harnesses = append(harnesses, strings.ToLower(strings.TrimSpace(name)))
			}
		}
		if len(harnesses) > 0 {
			lines = append(lines, func() {
				r.warn(SectionRuntimeIsolation, "%s run WITHOUT their sandbox: allow_unsandboxed_runtimes in %s",
					strings.Join(harnesses, ", "), routerpolicy.Path(filepath.Join(r.home, ".yakos-state")))
			})
		}
	}

	if len(lines) == 0 {
		return
	}
	writeln(r, "Runtime isolation")
	for _, f := range lines {
		f()
	}
	writeln(r, "")
}
