# Feature Specification: Real-Time Streaming Event Backend

**Feature Branch**: `001-streaming-event-backend`

**Created**: 2026-09-22

**Status**: Draft

**Input**: User description: "Build the real-time streaming backend defined by `README.md` and all decision and discovery documents under `submission/docs/`."

## Clarifications

### Session 2026-09-22

- Q: How should the system decide that two fall warnings represent the same physical fall? → A: Use a three-second window for the same device and room, anchored to the warning that created the alarm; duplicates do not extend the window.
- Q: Should alarm recovery use a separate resumable SSE cursor or only the documented alarm-history endpoint after reconnecting? → A: Reconnect SSE and recover missed alarms through `GET /alarms?since=<ts>`.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Accept Events Reliably (Priority: P1)

As a device operator, I need valid device events accepted durably so temporary processing delays or service restarts do not lose data.

**Why this priority**: Every downstream result depends on receiving the source event exactly once as an accepted fact.

**Independent Test**: Send valid, invalid, duplicate, late, and future-dated events and verify that only valid in-window events are accepted, every accepted event survives restart, and retries do not create duplicates.

**Acceptance Scenarios**:

1. **Given** a valid event within the accepted time range, **When** the device submits it, **Then** acceptance is confirmed only after the event is durable.
2. **Given** the same emitted event is submitted more than once, **When** each copy is received, **Then** it is stored and processed as one accepted event.
3. **Given** an event is malformed or outside the accepted time range, **When** it is submitted, **Then** it is rejected explicitly without changing state.

---

### User Story 2 - Query Accurate Health and Occupancy (Priority: P1)

As a care-system consumer, I need current device health and room occupancy so I can understand sensor availability and room use in real time.

**Why this priority**: Correct health and occupancy are the primary aggregations required from the event stream.

**Independent Test**: Submit heartbeat and presence histories in multiple arrival orders, including a delayed offline replay, and compare every query result with the same histories ordered by event time.

**Acceptance Scenarios**:

1. **Given** heartbeat events for a device, **When** health is queried, **Then** the newest event-time heartbeat and five-minute availability are returned.
2. **Given** presence transitions for a room, **When** occupancy is queried for one minute, five minutes, or one hour, **Then** the current state and occupied percentage for that window are returned.
3. **Given** historical heartbeat or presence events arrive late, **When** the affected state is queried after acceptance, **Then** the result reflects the corrected event-time history.
4. **Given** events are accepted concurrently in different arrival orders, **When** the same query cutoff is used, **Then** the resulting health and occupancy state is identical.

---

### User Story 3 - Receive Distinct Fall Alarms (Priority: P1)

As an alarm-feed consumer, I need each physical fall warning delivered promptly once, with a durable way to resume after disconnection.

**Why this priority**: Fall warnings are safety-critical and have the strictest latency requirement.

**Independent Test**: Submit distinct falls and sensor-jitter copies during an ingestion burst, disconnect and reconnect the consumer, and verify the distinct alarm set, order, timestamps, and delivery latency.

**Acceptance Scenarios**:

1. **Given** a new fall warning, **When** it is accepted, **Then** one durable alarm with the original event timestamp becomes available to subscribers.
2. **Given** jitter copies of the same fall, **When** they are accepted, **Then** consumers receive one logical alarm.
3. **Given** a consumer disconnects, **When** it reconnects and requests alarms since its last alarm creation time, **Then** every missed alarm is returned; boundary duplicates are allowed.
4. **Given** a fall warning has a late or accepted future timestamp, **When** it is ingested, **Then** it is published promptly rather than waiting for event time.

---

### User Story 4 - Remain Correct Under Pressure and Restart (Priority: P2)

As a service operator, I need ingestion, queries, and alarms to remain correct during bursts, offline replay, and hard restarts.

**Why this priority**: The service is only useful if its normal behavior survives the failure and load conditions described by the assignment.

