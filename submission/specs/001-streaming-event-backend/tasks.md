---

description: "Dependency-ordered tasks for the real-time streaming event backend"
---

# Tasks: Real-Time Streaming Event Backend

**Input**: Design documents from `submission/specs/001-streaming-event-backend/`

**Prerequisites**: [plan.md](./plan.md), [spec.md](./spec.md), [research.md](./research.md),
[data-model.md](./data-model.md), [quickstart.md](./quickstart.md), and
`submission/docs/api/openapi.yml`

**Tests**: Only focused tests required by the README, specification, or accepted ADR risks are
included. Broad unit coverage, mock frameworks, and coverage targets remain deferred.

**Organization**: Work follows the five approved delivery stages. Health and occupancy are separate
manual gates within User Story 2. Do not start a phase after a manual gate until the user confirms
the preceding stage.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel after the phase prerequisites are complete because it uses different files.
- **[Story]**: Maps the task to its specification user story.
- Every task names its target file or verification document.

## Phase 1: Setup

**Purpose**: Establish the self-contained Go submission without adding unused packages.

- [X] T001 Initialize the Go 1.27 module and pin Chi and pgx dependencies in `submission/go.mod` and `submission/go.sum`
- [X] T002 [P] Add the API build image, PostgreSQL 18 with persistent storage, and Adminer in `submission/deployment/Dockerfile.api`, `submission/.dockerignore`, and `submission/deployment/compose.yaml`
- [X] T003 [P] Add lean build, migration, API, test, and cleanup targets in `submission/Makefile`

---

## Phase 2: User Story 1 — Event Ingestion and Persistence (Priority: P1) 🎯 MVP

**Goal**: Accept every valid in-window event durably, reject invalid input explicitly, and make retries idempotent.

**Independent Test**: Submit valid, malformed, duplicate, boundary, late, future, and capacity-limited requests; verify only accepted events persist and committed data survives an API restart.

### Stage configuration

- [X] T004 [US1] Add only the HTTP, PostgreSQL, and ingest-deadline configuration required by Stage 1 in `submission/internal/config/config.go`

### Focused tests

- [X] T005 [P] [US1] Add event-schema and inclusive ±1-hour boundary tests required by FR-001–FR-003 in `submission/internal/event/model_test.go`
- [X] T006 [P] [US1] Add POST `/events` response tests for accepted, duplicate, malformed, out-of-window, and retryable `503` cases in `submission/internal/httpapi/events_test.go`
- [X] T007 [P] [US1] Add PostgreSQL integration tests for `(device_id, seq)` idempotency, durable insertion, and retry after commit-before-response failure in `submission/internal/eventstore/store_test.go`

### Implementation

- [X] T008 [US1] Create the single idempotent application schema with events, alarms, required constraints, and focused indexes in `submission/migrations/001_initial.sql`
- [X] T009 [US1] Implement the six event variants, strict JSON decoding inputs, field validation, and timestamp-window validation in `submission/internal/event/model.go`
- [X] T010 [US1] Implement append-only transactional insertion, duplicate lookup, and event reads in `submission/internal/eventstore/store.go`
- [X] T011 [US1] Implement the transaction-bound ingestion operation in `submission/internal/eventstore/store.go`
- [X] T012 [US1] Implement bounded POST `/events` ingestion with commit-before-acknowledgement, duplicate receipts, validation errors, and `503 Retry-After` in `submission/internal/httpapi/events.go` and register it in `submission/internal/httpapi/server.go`
- [X] T013 [US1] Wire configuration, pgx, HTTP lifecycle, and graceful API shutdown in `submission/cmd/api/main.go`
- [X] T014 [US1] Hand the Stage 1 checklist in `submission/specs/001-streaming-event-backend/quickstart.md` to the user and wait for explicit confirmation before T015

**Checkpoint**: User Story 1 is independently usable as the durable-ingestion MVP.

---

## Phase 3: User Story 2A — Device Health (Priority: P1)

