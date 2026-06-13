---
name: source-driven-development
description: Ground framework- and library-specific implementation decisions in official documentation for the exact installed version, citing the source URL, instead of writing from memory. Use when implementing framework features (routing, forms, auth, state), writing reusable boilerplate, or any version-sensitive API call where an outdated or hallucinated pattern would be costly.
allowed-tools: Read Edit Bash Grep WebFetch
argument-hint: "[<framework-or-feature>]"
mode: [implement]
tier: sonnet
invocable_by: [lead, backend, frontend, mobile, architect]
domains: [implementation, quality, correctness]
version: 1
references:
  - skill:hallucination-check
  - rule:lead-dispatch-discipline
  - playbook:02-code-quality
---

# source-driven-development

## Purpose

Back every framework-specific decision with the **official docs for the
version actually installed**, and cite the source — rather than relying
on training-data memory that may be stale or invented. The user gets
code whose every non-trivial pattern traces to an authoritative source
they can independently verify.

The failure mode this prevents: confidently writing an API call the way
it worked two major versions ago (or the way it never worked), which
demos until it hits the deprecated/changed path in production. Memory
about an API is not evidence.

Adapted from [addyosmani/agent-skills](https://github.com/addyosmani/agent-skills)
(MIT) — `source-driven-development`. The output-side companion is
yakOS `hallucination-check` (grounds an LLM *answer* against retrieved
context); this skill grounds *code* against authoritative docs.

## Scope

- **In:** framework/library-specific code — routing, forms, auth, state
  management, lifecycle hooks, build config — where the correct pattern
  is version-dependent.
- **Out:** version-independent correctness (variable renames, typos,
  file moves), pure logic (loops, conditionals, data structures), and
  cases where the user explicitly trades verification for speed.

## Automated pass

The specialist runs the DETECT → FETCH → IMPLEMENT → CITE workflow.

1. **DETECT.** Read the dependency manifest (`package.json`,
   `go.mod`, `requirements.txt`, `composer.json`, etc.) and pin the
   exact versions in play. Patterns differ across majors.
2. **FETCH.** Retrieve the official docs for the *specific feature and
   version* — not the homepage, not a search summary. Source priority:
   1. Official docs (e.g. react.dev, docs.djangoproject.com, pkg.go.dev)
   2. Official blogs / changelogs / migration guides
   3. Web standards (MDN, web.dev)
   4. Official compatibility/runtime data

   Stack Overflow, random blogs, and AI-generated summaries are NOT
   authoritative.
3. **IMPLEMENT.** Write code matching the documented pattern for the
   detected version. If the docs conflict with an existing codebase
   pattern, surface the conflict to the lead — don't silently pick one.
4. **CITE.** Every framework-specific pattern carries a full-URL
   citation (in the PR, the diagnosis, or a code comment), with the
   relevant passage quoted.

## Manual pass

The lead (or reviewer) checks: are versions actually pinned from the
manifest? Are citations to official docs, not blogs? Are any
unverifiable patterns explicitly flagged as such? A specialist that
returns framework code with no citations gets re-dispatched.

## Anti-rationalization

| Rationalization | Reality |
|---|---|
| "I'm confident about this API" | Confidence is not evidence; a major-version bump changes APIs silently. |
| "Fetching docs costs tokens" | Hallucinating a wrong pattern costs more — rework plus the bug it ships. |
| "I added a disclaimer" | A disclaimer doesn't replace verification; it just labels the guess. |
| "It's a simple call" | Simple wrong patterns get copy-pasted and multiplied across the codebase. |

## Known gotchas

- **Version drift.** The docs you cite must match the installed major;
  a citation to current docs for an old pinned version is still wrong.
- **Deprecated-but-functional.** A pattern can work today and be on a
  sunset path. Prefer the currently-documented pattern; if you keep a
  deprecated one for a reason, say why.
- **Don't ground against your own output.** A citation the model
  generated (a fabricated URL/anchor) is not a source. The URL must
  resolve.

## Tier rationale

Sonnet — judgment on source authority and version-matching, plus
reconciling docs against the existing codebase. Haiku can't weigh
source quality; Opus is unnecessary for documented-pattern lookup.