**Independent Test**: Run the baseline, burst, offline-replay, and hard-restart scenarios and compare accepted events, aggregations, and alarm delivery with ground truth.

**Acceptance Scenarios**:

1. **Given** a 30-second ten-times ingest burst, **When** ingestion is under pressure, **Then** accepted events remain durable and fall alarms continue to meet their latency target.
2. **Given** the service is hard-killed after accepting events, **When** it restarts, **Then** committed events and alarms recover and health and occupancy remain derivable without duplicate effects.
3. **Given** capacity is exhausted before an event can be accepted, **When** the request deadline is reached, **Then** the event is rejected explicitly as retryable rather than reported as accepted.
4. **Given** a new device begins sending valid events, **When** those events arrive, **Then** the device is handled without redeployment or pre-registration.

### Edge Cases

- An event timestamp is exactly one hour before or after receive time.
- Two events from one device have the same timestamp but different sequence numbers.
- Presence events from different devices in one room have the same timestamp.
- A late presence transition changes an interval but is older than the room's current state.
- A future-dated accepted state event is queried before and after its timestamp is reached.
- A fall warning is committed while no live-feed consumer is connected.
- A consumer reconnects at a boundary where an alarm may already have been delivered.
- A process stops after committing an event but before returning its acceptance response.
- The notification channel disconnects while durable alarms continue to be created.
- An unknown device or room is queried before it has any accepted events.

## Requirements *(mandatory)*

### Scope

The feature includes event ingestion, durable event history, device-health and room-occupancy results, deduplicated fall alarms, a live alarm feed, alarm history, recovery, backpressure behavior, and the observability needed to verify them. Authentication, dashboards, device management, and outputs derived from motion, sleep-state, or network-status events are outside the initial scope.

### Functional Requirements

- **FR-001**: The system MUST accept the six documented event types and validate their common and type-specific required fields.
- **FR-002**: The system MUST reject malformed events without changing durable event or derived state.
- **FR-003**: The system MUST accept event timestamps in the inclusive range from one hour before receive time through one hour after receive time and MUST explicitly reject timestamps outside that range.
- **FR-004**: The system MUST make an accepted event durable before confirming acceptance.
- **FR-005**: The system MUST treat repeated submissions with the same `(device_id, seq)` as one emitted event and MUST NOT create duplicate derived effects.
- **FR-006**: The system MUST retain accepted motion, sleep-state, and network-status events even though they produce no required output in the initial scope.
- **FR-007**: The system MUST use event timestamp, not arrival or insertion order, as the authoritative order for health and occupancy.
- **FR-008**: The system MUST order equal-timestamp events from one device by sequence number and MUST deterministically order equal-timestamp room-presence events by device identifier and sequence number.
- **FR-009**: Current-state and rolling-window results MUST exclude accepted future events until their event timestamp is reached.
- **FR-010**: Device health MUST expose the latest heartbeat at or before query time.
- **FR-011**: Five-minute availability MUST equal the number of distinct heartbeats in the five-minute event-time window divided by 300, limited to the range from 0 through 1.
- **FR-012**: An occupancy query MUST return not found until the room has at least one presence event at or before the query time.
- **FR-013**: Current room occupancy MUST reflect the latest presence event at or before query time.
- **FR-014**: Occupied percentage MUST equal occupied duration divided by total duration for the requested one-minute, five-minute, or one-hour window.
- **FR-015**: Occupancy calculation MUST use the last presence state at or before the window start and every transition within the window.
- **FR-016**: Late accepted heartbeat and presence events MUST correct every affected current or rolling result without allowing older state to overwrite newer current state.
- **FR-017**: A fall warning within three seconds of the source warning for an existing alarm from the same device and room MUST represent that logical alarm. Duplicate warnings MUST NOT extend the three-second window.
- **FR-018**: Every logical alarm MUST have a stable identifier and MUST expose its original warning timestamp, durable creation timestamp, room, device, and confidence.
- **FR-019**: Late and accepted future-dated fall warnings MUST become durable alarms immediately after ingestion rather than waiting for event time.
- **FR-020**: The live alarm feed MUST expose alarms in durable publication order and preserve that order within each room.
- **FR-021**: `GET /alarms?since=<ts>` MUST return alarms whose durable creation timestamp is greater than or equal to `since`.
- **FR-022**: Duplicate alarm delivery around reconnection and history-query boundaries MUST retain the same stable alarm identifier so consumers can deduplicate safely.
- **FR-023**: Temporary ingestion pressure MUST NOT lose any event whose acceptance was confirmed.
- **FR-024**: If an event cannot be accepted before its request deadline, the system MUST reject it explicitly as retryable and MUST NOT report successful acceptance.
- **FR-025**: A restart MUST recover committed events and alarms, from which health and occupancy remain queryable.
- **FR-026**: Retried ingestion and alarm delivery MUST NOT duplicate accepted events or logical alarms.
- **FR-027**: A missed transient alarm notification MUST NOT prevent later retrieval of the persisted alarm.
- **FR-028**: The system MUST handle new device and room identifiers without pre-registration or redeployment.
- **FR-029**: Queries for unknown devices or rooms MUST return an explicit not-found result rather than fabricated state.
- **FR-030**: The system MUST expose accepted and rejected event counts, ingest and query latency, alarm-delivery latency, and resource-saturation signals.

