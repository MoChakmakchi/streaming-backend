# Implementation Plan: Real-Time Streaming Event Backend

**Branch**: `001-streaming-event-backend` | **Date**: 2026-09-23 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `specs/001-streaming-event-backend/spec.md`

## Summary

Build a self-contained Go service under `submission/` with separate API and worker binaries. The
API validates and durably stores events in PostgreSQL, creates fall alarms synchronously, and
serves queries and SSE. River jobs process heartbeat and presence projections. PostgreSQL remains
the source of truth for raw events, projections, alarms, jobs, and restart recovery.

Implementation is delivered in five user-approved stages. Each stage ends at a manual verification
gate and work does not continue until the user confirms the result.

## Technical Context

**Language/Version**: Go 1.27.1

**Primary Dependencies**: Chi v5.2.4, pgx v5.11.0, River v0.47.0 with `riverpgxv5`; Go standard library for JSON, SSE, logging, and HTTP lifecycle

**Storage**: PostgreSQL 17 with persistent Docker storage; River tables in the same database

**Testing**: `go test`, `httptest`, `go test -race`, PostgreSQL integration tests, and the supplied generator/evaluator

**Target Platform**: Linux containers through Docker Compose; local macOS/Linux development

**Project Type**: HTTP service with separate API and background-worker executables

**Performance Goals**: Sustain about 5,000 events/second; tolerate 50,000 events/second for 30-second bursts; deliver 95% of alarms within one second

**Constraints**: Commit before acknowledgement; deterministic event-time results; no silent loss; bounded database and worker concurrency; retry-safe processing; persistent restart recovery

**Scale/Scope**: 5,000 active devices plus unseen devices without configuration; one assignment deployment, one PostgreSQL database, one API process, and one worker process by default

## Constitution Check

### Pre-design gate

- **Lean implementation — PASS**: Only the accepted stack is used. SSE, JSON handling, lifecycle,
  and logging use the standard library. No generic repositories, speculative interfaces, or empty
  packages are planned.
- **Durable boundaries — PASS**: Event storage and River enqueue share a transaction; fall event
  and alarm storage share a transaction; projection update and River completion share a
  transaction.
- **Confirmed architecture — PASS**: The design follows ADRs 0001–0008 and the approved
  `submission/` application root. No new service, database, cache, or broker is introduced.
- **Focused verification — PASS**: Tests cover README behavior and ADR risks: ingestion
  idempotency, ordering, late events, concurrent fall deduplication, transactional River behavior,
  restart recovery, backpressure, and alarm latency.
- **Protected records — PASS**: Planning artifacts elaborate accepted decisions. ADR 0005 was
  changed only after explicit approval.

### Post-design gate

**PASS.** The data model, API contract, and validation guide preserve all five principles. No
complexity exception is required.

## Project Structure

### Documentation (this feature)

```text
specs/001-streaming-event-backend/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
└── tasks.md                 # Created later by $speckit-tasks
```

### Source Code (repository root)

```text
submission/
├── cmd/
│   ├── api/
│   │   └── main.go                 # API wiring and lifecycle
│   └── worker/
│       └── main.go                 # River worker wiring and lifecycle
├── internal/
│   ├── config/                     # Environment configuration used by both binaries
│   ├── event/                      # Event model and boundary validation
│   ├── eventstore/                 # Append-only PostgreSQL event persistence
│   ├── httpapi/                    # HTTP routes, handlers, and SSE transport
│   ├── processing/                 # River job registration and dispatch
│   └── features/
│       ├── health/                 # Health projection and query
│       ├── occupancy/              # Occupancy projection and query
│       └── alarms/                 # Fall deduplication, history, and live delivery
├── migrations/                         # Application SQL migrations
├── docs/
│   ├── adr/                        # Existing accepted decisions
│   └── api/
│       └── openapi.yml             # Published API contract
├── test/
│   └── load/                       # Focused burst and alarm-latency checks
├── Makefile
├── compose.yaml
├── Dockerfile
└── go.mod
```

