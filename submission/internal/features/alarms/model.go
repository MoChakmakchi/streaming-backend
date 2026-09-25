package alarms

import "time"

type Alarm struct {
	EventID    int64     `json:"event_id"`
	RoomID     string    `json:"room_id"`
	DeviceID   string    `json:"device_id"`
	EventTime  time.Time `json:"ts"`
	Confidence float64   `json:"confidence"`
	CreatedAt  time.Time `json:"created_at"`
}

type cursor struct {
	CreatedAt time.Time
	EventID   int64
}
