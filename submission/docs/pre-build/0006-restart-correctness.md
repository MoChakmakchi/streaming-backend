# ADR 0006: Restart Correctness

**Status:** Accepted  
**Date:** 2026-09-22

## Decision

Derived state will be persisted continuously.

Health, occupancy, alarms, and River jobs are persisted in PostgreSQL. After a restart, the API reads the last committed state immediately and workers resume pending jobs. The raw event log is retained as a recovery and verification fallback.

The important transaction boundaries are:

- The receiver stores the event and, when asynchronous processing is required, enqueues the corresponding River job in the same transaction before acknowledging the request.
- Events are uniquely identified by `(device_id, seq)`, enforced by a database constraint, so retried ingestion does not create another event or job.
- Workers update derived state and complete the River job in the same transaction.
- Processing is idempotent so retried jobs do not produce duplicate effects.
- Fall alarms are deduplicated and persisted in the ingest transaction before subscribers are notified.
- Subscribers fetch missed alarms through inclusive `GET /alarms?since=<ts>` queries over durable alarm creation time. Stable IDs make boundary duplicates safe.
- PostgreSQL uses persistent storage; correctness never depends on container-local state.
- The raw event log is append-only and can be replayed to rebuild derived state if required.

The ingestion identifier `(device_id, seq)` detects repeated delivery of the same emitted event. Fall-warning deduplication is a separate concern because jitter copies have different sequence numbers.

## Normal flow

```text
Device
  │
  ▼
API receiver
  ├── normal ──► transaction: Event log + River job
  │                              │
  │                              ▼
  │                         River worker
  │                              │
  │                              └── transaction: Derived state + Job completion
  │
  └── fall ───► transaction: Event log + Alarm + Notification

Both paths acknowledge only after commit.
Query API reads persisted state; SSE clients catch up through alarm history.
```

## Restart flow

```text
PostgreSQL ──► API reads committed state
           ├─► Worker resumes pending or retried jobs
           └─► SSE clients fetch alarms from their last saved creation time
```

This provides fast restarts without requiring a full replay during normal recovery while preserving the ability to reconstruct state from the event log.

## Alternatives considered

- **Full replay on startup:** simpler source-of-truth model, but restart time grows with the event log.
- **Snapshots plus replay:** reduces replay time, but adds snapshot coordination and versioning that are not justified yet.

## Risks and required tests

- Verify the exact River integration uses transactional enqueueing and completion. River provides `InsertTx` and `JobCompleteTx`, but using a non-transactional API would reopen a failure window.
- Kill the API after commit but before its response and verify that a retried request creates neither another event nor another job.
- Kill workers before and after commit and verify that interrupted jobs resume without duplicate state changes.
- Verify jobs that exhaust their retries remain visible and raise an operational alert.
- Verify missed PostgreSQL notifications do not lose alarms; notifications are wake-up signals, while persisted alarm rows are the source of truth.
- Restart containers in different orders and verify the API and worker recover when PostgreSQL becomes available.
- Replay the event log and verify that it does not duplicate alarms or corrupt derived state.
- Verify database migrations and persisted River job payloads remain compatible across restarts and upgrades.
- Test late events separately: persisted state alone does not guarantee correct out-of-order updates.
- Test SSE reconnect boundaries and verify clients may receive duplicates but never miss persisted alarms.

## Scope

The guarantee covers process, container, and host restarts where the PostgreSQL volume remains intact. Database corruption or loss of the persistent volume is outside scope.
