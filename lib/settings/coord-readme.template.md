# yakOS multi-dev coord — project state

This directory holds shared coordination state for the project across
multiple yakOS sessions running on the same dev box. It is provisioned
by `yakos init --multi-dev`.

```
/var/lib/yakos/<project>/
├── coord/                      # this dir
│   ├── README.md               # this file
│   ├── activity.ndjson         # all sessions append; all sessions read
│   ├── sessions/
│   │   └── <user>@<host>-<pid>.ndjson    # per-session ledgers
│   └── active-claims.json      # per-file claims projection (M2+)
└── memory/                     # shared canonical memory (mirror of
                                  ~/.yakos-state/memory/<project>/)
```

## Permissions

Directory and files inherit group `yakos-coord` via setgid (mode 2775).
Each developer is a member of this group. `umask 0002` is required in
each user's shell for the group-write bit to land on appends.

If you see "permission denied" appending to `activity.ndjson`, check
your umask + group membership (`id`).

## Data formats

### `activity.ndjson` and `sessions/*.ndjson`

One JSON event per line:

```json
{
  "ts": "2026-05-22T17:15:00Z",
  "kind": "team_created | agent_spawn | team_deleted | send_message |
           claim_intent | claim_confirmed | claim_released | claim_renewed |
           mode_proposal | mode_response | note | session_launched | session_ended",
  "actor": {
    "user": "alice",
    "host": "dev01",
    "pid": 12345,
    "session_id": "abc-123",
    "agent": "go-api"
  },
  "detail": {
    "summary": "...",
    "ttl_seconds": 600,
    "expires_at": "...",
    "proposed_mode": "parallel | serialize | defer",
    "targets": ["..."],
    "response": "ack | reject"
  }
}
```

`activity.ndjson` is rotated at 5MB / 5 archives (matching the
`dispatch-log.ndjson` convention). Rotation during multi-writer access
is OK because of atomic rename.

### `active-claims.json` (M2+)

Atomic projection rebuilt by `peer-claim-confirm.sh`. Reader retries
once on JSON parse failure (race with rebuild).

## Commands

- `yakos peer status [<project>]` — show active peer sessions
- `yakos peer log [--since <iso>] [<project>]` — tail activity

## Cleanup

Stale per-session ledgers (older than 7 days with no activity) can be
removed by an operator. The shared activity log is the source of truth
for audit; per-session ledgers are convenience.

## Don't put here

Project source code, secrets, large binaries. This directory is for
coordination metadata only.
