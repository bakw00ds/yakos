# Domain 5: Performance + Load Testing

**Goal:** Verify the system meets performance targets under realistic
and peak load, and identify bottlenecks before production users find
them.

This playbook is invoked by project-specific performance-engineering
agents (v0.2: `performance-engineer`) and by release-audit skills
that include perf domain.

---

## Scope

- API latency under realistic concurrent load
- Database query performance (slow queries, n+1, missing indexes)
- Cache hit rates and latency (Redis, Memcached, CDN)
- Web initial load and runtime performance
- Mobile startup time and jank
- Background job / async task throughput (if any)

## Define targets first

Before testing, record what "good" looks like. If there are no
existing SLOs, propose baseline targets and record as a finding
("SLOs not defined — proposed baseline follows").

**Generic baseline (adjust per product requirements):**

| Metric | Target | Criticality if miss |
|---|---|---|
| API p50 latency (auth) | < 100ms | P1 |
| API p95 latency (auth) | < 300ms | P1 |
| API p50 latency (data read, authenticated) | < 200ms | P2 |
| API p95 latency (data read) | < 500ms | P2 |
| API p99 latency (anything) | < 1500ms | P2 |
| Web Time to Interactive (TTI) | < 3s on broadband | P2 |
| Web Largest Contentful Paint (LCP) | < 2.5s | P2 |
| Web Cumulative Layout Shift (CLS) | < 0.1 | P3 |
| Mobile cold start | < 2s | P2 |
| Mobile frame rate during scroll | ≥ 55 fps average | P3 |
| DB slow query (>100ms) rate | < 1% of queries | P2 |
| Error rate under target load | < 0.1% | P1 |

## Automated pass

### 5.1 API load testing with k6

Write (or use existing) k6 scripts for each critical endpoint group:

```javascript
// raw/05-performance/k6-auth-load.js
import http from 'k6/http';
import { check, sleep } from 'k6';

export const options = {
    stages: [
        { duration: '1m', target: 20 },   // ramp up
        { duration: '3m', target: 20 },   // sustained
        { duration: '1m', target: 100 },  // spike
        { duration: '2m', target: 100 },  // sustained peak
        { duration: '1m', target: 0 },    // ramp down
    ],
    thresholds: {
        http_req_duration: ['p(95)<500', 'p(99)<1500'],
        http_req_failed: ['rate<0.01'],
    },
};
// ... request logic
```

Run:

```bash
k6 run --out json=raw/05-performance/k6-auth.json raw/05-performance/k6-auth-load.js
```

Generate scenarios for: login / session creation; primary read flows;
primary write flows; heavy aggregations.

**Load levels:** baseline (current usage), 3× baseline, 10× baseline.

### 5.2 Postgres profiling

Enable `pg_stat_statements` if not already:

```sql
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
```

After load tests, dump top offenders:

```sql
SELECT query, calls, total_exec_time, mean_exec_time, rows
FROM pg_stat_statements
ORDER BY total_exec_time DESC
LIMIT 50;
```

Save to `raw/05-performance/pg-top-queries.sql`.

Analyze for:

- Queries with mean > 100ms → P2
- High call counts that look cacheable → P2
- Sequential scans on large tables (`EXPLAIN ANALYZE`) → P1/P2
- N+1 patterns (same query shape called 10+ times per request) → P2

Parse slow query log with pgBadger:

```bash
pgbadger -f json /var/log/postgresql/postgresql-*.log -o raw/05-performance/pgbadger.json
```

### 5.3 Cache health (Redis example)

```bash
redis-cli -u <url> INFO > raw/05-performance/redis-info.txt
redis-cli -u <url> --bigkeys > raw/05-performance/redis-bigkeys.txt
redis-cli -u <url> --latency-history -i 10 > raw/05-performance/redis-latency.txt &
# let run during load test, then kill
```

Check:

- Cache hit rate (`keyspace_hits / (keyspace_hits + keyspace_misses)`)
  — below 80% on hot paths = P2
- Memory usage trend during load
- Any keys > 1MB (big keys can stall) = P2
- Eviction rate (if `maxmemory-policy` causes churn) = P2