**Structure Decision**: Use the approved modular Go layout from ADR 0005. Add files only when the
owning stage needs them; do not pre-create every illustrated file. Tests stay beside the code they
exercise, except load scenarios under `submission/test/load/`.

Configuration is added with its first concrete consumer: API and database settings during
ingestion, worker settings when the worker is introduced, and load-related tuning during the final
pressure stage. Do not predefine configuration fields or helpers for later stages.

## Data and Transaction Design

- Store every accepted event once in an append-only `events` table with a unique
  `(device_id, seq)` constraint.
- In the ingest transaction, enqueue one River job only for a newly inserted heartbeat or presence
  event. Schedule accepted future events for their event time. Motion, sleep-state, and network
  events receive no job.
- Keep only current health and occupancy state as projections. Calculate rolling availability and
  occupancy percentages from indexed event history so late events correct results without repair
  jobs or stored time buckets.
- For fall warnings, take a transaction-scoped PostgreSQL advisory lock for the room, store the raw
  event, check the three-second device-and-room window anchored to each existing alarm's source
  warning, create at most one alarm, and notify listeners in the same transaction. Duplicate
  warnings do not extend the window.
- Workers update a projection and complete its River job in the same PostgreSQL transaction.
  Conditional upserts make retries and out-of-order execution idempotent.
- Use `LISTEN/NOTIFY` only to wake live-feed readers. Alarm rows and inclusive
  `GET /alarms?since=<ts>` queries provide recovery.

## Delivery Sequence and Manual Gates

### Stage 1 — Event ingestion and persistence

Create the submission scaffold, PostgreSQL setup, migrations, event validation, append-only event
store, and transactional River enqueue. Verify valid, invalid, duplicate, late, future, and
retryable-capacity cases. **Stop for manual confirmation.**

### Stage 2 — Device health

Add heartbeat processing, idempotent latest-heartbeat projection, five-minute availability query,
and late/out-of-order checks. **Stop for manual confirmation.**

### Stage 3 — Room occupancy

Add presence processing, deterministic current state, and one-minute, five-minute, and one-hour
occupied-duration queries that account for late transitions. **Stop for manual confirmation.**

### Stage 4 — Alarms

Add synchronous fall deduplication and persistence, alarm history, PostgreSQL notification, and
the SSE feed. Verify three-second deduplication, per-room order, reconnect catch-up, missed
notifications, and one-second latency. **Stop for manual confirmation.**

### Stage 5 — Recovery, pressure, and final verification

Add bounded concurrency, retryable overload responses, required observability, hard-restart tests,
and baseline/burst/offline/adversarial validation. Confirm PostgreSQL and River migration behavior
and complete the submission run instructions. **Stop for final manual confirmation.**

## Migration and Operations Plan

- Apply the application SQL migration with `psql -v ON_ERROR_STOP=1` before either binary starts;
  one idempotent initial migration is sufficient for the assignment.
- Apply River migrations with the version-pinned River CLI before starting API or worker. Do not
  race migrations from both processes.
- Use separate bounded pgx pools for API and worker processes. Worker concurrency remains below
  its pool capacity so River maintenance and ingestion/query traffic retain connections.
- Keep PostgreSQL durability settings enabled and store its data on a named volume.
- Emit structured `slog` records and expose only the counters, latency summaries, backlog age, and
  saturation signals required by FR-030; do not introduce a monitoring platform.

## Verification Strategy

- Prefer PostgreSQL-backed integration tests for transaction, ordering, retry, and restart risks.
- Keep handler tests limited to validation, status/error contracts, and SSE framing.
- Run concurrency-sensitive tests with the race detector.
- Use the supplied smoke evaluator for compatibility, but use dedicated load/restart scenarios for
  the requirements it does not cover.
- Do not add broad unit-test coverage or mock frameworks during the initial implementation.
