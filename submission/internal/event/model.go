package event

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

var (
	ErrInvalidJSON         = errors.New("invalid JSON")
	ErrInvalidEvent        = errors.New("invalid event")
	ErrTimestampOutOfRange = errors.New("timestamp outside acceptance window")
)

type Type string

const (
	TypeHeartbeat  Type = "heartbeat"
	TypePresence   Type = "presence"
	TypeMotion     Type = "motion"
	TypeSleepState Type = "sleep_state"
	TypeFallWarn   Type = "fall_warn"
	TypeNetStatus  Type = "net_status"
)

type Event struct {
	ID         int64
	DeviceID   string
	RoomID     string
	Type       Type
	Time       time.Time
	Sequence   int64
	ReceivedAt time.Time
	InRoom     *bool
	Magnitude  *float64
	SleepState *string
	Confidence *float64
	RSSI       *int
}

type wireEvent struct {
	DeviceID   string   `json:"device_id"`
	RoomID     string   `json:"room_id"`
	Type       Type     `json:"type"`
	Timestamp  string   `json:"ts"`
	Sequence   *int64   `json:"seq"`
	InRoom     *bool    `json:"in_room"`
	Magnitude  *float64 `json:"magnitude"`
	SleepState *string  `json:"state"`
	Confidence *float64 `json:"confidence"`
	RSSI       *int     `json:"rssi"`
}

func Decode(reader io.Reader, receivedAt time.Time) (Event, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()

	var input wireEvent
	if err := decoder.Decode(&input); err != nil {
		var syntaxError *json.SyntaxError
		if errors.As(err, &syntaxError) || errors.Is(err, io.ErrUnexpectedEOF) {
			return Event{}, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
		}
		return Event{}, fmt.Errorf("%w: %v", ErrInvalidEvent, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Event{}, fmt.Errorf("%w: request must contain one JSON object", ErrInvalidEvent)
	}

	if input.DeviceID == "" || input.RoomID == "" || input.Sequence == nil || *input.Sequence < 0 {
		return Event{}, fmt.Errorf("%w: device_id, room_id, and a non-negative seq are required", ErrInvalidEvent)
	}
	eventTime, err := time.Parse(time.RFC3339Nano, input.Timestamp)
	if err != nil {
		return Event{}, fmt.Errorf("%w: ts must be RFC 3339", ErrInvalidEvent)
	}
	if eventTime.Before(receivedAt.Add(-time.Hour)) || eventTime.After(receivedAt.Add(time.Hour)) {
		return Event{}, ErrTimestampOutOfRange
	}

	if err := input.validatePayload(); err != nil {
		return Event{}, err
	}

	return Event{
		DeviceID:   input.DeviceID,
		RoomID:     input.RoomID,
		Type:       input.Type,
		Time:       eventTime,
		Sequence:   *input.Sequence,
		ReceivedAt: receivedAt,
		InRoom:     input.InRoom,
		Magnitude:  input.Magnitude,
		SleepState: input.SleepState,
		Confidence: input.Confidence,
		RSSI:       input.RSSI,
	}, nil
}

func (input wireEvent) validatePayload() error {
	fieldCount := 0
	for _, present := range []bool{
		input.InRoom != nil,
		input.Magnitude != nil,
		input.SleepState != nil,
		input.Confidence != nil,
		input.RSSI != nil,
	} {
		if present {
			fieldCount++
		}
	}

	valid := false
	switch input.Type {
	case TypeHeartbeat:
		valid = fieldCount == 0
	case TypePresence:
		valid = input.InRoom != nil && fieldCount == 1
	case TypeMotion:
		valid = input.Magnitude != nil && *input.Magnitude >= 0 && *input.Magnitude <= 1 && fieldCount == 1
	case TypeSleepState:
		valid = input.SleepState != nil && (*input.SleepState == "asleep" || *input.SleepState == "awake" || *input.SleepState == "unknown") && fieldCount == 1
	case TypeFallWarn:
		valid = input.Confidence != nil && *input.Confidence >= 0 && *input.Confidence <= 1 && fieldCount == 1
	case TypeNetStatus:
		valid = input.RSSI != nil && fieldCount == 1
	}
	if !valid {
		return fmt.Errorf("%w: fields do not match event type %q", ErrInvalidEvent, input.Type)
	}
	return nil
}

func (e Event) Payload() ([]byte, error) {
	var payload any
	switch e.Type {
	case TypeHeartbeat:
		payload = struct{}{}
	case TypePresence:
		payload = struct {
			InRoom *bool `json:"in_room"`
		}{e.InRoom}
	case TypeMotion:
		payload = struct {
			Magnitude *float64 `json:"magnitude"`
		}{e.Magnitude}
	case TypeSleepState:
		payload = struct {
			State *string `json:"state"`
		}{e.SleepState}
	case TypeFallWarn:
		payload = struct {
			Confidence *float64 `json:"confidence"`
		}{e.Confidence}
	case TypeNetStatus:
		payload = struct {
			RSSI *int `json:"rssi"`
		}{e.RSSI}
	default:
		return nil, fmt.Errorf("unsupported event type %q", e.Type)
	}
	return json.Marshal(payload)
}