**Goal**: Return the latest event-time heartbeat and correct five-minute availability.

**Independent Test**: Insert heartbeat histories in different arrival orders, including late and future events, and verify the latest heartbeat never moves backward and availability equals distinct in-window heartbeats divided by 300.

### Focused tests

- [X] T015 [P] [US2] Add PostgreSQL tests for heartbeat ordering, late-window contribution, future exclusion, and ingestion idempotency in `submission/internal/features/health/service_test.go`
- [X] T016 [P] [US2] Add GET `/devices/{device_id}/health` compatibility and unknown-device tests in `submission/internal/httpapi/health_test.go`

### Implementation

- [X] T017 [US2] Define device-health values and response mapping in `submission/internal/features/health/model.go`
- [X] T018 [US2] Implement indexed latest-heartbeat and five-minute heartbeat queries in `submission/internal/features/health/store.go`
- [X] T019 [US2] Implement health calculation and not-found rules in `submission/internal/features/health/service.go`
- [X] T020 [US2] Align the source tree with the single API runtime and keep feature packages limited to query behavior
- [X] T021 [US2] Add structured API lifecycle and request logging with configurable log level in `submission/internal/config/config.go`, `submission/cmd/api/main.go`, `submission/deployment/compose.yaml`, and `submission/internal/httpapi/server.go`
- [X] T022 [US2] Implement and register GET `/devices/{device_id}/health` in `submission/internal/httpapi/health.go` and `submission/internal/httpapi/server.go`
- [X] T023 [US2] Hand the Stage 2 checklist in `submission/specs/001-streaming-event-backend/quickstart.md` to the user and wait for explicit confirmation before T024

**Checkpoint**: The health half of User Story 2 is correct and manually confirmed.

---

## Phase 4: User Story 2B — Room Occupancy (Priority: P1)

**Goal**: Return deterministic current room state and occupied percentage for all required windows.

**Independent Test**: Process presence transitions in different orders, including equal timestamps and late replay, and compare one-minute, five-minute, and one-hour results with the same history ordered by `(ts, device_id, seq)`.

### Focused tests

- [X] T024 [P] [US2] Add PostgreSQL tests for not found before the first applicable presence event, pre-first-event window handling, deterministic ties, concurrent late-event correction for one room, future exclusion, and all three windows in `submission/internal/features/occupancy/service_test.go`
- [X] T025 [P] [US2] Add GET `/rooms/{room_id}/occupancy` contract tests for supported windows, invalid windows, and unknown rooms in `submission/internal/httpapi/occupancy_test.go`

### Implementation

- [X] T026 [US2] Define room-occupancy values and supported windows in `submission/internal/features/occupancy/model.go`
- [X] T027 [US2] Implement indexed current-state and occupied-duration queries using the state at the window start in `submission/internal/features/occupancy/store.go`
- [X] T028 [US2] Implement occupancy percentage and not-found rules in `submission/internal/features/occupancy/service.go`
- [X] T029 [US2] Record the Stage 3 architecture refinement in `submission/docs/implementation-decisions.md` and align the feature artifacts
- [X] T030 [US2] Implement and register GET `/rooms/{room_id}/occupancy` in `submission/internal/httpapi/occupancy.go` and `submission/internal/httpapi/server.go`
- [X] T031 [US2] Hand the Stage 3 checklist in `submission/specs/001-streaming-event-backend/quickstart.md` to the user and wait for explicit confirmation before T032

**Checkpoint**: User Story 2 is complete and manually confirmed.

---

## Phase 5: User Story 3 — Distinct Fall Alarms (Priority: P1)

**Goal**: Persist and publish each logical fall promptly, with history-based recovery after disconnection.

**Independent Test**: Submit concurrent distinct falls and same-device/room warnings within three event-time seconds during an ingestion burst; verify one logical alarm per deduplication group, per-room creation order, original timestamps, sub-second p95 delivery, and inclusive history recovery.

### Focused tests

