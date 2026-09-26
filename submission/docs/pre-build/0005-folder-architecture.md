# ADR 0005: Folder Architecture

**Status:** Accepted  
**Date:** 2026-09-22

## Decision

We will use a modular Go application with separate API and worker executables:

```text
.
├── submission/                  # Self-contained submission
│   ├── cmd/                     # Executable entry points
│   │   ├── api/
│   │   │   └── main.go          # Starts ingestion, queries, and alarm feed
│   │   └── worker/
│   │       └── main.go          # Starts background event processing
│   ├── internal/                # Private application packages
│   │   ├── config/              # Shared configuration
│   │   ├── event/               # Event model and validation
│   │   ├── eventstore/          # PostgreSQL-backed event persistence
│   │   ├── httpapi/             # HTTP ingestion, queries, and SSE feed
│   │   ├── processing/          # Heartbeat and presence job processing
│   │   └── features/            # Independently owned business capabilities
│   │       ├── health/
│   │       ├── occupancy/
│   │       └── alarms/          # Synchronous deduplication and persisted feed state
│   ├── migrations/
│   ├── docs/
│   │   └── adr/                 # Accepted architecture decisions
│   ├── test/
│   │   └── load/
│   ├── Makefile                 # Submission development commands
│   ├── deployment/              # Local container definitions
│   │   ├── compose.yaml
│   │   ├── Dockerfile.api       # API image
│   │   └── Dockerfile.worker    # Worker image
│   └── go.mod
└── Makefile                     # Original assignment harness
```

`cmd/api` runs event ingestion, queries, and the alarm feed. `cmd/worker` runs background event processing. Both entry points remain small and only assemble components.

The health, occupancy, and alarm packages own their logic and persistence. They do not import one another. PostgreSQL event persistence lives in `eventstore`, while River remains an implementation detail of `processing`.

Feature packages follow this structure where needed:

```text
internal/features/<feature>/
├── model.go                     # Feature-owned data types
├── service.go                   # Business rules and operations
├── store.go                     # PostgreSQL persistence
└── *_test.go                    # Tests beside the code they cover
```

Files are created only when the feature needs them. River workers and asynchronous event dispatch stay in `processing`. HTTP concerns stay in `httpapi`, which calls the alarm feature during fall ingestion. Transport and worker packages call feature services rather than containing feature business logic.

The structure is guidance, not a requirement to create every file and folder upfront. Add a layer only when it has a clear responsibility; if it remains too thin or provides no useful separation, merge or remove it.

Tests live beside the code they test, except load tests under `test/load`.

This structure keeps the application small while allowing the API and worker to be developed, deployed, and scaled independently.
