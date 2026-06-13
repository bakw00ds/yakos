---
name: runbook-author
description: Given an alert definition (or a postmortem), produce a runbook entry — symptom, first 5 actions, escalation. Use when an alert lacks a response procedure or a postmortem action item calls for one.
allowed-tools: Read Bash
argument-hint: "[--alert <path>] [--postmortem <path>] [--out <path>]"
mode: [author]
---

# Runbook Author

## Purpose

Turn an alert definition (a Prometheus rule, a Datadog monitor, a
PagerDuty service config) OR a postmortem into a runbook entry the
on-call engineer can follow at 3am. Used by the `sre` agent. The
output is a single-page runbook stub the on-call edits as needed —
not a final doc, but a structured starting point that prevents the
"blank page at 3am" problem.

## Scope

- Reads ONE of:
  - an alert definition file (Prometheus `*.rules.yaml`, Datadog
    monitor JSON export, generic YAML with `name` + `query`)
  - a postmortem markdown (`postmortems/YYYY-MM-DD-*.md`)
- Produces a runbook entry at `--out` (default
  `runbooks/<alert-name>.md`).
- Does NOT auto-publish to a wiki; the operator commits or pastes
  the file themselves.
- Does NOT execute any of the actions it suggests — the runbook is
  text, not a script.

## When to use

- A new alert was added — runbook entry should land in the same PR.
- A postmortem identified a missing runbook (action item: "write
  runbook for the disk-full alert").
- During on-call rotation handoff, to fill in stub runbooks for
  alerts that exist but lack docs.

## When NOT to use

- For an alert whose root cause is genuinely unknown. "Investigate"
  is not a first action. If the team doesn't know the symptom-to-
  cause mapping yet, run a game-day or shadow an incident first.
- For automation / auto-remediation. Runbooks are human-targeted;
  if every step is a script, the work belongs in code, not docs.
- As a substitute for SLO definition. A runbook tells the on-call
  what to do; the SLO tells them whether to wake up.

## Automated pass

1. Detect input mode:
   ```sh
   if [ -n "${ALERT:-}" ]; then mode=alert
   elif [ -n "${POSTMORTEM:-}" ]; then mode=postmortem
   else echo "pass --alert or --postmortem" >&2; exit 2
   fi
   ```

2. Extract the salient fields per mode:

   **alert mode** — pull `name`, `expr`/`query`, `for`, severity,
   summary annotation, runbook_url annotation if already set.
   ```sh
   yq '.groups[].rules[] | select(.alert)' "$ALERT"
   ```

   **postmortem mode** — pull title, root cause section, detection
   section, mitigation steps, action items.

3. Render the runbook template. The skill enforces the section
   order — the on-call's eye expects the same order on every entry:

   ```markdown
   # Runbook: <alert name>

   **Severity:** sev-1 | sev-2 | sev-3
   **Owner team:** <team>
   **Pages:** yes | no
   **SLO impact:** <which SLO budget this burns>

   ## Symptom
   What the alert means in one sentence. What the user sees, not
   the metric.

   ## First 5 actions
   1. <single concrete check or action>
   2. <next>
   3. <next>
   4. <next>
   5. <next>

   ## If the first 5 don't help
   Escalate to <secondary on-call / team lead>. Page <service-owner>
   if the symptom is still active after <N> minutes.

   ## Known false positives
   - <condition under which this fires but isn't real>

   ## Related
   - Dashboard: <link>
   - SLO: <link>
   - Recent incidents: <links to postmortems>
   ```

4. For alert-mode, populate the **Symptom** section from the alert's
   `summary` annotation; populate **First 5 actions** with stubs:
   1. confirm the alert by checking the dashboard at <link>
   2. check recent deploys (`kubectl rollout history` / equivalent)
   3. check the dependency graph for upstream alerts firing simultaneously
   4. check error logs for the affected service in the last 15m
   5. if customer-facing, post to #incidents and start an incident channel

   These are STUBS — the operator overwrites them with the real
   symptom-specific actions. The skill marks them with a
   `<!-- TODO: replace -->` comment to make that obvious.

5. For postmortem-mode, populate **First 5 actions** from the
   postmortem's mitigation section. If the postmortem has 8
   mitigation steps, take the first 5 and list the rest under
   "If the first 5 don't help."

6. Write the file at `--out` (or
   `runbooks/<slug-of-alert-name>.md`). Refuse to overwrite if the
   path exists; require `--force`.

## Manual pass

For a one-off entry, the operator copies the template above into a
new file and fills it in. Five sections, ordered. The point of the
skill is consistency across runbooks, not novel content per entry.

## Known gotchas

- **Stub actions get committed verbatim.** The biggest failure mode
  is the operator commits the skill output without replacing the
  TODOs. The skill emits the file with a banner at the top:
  `> [!WARNING] This runbook contains TODO stubs. Replace before
  on-call relies on it.` The CI lint step in the runbooks
  repository should reject files with `<!-- TODO: replace -->`
  still present.
- **Runbook URL not wired into the alert.** The runbook lives in
  the repo, but the alert needs `annotations.runbook_url` pointing
  at the rendered page (e.g., a generated GitHub Pages URL). The
  skill does NOT update the alert def — that's a separate edit.
  Easy to forget; the runbook is useless if the on-call can't find
  it from the page.
- **First 5 actions get long.** The "5" is a forcing function.
  If the on-call needs 12 steps, the alert is too coarse —
  split it into multiple alerts each with a tighter symptom.
  Resist the urge to bullet-list 14 items.
- **Postmortem-derived runbooks go stale.** A runbook generated
  from a 2024 postmortem describes the 2024 architecture. Re-
  validate runbooks during on-call shadowing; mark stale ones with
  a `Last reviewed:` trailer.
- **Pages: yes for everything.** Reviewing the corpus of runbooks,
  if every alert pages, the team is at burnout risk. The skill
  does not enforce this — it's a culture call — but the runbook's
  `Pages:` field surfaces the count.

## References

- Google SRE Book chapters 11 (on-call) and 12 (effective
  troubleshooting).
- `lib/skills/postmortem-write/SKILL.md` — companion: produces
  the postmortem this skill can read.
- `runbooks/` — project-level runbook directory.
- The alerting repo's CI lint that catches unreplaced TODO stubs.
