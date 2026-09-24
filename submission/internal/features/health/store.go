package health

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type store struct {
	pool *pgxpool.Pool
}

func (s *store) snapshot(ctx context.Context, deviceID string, queryTime time.Time) (time.Time, int64, error) {
	var lastHeartbeat time.Time
	var heartbeatCount int64
	err := s.pool.QueryRow(ctx, `
		WITH latest AS (
			SELECT event_time
			FROM events
			WHERE device_id = $1
			  AND event_type = 'heartbeat'
			  AND event_time <= $2::timestamptz
			ORDER BY event_time DESC, seq DESC
			LIMIT 1
		)
		SELECT latest.event_time, count(e.seq)
		FROM latest
		LEFT JOIN events e
		  ON e.device_id = $1
		 AND e.event_type = 'heartbeat'
		 AND e.event_time > $2::timestamptz - interval '5 minutes'
		 AND e.event_time <= $2::timestamptz
		GROUP BY latest.event_time`,
		deviceID,
		queryTime,
	).Scan(&lastHeartbeat, &heartbeatCount)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("query device health: %w", err)
	}
	return lastHeartbeat, heartbeatCount, nil
}
