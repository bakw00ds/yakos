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
	file := "~/.yakos-state/" + codexhome.ProfileDirName + "/" + codexhome.HooksFileName // no absolute home path in the output
	if _, isolated := codexhome.Effective(r.home, r.env); !isolated {
		return []func(){func() {
			r.warn(SectionRuntimeIsolation,
				"codex hooks in %s are not loaded and codex will skip them silently: dispatch does not use that profile. Run 'yakos auth login codex' (or export OPENAI_API_KEY for the dispatching process), or re-run 'yakos hooks install --harness codex'",
				file)
		}}
	}
	in := hooksinstall.InspectShape(hooksinstall.HarnessCodex, profile, "")
	var lines []func()
	if in.BinaryMissing {
		lines = append(lines, func() {
			r.warn(SectionRuntimeIsolation,
				"codex hooks in %s run a yakos binary that no longer exists (%s): codex fails open when a hook cannot start, so the yakOS gate is OFF; re-run 'yakos hooks install --harness codex'",
				file, r.tilde(in.Binary))
		})
	}
	switch in.State {
	case "unsafe":
		lines = append(lines, func() {
			r.warn(SectionRuntimeIsolation,
				"codex hooks file %s (or its directory) is a link, belongs to another user, or is group/world-writable: dispatch will NOT trust it (no --dangerously-bypass-hook-trust, codex skips it, the yakOS gate is OFF). Fix the permissions or re-run 'yakos hooks install --harness codex'",
				file)
		})
	case "stale":
		lines = append(lines, func() {
			r.warn(SectionRuntimeIsolation,
				"codex hooks file %s differs from what this yakos installs: dispatch will NOT trust it (no --dangerously-bypass-hook-trust, so codex skips it and the yakOS gate is OFF). If it was edited by anything but yakos, treat it as tampering; re-run 'yakos hooks install --harness codex'",
				file)
		})
	}
	if len(lines) > 0 {
		return lines
	}
	return []func(){func() {
		r.ok(SectionRuntimeIsolation, "codex hooks installed in the yakOS profile and match this yakos; dispatch passes --dangerously-bypass-hook-trust only for exactly this file")
	}}
}

// tilde shortens a path under the home directory so output carries no absolute
// home path.
func (r *runner) tilde(p string) string {
	if r.home != "" && strings.HasPrefix(p, r.home+string(filepath.Separator)) {
		return "~" + p[len(r.home):]
	}
	return p
}
