# Implementation Decisions

ADRs record architectural decisions made before the build. This document records refinements made
during implementation when the concrete schema, queries, or runtime behavior provide new evidence.

## Derive health and occupancy from event history

**When**: Stage 3, while reviewing the occupancy schema and query.

**Related pre-build ADRs**: [0002](./adr/0002-receiver-processor-separation.md),
[0004](./adr/0004-event-processing-buffer.md), [0005](./adr/0005-folder-architecture.md),
[0006](./adr/0006-restart-correctness.md), and [0008](./adr/0008-backpressure.md).

**Original approach**: River workers maintained current health and occupancy rows. Rolling health
and occupancy values were still calculated from indexed raw events.

**Finding**: The projections cached only the latest event and did not remove the historical query.
They added jobs, projection writes, a worker process, and lag between current and rolling results.
Heartbeat and presence events also make up most generated traffic, so the extra writes affected the
busiest path.

**Change**: Calculate latest state and rolling results directly from indexed event history. The
event log already provides restart recovery, immediate late-event correction, and query-time
exclusion of future events. Fall alarms remain a separate durable entity because they require
deduplication, history, and prompt delivery.

**If reads become expensive**: Profile first, then add the narrowest useful rollup. Heartbeat
counts can use per-device time buckets; occupancy can use per-room intervals or occupied-duration
buckets. Late events would rebuild only the affected bucket or adjacent interval. Background
processing should return only if measured rollup work cannot remain on the request path.
