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
