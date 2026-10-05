package doctor

import (
	"fmt"
	"io"
)

// PrintHelp writes the --help text for `yakos doctor` to w.
// The output is byte-identical to doctor.sh --help (modulo the trailing EOF).
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos doctor [<project-path>] [--probe-runtime] [--probe-decision [--live]] [--preflight] [--policy] — verify YakOS install + environment health

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

If --probe-decision is passed, additionally reports on the decision provider
(ADR-0009; a provider is not a runtime):
    TYPESAFE_API_KEY set / not set (the value is never printed)
    decisions: block in <project>/.yakos.yml and its pinned model
    Question sets under lib/decisions/ (schema, pinned model, hash)
    Circuit-breaker state and today's spend against the budget caps
    With --live ONLY, one minimal real call (about $0.000002, not counted
        against the budget) reporting the
        HTTP outcome, latency, and drift between the served and pinned model

If --preflight is passed, ONLY the Preflight section runs (fast path,
skips every check above) — the session-start checks that catch common
blockers before dispatch:
    gh auth status + token scopes (repo, workflow); skips cleanly if gh
        is absent
    git usable (detects the macOS "Xcode license not accepted" failure
        and prints the fix)
    YAKOS_ROOT / YAKOS_LIB sanity: aliasing another git worktree of the
        same repo, or a case mismatch against the on-disk path
    Framework checkout has no modified tracked files (untracked ignored)
    Stale worktrees (missing on disk, or branch merged into main) and a
        count of merged local branches with no upstream
    Running daemon's build id matches this binary's (CLI<->daemon
        handshake; stale daemon detection)
    <work>/current/kanban.md exists, parses, and has no IN PROGRESS
        item whose most recent embedded date is >7 days old

If --policy is passed, ONLY the policy report runs (skips every check above
and takes no project path): risky configurations, one line each with a severity
(high, medium, low) and a fix hint. It reports environment variable names and
booleans only, never a value:
    SDK sidecar (structured questions) selectable without ANTHROPIC_API_KEY
    allow_unsandboxed_runtimes set in ~/.yakos-state/router-policy.yml, or that
        file refused by its owner-only trust check
    YAKOS_IMPL unset with the bash CLI tree present, so yakos dispatch runs
        codex and agy through the bash adapters without their sandbox flags
    codex on PATH with no yakOS-owned login profile
    YAKOS_DISPATCH_LOG, YAKOS_STATE_DIR and other state-path variables set in
        the environment (a project's .claude/settings.json env block can set them)
    Always exits 0: it is a report, not a gate.

Usage: yakos doctor [<project-path>] [--probe-runtime] [--probe-decision [--live]] [--preflight] [--policy]

Exit code:
    0   No errors (warnings/info/drift OK; --policy always exits 0)
    1   One or more errors
`)
}
