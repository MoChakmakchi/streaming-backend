package occupancy

import (
	"errors"
	"time"
)

var ErrInvalidWindow = errors.New("invalid occupancy window")

type Occupancy struct {
	InRoom        bool    `json:"in_room"`
	OccupiedPct   float64 `json:"occupied_pct"`
	WindowSeconds int     `json:"window_seconds"`
}

func ParseWindow(value string) (time.Duration, error) {
	switch value {
	case "1m":
		return time.Minute, nil
	case "5m":
		return 5 * time.Minute, nil
	case "1h":
		return time.Hour, nil
	default:
		return 0, ErrInvalidWindow
	}
}
