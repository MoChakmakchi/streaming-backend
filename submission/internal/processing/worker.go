package processing

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"teton/internal/event"
	"teton/internal/eventstore"
	"teton/internal/features/health"
)

type ProjectionWorker struct {
	river.WorkerDefaults[ProjectionArgs]

	pool   *pgxpool.Pool
	events *eventstore.Store
	health *health.Service
}

func NewProjectionWorker(pool *pgxpool.Pool, events *eventstore.Store, healthService *health.Service) *ProjectionWorker {
	return &ProjectionWorker{pool: pool, events: events, health: healthService}
}

func (w *ProjectionWorker) Work(ctx context.Context, job *river.Job[ProjectionArgs]) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin projection transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	source, err := w.events.GetTx(ctx, tx, job.Args.EventID)
	if err != nil {
		return fmt.Errorf("load source event: %w", err)
	}

	switch source.Type {
	case event.TypeHeartbeat:
		if err := w.health.Project(ctx, tx, source); err != nil {
			return fmt.Errorf("project health: %w", err)
		}
	case event.TypePresence:
		return river.JobSnooze(time.Minute)
	default:
		return fmt.Errorf("unsupported projection event type %q", source.Type)
	}

	if _, err := river.JobCompleteTx[*riverpgxv5.Driver](ctx, tx, job); err != nil {
		return fmt.Errorf("complete projection job: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit projection transaction: %w", err)
	}
	return nil
}
