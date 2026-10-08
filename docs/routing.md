# Routing

This file will hold the routing documentation (rules, fallbacks, the sensitive
routing class) as those pieces land. Today it covers the model registry (K-138),
the first piece of routing phase P1.

## Model registry

The registry answers one question: what models exist, and what is each one? For
every model on every harness (claude, codex, agy) it records who serves it, how
it is billed, which reasoning efforts it takes, its token limits, its price (only
for a model billed per API call), the tier aliases that resolve to it, whether it
is switched off and whether the signed-in account can use it. It does not choose a
model for a task; that is the router (K-139). Code: `cli-go/internal/modelreg`.

### Sources

Applied in this order. The catalog defines the models (discovered ids join only if
the overlay admits them); a later source only changes what the earlier ones say.

| Source | Where | Written by | May do |
|---|---|---|---|
| Catalog | `lib/settings/model-catalog.json`, embedded in the binary | the framework | define models, defaults and tier aliases |
| User overlay | `~/.yakos-state/model-registry.yml` | you | enable or disable a model, state its billing, price an `api` model, map a codex or agy tier alias, admit discovered ids |
| Project | `<project>/.yakos.yml`, key `models:` | the repository | disable models, nothing else |
| Discovery | `agy models`, cached in `~/.yakos-state/model-discovery.json` | the harness | set an availability flag |

### The catalog

The catalog has the models.dev shape (`name`, `family`, `modalities`, `limit`,
`cost`) plus yakOS keys. The seed has 29 entries.

