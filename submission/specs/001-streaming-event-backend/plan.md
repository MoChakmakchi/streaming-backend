# Implementation Plan: Real-Time Streaming Event Backend

**Branch**: `001-streaming-event-backend` | **Date**: 2026-09-23 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `submission/specs/001-streaming-event-backend/spec.md`

## Summary

Build a self-contained Go service under `submission/`. The API validates and durably stores events
in PostgreSQL, derives health and occupancy from indexed event history, creates fall alarms
synchronously, and serves queries and SSE. PostgreSQL remains the source of truth for events,
alarms, and restart recovery.

Implementation is delivered in five user-approved stages. Each stage ends at a manual verification
gate and work does not continue until the user confirms the result.

## Technical Context

**Language/Version**: Go 1.27

**Primary Dependencies**: Chi v5 and pgx v5; Go standard library for JSON, SSE, logging, and HTTP lifecycle. Exact versions are pinned in `go.mod` and `go.sum`.

**Storage**: PostgreSQL 18 with persistent Docker storage

**Testing**: `go test`, `httptest`, `go test -race`, PostgreSQL integration tests, and the supplied generator/evaluator

**Target Platform**: Linux containers through Docker Compose; local macOS/Linux development with Adminer for direct database inspection

**Project Type**: HTTP service

**Performance Goals**: Sustain about 5,000 events/second; tolerate 50,000 events/second for 30-second bursts; deliver 95% of alarms within one second

**Constraints**: Commit before acknowledgement; deterministic event-time results; no silent loss; bounded database concurrency; retry-safe ingestion; persistent restart recovery

**Scale/Scope**: 5,000 active devices plus unseen devices without configuration; one assignment deployment, one PostgreSQL database, and one API process by default

## Constitution Check

### Pre-design gate

- **Lean implementation — PASS**: Only the accepted stack is used. SSE, JSON handling, lifecycle,
  and logging use the standard library. No generic repositories, speculative interfaces, or empty
  packages are planned.
- **Durable boundaries — PASS**: Events commit before acknowledgement. Fall-event storage, alarm
  deduplication, and alarm storage share one transaction; live publication occurs only after a
  successful commit.
- **Confirmed architecture — PASS**: The Stage 3 refinement is recorded in
  `implementation-decisions.md`; ADRs remain unchanged as pre-build records. No new service,
  database, cache, or broker is introduced.
- **Focused verification — PASS**: Tests cover README behavior and ADR risks: ingestion
  idempotency, ordering, late events, concurrent fall deduplication, restart recovery,
  backpressure, and alarm latency.
- **Protected records — PASS**: ADRs remain untouched; the approved implementation refinement is
  recorded separately.

### Post-design gate

**PASS.** The data model, API contract, and validation guide preserve all five principles. No
complexity exception is required.

## Project Structure

### Documentation (this feature)

```text
submission/specs/001-streaming-event-backend/
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
│   └── api/
│       └── main.go                 # API wiring and lifecycle
├── internal/
│   ├── config/                     # Environment configuration
│   ├── event/                      # Event model and boundary validation
│   ├── eventstore/                 # Append-only PostgreSQL event persistence
│   ├── httpapi/                    # HTTP routes, handlers, and SSE transport
│   └── features/
│       ├── health/                 # Health query
│       ├── occupancy/              # Occupancy query
│       └── alarms/                 # Fall deduplication, history, and live delivery
├── migrations/                         # Application SQL migrations
├── docs/
│   ├── adr/                        # Existing accepted decisions
│   ├── implementation-decisions.md # Refinements found during implementation
│   └── api/
│       └── openapi.yml             # Published API contract
├── test/
│   └── load/                       # Focused burst and alarm-latency checks
├── Makefile
├── deployment/
│   ├── compose.yaml
│   └── Dockerfile.api                # API image
└── go.mod
```

**Structure Decision**: Use the approved modular Go layout from ADR 0005. Add files only when the
owning stage needs them; do not pre-create every illustrated file. Tests stay beside the code they
exercise, except load scenarios under `submission/test/load/`.

Configuration is added with its first concrete consumer: API and database settings during
ingestion and load-related tuning during the final pressure stage. Do not predefine configuration
fields or helpers for later stages.

## Data and Transaction Design

- Store every accepted event once in an append-only `events` table with a unique
  `(device_id, seq)` constraint.
- Calculate latest health, current occupancy, and rolling results from indexed event history.
  Query cutoffs exclude accepted future events, while late events correct the next query without
  repair work or stored time buckets.
- For fall warnings, take a transaction-scoped PostgreSQL advisory lock for the room, store the raw
  event, check the three-second device-and-room window anchored to each existing alarm's source
  warning, and create at most one alarm. Duplicate warnings do not extend the window.
- After commit, signal the API process's SSE broadcaster, which reads unseen alarm rows in durable
  order before publishing them. A reconnecting `/alarms/stream?since=<ts>` subscriber is registered
  before persisted alarms are replayed, closing the history/live race while stable IDs suppress
  overlap. `GET /alarms?since=<ts>` remains available for standalone history queries.

## Delivery Sequence and Manual Gates

### Stage 1 — Event ingestion and persistence

Create the submission scaffold, PostgreSQL setup, migrations, event validation, and append-only
event store. Verify valid, invalid, duplicate, late, future, and retryable-capacity cases. **Stop
for manual confirmation.**

### Stage 2 — Device health

Add indexed latest-heartbeat and five-minute availability queries with late/out-of-order checks.
**Stop for manual confirmation.**

### Stage 3 — Room occupancy

Add deterministic current state and one-minute, five-minute, and one-hour
occupied-duration queries that account for late transitions. **Stop for manual confirmation.**

### Stage 4 — Alarms

Add synchronous fall deduplication and persistence, alarm history, an in-process broadcaster, and
the SSE feed. Verify three-second deduplication, per-room order, gap-free reconnect catch-up,
commit-before-publication recovery, and one-second latency. **Stop for manual confirmation.**

### Stage 5 — Recovery, pressure, and final verification

Add bounded concurrency, retryable overload responses, required observability, hard-restart tests,
and baseline/burst/offline/adversarial validation. Confirm PostgreSQL migration behavior
and complete the submission run instructions. **Stop for final manual confirmation.**

## Migration and Operations Plan

- Apply the application SQL migration with `psql -v ON_ERROR_STOP=1` before the API starts;
  one idempotent initial migration is sufficient for the assignment.
- Use a bounded pgx pool so ingestion, queries, and alarm delivery retain connections under load.
- Keep PostgreSQL durability settings enabled and store its data on a named volume.
- Emit structured `slog` records and expose only the counters, latency summaries, and
  saturation signals required by FR-030; do not introduce a monitoring platform.

## Verification Strategy

- Prefer PostgreSQL-backed integration tests for transaction, ordering, retry, and restart risks.
- Keep handler tests limited to validation, status/error contracts, and SSE framing.
- Run concurrency-sensitive tests with the race detector.
- Use the supplied smoke evaluator for compatibility, but use dedicated load/restart scenarios for
  the requirements it does not cover.
- Do not add broad unit-test coverage or mock frameworks during the initial implementation.
