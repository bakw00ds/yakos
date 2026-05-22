# Co-Pilot Mode — multi-developer collaboration on a shared dev box

**Status:** v0.27 (Plan 1 M1 — awareness only). M2 (per-file claims)
and M3 (mode negotiation) ship in v0.28 / v0.29.

## What this is for

Two (or more) human developers using yakOS in parallel on the same
project, on the same physical / virtual machine, via SSH. Each
developer's session is independent; coord adds awareness so neither
agent steps on the other's work, and so peers can read what the other
is doing without paging the human.

Three problems it addresses (in order of milestone):

1. **Wasted work** (M1) — two agents do the same thing without knowing
2. **Edit conflicts** (M2) — two agents change the same file
3. **Memory drift** (M1) — shared memory store via symlink

## What this is NOT for

- **Cross-machine coordination.** Strictly single-box. Distributed
  multi-box is a much larger feature; out of scope.
- **Replacing git.** Edit conflicts produce blocks; humans resolve.
  yakOS doesn't try to be smarter than git.
- **Auto-merging.** Same reason.

## Topology

```
┌─────────────────────────────────────────────────────────────────────┐
│  Dev box (one machine; two SSH'd users: alice, bob)                 │
│                                                                      │
│  /var/lib/yakos/<project>/                       ← per-box shared    │
│    coord/                                          (group: yakos-coord)
│      activity.ndjson         ← all sessions append; all sessions read
│      sessions/<user>@<host>-<pid>.ndjson  ← per-session ledger       │
│      active-claims.json      ← M2 projection                         │
│    memory/                   ← shared canonical memory               │
│                                                                      │
│  /home/alice/agent-control/<project>/work/current/   ← private       │
│  /home/bob/agent-control/<project>/work/current/     ← private       │
│                                                                      │
│  /srv/code/<project>/                ← shared repo checkout          │
│                                       (group yakos-coord, g+w)       │
│                                                                      │
│  /home/{alice,bob}/.yakos-state/memory/<project>/ ──→ symlink to     │
│      /var/lib/yakos/<project>/memory/                                │
└─────────────────────────────────────────────────────────────────────┘
```

## One-time admin setup (Ubuntu / Debian)

Run **once as root** on the shared dev box. Adapt for other distros.

```bash
# 1. Create the coordination group + add both devs to it
sudo groupadd -f yakos-coord
sudo usermod -a -G yakos-coord alice
sudo usermod -a -G yakos-coord bob

# 2. Each user must log out + log back in (or `newgrp yakos-coord`)
#    for the group membership to take effect in their shell.

# 3. Create the shared coord root with setgid sticky-group bit
sudo mkdir -p /var/lib/yakos
sudo chgrp yakos-coord /var/lib/yakos
sudo chmod 2775 /var/lib/yakos     # setgid: new files inherit group

# 4. Confirm umask permits group writes (each user, in shell rc):
#    umask 0002
```

## Per-project provisioning

After the admin setup above, **either developer** runs (without sudo):

```bash
yakos init --multi-dev <name> --project <path-to-shared-checkout>
```

This:

- Creates `/var/lib/yakos/<name>/{coord,memory}/` inheriting group
  `yakos-coord` via setgid
- Drops `coord/README.md` documenting the directory's contents and format
- Symlinks `~/.yakos-state/memory/<name>/` → `/var/lib/yakos/<name>/memory/`
- Appends a `multi-dev: true` marker to `~/agent-control/<name>/.yakos.yml`

The **other developer** runs the same `yakos init --multi-dev` to get
their per-user memory symlink wired up. `init` is idempotent and
detects the existing coord state.

## Project checkout

Both developers check out the same repo into **the same path on the
dev box** (e.g., `/srv/code/<project>/`), group-writable by
`yakos-coord`. This is load-bearing: the per-file claims (M2) rely on
`<project>/.git` being one repo on one filesystem, edited from one
checkout.

Per-user git author identity (each dev's `git config`) is preserved —
commits are correctly attributed even though the working tree is shared.

## Editor integration over SSH

- VS Code Remote-SSH (most popular)
- JetBrains Gateway
- Neovim / Vim (native; cleanest for tmux+ssh)
- mosh (smoother over flaky connections than SSH)

Each editor connects to a per-user shell; the editor sessions are
independent of yakOS sessions.

## What v0.27 (M1) ships

- **Mirroring** of TeamCreate / Agent / TeamDelete / SendMessage events
  to `coord/activity.ndjson` + per-session ledger when coord dir exists
- **`yakos peer status`** — list active peer sessions for a project
- **`yakos peer log`** — tail the shared activity stream
- **`yakos init --multi-dev`** — provisions the coord dir + memory
  symlink for one user
- **`yakos doctor`** — coord-mode health checks
- **Shared memory** — symlinked from each user's `~/.yakos-state/memory/`
- **Lead persona** — bullet under `## Execution` reminding the lead to
  `yakos peer status` before dispatching

What v0.27 does NOT yet ship:

- Per-file claim hooks (M2 — v0.28)
- Mode negotiation protocol (M3 — v0.29)

## Failure modes

- **Coord dir missing or wrong perms** → `yakos doctor` warns;
  `yakos peer status` says "coordination not configured"; hooks no-op
  via `yakos_coord_enabled`; the two harnesses work independently as
  vanilla yakOS.
- **Group misconfigured** → `yakos init --multi-dev` detects and prints
  the admin recipe above; refuses to create the symlink until perms
  are correct; existing `~/.yakos-state/memory/<project>/` is preserved
  untouched.

## See also

- `lib/rules/multi-dev-coord.md` (v0.29 / M3) — soft rule loaded via
  CLAUDE.md import
- `lib/skills/peer-sync/SKILL.md` (v0.29 / M3) — session-start banner
  that summarizes peer activity
- `/var/lib/yakos/<project>/coord/README.md` — the on-disk format spec
