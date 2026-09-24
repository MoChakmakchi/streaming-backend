package eventstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"teton/internal/event"
)

type Store struct {
	pool *pgxpool.Pool
}

type InsertResult struct {
	EventID  int64
	Inserted bool
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Ingest(
	ctx context.Context,
	input event.Event,
	receivedAt time.Time,
) (InsertResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return InsertResult{}, fmt.Errorf("begin ingest transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	result, err := s.insert(ctx, tx, input, receivedAt)
	if err != nil {
		return InsertResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return InsertResult{}, fmt.Errorf("commit ingest transaction: %w", err)
	}
	return result, nil
}

func (s *Store) insert(
	ctx context.Context,
	tx pgx.Tx,
	input event.Event,
	receivedAt time.Time,
) (InsertResult, error) {
	payload, err := input.Payload()
	if err != nil {
		return InsertResult{}, fmt.Errorf("encode event payload: %w", err)
	}

	var eventID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO events (device_id, room_id, event_type, event_time, seq, payload, received_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (device_id, seq) DO NOTHING
		RETURNING id`,
		input.DeviceID, input.RoomID, input.Type, input.Time, input.Sequence, payload, receivedAt,
	).Scan(&eventID)
	if err == nil {
		return InsertResult{EventID: eventID, Inserted: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return InsertResult{}, fmt.Errorf("insert event: %w", err)
	}
	if err := tx.QueryRow(ctx,
		`SELECT id FROM events WHERE device_id = $1 AND seq = $2`,
		input.DeviceID, input.Sequence,
	).Scan(&eventID); err != nil {
		return InsertResult{}, fmt.Errorf("read duplicate event: %w", err)
	}
	return InsertResult{EventID: eventID}, nil
}
