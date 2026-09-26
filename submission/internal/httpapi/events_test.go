package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"teton/internal/event"
	"teton/internal/eventstore"
)

func TestPostEventsResponses(t *testing.T) {
	now := time.Date(2026, 5, 23, 18, 53, 49, 0, time.UTC)
	valid := `{"device_id":"dev_1","room_id":"room_1","type":"heartbeat","ts":"` + now.Format(time.RFC3339Nano) + `","seq":1}`
	tests := []struct {
		name       string
		body       string
		ingest     ingestFunc
		wantStatus int
		wantBody   string
	}{
		{
			name: "accepted",
			body: valid,
			ingest: func(context.Context, event.Event, time.Time) (eventstore.InsertResult, error) {
				return eventstore.InsertResult{Inserted: true}, nil
			},
			wantStatus: http.StatusAccepted,
			wantBody:   `{"status":"accepted"}`,
		},
		{
			name: "duplicate",
			body: valid,
			ingest: func(context.Context, event.Event, time.Time) (eventstore.InsertResult, error) {
				return eventstore.InsertResult{}, nil
			},
			wantStatus: http.StatusOK,
			wantBody:   `{"status":"duplicate"}`,
		},
		{
			name:       "malformed",
			body:       `{"device_id":"dev_1"}`,
			wantStatus: http.StatusBadRequest,
			wantBody:   `"code":"invalid_event"`,
		},
		{
			name:       "outside timestamp window",
			body:       `{"device_id":"dev_1","room_id":"room_1","type":"heartbeat","ts":"2000-01-01T00:00:00Z","seq":1}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantBody:   `"code":"timestamp_out_of_range"`,
		},
		{
			name: "retryable failure",
			body: valid,
			ingest: func(context.Context, event.Event, time.Time) (eventstore.InsertResult, error) {
				return eventstore.InsertResult{}, errors.New("database unavailable")
			},
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   `"code":"overloaded"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := NewEventsHandler(test.ingest, test.ingest, 1, 1, time.Second, func() time.Time { return now })
			request := httptest.NewRequest(http.MethodPost, "/events", strings.NewReader(test.body))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, test.wantStatus, response.Body.String())
			}
			if !strings.Contains(strings.TrimSpace(response.Body.String()), test.wantBody) {
				t.Fatalf("body = %s, want it to contain %s", response.Body.String(), test.wantBody)
			}
			if test.wantStatus == http.StatusServiceUnavailable && response.Header().Get("Retry-After") != "1" {
				t.Fatalf("Retry-After = %q, want 1", response.Header().Get("Retry-After"))
			}
		})
	}
}

func TestPostFallUsesAlarmIngest(t *testing.T) {
	now := time.Date(2026, 5, 23, 18, 53, 49, 0, time.UTC)
	normalIngest := func(context.Context, event.Event, time.Time) (eventstore.InsertResult, error) {
		t.Fatal("normal event ingest called for fall warning")
		return eventstore.InsertResult{}, nil
	}
	alarmIngest := func(_ context.Context, input event.Event, receivedAt time.Time) (eventstore.InsertResult, error) {
		if input.Type != event.TypeFallWarn || !receivedAt.Equal(now) {
			t.Fatalf("alarm ingest called with %#v at %s", input, receivedAt)
		}
		return eventstore.InsertResult{Inserted: true}, nil
	}
	handler := NewEventsHandler(normalIngest, alarmIngest, 1, 1, time.Second, func() time.Time { return now })
	request := httptest.NewRequest(
		http.MethodPost,
		"/events",
		strings.NewReader(`{"device_id":"dev_1","room_id":"room_1","type":"fall_warn","ts":"`+
			now.Format(time.RFC3339Nano)+`","seq":1,"confidence":0.92}`),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", response.Code, response.Body.String())
	}
}
