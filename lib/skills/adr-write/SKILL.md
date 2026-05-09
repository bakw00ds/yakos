---
name: adr-write
description: Scaffold a new Architecture Decision Record in Michael Nygard format (Context / Decision / Consequences) and update the ADR index
allowed-tools: Bash Read Write
argument-hint: "<title> [--supersedes <NNNN>] [--dir <path>]"
mode: [scaffold]
---

# ADR Write

## Purpose

Scaffold a new Architecture Decision Record (ADR) using Michael
Nygard's canonical format (Context / Decision / Consequences),
allocate the next sequence number, and update the ADR index.
Primary consumer: `architect`. Secondary: any agent making a
decision the team will want to revisit in 6+ months.

## Scope

- Allocates the next ADR number based on existing files in the ADR
  directory (default `docs/adr/`).
- Writes a scaffolded ADR file with frontmatter (status, date,
  deciders, supersedes) and the four canonical sections (Title,
  Status, Context, Decision, Consequences).
- Updates the ADR index (`docs/adr/README.md`) with a new row.
- If `--supersedes <NNNN>` is given, marks the old ADR
  `Superseded by ADR-<new>` and links bidirectionally.
- Does NOT fill in the Context / Decision / Consequences bodies —
  that's the human's (or architect agent's) job. The skill is a
  scaffolder, not a decision-maker.

## When to use

- A non-trivial architectural choice was just made: a new framework
  adopted, a database swap, a service-boundary moved, an auth model
  changed, a build-system migration committed.
- A decision is being revisited and the new decision supersedes
  an old ADR.
- Pre-implementation: the architect agent wants the decision
  written-down before the team starts coding against it.

## When NOT to use

- For routine code changes — ADRs are for decisions with multi-month
  / multi-team consequences, not for "we picked lodash over
  ramda for this one util".
- For bug fixes — those go in the commit message and (if structural)
  in `decisions.md`. ADR is heavyweight.
- As a substitute for design docs — ADR is the *decision*, not the
  *design*. Detailed design lives elsewhere; the ADR cites it.

## Automated pass

1. Resolve the ADR directory:
   ```sh
   adr_dir="${DIR:-docs/adr}"
   if [ ! -d "$adr_dir" ]; then
       # Try alternates before creating
       for alt in docs/architecture/decisions \
                  doc/adr architecture/decisions adr; do
           [ -d "$alt" ] && adr_dir="$alt" && break
       done
   fi
   mkdir -p "$adr_dir"
   ```

2. Allocate the next number (4-digit zero-padded):
   ```sh
   last=$(ls "$adr_dir"/[0-9][0-9][0-9][0-9]-*.md 2>/dev/null \
       | sed -E 's|.*/([0-9]{4})-.*|\1|' | sort -n | tail -1)
   next=$(printf '%04d' $(( 10#${last:-0} + 1 )))
   ```

3. Slugify the title:
   ```sh
   slug=$(echo "$TITLE" | tr '[:upper:]' '[:lower:]' \
       | sed -E 's/[^a-z0-9]+/-/g; s/^-+//; s/-+$//')
   path="$adr_dir/${next}-${slug}.md"
   ```

4. Write the scaffold:
   ```markdown
   ---
   status: proposed
   date: <YYYY-MM-DD>
   deciders: [<lead>, <architect>]
   supersedes: <NNNN-slug or null>
   superseded-by: null
   ---

   # ADR-<NNNN>: <Title>

   ## Status

   Proposed — awaiting review by <deciders>. Will move to
   Accepted on merge of this ADR.

   ## Context

   <What is the issue we're seeing that motivates this decision?
   What constraints, forces, and prior art are in play? Cite
   related ADRs by number; cite design docs by path.>

   ## Decision

   <What is the change we're making? State it as a positive
   directive. Avoid weasel words. The decision should be
   reviewable on its own — a reader should be able to tell
   whether it was followed.>

   ## Consequences

   <What becomes easier or harder as a result? What follow-on
   work is implied? What does this preclude? Be honest about the
   tradeoffs — every decision has them.>

   ### Positive

   - …

   ### Negative

   - …

   ### Neutral / follow-on

   - …
   ```

5. If `--supersedes <NNNN>` was given:
   - Set `supersedes:` in the new ADR's frontmatter.
   - In the old ADR, change `status:` to `superseded` and set
     `superseded-by:` to the new number.
   - In the old ADR's Status section body, append `Superseded by
     ADR-<new>: <title> (<date>)`.

6. Update the index (`$adr_dir/README.md`). Expected shape:
   ```markdown
   # Architecture Decision Records

   | # | Title | Status | Date |
   |---|-------|--------|------|
   | 0001 | … | accepted | … |
   ```

   Insert the new row in numerical order. If the index doesn't
   exist, create it with the canonical header and one row.

7. Print:
   - Path of the new ADR.
   - Path of the superseded ADR (if any), with the line that
     was changed.
   - Reminder: the scaffold is empty by design — the next step is
     to fill in Context / Decision / Consequences before
     committing.

## Manual pass

For one-off ADR creation without the skill (e.g., in a session
without yakos):

```sh
n=$(printf '%04d' $(( $(ls docs/adr/[0-9]*.md | wc -l) + 1 )))
cp docs/adr/template.md "docs/adr/${n}-my-decision.md"
$EDITOR "docs/adr/${n}-my-decision.md"
```

…and update the index by hand. Most teams keep a `template.md` in
the ADR dir; this skill generates one in step 4 if absent.

## Known gotchas

- **Numbering races.** Two agents writing ADRs in parallel can
  allocate the same number. The skill checks-then-writes
  non-atomically; if concurrent ADR authoring is plausible, gate
  the call behind the lead, or use a worktree per agent and
  reconcile numbers at merge.
- **Status drift.** ADRs frequently get stuck in `proposed` because
  no one moves them to `accepted` after merge. The skill writes
  `proposed`; a separate housekeeping pass (or the architect
  agent's discipline) flips to `accepted` on merge. Don't write
  `accepted` at scaffold time — it's a lie.
- **Supersession is not deletion.** A superseded ADR stays in the
  repo; the historical record matters. The skill never deletes;
  it links. If the operator asks to "remove" an old ADR, push
  back: supersede instead.
- **Index format drift.** Projects vary on the index shape (some
  use a tree, some a table, some a YAML file consumed by a static
  site). The skill assumes the table format above; for other
  formats, point `--dir` at the project's adr dir and let the
  project's own indexer (e.g., `adr-tools`, `log4brains`) regen.
- **Templates with extra fields.** Some teams add fields:
  `consulted`, `informed`, `tags`, `rfc-link`. If a
  `$adr_dir/template.md` exists, the skill uses it instead of the
  built-in scaffold; the skill still allocates numbers and updates
  the index.
- **Markdown linting.** Strict markdown linters (markdownlint
  MD041, MD033) sometimes flag the frontmatter or HTML-comment
  placeholders. The scaffold uses plain markdown; if the project's
  linter still complains, the operator adjusts the project's
  lint config — the skill won't soften the scaffold to dodge
  pickyness.

## References

- `lib/agents/architect.md` — primary consumer.
- Michael Nygard, "Documenting Architecture Decisions" (2011) —
  the canonical format.
- `adr-tools` (Nat Pryce) — the original CLI; this skill is
  yakos-native equivalent.
- `log4brains` — modern ADR static-site generator that consumes
  the same file format.
- `decisions.md` (project root) — lighter-weight alternative for
  decisions that don't merit an ADR.
