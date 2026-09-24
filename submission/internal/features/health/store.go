package health

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"teton/internal/event"
)

type store struct {
	pool *pgxpool.Pool
}

func (s *store) upsert(ctx context.Context, tx pgx.Tx, heartbeat event.Event) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO device_health (
			device_id, room_id, event_id, last_heartbeat_at, last_seq, updated_at
		) VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT (device_id) DO UPDATE SET
			room_id = EXCLUDED.room_id,
			event_id = EXCLUDED.event_id,
			last_heartbeat_at = EXCLUDED.last_heartbeat_at,
			last_seq = EXCLUDED.last_seq,
			updated_at = now()
		WHERE (EXCLUDED.last_heartbeat_at, EXCLUDED.last_seq) >
		      (device_health.last_heartbeat_at, device_health.last_seq)`,
		heartbeat.DeviceID,
		heartbeat.RoomID,
		heartbeat.ID,
		heartbeat.Time,
		heartbeat.Sequence,
	)
	return err
}

func (s *store) snapshot(ctx context.Context, deviceID string, queryTime time.Time) (time.Time, int64, error) {
	var lastHeartbeat time.Time
	var heartbeatCount int64
	err := s.pool.QueryRow(ctx, `
		SELECT h.last_heartbeat_at, count(e.id)
		FROM device_health h
		LEFT JOIN events e
		  ON e.device_id = h.device_id
		 AND e.event_type = 'heartbeat'
		 AND e.event_time > $2::timestamptz - interval '5 minutes'
		 AND e.event_time <= $2::timestamptz
		WHERE h.device_id = $1
		  AND h.last_heartbeat_at <= $2::timestamptz
		GROUP BY h.last_heartbeat_at`,
		deviceID,
		queryTime,
	).Scan(&lastHeartbeat, &heartbeatCount)
	return lastHeartbeat, heartbeatCount, err
}
