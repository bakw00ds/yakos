# internal/perfdash — yakOS Performance Dashboard

Read-only HTTP server exposing dispatch-log analytics as a single-page web UI
and a JSON API. Runs on a dedicated port alongside the JSON-RPC, WebSocket, and
REST API servers managed by `internal/serve`.

## Tokens and dollars (K-136)

Tokens are the primary unit; dollars are only for runs billed per API call.

- **Tokens** (`tokens`, `total_tokens`, the `tokens` series) are real usage
  counts: input + output + cache read + cache creation from the event's `usage`
  object (`cost.Event.Tokens`). The `est_input_tokens` / `est_output_tokens`
  fields are size estimates from byte counts and are **never** counted; an event
  with no `usage` object reports none.
- **Dollars** (`cost_usd`, `total_cost_usd`, the `cost` series) are API spend
  only (`cost.Event.SpendUSD`): the usage cost of an `api`-billed event, or of a
  legacy event that predates the `billing` field. A `subscription` or `local`
  event reports its tokens and **$0**. **Nothing is estimated**: there is no
  price table, and an event with no reported cost adds nothing. (Before K-136 the
  dashboard priced events without a cost from their `est_*` fields at a
  hard-coded Sonnet rate, $3/M input and $15/M output; that is gone.)
- **API-equivalent** (`api_equivalent_usd`) is what subscription runs would have
  cost at API rates, as the harness reported it. It is informational: shown
  muted beside spend, and never added to it.
- **`token_detail`** is the per-kind split **as logged**. It is not comparable
  across rows from different writers: a legacy bash-written codex row keeps its
  cached tokens inside `input`, while the Go dispatcher splits them out into
  `cache_read`. Only the total is comparable.

## Endpoint table

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/` | none | Embedded SPA HTML |
| `GET` | `/app.js` | none | Embedded SPA JavaScript |
| `GET` | `/styles.css` | none | Embedded SPA CSS |
| `GET` | `/api/perf/summary` | Bearer | Summary stats for the window |
| `GET` | `/api/perf/timeseries` | Bearer | Bucketed time-series |
| `GET` | `/api/perf/by_axis` | Bearer | Breakdown by dimension |
| `GET` | `/api/perf/recent` | Bearer | Last N dispatch entries |

### Query parameters

**`GET /api/perf/summary`**
- `window` — time window: `1h`, `6h`, `12h`, `24h` (default), `48h`, `7d`, `30d`

Response:
```json
{
  "total_dispatches": 142,
  "total_cost_usd": 1.2345,
  "avg_latency_ms": 45200,
  "p50_latency_ms": 38000,
  "p95_latency_ms": 120000,
  "top_agents": [{"key":"backend","dispatches":80,"cost_usd":0.82,"tokens":910000,
                  "token_detail":{"input":40000,"output":30000,"cache_read":800000,"cache_creation":40000}}],
  "top_runtimes": [{"key":"claude","dispatches":120,"cost_usd":1.10,"tokens":1200000,
                    "token_detail":{"input":50000,"output":40000,"cache_read":1000000,"cache_creation":110000}}],
  "total_tokens": 1500000,
  "token_detail": {"input":70000,"output":50000,"cache_read":1200000,"cache_creation":180000},
  "api_equivalent_usd": 3.4
}
```

`total_cost_usd` and every `cost_usd` are API spend only. `api_equivalent_usd`
is omitted when no event reported one. The `tokens`, `total_tokens`,
`token_detail` and `api_equivalent_usd` keys are additive: every pre-K-136 key
keeps its name and meaning.

**`GET /api/perf/timeseries`**
- `window` — time window (default `24h`)
- `bucket` — bucket size: `hour` (default), `day`, `6h`, `12h`
- `metric` — `dispatches` (default), `tokens`, `cost` (API spend), `latency`

Response: `[{"ts":"2026-06-03T14:00:00Z","value":12}]`

**`GET /api/perf/by_axis`**
- `axis` — `agent` (default), `runtime`, `project`, `day`
- `window` — time window (default `24h`)

Response:
```json
[{
  "key": "backend",
  "dispatches": 80,
  "cost_usd": 0.82,
  "avg_latency_ms": 45000,
  "p95_latency_ms": 118000,
  "tokens": 910000,
  "token_detail": {"input":40000,"output":30000,"cache_read":800000,"cache_creation":40000},
  "api_equivalent_usd": 0.4
}]
```

`api_equivalent_usd` is omitted when none of the row's events reported one.

**`GET /api/perf/recent`**
- `limit` — number of entries to return (default `50`)

Response: array of dispatch row objects with fields `ts`, `agent`, `runtime`,
`project`, `exit_code`, `duration_s`, `cost_usd`, `latency_ms`, plus the
additive `tokens`, `token_detail`, `billing` (`subscription`, `api` or `local`;
omitted on rows written before K-136) and `api_equivalent_usd` (omitted when
none).

## Auth model

- Single read-only bearer token per daemon instance.
- Token stored at `~/.yakos-state/perf-token` (mode 0600), separate from the
  WS token and REST read/write tokens (Phase 2 decision Q7).
- Token is delivered to the browser via the URL fragment
  `http://127.0.0.1:7895/#token=<hex>`. The fragment is never sent in HTTP
  requests or server logs. JavaScript reads it into `sessionStorage` on first
  load.
