# Submission, Real-time Streaming Backend

**Name:** Mo Chakmakchi
**Email:** mohchakm@gmail.com
**Link to your fork or solution:** https://github.com/MoChakmakchi/streaming-backend.git

---

## Stack and storage

I used Go, Chi, pgx, and PostgreSQL 18. Events arrive over HTTP and alarms use SSE. PostgreSQL
provides durable transactions, indexing, and restart recovery without another service.
Skipping a broker was intentional: I wanted to measure a single Go service and PostgreSQL with
commit-before-ack before adding infrastructure.

## Ordering and late events

`(device_id, seq)` provides idempotency. Health and occupancy use indexed history ordered by device
timestamp, so late events correct the next query. Accepted future events remain inactive until their
timestamp. Alarms use a server-generated `created_at` recovery cursor rather than device time.

## Backpressure

Normal events use bounded group commits, flushing at 400 events or 4 ms. Falls have reserved capacity
and create alarms in the ingest transaction. Fall jitter is deduplicated within three seconds of the
first warning: 0s, 2s, and 4s produce two alarms. `202` means committed, `200` means duplicate, and
`503` means not accepted and safe to retry.

## Restart correctness

Events and alarms live in PostgreSQL on a persistent volume. Reconnecting SSE clients provide their
last alarm creation time; the service subscribes before replaying history, avoiding a reconnect gap.

## How to run it locally

Requires Docker Compose, `make`, and Python 3.10+. Go and k6 are only needed for host-side tests and
load testing.

```bash
cd submission
make docker-init

cd ..
python3 eval/check.py smoke --target http://localhost:8090 --devices 50
```

Replace `smoke` with `offline`, `burst`, or `adversarial` for larger scenarios. The API is at
`http://localhost:8090`.

## Reported metrics

The isolated durable batch path reached 45,500 events/second with eight writers; four were retained
because more did not improve HTTP throughput. End-to-end testing reached about 14,500 events/second,
with alarm p95 at 150 ms under the heaviest delivered pressure. The laptop, LAN, and load generator
hit the same ceiling with a minimal HTTP server, so the application maximum and 50,000-events/second
result remain unverified. I would appreciate seeing the grader result.

## With another week

I would keep profiling and optimizing with dedicated load-generation hardware, and add production
metrics, tracing, and operational alerts. If PostgreSQL became the measured limit, I would consider
RabbitMQ as a durable queue, or Kafka if its replay and scale justified the extra machinery.

The [final design](submission/docs/design.md), [verification](submission/docs/verification.md),
[API contract](submission/docs/api/openapi.yml), and [decision history](submission/docs/pre-build/)
contain the details.