| Field | Meaning |
|---|---|
| `id` | The value of the harness's model flag; for claude a tier name. Ids match `^[a-z0-9][a-z0-9._:-]{0,63}$`, the rule dispatch applies before an id reaches a command line. |
| `provider` | Who serves the model, and so receives the request: `anthropic` on claude, `openai` on codex, `google` for everything agy lists, including the Claude and open-weight models it fronts. |
| `harnesses` | Runtimes whose model flag takes the id. A model on two harnesses is two entries. |
| `billing` | `subscription` (the harness login), `api` (billed per call) or `local`. Only an `api` model has a `cost`, in dollars per million tokens; tokens are the unit for the rest. |
| `effort_levels` | Reasoning efforts the model takes, with `default_effort`. On agy the effort is part of the id (`-low`, `-medium`, `-high`) and `--effort` must not accompany it, so those entries have `effort_in_id: true` and one level. |
| `limit` | `context`, `context_max` (codex's extended window) and `output`, in tokens. |
| `default_enabled` | `false` for an entry that ships off: `gpt-reserve` and `codex-auto-review`, which codex hides from its own picker. |

Seed sources, dated in the file: the four Claude Code tier names (2026-10-06);
`codex debug models`, codex-cli 0.154.0, ChatGPT login (2026-10-05), seven
entries with codex's own descriptions (that catalog is per login and plan); and
`agy models`, agy 1.2.17 (2026-10-06), eighteen ids, identical to the 1.2.7
listing of the day before. All are `subscription`, so none has a price. The
copy embedded at `cli-go/internal/modelreg/model-catalog.json` must equal the lib
file (`TestEmbeddedCatalogMatchesLib`).

### Tier aliases

| Alias | claude | codex | agy |
|---|---|---|---|
| `cheap` | `haiku` | (empty) | `gemini-3.8-flash-low` |
| `balanced` | `sonnet` | (empty) | `gemini-3.8-flash-high` |
| `best` | `opus` | (empty) | `claude-opus-5-5-medium` |
| `reasoning` | `opus` | (empty) | `gemini-3.1-pro-high` |
| `frontier` | `fable` | (empty) | `claude-opus-5-5-high` |

The catalog's `aliases` key equals the `aliases` key of
`lib/settings/model-aliases.json`, the file the bash CLI reads, which stays as it
was; `TestCatalogAliasesMatchLegacyFile` and
`TestCatalogAliasesAgreeWithTheRuntimeAliasTable` fail when the catalog, that file
or the table dispatch embeds drift apart. **The codex column is empty on
purpose.** Empty means the harness default and no `-m` flag: codex's catalog is
per login and plan, gives no documented tiers and rejects an id outside the
account's list. If your account has the ids, map them in the overlay.

### The user overlay

`~/.yakos-state/model-registry.yml` is optional. The example uses every key the
file accepts; the prices are example values.

```yaml
version: 1                    # optional; any value but 1 makes the whole file ignored
models:
  gpt-5.6-sol:
    enabled: false            # switch a model off, or true for one the catalog ships off
  claude-opus-5-5-high:
    billing: api              # subscription, api or local: how you pay for it
    pricing:                  # dollars per million tokens; kept only while billing is api
      input: 1.0              # input and output are required
      output: 4.0
      cache_read: 0.1         # optional
      cache_write: 1.25       # optional
aliases:                      # codex and agy columns; the claude column is fixed
  balanced:
    codex: gpt-5.6-terra
  best:
    agy: ""                   # "" clears a mapping: no model flag, the harness default
discovery:
  admit: [agy]                # ids `agy models` lists that the catalog lacks become entries
```

A `models:` key is an id and applies on every harness that lists it. A price on a
model that is not `api` is dropped with a warning that says to set `billing: api`
first. An alias may name an id the registry does not know (a model newer than the
catalog): it is accepted and reported.

**Trust.** The overlay can loosen, so it is read only when nobody else could have
written it: a regular file, not a symlink, owned by you, not group- or
world-writable, in a directory with the same properties. It is opened and compared
with the entry that was checked, so a swap in between is refused. These are the
file rules of the router policy and the budget policy plus the directory rules of
the default-runtime file; a 0644 file in a 0755 directory is trusted. The
directory is `$HOME/.yakos-state`, never the one `YAKOS_DISPATCH_LOG` can name,
because a project can set that variable for the processes it starts (K-129). With
no home directory, or one that is not an absolute path (`HOME=.` would be the
working directory), there is no overlay.

**Failure.** A file that fails the check, is over 256 KiB, is not YAML, has a top
level that is not a mapping or has a `version` other than 1 is ignored whole, with
one warning that names the file's role and no path. That loosens nothing, and it
also forgets any `enabled: false` the file held until you fix it. A mistyped entry
in a good file costs only that entry; an unknown key is reported and ignored. At
most 512 `models:` entries are read, and a file reports at most 25 problems (the
rest are counted in one closing line), so a hostile file cannot flood the terminal.

### Project rule

A project's `.yakos.yml` may say `models: {disable: [opus, gpt-5.6-sol]}`.
`disable` is the only key that does anything: `enable`, `add`, `aliases`,
`providers`, an id-keyed map or anything else under `models:` is reported and
ignored, so a cloned repository can narrow what runs and never widen it. A project
disable is applied after the overlay and beats an overlay `enabled: true`. It
disables the id on every harness, and an unknown id is reported. The reader takes
at most 256 ids from a regular `.yakos.yml` of at most 256 KiB. A file that is not
valid YAML gives a warning that says its disable list is not applied, because a
repository file cannot be trusted to be well formed and a typo anywhere in it
loses the restriction.

### Discovery

Discovery asks a harness which models the signed-in account can use. Only agy has
a listing wired in (`agy models`); claude and codex report `unsupported` and their
entries stay `unknown`.

- **Gate.** The P0a sign-in probe runs first (`auth.ProbeRuntime`: the CLI is on
  PATH and looks signed in; no network call), and a harness that fails it is
  skipped. It is best effort: a signed-out agy can still pass, and if `agy models`
  then fails or prints nothing usable, that is a failed probe.
- **Command.** `agy models` with fixed arguments, no shell and no stdin. It runs
  in a fresh private directory, `.discover-<pid>-<n>` inside the secured state
  directory (the pid is the owning yakOS process's), removed afterwards. There is
  no fallback to the temporary directory, because `TMPDIR` can be set by a project
  and agy run in a directory the project chose may load its workspace
  configuration: with no secured state directory (no home, a relative home, or a
  state directory that is a symlink or not a directory) the probe is skipped and
  agy is not run. On Unix it starts in a session of its own, so it has no
  controlling terminal to prompt on and the whole process group, not only agy, is
  killed when the probe times out, is cancelled or prints too much. When agy exits
  successfully but a helper it started still holds the output pipes, the probe
  returns what agy printed once the two-second wait delay has passed and kills the
  group then, so no helper outlives the probe. Windows has no process group and
  kills agy alone. A Ctrl-C at the terminal no longer reaches agy, so
  `yakos models probe` binds the probe to the interrupt and waits for the killed
  run before it exits. Its environment is a short allowlist: PATH and HOME, the
  platform basics, proxy and certificate settings, and agy's own credential
  families (`GEMINI_*`, `GOOGLE_*`, `GCLOUD_*`, `ANTIGRAVITY_*`). It is narrower
  than the one dispatch gives agy: no `GH_TOKEN`, `GITHUB_TOKEN`, `SSH_AUTH_SOCK`,
  `GIT_*`, `NODE_OPTIONS` or `YAKOS_*`, which an agent's git work needs and a
  listing does not. A relative PATH entry is refused. Output is capped at 256 KiB
  (stderr 16 KiB) as it is produced.
- **Never blocking.** `Snapshot` reads memory or one small file and runs nothing.
  `Kick` returns at once and refreshes in the background when there is no listing
  or it is older than 6 hours, unless one is running or a probe was skipped or
  failed in the last 60 seconds. `Probe` is bounded by `--timeout` (default 15
  seconds, at most 2 minutes) and kills the process when that ends; it returns
  within three seconds of that even if the sign-in check ignores its context.
  Concurrent probes of one harness share a single run, and a caller that arrives
  while the last caller is cancelling it starts a fresh run instead of inheriting
  the cancellation. Only `yakos models` builds a discoverer today; dispatch does
  not call it.
- **Output is data.** One line per model, `id<TAB>display name`. A leading UTF-8
  byte order mark is ignored (it must not cost the first id). A line with no tab,
  or an id that fails the id rule, is dropped and counted: a one-word message
  such as `unauthorized` on standard output must not become a one-model listing.
  At most 512 models are kept, and names lose
  control, ANSI escape and Unicode format or bidirectional characters and are cut
  to 80. **Nothing the command printed reaches a reason, a warning or an error,
  and none holds a path**: a failed exit reports `agy models exited with status N
  (run `agy models` to see its message)`, a failure to start says only that agy
  could not be started (permission denied, or gone from where PATH found it), and
  a cache that cannot be written says the state directory is not usable. Vendor
  error text can hold a token, an API-key URL or a home path, and a probe's output
  is read by agents and meant to be served over an API.
- **A listing that cannot be used changes nothing.** A non-zero exit, a timeout,
  oversized output or a listing with no valid id fails the probe and leaves the
  previous snapshot, in memory and on disk, so a glitch never marks every model
  unavailable.
- **Leftover directories.** A probe killed with SIGKILL, a crash or a power cut
  cannot remove its directory, so every probe sweeps the `.discover-*` directories
  of earlier ones before it makes its own, once the state directory is secured.
  The sweep goes by the owner in the name: a directory whose pid is alive (a probe
  in another yakOS process) stays, unless it is more than an hour old, since pids
  are reused; a directory of a dead pid goes (on Unix only: on Windows the liveness check
  always answers "alive", so a directory with a pid in its name waits the hour
  like any other leftover); a name with no pid (an earlier build) goes only when
  it is more than an hour old. It touches real directories the
  current user owns and nothing else: a symlink with such a name is left alone and
  nothing behind it is touched, a regular file is not a work directory, and
  removing a directory does not follow a link inside it. At most 32 are removed
  per probe, and a failure to remove one is silent. SIGKILL does not stop the
  agy that was running; only the directory is swept.
- **Cache.** `~/.yakos-state/model-discovery.json`, mode 0600, written by
  temporary file and rename in a directory made or tightened to 0700. It is read
  back through the overlay's trust check with every id validated again; an
  untrusted, corrupt, wrong-schema or oversized (over 1 MiB) file counts as
  absent. A snapshot is fresh for 6 hours, then shown as stale.
- **Effect.** `available` is `yes` when the id is listed, `no` when a listing was
  taken and lacks it, `unknown` otherwise. No entry is added unless the overlay
  says `discovery: {admit: [agy]}`; then listed ids the catalog lacks become
  entries (source `discovered`, provider `google`, `subscription`, enabled, effort
  from the id suffix), after the catalog entries and sorted by id. A listed id
  that is a tier alias word (`cheap`, `balanced`, ...) or a Claude tier name
  (`sonnet`, ...) is skipped with a warning, not admitted: dispatch reads those
  words as aliases or tiers, so an entry under that name could never be reached as
  the model. The billing of an admitted entry is labelled `discovered` (the
  `billing_by` field), meaning it was assumed to be `subscription`, not stated by
  the catalog; set `billing: api` in the overlay to say otherwise.

### Commands

```
yakos models list  [--harness <name>] [--project <path>] [--json]
yakos models show  <id> [--harness <name>] [--project <path>] [--json]
yakos models probe [--harness <name>] [--timeout <duration>] [--json]
yakos models enable|disable <id>
yakos models alias <alias> <codex|agy> <id|default>
yakos models pin <agent> <id> [--runtime <name>] | pin <agent> --clear
yakos models pricing <id> --input <usd> --output <usd> [--cache-read <usd>] [--cache-write <usd>] [--billing <mode>] | pricing <id> --clear
```

The last five write (see "Policy writers").

`list` and `show` never run a harness CLI; availability comes from the cache.
`--project` (default: the working directory) names the project whose `.yakos.yml`
may disable models. Warnings go to stderr, once each, and into the `--json` output.

```
ID                        HARNESS  BILLING       AVAILABLE  ALIASES
haiku                     claude   subscription  unknown    cheap
opus                      claude   subscription  unknown    best,reasoning
gpt-5.6-terra             codex    subscription  unknown    -
gpt-reserve               codex    subscription  disabled   -
gemini-3.8-flash-high     agy      subscription  yes        balanced
gemini-3.1-pro-low        agy      subscription  no         -
```

`AVAILABLE` is `disabled` for a switched-off model, whatever discovery saw, and
`yes (stale)` marks a listing older than 6 hours. `show` prints a block per
harness: provider, billing and where it was decided, the price or why there is
none, who enabled or disabled it, availability, aliases, efforts, limits, source.
`probe` runs discovery for the harnesses named (all three by default), caches the
answer and prints, per harness, `updated` with the count and what is new or gone
since the last listing (and the listed ids the catalog lacks), `skipped` with the
reason (`agy: skipped, not signed in; run: yakos auth login agy`) or `failed`.
Exit codes: 0 for success, including a skipped or unsupported probe; 1 for a usage
error, an unknown id or a probe that ran and failed. It never exits 2, the code
Claude Code hooks use to block a call.

### Tier classes and Clamp

A tier alias is also a cost class: `cheap` 1, `balanced` 2, `best` and `reasoning`
3, `frontier` 4. A model's rank on a harness is the rank of the alias that maps to
it there (the highest, if several do). `Registry.Clamp(harness, model, ceiling)`
lowers a model to a ceiling, a tier alias or a Claude tier name (`sonnet` is
`balanced`). It returns the model unchanged when the ceiling is not understood, the
model has no class on that harness, it is within the ceiling already, or nothing at
or below the ceiling is mapped there; it never raises a model or leaves the
harness. On claude it is the ordering `budget.ClampModel` has always applied to
`max_model` (haiku < sonnet < opus < fable): a test runs both for every tier and
ceiling and requires the same answer; `budget.ClampModel` now calls `Clamp`.
Codex models have no class until you map aliases in the overlay.

`Registry.EnforceCeiling(harness, model, ceiling)` is `Clamp` for a caller that
enforces the ceiling as a cost control, and dispatch uses it. It says which of
four things happened: the model is within the ceiling, it was lowered, it has no
class (unranked), or it is above the ceiling and the registry maps nothing at or
below it. Dispatch applies an agent's `max_model` ceiling on every runtime, not
only claude:

| Case | Result |
|---|---|
| Within the ceiling | Kept. |
| Above it, a lower class is mapped on the same harness | Replaced by the model of the highest class at or below the ceiling on that harness, with a notice. claude: the ceiling's own tier (`sonnet`). agy: that class's agy model (`balanced` is `gemini-3.8-flash-high`; `cheap` is `gemini-3.8-flash-low`). codex: none until the overlay maps its aliases. |
| Unranked: an id no alias names, or the harness default (no model flag) | **Refused**, naming the model, the ceiling and `yakos models show <id>`. Never passed through: 13 of agy's 18 listed ids have no class. |
| Above it, nothing at or below it is mapped on that harness | Refused, the same way. |

A replacement is always a model of the harness that runs the dispatch, never
another harness's. An agent a project names as its supervisor (`supervisor:
agent: watchdog`) takes the supervisor's ceiling, and the lower of that and its
own.

A replacement is judged by its own class too: an id that an overlay maps under a
dearer alias as well counts as the dearer one and is skipped, and the walk goes on
to the next candidate down (loading an overlay that does this warns, naming both
aliases).

One gap to close before the clamp serves a harness other than claude: a model that
no alias maps to has no class, so it passes any ceiling. On claude all four tiers
have one; on agy only the five models the aliases name do, and a sibling such as
`claude-opus-5-5-low` passes a `cheap` ceiling. Whoever wires Clamp to agy or codex
(K-139, K-142) must decide what an unranked model under a ceiling means, and
`Registry.ClassOf` tells the two cases apart. `TestClamp_UnrankedModelsPassThroughByDesign`
pins today's behaviour so changing it is deliberate.

### What this does not change

- Dispatch still resolves a `model:` alias through its own embedded table. An
  overlay alias for codex or agy changes what `yakos models` shows and what the
  router will read (K-139), not what `yakos dispatch` sends. The bash CLI never
  reads the registry, and the registry does not read the bash project key
  `model-aliases:`: a project cannot add aliases.
- The claude alias column cannot be overridden: the claude CLI takes tier names.
- Dispatch does not refuse a disabled model today; `enabled` is for routing
  candidates and for `list`. No dispatch-log field, accounting rule, listener or
  console page changes.

## Router rules (`rules:` in router-policy.yml)

The router sits at the one step `Run` and `RunStream` share. **R0** is the
default rule: the resolve chain described above (override, agent frontmatter,
per-domain, default-runtime, env, state default, claude; then the fallbacks,
filtered by the sign-in probe). With no rules in the policy file every decision
is R0 and equals that chain exactly. Pins and the cooldown (below) still apply
once any trusted `router-policy.yml` exists, rules or not: an operator who only
sets `gateway_classes` (K-141) also gets sticky conversations and the cooldown.

Rules live in `~/.yakos-state/router-policy.yml`, the file that also holds
`allow_unsandboxed_runtimes`, read by the same reader under the same trust check
(a regular file you own, not group or world writable; otherwise it is ignored
with a warning that names no path). At most six rules are read; they are numbered
`R1`..`R6` in file order and the first match wins.

```yaml
rules:
  - match: {domain: code-review}        # class, agent, domain, task_bytes_gt, tags
    action: {runtime: codex, model: gpt-5.5, fallbacks: [claude]}
  - match: {task_bytes_gt: 20000}
    action: {model: haiku}
    override_pins: true                 # outrank the agent's runtime:/model: pins
```

- A key left out of `match` matches anything; every key that is set must hold.
  `tags` has no source yet, so a rule that lists tags does not match.
- `action` keys left out leave the default choice alone. A rule's `model` is
  checked against the runtime that runs it, and dropped with a notice when that
  runtime cannot take it (a Claude tier never goes to codex or agy, another
  vendor's id never to Claude Code). It is not carried to a fallback runtime.
- Frontmatter `runtime:` and `model:` pins outrank a rule unless it sets
  `override_pins: true`. An explicit `--runtime`, `--model`, a bare runtime name
  as the agent, and a conversation's earlier routing always outrank a rule.
- A runtime that fails three times in a row (a non-zero exit counts, not only an
  exec error) is skipped for 60 seconds (in memory), per project root: one project's failures
  never cool a runtime for another project the same daemon serves. This is a preference: if
  nothing else can run, it is tried anyway. The cooldown, like the conversation
  pins below, only engages when a trusted `router-policy.yml` exists (any
  content, rules or not). With no policy file the resolve chain is exactly the
  one above, failures included.
- With a policy file present a conversation keeps the runtime and model of its
  first turn. The router never moves it, and the cooldown never moves a pinned
  conversation: its runtime is used even while cooling (the turn may fail with
  that runtime's own error). The cooldown only skips runtimes when choosing for
  a conversation with no pin or no conversation id. An explicit runtime or model
  on the request (`--runtime`, `--model`, the API field) is the operator moving
  the conversation, and re-pins it: later turns stay where the operator put it.
  A rule-derived or default decision never overwrites a pin.
- A pin is keyed by conversation id, agent, project root and the sha of the
  policy file. A pin made under another project root or an earlier version of
  the policy is ignored and replaced by the new decision, so a conversation id
  reused across projects, or a policy edit, starts fresh.
- A project `.yakos.yml` may only switch things off:
  `router: {disable_runtimes: [codex], disable_models: [gpt-5.5]}`. It cannot add
  a rule, a runtime or a provider. A `disable_models` entry is a concrete id, a
  tier alias or a Claude tier name, and matches the model that runs, judged on the
  runtime that runs it, when the two are the same word or resolve through the model
  registry to the same id: `balanced` blocks `sonnet` on claude (and `sonnet`
  blocks what `balanced` names), `gemini-3.8-flash-high` on agy, and on codex,
  where the shipped alias column is empty, the harness default (no model flag),
  and `best` or `reasoning` blocks `opus`. An entry
  never reaches across runtimes: `balanced` does not block a pinned codex id. An
  overlay that remaps an alias moves what the word names.

The ledger row of a dispatch carries `route_rule`, `route_reason`, `route_class`
and (when rules are in force) `policy_sha`. None of it goes into a prompt.

`route_reason` is owned by the router and is set on every row from the decision
(`default chain: runtime claude by frontmatter`, `rule R2 matched [...]: ...`,
`sticky: ...`). When a Claude dispatch also ran with the `gateway_classes`
aliases above, `; env-alias` is appended to that reason
(`default chain: runtime claude by frontmatter; env-alias`); `route_reason` is
just `env-alias` only if the router left it empty. `policy_sha` is the SHA-256
of the one trusted `router-policy.yml`; the router and the alias stamp read it
through the same helper (`routerpolicy.FileSHA` / `routerpolicy.Load`). It is
present when rules are in force or aliases were applied, absent otherwise.

### Explaining a route

`yakos router explain` answers "where would this dispatch go, and why" without
dispatching: it runs the same routing step `Run` does (probes included) and
starts nothing, writes no ledger row, pins no conversation and prints no notice.

```
$ yakos router explain backend --class chat
claude/sonnet rule=R3 chain=[claude codex]
agent: backend
runtime: claude
model: sonnet
provider: anthropic
rule: R3
chain: [claude codex]
reason: rule R3 matched [class=chat]: runtime=claude model=sonnet fallbacks=[codex]
fallback_from: -
route_class: chat
policy_sha: 3f4135ea62d49d5ac61c14051d709ac1dc84442814bc2d268d3e4d3abcc09a72
```

- `yakos router explain <agent> [--task-file F] [--class C] [--project DIR]
  [--json]`. `--task-file` supplies the task size for `task_bytes_gt` rules (the
  file is only measured, never read). `--class` is a route class a rule matches
  on, `default`, or a Claude Code request class; any other class is a usage error.
- `yakos dispatch --explain <agent> [task]` prints the same and exits 0. It takes
  every flag `dispatch` takes; an explicit `--runtime` or `--model` shows as
  `rule=override` (with `underlying_rule`: what the policy alone would have done).
- A `--class` of `subagent`, `opus`, `sonnet`, `haiku` or `fable` also prints
  `env: NAME=model` for the `gateway_classes` alias of that class, or says the
  operator's environment already sets it.
- A runtime skipped on its cooldown is listed as `skipped: <runtime> (cooling)`
  (the seconds left are left out so the output stays stable). The cooldown is in
  memory, so a one-off CLI process sees it only for the process it runs in.
- Output is deterministic (fixed order, no timestamps, no paths). `--json` has a
  fixed key set and never emits `null` for a list. Exit codes: 0 ok, 1 no route
  could be decided, 2 usage error (an unknown agent or class included).
- The router has no bash twin (K-143): `yakos router` always runs the Go
  implementation, and the bash CLI answers that it requires `YAKOS_IMPL=go`.
- The goldens are in `cli-go/cmd/yakos/testdata/router-explain/`; the
  `route-explain-golden` CI job runs them under `YAKOS_IMPL=go`.

### Not routed yet

- A console interactive pane (the Interactive toggle) is routed once, at its
  first turn, by `dispatch.Explain` (the same step `yakos router explain` runs);
  the engine it starts then keeps that runtime for the whole conversation, so a
  rule cannot move a live pane. Follow-up turns ride `/api/chat/send` and carry
  no route (K-148).
- The runtime pre-checks (`PreferredRuntime`, `ResolveRuntime`) see the route
  class only when the caller passes the task (`RouteQuery.Task`, and `Extra` for
  upstream outputs); the console chat handler and `yakos dispatch` do. A caller
  that omits it can still get a different runtime than `Run` picks for a
  sensitive request. They see no task size (`task_bytes_gt` rules).
- The bash dispatch path has no router at all. Since K-143 `yakos dispatch`
  runs the Go path unless `YAKOS_IMPL=bash`, so the router applies by default;
  under `YAKOS_IMPL=bash` there is no router and no `--explain`.

## Sensitive class (K-140)

A routing class that keeps a request holding a secret off a vendor the operator
did not choose to trust with it. It is **not an egress guarantee**: a harness
can still read any file it is told to, and a secret the scan cannot recognise
passes. Pair it with the sandbox's read denial (`allow_unsandboxed_runtimes`
stays off), which is what stops a file leaving the machine.

A dispatch is classified `sensitive` when any of this text contains a
secret-shaped string or names a never-path:

- the task, the agent's prompt, a knowledge block (when present), a Flows
  node's upstream outputs and transcript digests. The agent's own prompt is
  scanned for secret patterns only, not for credential-file names: framework
  prompts say "never edit `.env*`" and that is policy prose, not a request to
  read the file. Zero-width and soft-hyphen characters are stripped before the
  scan. The last three are passed by
  the caller as `Params.ScanExtra` / `Request.ScanExtra`
  (`dispatch.ClassifyFlowOutput` tells an engine whether an output is sensitive
  before it dispatches the node).
- Secret-shaped: the secret-scan hook's patterns (AWS, GitHub, Slack, Stripe,
  Anthropic and Google keys, PEM private keys). Never-paths: the egress layer's
  built-in list (`.env*`, `*.pem`, `*.key`, `secrets/**`, `credentials/**`,
  `id_rsa*`, `.aws/credentials`, `.netrc`, ...) matched case-insensitively
  against every path-like word in the text, plus `id_ecdsa*`, `id_dsa*`,
  `.kube/config`, `.pypirc`, `*.tfvars` and gcloud application-default
  credentials. Words are split on whitespace and punctuation of any script, and
  `@`, `*`, `_` and similar decoration is trimmed (`@.env`, `**.env**`); a word
  over 1024 bytes is matched by its first and last 1024 bytes. Base64, hex and
  split-up keys are out of scope for a routing class.

A project adds its own paths in `.yakos.yml`:
`router: {never_paths: ["internal/billing/*"]}`. It can only add: the built-in
patterns and paths are merged in on every scan, and no project key removes one.
At most 32 entries are read, and a glob with more than 8 wildcard characters or
more than two `**`, a bracket expression over 16 characters, or a length over 128 bytes is dropped with a warning (a scan-cost bound). One request
is scanned once: the result is remembered by content hash for 30 seconds, so a
chat pre-check and the dispatch after it share it.
Looser, higher-false-positive patterns (entropy, generic `password=`) are not
shipped; they would be opt-in.

For a sensitive request the candidate chain is restricted to `claude` (the
primary) and any runtime whose every catalog model is `billing=local` (none ships
today). The restriction covers an explicit `--runtime`, an agent pin, a policy
rule and every fallback list. If nothing is left the chain falls closed to
`claude`. If `claude` is unavailable (not signed in, disabled by the project)
the dispatch is **refused** with a `RouteRefusedError`, nothing runs, and one
`route_refused` event is written to the ledger (class and a fixed reason, never
request text). The class reaches the ledger as `route_class: sensitive`, and
`route_reason` ends `sensitive -> primary only (<reason>)`.

Reasons (a fixed vocabulary; the matched text and path are never logged):
`secret-pattern`, `never-path`, `scan-timeout`, `scan-oversize`,
`classifier-error`, `declared`. A scan that times out (2 s), finds more than
16 MiB of text, or panics classifies the request sensitive: it fails closed.
A caller-supplied class (`--class default`) never hides a sensitive request when
there is text to scan; `--class sensitive` on `yakos router explain` shows the
restriction without any text.

## Console chat routing (K-148)

The console chat panes go through the router. Everything below is SSE,
transcript and user-turn text; none of it enters a system prompt,
`--append-system-prompt` or the `--agents` JSON (`rule:cache-stability`).

- **Pane mode.** The pane's two selects are its routing mode, labelled in the
  header: `auto` (runtime `auto`: the router decides), `runtime` (a runtime, no
  model) and `pinned` (both). The runtime and model selects are filled from
  `GET /api/models` (RoleRead), the model registry with the operator overlay and
  the workspace's disables applied; ids outside the registry's id rule
  (`^[a-z0-9][a-z0-9._:-]{0,63}$`) are never offered. The last answer is cached in
  `localStorage` so a reload keeps a registry-only model; without it the static
  lists apply. The response has no path: registry warnings are a count.
- **`@prefix`.** A message that starts with `@claude`, `@codex` or `@agy`,
  optionally `:model` (`@codex:gpt-5 fix it`), is an override for that turn. The
  browser strips the prefix and sends `overrideRuntime` / `overrideModel`; the
  server validates both again (known runtime, id rule, model checked against that
  runtime) and applies them over the pane's selects. The pane's model is dropped
  unless the override names the same runtime. A live interactive pane refuses an
  override: its engine is one runtime.
- **Route event.** The first event of a one-shot turn is `route` (runtime,
  provider, model, rule id, reason, class, `fallback_from`, and `pinned`:
  `override`, `pane` or `router`). `dispatch.Params.EmitRoute` turns it on, so a
  transport that forwards every chunk (gRPC) sees no new frame. It is persisted as
  a transcript turn with role `route` (`runtime`, `model`, `text` = reason,
  `rule_id`, `fallback_from`, `pinned`); the schema only grew, and a reader that
  does not know the role skips the line. The console shows it as the "why this
  model" chip.
- **Native sessions per runtime.** The conversation's meta store keeps one
  session id per runtime. A one-shot turn hands over all of them and `RunStream`
  resumes the one it routes to, so claude, codex, claude again finds the first
  claude session. The id of the runtime that answered is stored from its summary.
- **Handoff.** When the operator moves a conversation to another runtime (the
  request names a runtime that differs from the last `route` turn's, and the new
  runtime has no session of its own in the conversation), a digest of the earlier
  turns is appended to that turn's task, after the operator's words: the 12 latest
  user and assistant turns, each cut to 1500 bytes, 6 KiB in all, newest kept,
  oldest first, labelled as context and not as an instruction. It is scanned
  first: private-key blocks, cloud and token prefixes, JWTs, bearer values and
  `key=value` secrets become `[redacted]`. The scan is a safety net for text
  headed to another vendor's model, not a guarantee. The transcript keeps the
  operator's own words. A `handoff` event follows the `route` event, and the
  console shows the "context reset (cache)" banner: the new runtime starts without
  the earlier prompt cache. The router's own moves (sticky, fallback, auto) are
  not handoffs.
- **Sensitive requests and the override.** The interactive first turn is
  classified like a one-shot turn (the task is scanned; a refusal ends the turn
  with the same `dispatch failed: ... route refused` error frame and a
  `route_refused` ledger event). When the class (or an unavailable runtime) puts
  a turn somewhere other than the `@runtime` the operator asked for, the route
  chip names the refused override (`override_refused`, persisted on the route
  turn) and `pinned` reads `router`. The handoff digest is scanned over the whole
  turn before it is cut, with the secret-scan hook's patterns plus URL
  credentials and credential-named `KEY=value` pairs.
- **K-148b, left out on purpose.** Tool and thinking cards (K-144 events) are not
  persisted in the transcript; a live pane's runtime cannot be switched (a
  dispatch naming another runtime, codex to agy included, is refused with 409, so
  the chip never names a runtime the engine is not on; start a new conversation);
  the handoff banner is shown live but not persisted; and a follow-up sent to a
  live interactive pane (`/api/chat/send`) emits no route event: the pane routes
  once, at its first turn.

## Policy writers: `yakos models` and `yakos router policy` (K-153)

Policy is written from a terminal:

- `yakos models enable|disable <id>`, `alias`, `pricing` edit
  `~/.yakos-state/model-registry.yml`. `pin` adds a rule to `router-policy.yml`
  that sends one agent to one runtime and model ahead of the others
  (`override_pins: true`, placed first; `--clear` removes it). `yakos router policy
  set --rules-file <file|->` replaces the `rules:` list, `get [--json]` shows it.
  A price is refused for a model that is not billed `api` (the registry would
  ignore it); give `--billing api` with it.
- Every writer is `statepath.EditYAML`: the file is read with the same trust check
  the readers use (a symlink, another user's file or a group- or world-writable
  file or directory is refused, never overwritten), keys the edit did not touch and
  their comments are kept, the result is checked as the reader will read it (a
  router rule the router would drop, an overlay entry it would ignore, is refused),
  and the file is replaced with a 0600 temporary file and a rename, under a lock
  file so two writers cannot lose each other's change.
- The privileged router keys (`allow_unsandboxed_runtimes`, `hooks_endpoint`,
  `openai_endpoint`) are shown and never set by these commands. Two checks hold
  that: `router policy set` refuses YAML anchors, aliases and `<<` merge keys in the
  rules input, and the writer re-parses the composed file and refuses the write
  unless every top-level key other than `rules:` has the same value (aliases
  resolved) as before.
- Every write appends one `config_changed` line to the dispatch log through
  `dispatch.Account`: the operator (the OS user for the CLI), the file's base name,
  a fixed action word, and the file's sha before and after. The line always goes
  to the log in the home state directory (`~/.yakos-state`), whatever
  `YAKOS_DISPATCH_LOG` says, because a project can set that variable. The log is
  opened and locked before the file is written; if it cannot be opened the command
  exits 1 and writes nothing. The audit records the OS user, not an authenticated
  identity.

## Claude Code request-class aliases (K-141)

The user-level router policy (`~/.yakos-state/router-policy.yml`, the same
owner-only file `allow_unsandboxed_runtimes` lives in) has a `gateway_classes`
key that maps Claude Code request classes to Claude model ids:

```yaml
gateway_classes:
  subagent: haiku
  haiku: claude-haiku-4-5-20251001
```

yakOS realises it only by setting documented Claude Code environment variables
on the `claude` child process; it does not proxy anything.

### Verified env knobs

Checked against Claude Code **2.1.293** (`claude --version`; the installed
binary reads every name below, and `claude --help` lists no model-class flag
besides `--model` and `--fallback-model`). The names were confirmed in the
installed binary. The meaning of each knob is the one in Anthropic's published
model-configuration page; this environment had no network access to re-fetch
it, so re-check the table when the Claude Code version moves.

| Class     | Variable                         | Value accepted by yakOS                    |
|-----------|----------------------------------|--------------------------------------------|
| `subagent`| `CLAUDE_CODE_SUBAGENT_MODEL`     | tier name (haiku, sonnet, opus, fable) or a Claude id |
| `opus`    | `ANTHROPIC_DEFAULT_OPUS_MODEL`   | a Claude id                                |
| `sonnet`  | `ANTHROPIC_DEFAULT_SONNET_MODEL` | a Claude id                                |
| `haiku`   | `ANTHROPIC_DEFAULT_HAIKU_MODEL`  | a Claude id                                |
| `fable`   | `ANTHROPIC_DEFAULT_FABLE_MODEL`  | a Claude id (the CLI's own fallback message tells users to set it) |

The `opus`, `sonnet`, `haiku` and `fable` classes redefine what that tier alias
resolves to, so a bare tier name is refused there (an alias pointed at itself).
Whatever else Claude Code sends through a tier alias (for example its small
background model, which follows the haiku alias) is the harness's decision;
yakOS records what it set, not what Claude chose.

Present in the binary but **not mapped**, because they are not documented:
`ANTHROPIC_SMALL_FAST_MODEL` (the old name of the haiku knob),
`CLAUDE_CODE_SUBAGENT_MODEL_FORCE`, `CLAUDE_CODE_AUTO_MODE_MODEL`,
`CLAUDE_CODE_BG_CLASSIFIER_MODEL`. Compaction, workflow and main-model-by-class
have no documented knob and wait for the gateway hint headers (K-151).

### Rules

- Only Claude model ids (`^[a-z0-9][a-z0-9._:-]{0,63}$`, starting `claude-`, or
  the Bedrock and Vertex `anthropic.claude-` and `us.anthropic.claude-`
  spellings). An unknown class, a duplicate, a non-Claude id or a wrong shape
  ignores the whole key, with one path-free note on stderr. Claude Code is never
  routed to a non-Claude model.
- Only the user-level file: a project `.yakos.yml` cannot set classes.
- An operator's own value of the variable, already in the environment, always
  wins. `yakos doctor --policy` says which classes that affects.
- Applied once per process and never varied per turn: the table is read once
  and the same values go into the framed dispatch, the chat dispatch, the
  interactive session and `yakos start`, through one helper
  (`runtime.ApplyGatewayAliases`). A different model is a different prompt
  cache. Edit the policy, then restart the daemon.
- Not applied to codex or agy, and not to the Agent-SDK sidecar.
- `yakos start` applies them on the Go implementation (`YAKOS_IMPL=go`); the
  bash `cli/lib/start.sh` path does not know the policy and sets nothing.
- The one-shot dispatch ledger row of a claude run that had aliases set carries
  `; env-alias` appended to the router's `route_reason`, and `policy_sha` (the
  sha256 of the trusted policy file, as for any routed row).

### Commands

- `yakos start --print-env` prints the aliases the claude runtime would get and
  exits (names and model ids only). `yakos router explain <agent> --class
  <subagent|opus|sonnet|haiku|fable>` prints the aliases of one class together
  with the routing decision (see "Explaining a route").
- `yakos doctor --policy` lists the active classes and the ones the operator's
  environment overrides.
