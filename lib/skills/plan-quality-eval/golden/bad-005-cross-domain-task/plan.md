# Plan: Add Product Search Feature

## Summary
Add a full-text product search endpoint and update the React search
component to use it. Estimated: 2 days.

## Assumptions
- PostgreSQL 15 with pg_trgm extension enabled.
- Go 1.22 backend; React 18 frontend with TypeScript.
- YAKOS_SEARCH_MAX_RESULTS env var caps search results at 50.
- YAKOS_SEARCH_MIN_SIMILARITY env var sets the trigram threshold.
- Frontend team uses React Query for data fetching.

## Tasks

### T-1: Add GET /v1/products/search?q= endpoint and update SearchBar component
agent_type: backend
estimate: 2d
done_means: `TestSearchEndpoint` passes for backend; SearchBar component
  renders results from the new endpoint.

### T-2: Add pg_trgm index on products.name and products.description
agent_type: db-migrations
estimate: 0.5d
blockedBy: [T-1]
blockedBy_reason: Index must match the query pattern used in T-1.
done_means: `CREATE INDEX CONCURRENTLY` executes without error; `\d products`
  shows both indexes.

## Risks
- No irreversible steps. Index creation is concurrent; rollback by dropping
  the index. Rollback: `DROP INDEX CONCURRENTLY IF EXISTS products_name_trgm_idx`.
