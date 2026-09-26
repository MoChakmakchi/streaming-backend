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

These local comparisons guided the implementation; they are not production capacity claims.

### Phase 1: isolate the bottleneck

| Experiment | Result | Decision |
|---|---|---|
| Remove explicit `BEGIN`/`COMMIT` around the single normal-event insert | Direct database throughput increased from roughly 3,800–5,000 to more than 9,000 events per second | Retained; PostgreSQL still commits the statement durably before acknowledgement |
| Set `commit_delay=200` microseconds with `commit_siblings=5` | Accepted throughput increased from 3,176 to 3,396 events per second and HTTP p95 fell from 78.51 ms to 40.82 ms | Removed after application batching made it redundant and could delay direct fall commits |
| Increase `commit_delay` to 500 microseconds | No meaningful throughput improvement and worse tail latency | Reverted; database-level delay was later removed |
| Release the admission slot before writing the HTTP response | No measurable improvement | Reverted |
| Increase normal ingestion concurrency from 24 to 28 | Accepted throughput fell and latency increased | Reverted in the single-row design; later replaced by bounded batch capacity |
| Wait 10 ms for an ingestion slot before returning 503 | Accepted throughput remained near 4,100 events per second while HTTP p95 rose to about 58 ms and peak in-flight requests rose to 816 | Reverted to immediate 503 |
| Run the API directly on the host instead of in Docker | Throughput did not improve | Inconclusive; later same-network testing isolated Docker Desktop port forwarding as a separate limit |
| Benchmark the HTTP stack with storage stubbed | Approximately 279,000 operations per second | Routing, validation, metrics, response encoding, and disabled request logging were ruled out as the primary bottleneck |

The process of elimination led to the first group-batching prototype for normal events. A collector
flushed when the batch reached 400 events or its oldest event had waited 4 ms, while one writer
committed the previous batch. The unbuffered handoff exposed writer saturation rather than hiding it
in a backlog. Requests remained pending until commit, while fall warnings continued through the
direct atomic path.

### Phase 2: instrument the batch pipeline

Batching initially appeared to expose an application throughput problem. Timings were added around
collection, preparation, connection acquisition, transaction start, insertion, and commit, alongside
PostgreSQL wait-event and WAL I/O sampling.

The first implementation used one `INSERT` per event inside each transaction. Replacing those with
one multi-row `INSERT` reduced normal insertion of roughly 21 events to about 0.4–0.5 ms and removed
the observed insertion spikes. The remaining failure occurred with both writers blocked in commit
for about 80 ms. PostgreSQL reported `WalInitWrite` and `WalInitSync` at the same time, with WAL
segment initialization taking roughly the same duration.

This established that the remaining latency was below the application layer in PostgreSQL durability,
specifically WAL segment initialization. More writers cannot remove a shared WAL stall.

### Phase 3: remove WAL initialization stalls

Increasing writer count improved the isolated batch path until PostgreSQL contention outweighed the
additional parallelism:

| Batch writers | Isolated throughput |
|---:|---:|
| 1 | 17,400 events/second |
| 2 | 30,200 events/second |
| 4 | 36,100 events/second |
| 8 | 45,500 events/second |
| 12 | 40,500 events/second |

Additional writers did not improve end-to-end HTTP throughput, so the API retains four. A clean
comparison using a remote load generator and native API measured 14,679 events/second with four
writers, 14,676 with six, and 14,643 with eight. Six writers produced a lower p95 in one run, but
without a throughput gain or a requirement-driven need for the extra database concurrency. Writer
count was not the cause of the shared WAL stalls found in Phase 2.

A later 6 ms batch-wait trial did not improve throughput, so the 4 ms wait was retained. The
database was not reset between those runs, so their exact rates are not used as a capacity
comparison.

Changing WAL retention sizes was not retained because it did not address the observed segment
initialization wait. PostgreSQL's `wal_init_zero=off` removed that measured stall in the Docker
environment while retaining synchronous durable commits. A fresh 5,000-events/second baseline then
completed at roughly 4,970 events/second with no overload responses and 32 ms HTTP p95. The setting
is retained in the local deployment configuration.

