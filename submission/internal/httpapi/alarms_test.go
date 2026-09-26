package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"teton/internal/features/alarms"
)

func TestGetAlarms(t *testing.T) {
	createdAt := time.Date(2026, 5, 23, 18, 53, 49, 0, time.UTC)
	alarm := alarms.Alarm{
		EventID:    42,
		RoomID:     "room_1",
		DeviceID:   "dev_1",
		EventTime:  createdAt.Add(-time.Second),
		Confidence: 0.92,
		CreatedAt:  createdAt,
	}
	handler := NewAlarmHistoryHandler(func(_ context.Context, since *time.Time) ([]alarms.Alarm, error) {
		if since == nil || !since.Equal(createdAt) {
			t.Fatalf("since = %v, want %s", since, createdAt)
		}
		return []alarms.Alarm{alarm}, nil
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/alarms?since="+createdAt.Format(time.RFC3339Nano), nil)
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", response.Code, response.Body.String())
	}

	var body struct {
		Alarms []alarms.Alarm `json:"alarms"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Alarms) != 1 || body.Alarms[0].EventID != alarm.EventID {
		t.Fatalf("alarms = %#v", body.Alarms)
	}
}

func TestGetAlarmsRejectsInvalidSince(t *testing.T) {
	handler := NewAlarmHistoryHandler(func(context.Context, *time.Time) ([]alarms.Alarm, error) {
		t.Fatal("history query called for invalid since")
		return nil, nil
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/alarms?since=invalid", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
}

func TestAlarmStreamFramesInOrder(t *testing.T) {
	createdAt := time.Date(2026, 5, 23, 18, 53, 49, 0, time.UTC)
	updates := make(chan alarms.Alarm, 2)
	updates <- alarms.Alarm{EventID: 41, RoomID: "room_1", DeviceID: "dev_1", CreatedAt: createdAt}
	updates <- alarms.Alarm{EventID: 42, RoomID: "room_1", DeviceID: "dev_1", CreatedAt: createdAt.Add(time.Second)}
	close(updates)

	unsubscribed := false
	handler := NewAlarmStreamHandler(
		func(context.Context, *time.Time) ([]alarms.Alarm, error) {
			t.Fatal("history query called without a since parameter")
			return nil, nil
		},
		func() (<-chan alarms.Alarm, func()) {
			return updates, func() { unsubscribed = true }
		},
		func(time.Duration) {},
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/alarms/stream", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if response.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("Content-Type = %q", response.Header().Get("Content-Type"))
	}
	if !unsubscribed {
		t.Fatal("stream did not unsubscribe")
	}
	body := response.Body.String()
	first := strings.Index(body, `"event_id":41`)
	second := strings.Index(body, `"event_id":42`)
	if first < 0 || second <= first || strings.Count(body, "event: alarm\n") != 2 {
		t.Fatalf("SSE body = %q", body)
	}
}

func TestAlarmStreamReplaysWithoutHistoryLiveDuplicates(t *testing.T) {
	createdAt := time.Date(2026, 5, 23, 18, 53, 49, 0, time.UTC)
	history := []alarms.Alarm{
		{EventID: 41, CreatedAt: createdAt},
		{EventID: 42, CreatedAt: createdAt.Add(time.Second)},
	}
	updates := make(chan alarms.Alarm, 2)
	updates <- history[1]
	updates <- alarms.Alarm{EventID: 43, CreatedAt: createdAt.Add(2 * time.Second)}
	close(updates)

	subscribed := false
	deliveries := 0
	handler := NewAlarmStreamHandler(
		func(_ context.Context, since *time.Time) ([]alarms.Alarm, error) {
			if !subscribed {
				t.Fatal("history queried before live subscription")
			}
			if since == nil || !since.Equal(createdAt) {
				t.Fatalf("since = %v, want %s", since, createdAt)
			}
			return history, nil
		},
		func() (<-chan alarms.Alarm, func()) {
			subscribed = true
			return updates, func() {}
		},
		func(time.Duration) { deliveries++ },
	)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/alarms/stream?since="+createdAt.Format(time.RFC3339Nano),
		nil,
	)
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	previous := -1
	for _, eventID := range []string{"41", "42", "43"} {
		if strings.Count(body, `"event_id":`+eventID) != 1 {
			t.Fatalf("event %s count in SSE body = %d, want 1; body = %q", eventID, strings.Count(body, `"event_id":`+eventID), body)
		}
		position := strings.Index(body, `"event_id":`+eventID)
		if position <= previous {
			t.Fatalf("events are out of order in SSE body: %q", body)
		}
		previous = position
	}
	if deliveries != 1 {
		t.Fatalf("observed live deliveries = %d, want 1", deliveries)
	}
}
