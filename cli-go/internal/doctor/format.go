package doctor

import (
	"fmt"
	"io"
)

// PrintHelp writes the --help text for `yakos doctor` to w.
// The output is byte-identical to doctor.sh --help (modulo the trailing EOF).
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos doctor [<project-path>] [--probe-runtime] — verify YakOS install + environment health

Without arguments, checks:
    Required commands (bash, git, jq)
    Optional commands (gtimeout, gsed, shellcheck, python3) — surfaced as INFO
    ~/.yakos pointer exists and resolves to an existing repo
    Symlinks under ~/.claude/{agents,skills,rules,playbooks}/ that target
        YakOS resolve cleanly
    ~/.claude/settings.json is valid JSON if present
    ~/.claude/projects/ is intact (informational; never modified)

If <project-path> is passed, additionally checks:
    For each file in <project>/scripts/hooks/, compares the file's SHA-256
    against its .framework-hash sibling (written by 'yakos init') and
    surfaces DRIFT (informational, not an error — projects are expected
    to customize).
    Pre-push version gate installation status and drift.

If --fix is passed, attempts auto-remediation of cheap fixes:
    - missing ~/.yakos-state subdirs (memory, runtime-probes)
    - missing yakOS gitignore patterns in <project>/.gitignore
    - missing per-project .session-started-history.ndjson
    - missing or stale .framework-hash siblings on hook scripts
      (only refreshes when the hook content matches framework src;
      preserves intentional project drift)

If --probe-runtime is passed, additionally reports:
    Filesystem-side state of Claude Code Agent Teams (~/.claude/teams/,
    ~/.claude/tasks/, count of active teams, inbox files).
    The last known state of in-session-only runtime tools (TaskCreate /
    TaskList / TaskUpdate) recorded at ~/.yakos-state/runtime-probe.json.
    The exact prompt to ask in a Claude Code session to refresh the
    last-known state.

Usage: yakos doctor [<project-path>] [--probe-runtime]

Exit code:
    0   No errors (warnings/info/drift OK)
    1   One or more errors
`)
}