### Phase 4: separate storage, transport, and generator limits

Targeted probes produced different ceilings because they exercised different layers:

| Path | Measured result | Interpretation |
|---|---:|---|
| Direct single-row PostgreSQL insert | About 8,300 events/second | Per-event durable commit cost without HTTP |
| Production storage path without HTTP | About 5,700 events/second | Includes event mix, batching, and direct fall handling |
| Native k6 to the Docker-published API port | About 5,000 events/second | Limited substantially by Docker Desktop host-to-container forwarding |
| k6 and the durable API on the same Docker network | About 10,400–11,000 events/second | Removed port forwarding, but the generator then shared the Docker VM's CPU with the API and database |
| Remote k6 to the API running natively | About 14,500–14,700 events/second | Removed load-generator contention and Docker forwarding from the HTTP path; PostgreSQL remained in Docker |
| Remote k6 to a minimal in-memory `202` HTTP sink | About 14,300 events/second | Removing application logic and PostgreSQL did not raise the limit, isolating it to the test environment's load-generation and HTTP transport path |

At a requested 15,000 events/second, the same-network durable run accepted about 11,000 events per
second with no HTTP errors or `503` responses; k6 dropped the remainder before sending them.
Preallocating more virtual users reduced throughput to about 9,000 events per second and increased
p95 from 306 ms to 562 ms, showing generator and shared-resource contention rather than additional
server capacity.

With k6 on a second machine and the Go API running directly on the host, repeated runs at a requested
15,000 events/second produced about 14,500–14,700 accepted events per second with no reported
overload responses. Remaining loss appeared as k6 dropped iterations and transport failures. This
topology produced the highest measured result without removing PostgreSQL from Docker.

A final control replaced the application with a minimal Go HTTP server that only read the request
body and returned `202`. It reached about 14,300 events per second and showed the same request
timeouts and dropped iterations. Removing validation, batching, and database persistence therefore
did not improve the result. The remaining limit was in the available load-generator, LAN, or host
HTTP transport environment rather than the application-specific ingest path.

The highest verified durable application throughput in this environment is therefore approximately
14,500 events per second. This is a verified lower bound, not the application's upper ceiling. The
actual ceiling remains unknown because the control HTTP server reached the same environmental limit.
The required 50,000-events/second burst was attempted, but the generator could not deliver that rate
through this test environment.

### Tuning on other hardware

The retained defaults are four batch writers, batches of up to 400 events, and a 4 ms maximum wait.
If benchmarking on different hardware, tune one value at a time:

- Batch writers control database concurrency. Increase them only while PostgreSQL has spare capacity;
  too many writers add contention.
- Batch size and maximum wait trade commit efficiency against request latency. Larger or longer
  batches are not automatically faster.
- Pending-batch and ingestion capacity absorb short spikes but consume memory and allow more requests
  to wait; they do not increase sustained database throughput.

Fall-warning concurrency is deliberately separate and should not be increased to improve bulk-event
throughput, because its purpose is to protect alarm latency.

## Respond to measured bottlenecks

- If the API is saturated, consider API replicas behind a load balancer.
- If PostgreSQL writes are saturated, inspect connection pooling, transactions, queries, and indexes
  before increasing database capacity.
- If historical queries become expensive, add the narrowest useful health or occupancy rollup.
- If maintaining rollups cannot remain on the request path, introduce background processing.
- If sustained event volume exceeds a single PostgreSQL instance, evaluate partitioning, retention,
  and durable streaming infrastructure.
- If the load generator saturates first, move it to separate hardware before changing the service.

Any future asynchronous design must preserve commit-before-ack durability and avoid a non-atomic
write to PostgreSQL and a separate queue.

Kubernetes or additional messaging infrastructure should be introduced for a demonstrated
operational or scaling need, not as a performance optimization by itself.
