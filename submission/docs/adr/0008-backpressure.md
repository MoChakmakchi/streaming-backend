# ADR 0008: Backpressure

**Status:** Accepted  
**Date:** 2026-09-22

## Decision

PostgreSQL and River will provide the durable buffer during bursts. The service will not use an in-memory write-behind queue.

The receiver performs lightweight validation and then:

- stores every accepted event in PostgreSQL before acknowledging it;
- enqueues River jobs only for heartbeat and presence events;
- creates and deduplicates fall alarms in the ingest transaction; and
- stores motion, sleep-state, and network-status events without creating processing jobs.

Removing fall alarms from River keeps worker backlog out of the one-second alarm-latency path.

## Flow control

Request concurrency, PostgreSQL connection pools, and River worker counts are bounded. When PostgreSQL is busy, requests wait for capacity rather than accumulating in an unbounded memory queue.

Background worker concurrency is limited so workers cannot consume all database capacity needed by ingestion and queries. Worker containers can be scaled when the normal queue falls behind.

If the service cannot accept an event before the request deadline, it returns `503 Service Unavailable` with `Retry-After`. This is an explicit rejection, not an accepted event. End-to-end delivery then depends on the sender retrying it safely.

Timestamp acceptance is checked when the event is received, not when a delayed River job is processed.

## Alarm delivery

The fall ingest transaction stores the raw event, stores the logical alarm when it is not a duplicate, and issues a PostgreSQL notification. The notification is delivered only after commit.

Notifications are transient wake-up signals. The SSE feed reads persisted alarms and periodically checks for missed notifications. Clients catch up through `GET /alarms?since=<ts>`.

## Risks

- **PostgreSQL saturation:** every accepted event must commit before acknowledgment, so database capacity limits ingestion throughput.
- **Stale projections:** events remain safe in the event log, but health and occupancy can lag while the River queue drains.
- **Retry storms:** sender, database, and job retries can create another spike after recovery. Retries must use bounded concurrency and backoff with jitter.
- **Missed live notifications:** an API listener can disconnect or restart after an alarm commits. History-based catch-up must deliver the persisted alarm afterward.

## Observability

Monitor accepted and rejected event counts, ingest latency, PostgreSQL pool saturation, River queue depth, oldest-job age, projection lag, retry counts, and alarm delivery latency.

## Required tests

- Sustain the stated 50,000-events-per-second burst for 30 seconds and verify that acknowledged events are retained.
- Verify alarm delivery remains within one second p95 while the normal River queue has a backlog.
- Verify health and occupancy catch up correctly after a burst.
- Exhaust bounded capacity and verify the service returns `503` rather than acknowledging or silently dropping an event.
- Restart during a backlog and verify retries do not overwhelm PostgreSQL or duplicate effects.
- Disconnect the notification listener and verify `GET /alarms?since=<ts>` catch-up delivers the missed alarm.
