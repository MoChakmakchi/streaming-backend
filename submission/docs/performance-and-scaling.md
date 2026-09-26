# Performance and Scaling

This document records what to measure as the system grows and which scaling changes those results
could justify. The options below are not implementation commitments.

## Current baseline

The API validates and persists accepted events in PostgreSQL. Health and occupancy are calculated
from indexed event history, while alarms are persisted as durable records.

## What to test

- Sustained and burst ingestion throughput through the HTTP API
- Durably accepted events and errors by HTTP status
- p95 and p99 ingestion latency
- Alarm delivery within one second during the required burst
- Health and occupancy query performance as event history grows

Start with the supplied generator. Add a dedicated HTTP load test only if it cannot produce the
required workload or measurements. Use a targeted database benchmark only when the end-to-end
results identify PostgreSQL as the likely constraint.

Record the hardware, container resources, dataset size, test duration, and workload for each result
so repeated runs are comparable.

## Test-based improvements

### Conditional PostgreSQL group commit

Load testing identified the per-event commit path as a likely throughput constraint. Before adding
application batching, PostgreSQL is configured with `commit_delay=200` microseconds and
`commit_siblings=5`. The delay applies only when at least five other transactions are active, so it
can group concurrent commits behind one WAL flush without adding delay at low load.

This keeps `fsync` and `synchronous_commit` enabled, so requests are still acknowledged only after
their transactions are durable.

A controlled 60-second baseline on a fresh database improved accepted throughput from 3,176 to
3,396 events per second. HTTP p95 fell from 78.51 ms to 40.82 ms, and dropped iterations fell from
6,597 to 3,149. Average ingest time also fell from 3.58 ms to 3.41 ms. The setting is retained as a
measured baseline improvement.

It did not make the required burst sustainable. The next candidate is bounded transaction batching
for normal events while fall warnings continue through their direct transaction path.

## Respond to measured bottlenecks

- If the API is saturated, consider API replicas behind a load balancer.
- If PostgreSQL writes are saturated, inspect connection pooling, transactions, queries, and indexes
  before increasing database capacity.
- If historical queries become expensive, add the narrowest useful health or occupancy rollup.
- If maintaining rollups cannot remain on the request path, introduce background processing.
- If sustained event volume exceeds a single PostgreSQL instance, evaluate partitioning, retention,
  and durable streaming infrastructure.

Any future asynchronous design must preserve commit-before-ack durability and avoid a non-atomic
write to PostgreSQL and a separate queue.

Kubernetes or additional messaging infrastructure should be introduced for a demonstrated
operational or scaling need, not as a performance optimization by itself.
