# Final Design

This is the implemented architecture. The documents in [`pre-build/`](./pre-build/) preserve the
decisions made before coding; later changes are recorded in
[`spec/implementation-drift.md`](./spec/implementation-drift.md).

```text
POST /events
├─ normal events → bounded collector → batch writers → multi-row PostgreSQL commit
└─ fall warnings → reserved capacity → atomic event + alarm transaction
                                                   └─ durable replay + SSE

Health and occupancy queries → indexed event history in PostgreSQL
```

Normal events flush when a batch reaches 400 events or its oldest request waits 4 ms. Four writers
commit batches, with at most 15 batches pending. Requests receive `202` only after commit; duplicate
`(device_id, seq)` values receive `200`. Full admission returns retryable `503`, never false success.

Fall warnings bypass the normal batches through four reserved slots. The event, three-second
same-device-and-room deduplication, and alarm are one transaction. The window is anchored to the
warning that created the alarm: warnings at 0s, 2s, and 4s create alarms at 0s and 4s.

Alarm `event_time` preserves the device timestamp. Server-generated `created_at` provides the
durable history and reconnect cursor, so recovery does not depend on a device clock. SSE subscribes
to live delivery before replaying inclusive history, then removes overlap by stable alarm ID.

Health and occupancy are derived from event history ordered by device time. Late events therefore
correct the next query. Accepted future events remain stored but do not affect results until their
timestamp is reached. PostgreSQL and its named volume hold all recoverable state.