- [X] T032 [P] [US3] Add PostgreSQL tests for concurrent first-warning-anchored three-second deduplication, including a `0s/2s/4s` chain producing two alarms, room ordering, late/future immediate creation, stable IDs, and commit-before-publication behavior in `submission/internal/features/alarms/service_test.go`
- [X] T033 [P] [US3] Add GET `/alarms` and SSE tests for inclusive `created_at`, framing, per-room order, disconnect recovery, and boundary duplicates in `submission/internal/httpapi/alarms_test.go`

### Implementation

- [X] T034 [US3] Define logical-alarm values and API mapping in `submission/internal/features/alarms/model.go`
- [X] T035 [US3] Implement room advisory locking, first-warning-anchored three-second device/room lookup, alarm insertion, and inclusive history in `submission/internal/features/alarms/store.go`
- [X] T036 [US3] Implement the atomic fall-ingest transaction and alarm-history operations in `submission/internal/features/alarms/service.go`
- [X] T037 [US3] Route fall warnings through the alarm service while preserving normal-event ingestion behavior in `submission/internal/httpapi/events.go`
- [X] T038 [US3] Implement the in-process alarm broadcaster with post-commit wake-up and durable-order reads in `submission/internal/features/alarms/feed.go`
- [X] T039 [US3] Implement GET `/alarms` and GET `/alarms/stream` with SSE flushing and cancellation in `submission/internal/httpapi/alarms.go` and register both in `submission/internal/httpapi/server.go`
- [X] T040 [US3] Wire alarm storage and the in-process broadcaster into `submission/cmd/api/main.go`
- [X] T041 [US3] Hand the Stage 4 checklist in `submission/specs/001-streaming-event-backend/quickstart.md` to the user and wait for explicit confirmation before T042

**Checkpoint**: User Story 3 is complete and manually confirmed.

---

## Phase 6: User Story 4 — Pressure and Restart Correctness (Priority: P2)

**Goal**: Preserve correctness under burst load, retries, process failure, and restart.

**Independent Test**: Run baseline, burst, offline replay, capacity exhaustion, and hard-restart scenarios; compare durable events, query results, alarms, recovery behavior, and latency with ground truth.

### Focused tests

- [ ] T042 [P] [US4] Add saturation and retry-storm tests proving bounded admission, explicit `503 Retry-After`, and no false acknowledgement in `submission/internal/httpapi/backpressure_test.go`
- [ ] T043 [P] [US4] Add failure-boundary checks for API commit-before-response, alarm commit-before-publication, persistent event history, and migration compatibility across restart in `submission/test/load/restart.sh`

### Implementation and verification

- [ ] T044 [US4] Verify health and occupancy are immediately reconstructed from committed event history after restart in `submission/test/load/restart.sh`
- [ ] T045 [US4] Tune the API pgx pool, request admission, and ingest deadlines through `submission/internal/config/config.go` and `submission/cmd/api/main.go`
- [ ] T046 [P] [US4] Implement the required counters, latency summaries, and saturation reporting in the owning packages, with HTTP export in `submission/internal/httpapi/metrics.go`; add shared metric plumbing only if the implementation requires it
- [ ] T047 [US4] Add structured ingest, query, alarm, and shutdown instrumentation in `submission/internal/httpapi/events.go`, the health and occupancy packages, and `submission/internal/features/alarms/service.go`
- [ ] T048 [P] [US4] Implement a five-minute 5,000-events/second baseline and two 30-second 50,000-events/second bursts, then verify alarm latency and health and occupancy results against accepted-event ground truth in `submission/test/load/main.go`
- [ ] T049 [P] [US4] Implement the hard-kill, restart-order, persistent-volume, and query-recovery scenario in `submission/test/load/restart.sh`
- [ ] T050 [US4] Add final integration, race, load, restart, and evaluator targets to `submission/Makefile`
- [ ] T051 [US4] Run the automated Stage 5 targets from `submission/Makefile`, including ingestion and query verification for a 5,001st device, then prepare the metrics and recovery results for user review

