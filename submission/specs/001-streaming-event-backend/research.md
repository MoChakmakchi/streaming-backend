# Research: Real-Time Streaming Event Backend

## Runtime and dependencies

**Decision**: Use Go 1.27, Chi v5, pgx v5, and PostgreSQL 18. Exact module versions are pinned in
`go.mod` and `go.sum`.

**Rationale**: These are current compatible releases. Chi stays close to `net/http`; pgx provides a
direct PostgreSQL pool and transaction API. The design uses no PostgreSQL-version-specific feature
beyond the standard transactional and indexing capabilities required by the application.

**Alternatives considered**: Go 1.26 remains compatible but is not the current toolchain. An ORM,
separate broker, and validation framework add no required capability.

Sources: [Go releases](https://go.dev/doc/devel/release), [Chi](https://github.com/go-chi/chi),
[pgx](https://github.com/jackc/pgx)

## Event history queries

**Decision**: Calculate latest health, latest occupancy, and rolling results from indexed event
history.

**Rationale**: The bounded per-device and per-room windows use selective partial indexes. Late
events affect the next query immediately, and accepted future events remain excluded until the
query cutoff reaches their event time.

The implementation-stage refinement that led to this design is recorded in
[`implementation-decisions.md`](../../docs/implementation-decisions.md).

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

## Alarm live delivery and recovery

**Decision**: After an alarm commits, signal one in-process SSE broadcaster. The broadcaster reads
unseen alarm rows in durable creation order before publishing them. Reconnecting clients use
inclusive `GET /alarms?since=<ts>` over alarm creation time.

**Rationale**: The assignment runs one API process, so database notifications and periodic polling
add no required capability. Reading after an in-process wake-up preserves durable order across
concurrent request goroutines. The alarm table remains the source of truth if a client disconnects
or the process stops between commit and publication. Stable IDs make inclusive boundary duplicates
harmless.

**Alternatives considered**: PostgreSQL `LISTEN/NOTIFY` or a broker can provide cross-process
wake-ups if API replicas are introduced later.

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

**Decision**: Apply one idempotent application SQL migration with `psql`. Keep PostgreSQL `fsync`
and synchronous commit enabled. Use real PostgreSQL integration tests and the supplied generator
rather than mocks or another load tool.

**Rationale**: This is reproducible and avoids a migration framework for one initial schema.
Synchronous commit is required before acknowledging accepted events. Real database tests cover
the transactional behavior mocks cannot prove.

**Alternatives considered**: Application-start migrations couple service availability to schema
changes. Asynchronous commit weakens crash durability. A separate load framework is deferred
unless the provided harness and focused load script prove insufficient.

Sources: [PostgreSQL WAL settings](https://www.postgresql.org/docs/current/runtime-config-wal.html),
[Go race detector](https://go.dev/doc/articles/race_detector)
