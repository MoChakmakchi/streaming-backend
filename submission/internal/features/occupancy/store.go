package occupancy

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type store struct {
	pool *pgxpool.Pool
}

func (s *store) snapshot(
	ctx context.Context,
	roomID string,
	windowStart time.Time,
	queryTime time.Time,
) (bool, float64, error) {
	var inRoom bool
	var occupiedSeconds float64
	err := s.pool.QueryRow(ctx, `
		WITH initial AS (
			SELECT event_time, device_id, seq, (payload->>'in_room')::boolean AS in_room
			FROM events
			WHERE room_id = $1
			  AND event_type = 'presence'
			  AND event_time <= $2::timestamptz
			ORDER BY event_time DESC, device_id DESC, seq DESC
			LIMIT 1
		), transitions AS (
			SELECT event_time, device_id, seq, (payload->>'in_room')::boolean AS in_room
			FROM events
			WHERE room_id = $1
			  AND event_type = 'presence'
			  AND event_time > $2::timestamptz
			  AND event_time <= $3::timestamptz
		), timeline AS (
			SELECT * FROM initial
			UNION ALL
			SELECT * FROM transitions
		), current_state AS (
			SELECT in_room
			FROM timeline
			ORDER BY event_time DESC, device_id DESC, seq DESC
			LIMIT 1
		), segments AS (
			SELECT
				in_room,
				GREATEST(event_time, $2::timestamptz) AS started_at,
				LEAD(event_time, 1, $3::timestamptz) OVER (
					ORDER BY event_time, device_id, seq
				) AS ended_at
			FROM timeline
		)
		SELECT current_state.in_room, COALESCE((
			SELECT SUM(EXTRACT(EPOCH FROM (ended_at - started_at)))
			FROM segments
			WHERE in_room
		), 0)::double precision
		FROM current_state`,
		roomID,
		windowStart,
		queryTime,
	).Scan(&inRoom, &occupiedSeconds)
	if err != nil {
		return false, 0, fmt.Errorf("query room occupancy: %w", err)
	}
	return inRoom, occupiedSeconds, nil
}
