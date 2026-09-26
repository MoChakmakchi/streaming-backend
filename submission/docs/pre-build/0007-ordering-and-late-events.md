# ADR 0007: Ordering and Late Events

**Status:** Accepted  
**Date:** 2026-09-22

## Decision

Event time is authoritative. Results must not depend on arrival order or the order in which River workers process jobs.

Events are accepted when their timestamp is within the inclusive range:

```text
received_at - 1 hour <= event.ts <= received_at + 1 hour
```

Events outside that range are rejected explicitly. Accepted events are persisted immediately and identified by `(device_id, seq)` so ingestion retries do not create duplicates.

Within a device, events are ordered by `(ts, seq)`. Presence events across devices in the same room use `(ts, device_id, seq)` for deterministic tie-breaking.

## Feature behavior

### Device health

A heartbeat updates the latest heartbeat only when its timestamp is newer. Late heartbeats contribute to availability when they fall within the requested five-minute window.

### Room occupancy

Occupancy is unknown until a room has a presence event at or before the query time, so the query returns `404` before then. Presence history is retained in event-time order.

A late presence event does not replace a newer current state, but it can change the occupied duration of affected windows. Occupancy queries use the latest state at or before the start of the window and apply transitions within the window.

Concurrent workers may process presence events in any order; database writes and queries must still produce the same result.

### Fall alarms

Fall alarms are persisted and published promptly, including when their event timestamp is late or slightly in the future. The original timestamp is retained.

The alarm feed follows durable alarm creation order rather than event time.

## Future-dated events

Accepted future events are stored immediately. Current-state and rolling-window queries only use events whose timestamp is not later than the query time, so they become effective naturally when their timestamp is reached.

## Alternatives considered

- **Arrival-order processing:** rejected because devices replay buffered historical events.
- **Reordering buffer:** rejected because events may arrive up to one hour late and alarms must be delivered within one second.

## Required tests

- Process events for the same device in different worker orders and verify identical results.
- Replay a device's buffered events and verify that health and occupancy windows are corrected.
- Process concurrent late presence events for the same room and verify deterministic current state and history.
- Verify an occupancy query returns `404` before the room's first applicable presence event.
- Test timestamps exactly on and immediately outside both one-hour boundaries.
- Verify duplicate ingestion does not create another event or, where applicable, another River job.
- Verify late and future-dated fall alarms are delivered promptly with their original timestamp.
