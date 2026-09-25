package alarms

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"teton/internal/event"
	"teton/internal/eventstore"
)

type Service struct {
	pool  *pgxpool.Pool
	store store
	feed  *Feed
}

func NewService(pool *pgxpool.Pool, feed *Feed) *Service {
	return &Service{
		pool:  pool,
		store: store{pool: pool},
		feed:  feed,
	}
}

func (s *Service) Ingest(
	ctx context.Context,
	input event.Event,
	receivedAt time.Time,
) (eventstore.InsertResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return eventstore.InsertResult{}, fmt.Errorf("begin fall ingest transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := s.store.lockRoom(ctx, tx, input.RoomID); err != nil {
		return eventstore.InsertResult{}, err
	}
	result, err := eventstore.Insert(ctx, tx, input, receivedAt)
	if err != nil {
		return eventstore.InsertResult{}, err
	}

	created := false
	if result.Inserted {
		_, created, err = s.store.insert(ctx, tx, result.EventID, input)
		if err != nil {
			return eventstore.InsertResult{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return eventstore.InsertResult{}, fmt.Errorf("commit fall ingest transaction: %w", err)
	}
	if created {
		s.feed.Wake(input.RoomID)
	}
	return result, nil
}

func (s *Service) List(ctx context.Context, since *time.Time) ([]Alarm, error) {
	return s.store.list(ctx, since)
}
