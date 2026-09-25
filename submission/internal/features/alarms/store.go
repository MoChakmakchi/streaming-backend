package alarms

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"teton/internal/event"
)

type store struct {
	pool *pgxpool.Pool
}

func (s *store) lockRoom(ctx context.Context, tx pgx.Tx, roomID string) error {
	if _, err := tx.Exec(
		ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		roomID,
	); err != nil {
		return fmt.Errorf("lock alarm room: %w", err)
	}
	return nil
}

func (s *store) insert(
	ctx context.Context,
	tx pgx.Tx,
	eventID int64,
	input event.Event,
) (Alarm, bool, error) {
	var alarm Alarm
	err := tx.QueryRow(ctx, `
		INSERT INTO alarms (
			source_event_id,
			device_id,
			room_id,
			event_time,
			confidence,
			created_at
		)
		SELECT $1, $2, $3, $4, $5, clock_timestamp()
		WHERE NOT EXISTS (
			SELECT 1
			FROM alarms
			WHERE device_id = $2
			  AND room_id = $3
			  AND event_time >= $4::timestamptz - interval '3 seconds'
			  AND event_time <= $4::timestamptz + interval '3 seconds'
		)
		RETURNING id, room_id, device_id, event_time, confidence, created_at`,
		eventID,
		input.DeviceID,
		input.RoomID,
		input.Time,
		*input.Confidence,
	).Scan(
		&alarm.EventID,
		&alarm.RoomID,
		&alarm.DeviceID,
		&alarm.EventTime,
		&alarm.Confidence,
		&alarm.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Alarm{}, false, nil
	}
	if err != nil {
		return Alarm{}, false, fmt.Errorf("insert alarm: %w", err)
	}
	return alarm, true, nil
}

func (s *store) list(ctx context.Context, since *time.Time) ([]Alarm, error) {
	query := `
		SELECT id, room_id, device_id, event_time, confidence, created_at
		FROM alarms`
	args := []any{}
	if since != nil {
		query += ` WHERE created_at >= $1`
		args = append(args, *since)
	}
	query += ` ORDER BY created_at, id`

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list alarms: %w", err)
	}
	defer rows.Close()

	alarms := []Alarm{}
	for rows.Next() {
		var alarm Alarm
		if err := rows.Scan(
			&alarm.EventID,
			&alarm.RoomID,
			&alarm.DeviceID,
			&alarm.EventTime,
			&alarm.Confidence,
			&alarm.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan alarm: %w", err)
		}
		alarms = append(alarms, alarm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate alarms: %w", err)
	}
	return alarms, nil
}

func (s *store) listRoomAfter(
	ctx context.Context,
	roomID string,
	after cursor,
) ([]Alarm, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, room_id, device_id, event_time, confidence, created_at
		FROM alarms
		WHERE room_id = $1
		  AND (created_at, id) > ($2, $3)
		ORDER BY created_at, id`,
		roomID,
		after.CreatedAt,
		after.EventID,
	)
	if err != nil {
		return nil, fmt.Errorf("list unpublished alarms: %w", err)
	}
	defer rows.Close()

	alarms := []Alarm{}
	for rows.Next() {
		var alarm Alarm
		if err := rows.Scan(
			&alarm.EventID,
			&alarm.RoomID,
			&alarm.DeviceID,
			&alarm.EventTime,
			&alarm.Confidence,
			&alarm.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan unpublished alarm: %w", err)
		}
		alarms = append(alarms, alarm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate unpublished alarms: %w", err)
	}
	return alarms, nil
}

func (s *store) latestCursors(ctx context.Context) (map[string]cursor, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (room_id) room_id, created_at, id
		FROM alarms
		ORDER BY room_id, created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("read latest alarm cursors: %w", err)
	}
	defer rows.Close()

	cursors := map[string]cursor{}
	for rows.Next() {
		var roomID string
		var latest cursor
		if err := rows.Scan(&roomID, &latest.CreatedAt, &latest.EventID); err != nil {
			return nil, fmt.Errorf("scan latest alarm cursor: %w", err)
		}
		cursors[roomID] = latest
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate latest alarm cursors: %w", err)
	}
	return cursors, nil
}
