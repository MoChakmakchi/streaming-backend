package eventstore

import (
	"context"
	"encoding/json"
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

func (s *Store) Insert(ctx context.Context, tx pgx.Tx, input event.Event, receivedAt time.Time) (InsertResult, error) {
	payload, err := input.Payload()
	if err != nil {
		return InsertResult{}, err
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

func (s *Store) Get(ctx context.Context, eventID int64) (event.Event, error) {
	return scanEvent(s.pool.QueryRow(ctx, `
		SELECT id, device_id, room_id, event_type, event_time, seq, payload, received_at
		FROM events WHERE id = $1`, eventID))
}

func (s *Store) GetTx(ctx context.Context, tx pgx.Tx, eventID int64) (event.Event, error) {
	return scanEvent(tx.QueryRow(ctx, `
		SELECT id, device_id, room_id, event_type, event_time, seq, payload, received_at
		FROM events WHERE id = $1`, eventID))
}

func scanEvent(row pgx.Row) (event.Event, error) {
	var result event.Event
	var payload []byte
	if err := row.Scan(
		&result.ID,
		&result.DeviceID,
		&result.RoomID,
		&result.Type,
		&result.Time,
		&result.Sequence,
		&payload,
		&result.ReceivedAt,
	); err != nil {
		return event.Event{}, err
	}

	fields := struct {
		InRoom     *bool    `json:"in_room"`
		Magnitude  *float64 `json:"magnitude"`
		SleepState *string  `json:"state"`
		Confidence *float64 `json:"confidence"`
		RSSI       *int     `json:"rssi"`
	}{}
	if err := json.Unmarshal(payload, &fields); err != nil {
		return event.Event{}, fmt.Errorf("decode event payload: %w", err)
	}
	result.InRoom = fields.InRoom
	result.Magnitude = fields.Magnitude
	result.SleepState = fields.SleepState
	result.Confidence = fields.Confidence
	result.RSSI = fields.RSSI
	return result, nil
}
