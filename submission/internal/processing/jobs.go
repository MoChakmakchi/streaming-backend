package processing

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"teton/internal/event"
	"teton/internal/eventstore"
)

type ProjectionArgs struct {
	EventID int64 `json:"event_id"`
}

func (ProjectionArgs) Kind() string { return "project_event" }

type IngestResult struct {
	EventID  int64
	Inserted bool
}

type Ingestor struct {
	pool        *pgxpool.Pool
	events      *eventstore.Store
	riverClient *river.Client[pgx.Tx]
}

func NewIngestor(pool *pgxpool.Pool, events *eventstore.Store, riverClient *river.Client[pgx.Tx]) *Ingestor {
	return &Ingestor{pool: pool, events: events, riverClient: riverClient}
}

func (i *Ingestor) Ingest(ctx context.Context, input event.Event, receivedAt time.Time) (IngestResult, error) {
	tx, err := i.pool.Begin(ctx)
	if err != nil {
		return IngestResult{}, fmt.Errorf("begin ingest transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	inserted, err := i.events.Insert(ctx, tx, input, receivedAt)
	if err != nil {
		return IngestResult{}, err
	}
	if inserted.Inserted && (input.Type == event.TypeHeartbeat || input.Type == event.TypePresence) {
		var options *river.InsertOpts
		if input.Time.After(receivedAt) {
			options = &river.InsertOpts{ScheduledAt: input.Time}
		}
		if _, err := i.riverClient.InsertTx(ctx, tx, ProjectionArgs{EventID: inserted.EventID}, options); err != nil {
			return IngestResult{}, fmt.Errorf("enqueue projection: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return IngestResult{}, fmt.Errorf("commit ingest transaction: %w", err)
	}
	return IngestResult(inserted), nil
}
