# Data Model: Real-Time Streaming Event Backend

PostgreSQL is the source of truth. All timestamps use `timestamptz` and are returned as RFC 3339
UTC values. Raw events are append-only; projection and alarm writes occur only through the
transactions described below.

## Event

One accepted device event.

| Field | Type | Rules |
|---|---|---|
| `id` | `bigint` identity | Primary key; internal River job reference |
| `device_id` | `text` | Required, non-empty |
| `room_id` | `text` | Required, non-empty |
| `event_type` | `text` | One of the six supported event types |
| `event_time` | `timestamptz` | Required; accepted within inclusive receive time ±1 hour |
| `seq` | `bigint` | Required, non-negative, monotonic per device; gaps allowed |
| `payload` | `jsonb` | Validated type-specific fields only |
| `received_at` | `timestamptz` | Set by the service when receipt begins |

Constraints and indexes:

- `UNIQUE (device_id, seq)` is the ingestion idempotency key.
- A partial index on `(device_id, event_time, seq)` for heartbeat rows supports health windows.
- A partial index on `(room_id, event_time, device_id, seq)` for presence rows supports current
  state and occupancy windows.
- No update or delete operation is exposed. Events are retained for the assignment lifetime.

Payload validation:

| Event type | Payload |
|---|---|
| `heartbeat` | Empty |
| `presence` | Required Boolean `in_room` |
| `motion` | Required number `magnitude` from 0 through 1 |
| `sleep_state` | Required `state`: `asleep`, `awake`, or `unknown` |
| `fall_warn` | Required number `confidence` from 0 through 1 |
| `net_status` | Required integer `rssi` |

## Device Health

The latest processed heartbeat for one device.

| Field | Type | Rules |
|---|---|---|
| `device_id` | `text` | Primary key |
| `room_id` | `text` | Room from the latest heartbeat |
| `event_id` | `bigint` | References the source event |
| `last_heartbeat_at` | `timestamptz` | Source event time |
| `last_seq` | `bigint` | Tie-breaker for equal event times |
| `updated_at` | `timestamptz` | Projection write time |

The worker replaces the row only when `(event_time, seq)` is newer than the stored pair. Repeating
the same job is therefore harmless. Five-minute availability is calculated from distinct accepted
heartbeat events in `(query_time - 5 minutes, query_time]`, divided by 300 and capped at 1.

## Room Occupancy

The latest processed presence state for one room.

| Field | Type | Rules |
|---|---|---|
| `room_id` | `text` | Primary key |
| `in_room` | `boolean` | Latest presence state; absent rooms are not found |
| `event_id` | `bigint` | References the source event |
| `last_event_at` | `timestamptz` | Source event time |
| `last_device_id` | `text` | First equal-time tie-breaker |
| `last_seq` | `bigint` | Second equal-time tie-breaker |
| `updated_at` | `timestamptz` | Projection write time |

The worker replaces the row only when `(event_time, device_id, seq)` is newer. Occupied percentage
for a query window is calculated from:

1. At least one presence event at or before the query cutoff; otherwise the room is not found.
2. The last presence state at or before the window start, defaulting to unoccupied when the
   room's first applicable presence event falls inside the window.
3. Every transition after the window start and at or before the query cutoff, ordered by
   `(event_time, device_id, seq)`.
4. Occupied duration divided by the fixed window duration.

Because the calculation reads event history, a late accepted transition changes the next query
without rewriting stored buckets.

## Logical Alarm

One deduplicated physical fall.

| Field | Type | Rules |
|---|---|---|
| `id` | `bigint` identity | Stable API `event_id` and primary key |
| `source_event_id` | `bigint` | Unique reference to the warning that created the alarm |
| `device_id` | `text` | Required |
| `room_id` | `text` | Required |
| `event_time` | `timestamptz` | Original warning timestamp |
| `confidence` | `double precision` | From 0 through 1 |
| `created_at` | `timestamptz` | Durable alarm creation time used by history recovery |

Indexes:

- `(device_id, room_id, event_time)` supports the three-second deduplication check.
- `(created_at, id)` supports inclusive alarm history.
- `(room_id, created_at, id)` supports deterministic per-room publication order.

Fall ingest takes a transaction-scoped room advisory lock. If the warning is within three seconds
of the source warning for an existing alarm from the same device and room, only the raw event is
stored. Otherwise the transaction inserts the alarm and sends a PostgreSQL notification. The
first accepted warning that creates the logical alarm supplies its timestamp and confidence;
duplicate warnings do not extend the window.

## River Projection Job

River owns its tables and migrations. Application job arguments contain only the source event ID.

| Property | Rule |
|---|---|
| Kind | One projection-job kind dispatched by source event type |
| Queue | Default projection queue |
| Creation | Same transaction as a newly inserted heartbeat or presence event |
| Schedule | Immediate unless the event timestamp is in the future |
| Completion | Same transaction as the conditional projection update |
| Retry | River default; projection update remains idempotent |

## Relationships

```text
Event 1 ─── 0..1 Device Health source
Event 1 ─── 0..1 Room Occupancy source
Event 1 ─── 0..1 Logical Alarm source
Event 1 ─── 0..1 River Projection Job
```

## Transaction Boundaries

### Heartbeat or presence ingest

1. Begin transaction.
2. Insert the event with `ON CONFLICT (device_id, seq) DO NOTHING`.
3. If inserted, enqueue its River job in the same transaction.
4. Commit, then acknowledge. A duplicate returns the existing event identity without another job.

### Other non-fall ingest

Insert the event and commit before acknowledgement. No job is created.

### Fall ingest

1. Begin transaction and take the room advisory lock.
2. Insert the raw event, returning the existing identity for an ingestion retry.
3. For a new event, check the three-second window anchored to each existing alarm's source warning.
4. Insert and notify only when no logical alarm matches.
5. Commit, then acknowledge.

### Projection work

1. Begin transaction and load the source event.
2. Conditionally upsert the owning projection using its deterministic ordering tuple.
3. Complete the running River job in the same transaction.
4. Commit and return success.

## State Transitions

```text
request ── invalid/outside window ──► rejected
        ├─ existing (device_id, seq) ─► duplicate acknowledgement
        └─ new ─► durable event
                  ├─ heartbeat/presence ─► queued/scheduled ─► projection committed
                  ├─ fall ─► existing logical alarm or new durable alarm
                  └─ other type ─► retained only
```
