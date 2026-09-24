package event

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDecodeEventVariants(t *testing.T) {
	receivedAt := time.Date(2026, 5, 23, 18, 53, 49, 123_000_000, time.UTC)
	tests := []struct {
		name string
		body string
	}{
		{"heartbeat", `{"device_id":"dev_1","room_id":"room_1","type":"heartbeat","ts":"2026-05-23T18:53:49.123Z","seq":1}`},
		{"presence", `{"device_id":"dev_1","room_id":"room_1","type":"presence","ts":"2026-05-23T18:53:49.123Z","seq":2,"in_room":true}`},
		{"motion", `{"device_id":"dev_1","room_id":"room_1","type":"motion","ts":"2026-05-23T18:53:49.123Z","seq":3,"magnitude":0.81}`},
		{"sleep state", `{"device_id":"dev_1","room_id":"room_1","type":"sleep_state","ts":"2026-05-23T18:53:49.123Z","seq":4,"state":"asleep"}`},
		{"fall warning", `{"device_id":"dev_1","room_id":"room_1","type":"fall_warn","ts":"2026-05-23T18:53:49.123Z","seq":5,"confidence":0.92}`},
		{"network status", `{"device_id":"dev_1","room_id":"room_1","type":"net_status","ts":"2026-05-23T18:53:49.123Z","seq":6,"rssi":-68}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(test.body), receivedAt); err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
		})
	}
}

func TestDecodeRejectsInvalidEvents(t *testing.T) {
	receivedAt := time.Date(2026, 5, 23, 18, 53, 49, 0, time.UTC)
	tests := []struct {
		name string
		body string
	}{
		{"missing field", `{"device_id":"dev_1","room_id":"room_1","type":"heartbeat","ts":"2026-05-23T18:53:49Z"}`},
		{"unknown field", `{"device_id":"dev_1","room_id":"room_1","type":"heartbeat","ts":"2026-05-23T18:53:49Z","seq":1,"extra":true}`},
		{"wrong variant field", `{"device_id":"dev_1","room_id":"room_1","type":"heartbeat","ts":"2026-05-23T18:53:49Z","seq":1,"in_room":true}`},
		{"invalid range", `{"device_id":"dev_1","room_id":"room_1","type":"motion","ts":"2026-05-23T18:53:49Z","seq":1,"magnitude":1.1}`},
		{"invalid state", `{"device_id":"dev_1","room_id":"room_1","type":"sleep_state","ts":"2026-05-23T18:53:49Z","seq":1,"state":"resting"}`},
		{"trailing json", `{"device_id":"dev_1","room_id":"room_1","type":"heartbeat","ts":"2026-05-23T18:53:49Z","seq":1} {}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Decode(strings.NewReader(test.body), receivedAt)
			if !errors.Is(err, ErrInvalidEvent) {
				t.Fatalf("Decode() error = %v, want ErrInvalidEvent", err)
			}
		})
	}
}

func TestDecodeTimestampWindowIsInclusive(t *testing.T) {
	receivedAt := time.Date(2026, 5, 23, 18, 53, 49, 0, time.UTC)
	for _, eventTime := range []time.Time{receivedAt.Add(-time.Hour), receivedAt.Add(time.Hour)} {
		body := `{"device_id":"dev_1","room_id":"room_1","type":"heartbeat","ts":"` + eventTime.Format(time.RFC3339Nano) + `","seq":1}`
		if _, err := Decode(strings.NewReader(body), receivedAt); err != nil {
			t.Fatalf("Decode() at boundary %s error = %v", eventTime, err)
		}
	}

	for _, eventTime := range []time.Time{receivedAt.Add(-time.Hour - time.Nanosecond), receivedAt.Add(time.Hour + time.Nanosecond)} {
		body := `{"device_id":"dev_1","room_id":"room_1","type":"heartbeat","ts":"` + eventTime.Format(time.RFC3339Nano) + `","seq":1}`
		_, err := Decode(strings.NewReader(body), receivedAt)
		if !errors.Is(err, ErrTimestampOutOfRange) {
			t.Fatalf("Decode() outside boundary %s error = %v, want ErrTimestampOutOfRange", eventTime, err)
		}
	}
}
