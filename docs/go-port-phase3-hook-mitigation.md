# Phase 3 hook-mitigation spec

**Status:** design proposal, not approved for implementation.
**Owner:** rotating lead; operator sign-off required before Phase 3 starts.
**Source of truth:** this file. Pairs with `docs/go-port-plan.md` §5.

## 1. Why this doc exists

Today, an operator can `cat lib/hooks/cycle-counter.sh`, edit 80 lines of bash, and see results the next session — no rebuild, no restart, no toolchain. Phase 3 of the Go port (per `go-port-plan.md` §5) trades that property for true Windows-native execution and sub-millisecond hook fires. The risk register (plan §7) flags "premature loss of operator hook editability" as Medium/High; this doc is the mandatory mitigation design referenced there. Phase 3 does not start until this design is approved AND one of the §5 go/no-go triggers fires.

## 2. Comparison matrix

| Dimension | A: Starlark only | B: Lua only | C: Bash escape only | D: Hybrid (Go + Starlark + bash-user) |
|---|---|---|---|---|
| Editability | High (text file) | High (text file) | Highest (today's bash) | High (Starlark) + Highest tier-2 (bash) |
| Portability (Windows-native) | Excellent (pure Go) | Excellent (pure Go) | Poor (needs bash on PATH) | Excellent on tier-1; tier-2 documented bash-required |
| Performance | <1 ms cold | 1–5 ms cold | 5–40 ms (same as today) | <1 ms tier-1; 5–40 ms only if tier-2 fires |
| Learning curve | Moderate (Python-ish, but constrained) | Low for Lua-familiar | None | Moderate, but optional — operator can ignore Starlark and stay on bash |
| Security / sandboxing | Tight (Starlark is sandbox-first) | Looser (Lua C-bindings concern; gopher-lua mitigates) | None (full shell) | Tight tier-1; tier-2 inherits today's shell trust model |
| Maintenance burden | One interpreter, one bridge | One interpreter, one bridge | None (delegates to shell) | Two extension surfaces; documented decision tree |
| Windows-native impact | Full | Full | Defeats Phase 3's point | Full for default path; tier-2 explicitly degraded |
| Migration cost for existing hooks | Rewrite all 30 to Starlark | Rewrite all 30 to Lua | Zero (just relocate) | Translate 30 baseline hooks to Go; operators move customizations to Starlark or bash-user |
| Precedent | Bazel, Tilt, Buildkite Pipelines | Neovim, Redis, Nginx | Husky, lefthook | git itself (native C + `~/.git_template` hooks shell out) |

## 3. Recommendation

**Adopt strategy D (hybrid).** Concretely:

- **Tier 0 (Go-native, the default).** Each hook ships as a Go function compiled into the `yakos` binary. This is the fast path: no interpreter, no shell, native types, schema-validated input.
- **Tier 1 (Starlark customization).** If a file `lib/hooks/<name>.star` exists next to the Go-native hook, the Go hook entry-point loads and runs it via `go.starlark.net`. The Starlark script receives a typed `ctx` object exposing hook input, env, and a narrow API (`ctx.log`, `ctx.set_exit_code`, `ctx.read_file(path)`, `ctx.write_artifact(name, bytes)`). No arbitrary syscalls. The Starlark layer can short-circuit the Go-native logic, augment it, or be a no-op.
- **Tier 2 (bash-user-hooks escape hatch).** After Tier 0 (and Tier 1, if present) complete successfully, the Go entry-point checks for `lib/hooks-user/<name>.sh`. If present AND `bash` is on PATH, it executes the script with the same stdin/env contract as today's hooks. If bash is missing (Windows-native install with no Git Bash), the script is skipped with a single-line diagnostic; the Go-native hook's exit code wins.

Rationale, briefly:

- C-alone (bash escape only) cannot deliver Phase 3's Windows-native goal — it's a Trojan horse for the bash dependency.
- A-alone (Starlark only) is technically clean but forces an immediate translation of every operator customization and a real learning curve. The deferred-with-criteria nature of Phase 3 makes a forced migration disproportionate.
- B (Lua) is fine but Starlark is already the de-facto choice for Go-hosted DSLs; staying inside one community (Bazel/Tilt) means fewer dependency-churn surprises and a stricter determinism story than gopher-lua offers.
- D pays the cost of two extension surfaces but earns: native speed on the default path, portable customization for the common case, and an honest pressure valve for power users who need real shell.

## 4. Implementation outline

### File layout (post-Phase 3)

```
lib/hooks/                         # Go-baseline (read-only for operators)
  README.md                        # explains the three tiers
  cycle-counter.star               # optional override — operator-editable
  ...

lib/hooks-user/                    # operator-owned bash escape hatch
  README.md                        # documents bash-required + Windows caveat
  cycle-counter.sh                 # optional supplemental shell

cli-go/internal/hooks/             # Go-native implementations
  cyclecounter/cyclecounter.go     # baseline logic
  cyclecounter/cyclecounter_test.go
  runner/runner.go                 # tier dispatcher: Go -> Starlark -> bash-user
  starlarkbridge/bridge.go         # ctx object construction, sandbox bounds
  bashbridge/bashbridge.go         # bash-on-PATH detection + invocation
```

### Go interfaces (sketch)

```go
// internal/hooks/runner
type Hook interface {
    Name() string
    Run(ctx context.Context, in HookInput) (HookOutput, error)
}

type HookInput struct {
    Event   string             // PreToolUse, PostToolUse, ...
    Tool    string
    Payload map[string]any     // schema-validated upstream
    Env     map[string]string
    WorkDir string
}

type HookOutput struct {
    ExitCode  int
    Stdout    []byte
    Stderr    []byte
    Artifacts map[string][]byte  // written to work/current/hooks/<name>/
}
```

The runner composes tiers:

```go
func (r *Runner) Run(ctx context.Context, h Hook, in HookInput) (HookOutput, error) {
    out, err := h.Run(ctx, in)                    // Tier 0
    if err != nil { return out, err }
    if star, ok := r.starlarkOverride(h.Name()); ok {
        out, err = star.Apply(ctx, in, out)       // Tier 1
        if err != nil { return out, err }
    }
    if bash, ok := r.bashUserHook(h.Name()); ok && r.bashAvailable {
        out, err = bash.Apply(ctx, in, out)       // Tier 2
    }
    return out, err
}
```

### Sample Starlark hook (Tier 1 override of `cycle-counter`)

```python
# lib/hooks/cycle-counter.star
def on_event(ctx):
    # default Go logic already ran; we just bump the threshold
    threshold = 7  # was 10
    counter = int(ctx.read_file(".cycle-count") or "0") + 1
    ctx.write_artifact(".cycle-count", str(counter))
    if counter % threshold == 0:
        ctx.write_artifact(".retro-due", "")
        ctx.log("retro marker dropped at cycle %d" % counter)
```

## 5. Migration path

When Phase 3 ships:

1. **Today's `lib/hooks/*.sh` are translated to Go** and live in `cli-go/internal/hooks/<name>/`. They become the Tier-0 baseline. The original `.sh` files are NOT deleted in the same release — they're moved to `lib/hooks/legacy/` (read-only, documented as reference) for one release cycle, then removed in Phase 3.1.
2. **Operators with light customizations** (changed a threshold, added a log line) port their delta to a sibling `lib/hooks/<name>.star` file. A `yakos hooks migrate` helper subcommand offers a stub-Starlark scaffold for each customized hook detected via git history.
3. **Operators with heavy customizations** (genuine shell pipelines) move the file to `lib/hooks-user/<name>.sh`. It keeps working on platforms with bash; on Windows-native it's a documented no-op.
4. **Auto-translation is NOT offered.** Translating bash → Starlark mechanically produces unreadable Starlark and hides semantic bugs. The migration helper scaffolds; it does not translate.
5. **Compatibility audit:** for one release cycle, both the Go binary and bash `yakos` (still installed via shadow mode) fire the same hooks; CI compares artifact bytes for divergence.

## 6. Phase 3 go/no-go restated

Per plan §5, Phase 3 starts only if at least one of:

1. ≥3 distinct operators have submitted written feedback demanding true Windows-native (no Git Bash dependency).
2. Phase 1+2 surface repeated bash↔Go interop bugs (hook env-marshalling, line-ending breakage, exit-code translation).
3. Performance audit shows hook latency dominates measured workflow cost (>20% of wall-clock in a representative `dispatch + PostToolUse + retro` cycle).

Given today's state (Phase 1 just kicked off, no Windows operators on record, hooks measured at 5–40 ms which is below noise for most workflows), Phase 3 stays deferred. This doc exists so that when a trigger fires, design isn't on the critical path.

## 7. Open questions

| # | Question | Recommendation | Decide-before |
|---|---|---|---|
| 1 | Should Tier 1 (Starlark) be allowed to override Tier 0, or only augment it? | Allow override, but require `override = True` declaration in the .star file. Auditable. | Phase 3 design freeze |
| 2 | Where do bash-user-hooks live on Windows-native installs where bash is missing — present-but-skipped, or refuse-to-install? | Present-but-skipped with one-line diagnostic on first skip. | Phase 3 implementation start |
| 3 | Sandbox: should Starlark hooks have read access to arbitrary `$HOME` paths, or only to `work/current/` + `lib/hooks/`? | Only `work/current/` and explicit allow-list paths via `ctx.read_file`. Tighter is reversible; looser is not. | Phase 3 design freeze |
| 4 | Do we ship a `yakos hooks lint` subcommand for Starlark hooks? | Yes — leverage `go.starlark.net`'s static analysis hooks; cheap to add, expensive to omit. | Phase 3 milestone 1 |
| 5 | Do we deprecate `lib/hooks-user/*.sh` if Tier 1 adoption is high after one release? | No — the escape hatch is the point. Deprecate only if usage stays at zero for 2 releases. | Phase 3.1 |
| 6 | Per-operator hook registry (`~/.yakos/hooks-user/`) in addition to repo-local? | Defer. Solve the repo-local case first; per-operator hooks invite the same drift problems global git hooks have. | Phase 3.1 |
| 7 | Compatibility window for `lib/hooks/legacy/` shell copies? | One release cycle, then removal. Document the date in the Phase 3 changelog entry. | Phase 3 ship |
