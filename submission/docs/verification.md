# Verification and Performance

The checks are split by purpose rather than reproducing the private grader.

The service and its focused integration tests remain Go. JavaScript is limited to the k6
scenario, while the dependency-free Python probe matches the supplied generator and evaluator.
Rewriting either tool in Go would add code without improving the behavior being verified.

| Check | Purpose |
|---|---|
| Focused Go tests | Validation, idempotency, ordering, late events, occupancy, and fall deduplication |
| Supplied evaluator | API compatibility plus the provided offline, skew, jitter, and replay scenarios |
| k6 | Fixed-rate baseline and burst traffic, HTTP latency, failures, overload, and dropped iterations |
| Deterministic probe | Exact health and occupancy results, a new device, SSE order, alarm p95, and history recovery |
| Restart script | Hard API kill, persistent data, idempotent migration, query recovery, and retry safety |

`event_generator/generate.py` remains useful for device behavior but sends requests synchronously.
The k6 scenario therefore supplies pressure without reimplementing offline or jitter behavior. The
probe uses unique identifiers, so it can verify known results while unrelated k6 traffic is active.

## Commands

Run the full pressure scenario in one terminal:

```bash
cd submission
make test-load
```

The target runs k6 locally so the load generator does not consume the Docker Desktop resources
allocated to the API and PostgreSQL. This path traverses Docker Desktop's published-port forwarding,
which local comparison showed can limit measured throughput. Capacity investigation can instead run
k6 on the Compose network, but those results also include contention because the generator then
shares Docker's CPU and memory with the API and PostgreSQL. Neither setup replaces an independent
load generator for a production capacity claim. k6 remains test tooling rather than part of the
application stack.

Run the probe in another terminal. The default k6 scenario begins its first burst after one minute,
so this places the probe inside that burst:

```bash
cd submission
make test-probe PROBE_DELAY=65
```

The full k6 scenario is three minutes at 5,000 events/second with two additional
45,000-events/second streams lasting 30 seconds. They start after one and two minutes, producing the
configured 50,000-events/second pressure periods. All timings are configurable. k6 reports accepted,
duplicate, overloaded, unexpected, and dropped work separately.

Run restart and supplied-evaluator checks separately. The supplied evaluator reuses fixed device
identifiers and queries alarms from the beginning of history, so use a fresh database for each
independent scorecard run.

```bash
cd submission
make test-restart

make docker-destroy
make docker-init
make -C .. smoke SERVICE_URL=http://localhost:8090 DEVICES=50
```

## Pre-submission checklist

- [x] `make build` and `make test` — result: passed
- [x] `make test-integration` — result: passed on a fresh database
- [x] `make test-race` — result: passed
- [x] Supplied smoke and offline scenarios on fresh databases — result: smoke accepted 1,465
  events with 17/17 alarms; offline replay accepted 4,749 events with 57/57 alarms and no HTTP
  failures. Dedicated k6 tests supplied the stronger pressure check; the combined adversarial
  scenario was not repeated separately.
- [x] Full k6 run attempted the requested arrival rates — result: 890,008 accepted, no `503`
  responses, 2,724 request timeouts, and 1,129,478 dropped iterations; the test environment did not
  deliver the requested 50,000-events/second bursts
- [x] Standalone deterministic probe — result: passed; alarm delivery p95 60.128 ms
- [x] Alarm-only probe during a configured 50,000-events/second pressure run — result: passed twice;
  alarm delivery p95 was 17.923 ms early in the run and 150.442 ms later; the generator saturated
  below its configured rate
- [x] `make test-restart` preserved health, occupancy, and one logical alarm without duplicate events — result: passed; 3 events, 1 alarm
- [x] `/metrics` showed event outcomes, ingest/query/alarm latency, HTTP concurrency, and PostgreSQL pool pressure

The highest observed rate of durably acknowledged events was approximately 14,500 events per second
with k6 on a second machine, the API running natively, and PostgreSQL in Docker. This is not an
application capacity claim. A minimal HTTP server with no validation or persistence reached
approximately the same limit, so the application's upper ceiling could not be measured with the
available load-generation and network environment.

### Headline benchmark environment

