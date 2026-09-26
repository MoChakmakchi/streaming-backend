package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"teton/internal/event"
	"teton/internal/eventstore"
)

func TestPostEventsRejectsWhenAdmissionIsFull(t *testing.T) {
	now := time.Date(2026, 5, 23, 18, 53, 49, 0, time.UTC)
	calls := 0

	ingest := func(context.Context, event.Event, time.Time) (eventstore.InsertResult, error) {
		calls++
		return eventstore.InsertResult{Inserted: true}, nil
	}
	handler := NewEventsHandler(ingest, ingest, 1, 1, time.Second, func() time.Time { return now }).(*eventsHandler)
	handler.normalCapacity <- struct{}{}

	overloaded := postEvent(handler, heartbeatBody(now, 2))
	if overloaded.Code != http.StatusServiceUnavailable {
		t.Fatalf("overloaded status = %d, want 503", overloaded.Code)
	}
	if overloaded.Header().Get("Retry-After") != "1" {
		t.Fatalf("Retry-After = %q, want 1", overloaded.Header().Get("Retry-After"))
	}
	if calls != 0 {
		t.Fatalf("ingest calls while full = %d, want 0", calls)
	}

	fall := postEvent(handler, fallBody(now, 2))
	if fall.Code != http.StatusAccepted {
		t.Fatalf("fall status while normal admission is full = %d, want 202", fall.Code)
	}
	if calls != 1 {
		t.Fatalf("fall ingest calls while normal admission is full = %d, want 1", calls)
	}

	<-handler.normalCapacity

	retry := postEvent(handler, heartbeatBody(now, 2))
	if retry.Code != http.StatusAccepted {
		t.Fatalf("retry status = %d, want 202", retry.Code)
	}
	if calls != 2 {
		t.Fatalf("ingest calls after retry = %d, want 2", calls)
	}
}

func postEvent(handler http.Handler, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/events", strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func heartbeatBody(ts time.Time, seq int) string {
	return `{"device_id":"dev_1","room_id":"room_1","type":"heartbeat","ts":"` +
		ts.Format(time.RFC3339Nano) + `","seq":` + strconv.Itoa(seq) + `}`
}

func fallBody(ts time.Time, seq int) string {
	return `{"device_id":"dev_1","room_id":"room_1","type":"fall_warn","ts":"` +
		ts.Format(time.RFC3339Nano) + `","seq":` + strconv.Itoa(seq) + `,"confidence":0.92}`
}
