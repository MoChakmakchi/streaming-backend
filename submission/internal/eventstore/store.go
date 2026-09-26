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

const insertEventSQL = `
	INSERT INTO events (device_id, room_id, event_type, event_time, seq, payload, received_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7)
	ON CONFLICT (device_id, seq) DO NOTHING
	RETURNING id`

type Store struct {
	pool *pgxpool.Pool
}

type queryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
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
	return Insert(ctx, s.pool, input, receivedAt)
}

func Insert(
	ctx context.Context,
	db queryRower,
	input event.Event,
	receivedAt time.Time,
) (InsertResult, error) {
	payload, err := input.Payload()
	if err != nil {
		return InsertResult{}, fmt.Errorf("encode event payload: %w", err)
	}

	var eventID int64
	err = db.QueryRow(ctx, insertEventSQL,
		input.DeviceID, input.RoomID, input.Type, input.Time, input.Sequence, payload, receivedAt,
	).Scan(&eventID)
	if err == nil {
		return InsertResult{EventID: eventID, Inserted: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return InsertResult{}, fmt.Errorf("insert event: %w", err)
	}
	return InsertResult{}, nil
}
