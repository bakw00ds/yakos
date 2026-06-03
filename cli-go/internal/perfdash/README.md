# internal/perfdash — yakOS Performance Dashboard

Read-only HTTP server exposing dispatch-log analytics as a single-page web UI
and a JSON API. Runs on a dedicated port alongside the JSON-RPC, WebSocket, and
REST API servers managed by `internal/serve`.

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
  "top_agents": [{"key":"backend","dispatches":80,"cost_usd":0.82}],
  "top_runtimes": [{"key":"claude","dispatches":120,"cost_usd":1.10}]
}
```

**`GET /api/perf/timeseries`**
- `window` — time window (default `24h`)
- `bucket` — bucket size: `hour` (default), `day`, `6h`, `12h`
- `metric` — `dispatches` (default), `cost`, `latency`

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
  "p95_latency_ms": 118000
}]
```

**`GET /api/perf/recent`**
- `limit` — number of entries to return (default `50`)

Response: array of dispatch row objects with fields `ts`, `agent`, `runtime`,
`project`, `exit_code`, `duration_s`, `cost_usd`, `latency_ms`.

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
┌──────────────────────────────────────────────────────────────────┐
│ yakOS Performance Dashboard   [24h ▼] [Refresh]  Updated 14:32  │
├────────────┬────────────┬───────────────┬───────────────────────┤
│ Dispatches │ Cost (USD) │  Avg Latency  │    p95 Latency        │
│   142      │   $1.23    │   45.2 s      │    120.0 s            │
├──────────────────────────────────────────────────────────────────┤
│ Timeseries  [Dispatches ▼] [Hourly ▼]                           │
│  SVG line chart with area fill (inline SVG, no CDN dep)          │
├───────────────────────┬──────────────────────────────────────────┤
│ Breakdown by [Agent▼] │                                          │
│ Key | Dispatch | Cost │ Avg Lat | p95 Lat                        │
├───────────────────────┼──────────────────────────────────────────┤
│ Top Agents            │ Top Runtimes                             │
│ key | dispatches | $  │ key | dispatches | $                     │
├──────────────────────────────────────────────────────────────────┤
│ Recent Dispatches                                                │
│ Time | Agent | Runtime | Project | Exit | Duration | Cost        │
└──────────────────────────────────────────────────────────────────┘
```

- No CDN fetches at runtime. Chart.js is NOT used; the timeseries is an
  inline SVG with gradient fill, grid lines, and data dots.
- Auto-refreshes every 30 seconds. Manual refresh button.
- All controls (window, metric, bucket, axis) trigger immediate reload.

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
