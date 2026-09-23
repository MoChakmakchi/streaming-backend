# ADR 0004: Event Processing Buffer

**Status:** Accepted  
**Date:** 2026-09-22

## Decision

We will use River to coordinate asynchronous event processing.

The receiver will store each event in the PostgreSQL event log. Events that affect asynchronous projections are given a River job in the same transaction. River provides durable jobs, retries, and worker coordination while reusing PostgreSQL, so no additional infrastructure is required.

Heartbeat and presence events use River. Fall alarms are created during ingestion to keep River backlog out of the alarm-latency path. Motion, sleep-state, and network-status events are retained in the event log but do not need processing jobs for the required outputs.

## Limitation

PostgreSQL is shared by event ingestion, River workers, and query state, so it may become a bottleneck under burst load. We will monitor database throughput, queue depth, and alarm latency.

We will not use write-behind memory buffers or cache-first persistence because accepted events must be durable and alarms must remain within the one-second delivery target.

## Alternatives considered

### In-memory queue

This is the simplest option, but queued events would be lost if the process crashes. It does not meet the recovery requirements.

### RabbitMQ

RabbitMQ provides a mature durable queue, but adds another service to operate. Its queue model is also less useful than an event log when events must be replayed.

### Kafka

Kafka is the strongest fit for a large-scale production event stream. It provides durable retention, replay, partitioning, and independent consumers, but adds significant operational complexity for this assignment.

If throughput or independent scaling later exceeds the PostgreSQL and River approach, Kafka is the likely replacement.