### 5.4 Backend profiling under load

Capture during a load test. For Go (pprof):

```bash
curl -o raw/05-performance/cpu.pprof "http://<staging>/debug/pprof/profile?seconds=60"
curl -o raw/05-performance/heap.pprof "http://<staging>/debug/pprof/heap"
curl -o raw/05-performance/goroutine.pprof "http://<staging>/debug/pprof/goroutine"
```

Equivalents: Node `--prof` / `clinic`, Python `py-spy`, Java
`async-profiler`. Record top 20 hot functions and top 20 allocators.

### 5.5 Microbenchmarks

For critical algorithmic paths:

```bash
go test -bench=. -benchmem -benchtime=3s ./internal/... > raw/05-performance/benchmarks.txt
```

Compare against prior release benchmarks if available. Regressions
> 20% → P2.

### 5.6 Web performance — Lighthouse CI

Already run in playbook 03 — reuse those results. Pull out
perf-specific issues as Domain 5 findings when the root cause is
backend or infra (vs. a11y markup fixes which stay in Domain 3).

### 5.7 Mobile performance profiling

Run the app in profile / release mode (NOT debug):

```bash
# Flutter
flutter run --profile --trace-startup
```

Use the platform's timeline / instruments to capture:

- Frame build + raster times on critical screens
- Shader compilation jank on first run (iOS especially)
- Memory growth over a 5-minute active-use session

Record findings for any screen with > 16 ms frames (60 fps) during
normal use.

## Manual pass

### Manual §Architecture-level perf review

- [ ] Are there obvious N+1 patterns in the ORM/query layer?
- [ ] Is pagination used everywhere it should be? Any unbounded list
  endpoints?
- [ ] Are bulk operations batched (e.g., 100 inserts in one
  transaction, not 100)?
- [ ] Caching strategy: what's cached, what's TTL, what's the
  invalidation story?
- [ ] Reads/writes separated where it makes sense? Read replicas?
- [ ] Async work: long-running requests offloaded to background jobs?

### Manual §Index review

- [ ] For each table, indexes match actual query patterns
  (cross-reference `pg_stat_statements`)
- [ ] No over-indexing (each index has a write cost)
- [ ] Composite index column order matches common WHERE patterns
- [ ] No unused indexes:

```sql
SELECT schemaname, relname, indexrelname, idx_scan
FROM pg_stat_user_indexes
WHERE idx_scan = 0;
```

### Manual §Connection pooling

- [ ] DB pool size appropriate for expected concurrency
- [ ] Connection pool metrics exposed (in-use, idle, wait count)
- [ ] Cache pool sized appropriately

### Manual §Timeout hygiene

- [ ] Every outbound HTTP call has a timeout
- [ ] DB query timeouts configured
- [ ] Server request timeout / read timeout set
- [ ] Circuit breakers on flaky external services considered

### Manual §Memory behavior

- [ ] Any global maps that grow unbounded?
- [ ] Goroutine / thread / handle count stable under sustained load
- [ ] Frontend: image caches bounded, streams disposed

### Manual §Frontend specifics

- [ ] Lists virtualized rather than rendered all at once
- [ ] Large images sized / resized before decoding
- [ ] Bundles tree-shaken; non-critical routes deferred / lazy-loaded
- [ ] Shader / asset warmup for mobile release builds

## Findings synthesis

Group by:

1. Target misses (table: metric, target, observed, criticality)
2. Database hotspots
3. API hotspots
4. Frontend perf issues (web)
5. Mobile perf issues
6. Missing infra (no `pg_stat_statements`, no metrics, etc.) → P2

Include trace / profile artifacts in `raw/05-performance/` and
reference by file.

## Known gotchas (cross-project)

- HTTP clients with default no-timeout: audit for bare client
  declarations.
- Connection pool defaults are usually low; production needs
  explicit tuning.
- Web bundles default-large; verify tree-shaking and deferred imports
  for non-critical routes.
- `KEYS` in production code (Redis) is P0 — that's a security/perf
  hybrid. Grep for it.
