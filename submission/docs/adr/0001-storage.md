# ADR 0001: Storage

**Status:** Accepted  
**Date:** 2026-09-22

## SQL vs. NoSQL

We will use SQL.

The service needs strong consistency, durable writes, deduplication, and reliable recovery after a restart. SQL gives us transactions and unique constraints, which make those requirements simpler to implement and reason about.

The expected load is manageable with batching and partitioning. NoSQL would add complexity without a clear benefit for this use case.

## Database choice

We will use PostgreSQL. It provides durable transactional storage, handles concurrent writes well, and has straightforward support for constraints, indexing, and recovery after process restarts.

MariaDB and MySQL could also meet the persistence requirements. PostgreSQL was preferred because we are more familiar with it, and the assignment gives no specific reason to choose them instead.

## Event storage

Accepted events will be stored in an append-only PostgreSQL table. This table is the durable event log and supports replay, recovery, and investigation without introducing another storage system.
