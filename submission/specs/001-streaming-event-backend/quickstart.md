# Quickstart and Manual Verification

Run each stage's checks after that stage is implemented. Stop at every gate and confirm the result
before continuing to the next stage.

API shapes and error responses are defined in
[`submission/docs/api/openapi.yml`](../../docs/api/openapi.yml).

## Prerequisites

- Docker with Compose
- Go 1.27 or newer for host-side tests
- Python 3.10 or newer for the supplied generator and evaluator
- `curl`

The submission Makefile will provide these commands:

```bash
cd submission
make docker-init        # Build, start containers, and apply migrations
make test
make test-integration
make test-load
make docker-down
```

The Compose API listens on `http://localhost:8090`. PostgreSQL data remains in a named Compose
volume across normal `make docker-down` and container restarts.

## Stage 1 — Event ingestion and persistence

Start the stack, then define a current timestamp:

```bash
cd submission
make docker-init
NOW="$(date -u +"%Y-%m-%dT%H:%M:%S.000Z")"
```

Submit a valid event:

```bash
curl -i -X POST http://localhost:8090/events \
  -H 'Content-Type: application/json' \
  -d "{\"device_id\":\"dev_manual\",\"room_id\":\"room_manual\",\"type\":\"heartbeat\",\"ts\":\"$NOW\",\"seq\":1}"
```

Expected: `202 Accepted` with status `accepted`. Repeat the identical request and expect `200 OK`
with status `duplicate` and the same event ID.

Submit malformed and out-of-window events:

```bash
curl -i -X POST http://localhost:8090/events \
  -H 'Content-Type: application/json' \
  -d '{"device_id":"dev_manual"}'

curl -i -X POST http://localhost:8090/events \
  -H 'Content-Type: application/json' \
  -d '{"device_id":"dev_manual","room_id":"room_manual","type":"heartbeat","ts":"2000-01-01T00:00:00Z","seq":2}'
```

Expected: `400 Bad Request` for the malformed body and `422 Unprocessable Entity` for the old
timestamp. Neither creates an event. Run `make test-integration` to verify durable insertion,
idempotency, and the exact ±1-hour boundaries.

**Manual gate:** confirm persistence, duplicate handling, validation, and restart survival before
Stage 2.

## Stage 2 — Device health

Post two heartbeat events with distinct timestamps and sequences, then query:

```bash
curl http://localhost:8090/devices/dev_manual/health
```

Expected response shape:

```json
{"last_heartbeat_ts":"...","availability_5m":0.01}
```

The exact availability value depends on how many distinct manual heartbeats remain in the current
five-minute window.

Submit an older heartbeat after the newer one. The latest timestamp must not move backward, while
an older heartbeat inside the five-minute window must contribute to availability. An unknown
device must return `404`.

Run the focused ordering checks:

```bash
cd submission
make test-integration TEST=health
```

**Manual gate:** confirm latest-heartbeat ordering and five-minute availability before Stage 3.

## Stage 3 — Room occupancy

Before posting any presence event for `room_manual`, query it and verify that the response is `404 Not Found`.

Submit an occupied transition, then an unoccupied transition with the next sequence number. Query every supported window:

```bash
curl 'http://localhost:8090/rooms/room_manual/occupancy?window=1m'
curl 'http://localhost:8090/rooms/room_manual/occupancy?window=5m'
curl 'http://localhost:8090/rooms/room_manual/occupancy?window=1h'
```

Expected: each response contains `in_room`, `occupied_pct`, and `window_seconds`; the percentage is between 0 and 1. Insert a late presence transition within the window and verify that the next query corrects the percentage without replacing a newer current state.

```bash
cd submission
make test-integration TEST=occupancy
```

**Manual gate:** confirm current state, all three windows, tie-breaking, and late-event correction
before Stage 4.

## Stage 4 — Alarm history and live feed

In one terminal, open the SSE stream:

```bash
curl -N http://localhost:8090/alarms/stream
```

In another terminal, post a fall warning using a current timestamp. Post a second warning from the same device and room within three event-time seconds but with a different sequence number.

Expected: the stream emits one `alarm` event and history contains one logical alarm:

```bash
curl 'http://localhost:8090/alarms?since=0'
```

Record its `created_at`, disconnect the stream, create another alarm, then reconnect from the
recorded value:

```bash
curl -N 'http://localhost:8090/alarms/stream?since=<created_at>'
```

Expected: the boundary alarm may repeat across the two connections, the missed alarm is replayed,
and the connection remains open for new alarms. Each alarm appears only once within the reconnected
stream even if it overlaps persisted history and buffered live delivery.
`GET /alarms?since=<created_at>` remains available when only history is needed.

```bash
cd submission
make test-integration TEST=alarms
```

**Manual gate:** confirm deduplication, history recovery, per-room order, and SSE delivery before Stage 5.

## Stage 5 — Restart and pressure

Create known health, occupancy, and alarm state, then hard-kill the application containers:

```bash
cd submission
docker compose -f deployment/compose.yaml kill api
docker compose -f deployment/compose.yaml up -d api
```

Expected: committed events and alarms remain available, and health and occupancy return the same
results without duplicate alarms.

Run focused verification:

```bash
cd submission
make test-integration
make test-load

cd ..
make smoke SERVICE_URL=http://localhost:8090
make offline SERVICE_URL=http://localhost:8090
make burst SERVICE_URL=http://localhost:8090
```

Expected:

- Every acknowledged event remains in durable history.
- Late replay corrects health and occupancy.
- Alarm count matches distinct falls.
- Alarm latency remains at or below one second p95 during the dedicated 50,000-events/second test.
- Overload produces explicit `503` responses with `Retry-After`, never false acceptance.
- Logs and metrics identify ingest rejection, query latency, PostgreSQL saturation, and alarm
  latency.

**Final manual gate:** confirm the evaluator, restart, burst, and observability results before
completing the submission write-up.
