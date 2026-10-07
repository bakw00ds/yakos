package doctor

import (
	"path/filepath"
	"strings"

	"github.com/bakw00ds/yakos/internal/codexhome"
	"github.com/bakw00ds/yakos/internal/hooksinstall"
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

	lines = append(lines, r.codexHooksLines()...)

	pol, perr := routerpolicy.Load(filepath.Join(r.home, ".yakos-state"))
	switch {
	case perr != nil: // Load reports a missing file as an empty policy, so this is a real problem
		lines = append(lines, func() {
			r.warn(SectionRuntimeIsolation, "router policy ignored (codex stays sandboxed, agy keeps --sandbox): %v", perr)
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

// codexHooksLines reports the K-145 codex hooks file in the yakOS profile. A
// hooks.json that codex will not load is skipped silently, which looks like "no
// gate" to the operator, so each way that can happen gets a line (texts from the
// K-156 spike).
func (r *runner) codexHooksLines() []func() {
	if _, err := r.lookPath("codex"); err != nil || !codexhome.ProfileHasHooks(r.home) {
		return nil
	}
	profile := codexhome.ProfileDir(r.home)
	if _, isolated := codexhome.Effective(r.home, r.env); !isolated {
		return []func(){func() {
			r.warn(SectionRuntimeIsolation,
				"codex hooks in %s are not loaded and codex will skip them silently: dispatch does not use that profile. Run 'yakos auth login codex' (or export OPENAI_API_KEY for the dispatching process), or re-run 'yakos hooks install --harness codex'",
				filepath.Join(profile, codexhome.HooksFileName))
		}}
	}
	if hooksinstall.ShapeDrift(hooksinstall.HarnessCodex, profile, "") == "stale" {
		return []func(){func() {
			r.warn(SectionRuntimeIsolation,
				"codex hook file drift: %s differs from what yakos writes, so codex will report its hooks as modified; re-run 'yakos hooks install --harness codex'",
				filepath.Join(profile, codexhome.HooksFileName))
		}}
	}
	return []func(){func() {
		r.ok(SectionRuntimeIsolation, "codex hooks installed in the yakOS profile; dispatch passes --dangerously-bypass-hook-trust so the per-hook trust step is not needed")
	}}
}
