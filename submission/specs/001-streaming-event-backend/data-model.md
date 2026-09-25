# Data Model: Real-Time Streaming Event Backend

PostgreSQL is the source of truth. All timestamps use `timestamptz` and are returned as RFC 3339
UTC values. Raw events are append-only; alarm writes occur only through the transaction described
below.

## Event

One accepted device event.

| Field | Type | Rules |
|---|---|---|
| `id` | `bigint` identity | Primary key and stable internal event reference |
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

## Device Health Query

Device health is derived from heartbeat events for one device at the query cutoff. The latest
heartbeat is ordered by `(event_time, seq)`. Five-minute availability is the number of accepted
heartbeat events in `(query_time - 5 minutes, query_time]`, divided by 300 and capped at 1.

## Room Occupancy Query

Room occupancy is derived from presence events for one room. Occupied percentage for a query
window is calculated from:

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
stored. Otherwise the transaction inserts the alarm. After commit, the service publishes the new
alarm through a broadcaster that reads unseen alarms in durable creation order. The first accepted
warning that creates the logical alarm supplies its timestamp and confidence; duplicate warnings
do not extend the window.

## Relationships

```text
Event 1 ─── 0..1 Logical Alarm source
```

## Transaction Boundaries

### Non-fall ingest

Insert the event with `ON CONFLICT (device_id, seq) DO NOTHING` and commit before acknowledgement.
A duplicate returns the existing event identity.

### Fall ingest

1. Begin transaction and take the room advisory lock.
2. Insert the raw event, returning the existing identity for an ingestion retry.
3. For a new event, check the three-second window anchored to each existing alarm's source warning.
4. Insert an alarm only when no logical alarm matches.
5. Commit, signal live publication when an alarm was created, then acknowledge.

## State Transitions

```text
request ── invalid/outside window ──► rejected
        ├─ existing (device_id, seq) ─► duplicate acknowledgement
        └─ new ─► durable event
                  ├─ fall ─► existing logical alarm or new durable alarm
                  └─ other type ─► queryable history
```
