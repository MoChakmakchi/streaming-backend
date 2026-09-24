package health

import "time"

type Health struct {
	LastHeartbeatAt time.Time `json:"last_heartbeat_ts"`
	Availability5m  float64   `json:"availability_5m"`
}
