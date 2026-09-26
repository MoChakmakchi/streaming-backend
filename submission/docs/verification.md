# Verification

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

The full k6 scenario is three minutes at 5,000 events/second with two additional 45,000-events/second
streams lasting 30 seconds. k6 reports accepted, duplicate, overloaded, unexpected, and dropped
work separately.

Run restart and supplied-evaluator checks separately. The supplied evaluator reuses fixed device
identifiers and queries alarms from the beginning of history, so use a fresh database for each
independent scorecard run.

```bash
cd submission
make test-restart

make docker-destroy
make docker-init
make test-evaluator
```

## Pre-submission checklist

- [ ] `make build` and `make test` — result: pending
- [ ] `make test-integration` — result: pending
- [ ] `make test-race` — result: pending
- [ ] Supplied smoke, offline, burst, and adversarial scenarios on a fresh database — result: pending
- [x] Full k6 run attempted the requested arrival rates — result: 890,008 accepted, no `503`
  responses, 2,724 request timeouts, and 1,129,478 dropped iterations; the test environment did not
  deliver the requested 50,000-events/second bursts
- [x] Standalone deterministic probe — result: passed; alarm delivery p95 19.341 ms
- [ ] Probe passed during a burst; alarm delivery p95 recorded — result: pending
- [x] `make test-restart` preserved health, occupancy, and one logical alarm without duplicate events — result: passed; 3 events, 1 alarm
- [ ] `/metrics` showed event outcomes, latency summaries, concurrency, and PostgreSQL pool pressure — result: pending

The highest verified durable rate was approximately 14,500 events per second with k6 on a second
machine, the API running natively, and PostgreSQL in Docker. A minimal HTTP server with no validation
or persistence reached approximately the same limit, so the application's upper ceiling could not
be measured with the available load-generation and network environment.

Keep recorded results brief: pass/fail, the k6 achieved rate and error counts, alarm p95, and any
observed saturation point are sufficient.