| Item | Recorded value |
|---|---|
| Application host | MacBook Air, Apple M3 (8 cores), 16 GB RAM, macOS 26.1 arm64 |
| Docker allocation | 8 CPUs, 7.75 GiB RAM, 1 GiB swap; no per-container CPU or memory limits |
| Software | Go 1.27.1, PostgreSQL 18, Docker CLI 29.6.1 |
| Database | PostgreSQL in Docker, named volume on host-local storage; synchronous commit retained |
| Highest-rate topology | k6 on a second machine over the LAN → native Go API → PostgreSQL in Docker |
| Workload | 5,000 device IDs, 30 seconds, 15,000 requested events/second, roughly 440,000 accepted; approximately 96% heartbeat, 3.9% presence, and 0.1% fall warnings |
| Missing records | Load-generator hardware, Docker engine version, storage model, and starting database row count were not captured |

These details describe the approximately 14,500-events/second result. Other comparisons used
different topologies, listed below, and should not be compared as if they were one controlled run.

## Performance and scaling

This document records what to measure as the system grows and which scaling changes those results
could justify. The options below are not implementation commitments.

**Durability invariant:** an application event is counted as accepted only after its PostgreSQL
transaction commits. The minimal in-memory HTTP sink was only a transport control; no application
throughput result uses enqueue-time or non-durable acknowledgement.

### Current baseline

The API validates and persists accepted events in PostgreSQL. Health and occupancy are calculated
from indexed event history, while alarms are persisted as durable records.

### What to test

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

### Test-based improvements

These local comparisons guided the implementation; they are not production capacity claims.

#### Phase 1: isolate the bottleneck

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

#### Phase 2: instrument the batch pipeline

Batching initially appeared to expose an application throughput problem. Timings were added around
collection, preparation, connection acquisition, transaction start, insertion, and commit, alongside
PostgreSQL wait-event and WAL I/O sampling.

The first implementation used one `INSERT` per event inside each transaction. Replacing those with
one multi-row `INSERT` reduced normal insertion of roughly 21 events to about 0.4–0.5 ms and removed
the observed insertion spikes. The remaining failure occurred with both writers blocked in commit
for about 80 ms. PostgreSQL reported `WalInitWrite` and `WalInitSync` at the same time, with WAL
segment initialization taking roughly the same duration.

No controlled end-to-end before/after throughput run was captured for this change, so only the
measured insertion-time improvement is claimed.

This established that the remaining latency was below the application layer in PostgreSQL durability,
specifically WAL segment initialization. More writers cannot remove a shared WAL stall.

#### Phase 3: remove WAL initialization stalls

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

#### Phase 4: separate storage, transport, and generator limits

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

The highest observed rate of durably acknowledged events in this environment was approximately
14,500 events per second. This is an environment-specific observation, not an application capacity
claim. The application's upper ceiling remains unknown because the control HTTP server reached the
same limit. The required 50,000-events/second burst was attempted, but the generator could not
deliver that rate through this test environment.

During a separate configured 50,000-events/second pressure run, two external alarm-only probes
passed with delivery p95 values of 17.923 ms and 150.442 ms. The generator again saturated below its
requested rate, so this verifies alarm latency under the maximum pressure delivered by the local
environment rather than at a proven 50,000 accepted events per second.

#### Tuning on other hardware

| Setting | Retained value |
|---|---:|
| Normal batch writers | 4 |
| Maximum batch size | 400 events |
| Maximum batch wait | 4 ms |
| Pending batches | 15 (up to 6,000 events) |
| Normal admission capacity | 8,000 requests |
| Fall-warning concurrency | 4 |
| PostgreSQL pool maximum | 32 connections |
| Ingest deadline | 2 seconds |
| PostgreSQL `wal_init_zero` | `off` |

When normal admission is full, the API returns `503` with `Retry-After: 1`. A queued request whose
context has already expired is skipped before persistence. If it expires after its batch has begun,
the commit may still succeed while the client receives `503`; retrying is safe because
`(device_id, seq)` is unique.

On different hardware, tune one value at a time:

- Batch writers control database concurrency. Increase them only while PostgreSQL has spare capacity;
  too many writers add contention.
- Batch size and maximum wait trade commit efficiency against request latency. Larger or longer
  batches are not automatically faster.
- Pending-batch and ingestion capacity absorb short spikes but consume memory and allow more requests
  to wait; they do not increase sustained database throughput.

Fall-warning concurrency is deliberately separate and should not be increased to improve bulk-event
throughput, because its purpose is to protect alarm latency.

### Respond to measured bottlenecks

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