**Checkpoint**: All four user stories meet the assignment's correctness and performance conditions.

---

## Phase 7: Final Consistency

**Purpose**: Verify the completed submission without expanding scope or rewriting approved documents.

- [ ] T052 Run formatting, vetting, focused tests, and the race detector across `submission/`, fixing only implementation defects found by those checks
- [ ] T053 Verify endpoint behavior against `submission/docs/api/openapi.yml` and prepare every applicable scenario in `submission/specs/001-streaming-event-backend/quickstart.md` for final user testing without changing decision records unless separately approved
- [ ] T054 Hand the final Stage 5 checklist in `submission/specs/001-streaming-event-backend/quickstart.md` and the results from T051–T053 to the user, then wait for explicit final confirmation

---

## Dependencies and Execution Order

### Phase dependencies

```text
Setup
  └─► US1 Ingestion ── manual gate
       └─► US2 Health ── manual gate
            └─► US2 Occupancy ── manual gate
                 └─► US3 Alarms ── manual gate
                      └─► US4 Pressure/Restart
                           └─► Final consistency ── final manual gate
```

- Setup has no dependencies.
- The accepted manual workflow is sequential even where feature code could otherwise be parallel.
- Each gate requires explicit user confirmation before the following phase starts.

### User-story dependencies

- **US1** depends only on Setup and is the MVP.
- **US2 Health** depends on US1's durable indexed events.
- **US2 Occupancy** depends on US1's durable indexed events.
- **US3** depends on US1 ingestion.
- **US4** validates and tunes all preceding stories together.

### Within each story

1. Add only the focused tests named for README or ADR risks.
2. Implement models and persistence.
3. Implement feature behavior and transaction boundaries.
4. Add HTTP integration.
5. Run the quickstart checks and stop at the manual gate.

## Parallel Opportunities

- **US1**: T005, T006, and T007 can be authored in parallel; after them, T008 and T009 touch independent files.
- **US2 Health**: T015 and T016 can be authored in parallel.
- **US2 Occupancy**: T024 and T025 can be authored in parallel.
- **US3**: T032 and T033 can be authored in parallel.
- **US4**: T042 and T043 can run in parallel; T048 and T049 can be implemented in parallel after the system behavior is complete.
- Do not use cross-phase parallelism to bypass a manual confirmation gate.

### Parallel example: US1

```text
T005: Event validation and timestamp-boundary tests
T006: HTTP ingestion contract and backpressure tests
T007: Transactional persistence and idempotency tests
```

### Parallel example: US2

```text
T015: Health event-history query tests
T016: Health HTTP contract tests

After the health gate:
T024: Occupancy persistence and ordering tests
T025: Occupancy HTTP contract tests
```

### Parallel example: US3

```text
T032: Alarm transaction and deduplication tests
T033: Alarm history and SSE tests
```

### Parallel example: US4

```text
T042: Backpressure and retry-storm tests
T043: Restart transaction-boundary tests

After behavior is stable:
T048: Load runner
T049: Restart runner
```

## Implementation Strategy

### MVP first

1. Complete Setup.
2. Complete US1 through T014.
3. Stop and manually verify durable ingestion before adding queries.

### Incremental delivery

1. Durable ingestion and one initial schema.
2. Device health, followed by manual confirmation.
3. Room occupancy, followed by manual confirmation.
4. Fall alarms and live delivery, followed by manual confirmation.
5. Pressure, restart, observability, and final consistency checks.
6. Final manual confirmation after every automated check is complete.

## Notes

- `[P]` means different files and no dependency on another incomplete task in the same group.
- Task labels map directly to the specification user stories.
- The initial migration is owned by US1 and creates the complete known schema once.
- Files and folders are added only when their task requires them.
- ADRs remain unchanged as pre-build records. Approved implementation refinements are recorded in
  `submission/docs/implementation-decisions.md` and reflected in the active specification.
