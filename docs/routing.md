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
  are reused; a directory of a dead pid goes; a name with no pid (an earlier build)
  goes only when it is more than an hour old. It touches real directories the
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
  from the id suffix), after the catalog entries and sorted by id.

### Commands

```
yakos models list  [--harness <name>] [--project <path>] [--json]
yakos models show  <id> [--harness <name>] [--project <path>] [--json]
yakos models probe [--harness <name>] [--timeout <duration>] [--json]
```

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
ceiling and requires the same answer. Codex models have no class until you map
aliases in the overlay. Making `budget.ClampModel` and dispatch use it is a later
change.

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
