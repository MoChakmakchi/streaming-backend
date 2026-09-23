# Research: Real-Time Streaming Event Backend

## Runtime and dependencies

**Decision**: Use Go 1.27, Chi v5, pgx v5, River with its pgx v5 driver, and PostgreSQL 18. Exact module versions are pinned in `go.mod` and `go.sum`; River and its CLI use the same resolved version.

**Rationale**: These are current compatible releases. Chi stays close to `net/http`; pgx provides a
direct PostgreSQL pool and transaction API; River's pgx v5 driver shares application transactions.
The design uses no PostgreSQL-version-specific feature beyond the standard transactional and
indexing capabilities required by River and the application.

**Alternatives considered**: Go 1.26 remains compatible but is not the current toolchain. An ORM,
separate broker, and validation framework add no required capability.

Sources: [Go releases](https://go.dev/doc/devel/release), [Chi](https://github.com/go-chi/chi),
[pgx](https://github.com/jackc/pgx), [River release](https://github.com/riverqueue/river/releases/tag/v0.47.0)

## Transactional River integration

**Decision**: Insert projection jobs with `Client.InsertTx` in the raw-event transaction. Workers
update state and call `river.JobCompleteTx` in one pgx transaction. Use one normal queue with
bounded configurable workers and River's default retries.

**Rationale**: The job appears only when the event commits, and job completion cannot commit
without the projection update. This closes the restart windows identified in ADR 0006 without a
second broker or outbox. River retries are already durable and workers remain idempotent.

**Alternatives considered**: Non-transactional insertion/completion reopens failure windows.
Custom retry logic, priorities, and multiple queues are unnecessary because alarms bypass River.

Sources: [transactional enqueueing](https://riverqueue.com/docs/transactional-enqueueing),
[transactional completion](https://riverqueue.com/docs/transactional-job-completion),
[reliable workers](https://riverqueue.com/docs/reliable-workers)

## Future heartbeat and presence events

**Decision**: Store future events immediately and set `river.InsertOpts.ScheduledAt` to their event
time. Fall alarms remain immediate.

**Rationale**: Scheduled jobs are durable and prevent future state events from changing current
projections before their event time. River may promote a scheduled job a few seconds late, which
is acceptable for projections; the one-second requirement applies to alarms.

**Alternatives considered**: Updating current state immediately is incorrect. A custom scheduler
duplicates River. Querying only raw history would make the accepted worker/projection design
pointless.

Source: [River scheduled jobs](https://riverqueue.com/docs/scheduled-jobs)

## Event history and projections

**Decision**: Persist the raw envelope plus type-specific JSON in one append-only table. Keep
current health and occupancy rows as projections; calculate rolling windows from indexed raw
heartbeat and presence history.

**Rationale**: Current-state reads remain cheap while late events automatically affect the next
rolling-window query. This avoids stored time buckets, correction jobs, and duplicated event
history.

**Alternatives considered**: Recomputing the entire event log on every query does not use the
persisted projections. Persisted rolling buckets require complex late-event repair and are not
needed for the assignment.

## Index and ordering strategy

**Decision**: Enforce `UNIQUE (device_id, seq)` and add only the partial B-tree indexes required by
heartbeat, presence, and alarm queries. Every ordered read includes the documented deterministic
tie-breakers.

**Rationale**: Unique constraints make sender retries safe. Equality keys followed by event time
support window reads without speculative indexes, and explicit ordering avoids database-dependent
results.

**Alternatives considered**: A broad collection of indexes increases ingest cost. Arrival order
and implicit row order violate the assignment.

Sources: [PostgreSQL constraints](https://www.postgresql.org/docs/current/ddl-constraints.html),
[multicolumn indexes](https://www.postgresql.org/docs/current/indexes-multicolumn.html),
[sorting rows](https://www.postgresql.org/docs/current/queries-order.html)

## Concurrent fall deduplication

**Decision**: Serialize the rare fall-ingest path with a transaction-scoped PostgreSQL advisory
lock keyed by room, then check the same-device three-second event-time window anchored to each
existing alarm's source warning before inserting an alarm. Duplicate warnings do not extend the
window.

**Rationale**: A room lock closes the concurrent check-and-insert race and also establishes a
single durable creation order for alarms in that room. It does not reduce throughput for normal
events.

**Alternatives considered**: An unlocked check is racy under `READ COMMITTED`. Serializable
transactions add retry complexity to all fall writes. A range exclusion constraint adds an
extension and obscures the accepted rule.

Sources: [PostgreSQL advisory locks](https://www.postgresql.org/docs/current/explicit-locking.html),
[transaction isolation](https://www.postgresql.org/docs/current/transaction-iso.html)

## Alarm notification and recovery

**Decision**: Persist the alarm and issue `NOTIFY` in the same transaction. SSE listeners query
durable alarms after a wake-up and periodically check for missed notifications. Reconnecting
clients use inclusive `GET /alarms?since=<ts>` over alarm creation time.

**Rationale**: PostgreSQL delivers notifications only after commit, but notifications are not
durable. The alarm table therefore remains the source of truth and stable IDs make inclusive
boundary duplicates harmless.

**Alternatives considered**: Treating notifications as delivery can lose alarms during disconnects.

Sources: [PostgreSQL NOTIFY](https://www.postgresql.org/docs/current/sql-notify.html),
[LISTEN](https://www.postgresql.org/docs/current/sql-listen.html)

## SSE and HTTP lifecycle

**Decision**: Implement SSE directly with `net/http`: `text/event-stream`, JSON `data` frames,
`ResponseController.Flush`, and request/application context cancellation. Keep the stream outside
finite request-timeout middleware.

**Rationale**: The protocol is small and the standard library supplies flushing and cancellation.
An SSE dependency would add no useful behavior.

**Alternatives considered**: WebSockets and long polling are valid but add client or polling
complexity without helping the required one-way feed.

Sources: [server-sent events](https://html.spec.whatwg.org/multipage/server-sent-events.html),
[Go ResponseController](https://pkg.go.dev/net/http#ResponseController)

## Migrations, durability, and verification

**Decision**: Apply one idempotent application SQL migration with `psql`; apply River migrations
once with its pinned CLI. Keep PostgreSQL `fsync` and synchronous commit enabled. Use real
PostgreSQL integration tests and the supplied generator rather than mocks or another load tool.

**Rationale**: This is reproducible and avoids a migration framework for one initial schema.
Synchronous commit is required before acknowledging accepted events. Real database tests cover
the transactional behavior mocks cannot prove.

**Alternatives considered**: Application-start migrations can race between API and worker.
Asynchronous commit weakens crash durability. A separate load framework is deferred unless the
provided harness and focused load script prove insufficient.

Sources: [River migrations](https://riverqueue.com/docs/migrations),
[PostgreSQL WAL settings](https://www.postgresql.org/docs/current/runtime-config-wal.html),
[Go race detector](https://go.dev/doc/articles/race_detector)
