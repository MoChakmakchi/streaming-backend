# ADR 0002: Separate Event Reception from Processing

**Status:** Accepted  
**Date:** 2026-09-22

## Decision

Event reception and processing will be logically separate.

The receiver will validate events, hand accepted events to durable storage, and return quickly. It will only confirm acceptance after the event has been stored successfully. Fall alarms are deduplicated and persisted as part of this ingest transaction.

Workers will process heartbeat and presence jobs and update their derived state. The query API will read persisted state and publish persisted alarms without performing background event processing itself.

This keeps ingestion responsive during bursts and prevents processing delays from blocking request handling or queries. The receiver and query API run in the API process, while background processing runs in the worker process. Both remain part of one application and share PostgreSQL.