- `LoadOrCreatePerfToken(stateDir)` generates a 256-bit hex token on first use.
- `RotatePerfToken(stateDir)` generates a new token; `yakos serve --rotate-perf-token` exposes this.

## UI structure (screenshot description)

```
┌──────────────────────────────────────────────────────────────────────────┐
│ yakOS Performance Dashboard           [24h ▼] [Refresh]  Updated 14:32  │
├──────────────┬────────────┬────────────┬───────────────┬────────────────┤
│ Total Tokens │ Dispatches │ Cost (USD) │  Avg Latency  │  p95 Latency   │
│ 1,500,000    │   142      │ $1.23      │   45.2 s      │   120.0 s      │
│ in / out /   │            │ API-billed │               │                │
│ cache        │            │ runs only  │               │                │
├──────────────┴────────────┴────────────┴───────────────┴────────────────┤
│ Timeseries  [Tokens ▼] [Hourly ▼]                                        │
│  SVG line chart with area fill (inline SVG, no CDN dep)                  │
├──────────────────────────────────────────────────────────────────────────┤
│ Breakdown by [Agent▼]                                                    │
│ Key | Dispatches | Tokens | Cost | API equiv. | Avg Lat | p95 Lat        │
├───────────────────────────────┬──────────────────────────────────────────┤
│ Top Agents                    │ Top Runtimes                             │
│ key | dispatches | tokens | $ │ key | dispatches | tokens | $            │
├──────────────────────────────────────────────────────────────────────────┤
│ Recent Dispatches                                                        │
│ Time | Agent | Runtime | Project | Exit | Duration | Tokens | Billing | $│
└──────────────────────────────────────────────────────────────────────────┘
```

Tokens lead everywhere; the dollar parts exist only while something has API
spend:

- The **Cost (USD) card** is shown only when the window's `total_cost_usd` is
  above 0. The **Cost (USD) chart series** is offered only then too, and if it
  was selected it falls back to Tokens.
- A **Cost (USD) column** appears in a table only when some displayed row in
  that table has `cost_usd` above 0; header and cells are rendered together so
  they never disagree. A row with no spend (a subscription or local run) shows
  an em dash there, never `$0.00`.
- The muted **API equiv. (USD)** column (Breakdown only) and the muted
  "≈ $X at API rates (not spend)" note under the Tokens card appear only when
  some event reported an `api_equivalent_usd`.
- Tokens cells carry the per-kind split as a tooltip ("as logged").
- Every interpolated value goes through `td()` / `escapeHTML()`, which also
  escape quotes so values are safe in attributes.

- No CDN fetches at runtime. Chart.js is NOT used; the timeseries is an
  inline SVG with gradient fill, grid lines, and data dots.
- Auto-refreshes every 30 seconds. Manual refresh button.
- All controls (window, metric, bucket, axis) trigger immediate reload.
- `go test ./internal/perfdash` runs `testdata/render_check.js` under Node (when
  `node` is on PATH): the real `app.js` against a fake DOM built from
  `index.html`, checking the rules above and the escaping.

## Integration with internal/serve

`serve.Config` gains three new fields:

| Field | Default | Purpose |
|-------|---------|---------|
| `PerfAddr` | `127.0.0.1:7895` | Bind address for the dashboard |
| `PerfTokenPath` | (derived from RESTStateDir) | Override token path |
| `NoPerfDash` | `false` | Disable the dashboard |

The daemon logs the URL at startup:
```
yakos serve: perf dashboard: http://127.0.0.1:7895/#token=<perf-token>
```

CLI flags added to `yakos serve`:
- `--perf-addr <addr>` — override bind address
- `--no-perf` — disable the dashboard
- `--rotate-perf-token` — rotate and exit

## Constraints

- `net/http` stdlib only; no third-party server libraries.
- Loopback-only. Cross-machine requires mTLS (Phase 3 scope).
- Strictly read-only. No mutation endpoints exist.
- Static assets embedded via `//go:embed` (Decision D).