### Key Entities

- **Device Event**: An immutable emitted fact containing device, room, type, event timestamp, sequence number, type-specific data, and receive time.
- **Device Health**: The latest applicable heartbeat and availability over the preceding five minutes for one device.
- **Room Presence Transition**: A timestamped occupied or unoccupied state used to reconstruct room occupancy intervals.
- **Room Occupancy**: Current room state and occupied-duration percentage for a requested window and query cutoff.
- **Logical Alarm**: One deduplicated fall warning with a stable identifier, source details, original event timestamp, and durable creation timestamp.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: The system ingests events from 5,000 devices at approximately 5,000 events per second for five minutes with no acknowledged event missing from durable history.
- **SC-002**: During each 50,000-events-per-second burst lasting 30 seconds, 95% of distinct fall alarms are available to feed consumers within one second of ingestion.
- **SC-003**: For baseline, out-of-order, offline-replay, and concurrent-ingestion scenarios, every sampled health and occupancy result matches the result calculated from accepted events ordered by event time.
- **SC-004**: The number of persisted logical alarms matches the number of distinct physical falls in the evaluation input, with no missing or extra alarms.
- **SC-005**: After a hard restart, all previously committed events and alarms remain queryable without a duplicate logical effect.
- **SC-006**: A reconnecting consumer retrieves every missed alarm from its last alarm creation timestamp; duplicates are allowed, missing alarms are not.
- **SC-007**: Events exactly on either one-hour acceptance boundary are accepted, while events immediately outside either boundary are rejected.
- **SC-008**: A 5,001st device is ingested and becomes queryable without configuration changes or redeployment.
- **SC-009**: Operators can determine from exposed signals whether ingestion, queries, or alarm delivery is delayed or failing.

## Assumptions

- Device sequence numbers are monotonic for each stable device identifier and may contain gaps.
- Heartbeats are expected once per second, so full five-minute availability corresponds to 300 distinct heartbeats.
- Presence describes room state; when multiple devices report presence for one room, the latest event-time transition wins.
- Sensor-jitter copies of one physical fall come from the same device and room within three seconds of the warning that created the alarm.
- Rolling windows use server query time as their cutoff and include the state in effect at the beginning of the window.
- Senders retry events that receive an explicit retryable rejection.
- Accepted history and recent alarms are retained for at least the evaluation period and long enough to cover the largest query and recovery window.
- No authentication or authorization is required for the assignment environment.
- The supplied generator, evaluator, README, and event schema remain the external behavioral dependencies.
