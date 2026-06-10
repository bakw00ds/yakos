# telemetry

Opt-in, anonymised command telemetry for yakOS Go CLI (Phase 1.5, ideas rank 10).

## Default: OFF

No telemetry of any kind is recorded or transmitted until the operator explicitly
runs `yakos telemetry enable`. This is non-negotiable (Decision B,
`docs/go-port-decisions-2026-06-02.md`).

## Opt-in flow

```
# Enable local-only recording:
yakos telemetry enable

# Enable with endpoint shipping:
yakos telemetry enable --endpoint https://your-ingest.example.com/v1/telemetry

# Check status:
yakos telemetry status

# See recent records:
yakos telemetry show --limit 20

# Change or add endpoint (also enables if currently off):
yakos telemetry set-endpoint https://your-ingest.example.com/v1/telemetry

# Disable:
yakos telemetry disable

# Delete local log (e.g. "I changed my mind"):
yakos telemetry purge
```

## Record schema

Each record is one NDJSON line in `~/.yakos-state/telemetry.ndjson`.

| Field | Type | Example | Notes |
|---|---|---|---|
| `ts` | RFC3339 string | `"2026-06-03T15:00:00Z"` | UTC timestamp of command completion |
| `yakos_version` | string | `"0.37.0.0 (go)"` | Full version string |
| `os` | string | `"darwin"` | `GOOS` value |
| `arch` | string | `"arm64"` | `GOARCH` value |
| `command` | string | `"validate"` | Top-level subcommand |
| `subcommand` | string | `"lint"` | Second-level subcommand, or `""` |
| `exit_code` | int | `0` | Process exit code |
| `duration_ms` | int | `142` | Wall-clock ms (best-effort) |
| `agent_count` | int | `0` | Agents dispatched (0 for non-dispatch) |
| `runtime` | string or null | `"claude"` | Runtime used; null for non-dispatch |
| `session_hash` | string | `"a3f7..."` | SHA-256 of `CLAUDE_SESSION_ID`; irreversible |

## What is NEVER recorded

- File paths or file contents
- Usernames, hostnames, environment variable values
- Project names or paths
- Tool inputs or outputs
- Prompt text or model responses
- Anything that could identify the operator beyond the session_hash

The `redact.go` helper enforces this defensively — any field that accidentally
contains a known PII pattern is replaced with `[REDACTED]` before writing.

## Local-only mode (default when enabled)

Records are written only to `~/.yakos-state/telemetry.ndjson`. No network calls.
Operators can ship to their own endpoint via a side-script if desired.

## Endpoint-shipped mode

When an endpoint URL is configured via `set-endpoint` or `enable --endpoint`,
records are additionally POSTed to that URL as JSON (best-effort, fail-silent).
Already-shipped records are tracked in `~/.yakos-state/telemetry-shipped.ndjson`
to avoid double-shipping on re-runs.

There is **no default endpoint** operated by yakOS. Operators supply their own.

## Privacy guarantees

- Default off: the config file not existing means disabled.
- No PII ever, even after opt-in.
- Network calls fail-silent. The CLI never blocks on telemetry shipping.
- Files are mode 0600 (owner-only read/write); on Windows the NTFS DACL
  restricts access to the current user via `winsec.SecureFile`.
- The session_hash is SHA-256 of `CLAUDE_SESSION_ID` (or a random per-process
  nonce when the env var is absent). SHA-256 is one-way — no username or
  identity can be derived from it.

## Files

| Path | Description |
|---|---|
| `~/.yakos-state/telemetry.yml` | Config (enabled?, endpoint) |
| `~/.yakos-state/telemetry.ndjson` | Local record log |
| `~/.yakos-state/telemetry-shipped.ndjson` | Records shipped to endpoint |

## Package layout

| File | Purpose |
|---|---|
| `event.go` | `Event` struct matching schema above |
| `config.go` | `Config` struct + `LoadConfig`/`SaveConfig` |
| `recorder.go` | `Record`, `CountRecords`, `ReadRecentRecords`, `PurgeLog` |
| `shipper.go` | `ShipPending` (best-effort POST; fail-silent) |
| `session.go` | `SessionHash` (memoised sha256 of session ID) |
| `redact.go` | `RedactEvent` defensive PII guard |
| `telemetry.go` | `Run`, `ParseArgs`, `PrintHelp` — CLI sub-subcommand logic |
| `secure_unix.go` | `SecureConfigFile` no-op on Unix/macOS |
| `secure_windows.go` | `SecureConfigFile` delegates to `winsec.SecureFile` |
