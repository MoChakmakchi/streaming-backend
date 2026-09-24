package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"teton/internal/event"
	"teton/internal/processing"
)

type IngestFunc func(context.Context, event.Event, time.Time) (processing.IngestResult, error)

type eventsHandler struct {
	ingest   IngestFunc
	capacity chan struct{}
	deadline time.Duration
	now      func() time.Time
}

func NewEventsHandler(ingest IngestFunc, maxConcurrent int, deadline time.Duration, now func() time.Time) http.Handler {
	return &eventsHandler{
		ingest:   ingest,
		capacity: make(chan struct{}, maxConcurrent),
		deadline: deadline,
		now:      now,
	}
}

func (h *eventsHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	receivedAt := h.now().UTC()
	input, err := event.Decode(request.Body, receivedAt)
	if err != nil {
		switch {
		case errors.Is(err, event.ErrTimestampOutOfRange):
			writeError(response, http.StatusUnprocessableEntity, "timestamp_out_of_range", err.Error())
		case errors.Is(err, event.ErrInvalidJSON):
			writeError(response, http.StatusBadRequest, "invalid_json", err.Error())
		default:
			writeError(response, http.StatusBadRequest, "invalid_event", err.Error())
		}
		return
	}

	ctx, cancel := context.WithTimeout(request.Context(), h.deadline)
	defer cancel()
	select {
	case h.capacity <- struct{}{}:
		defer func() { <-h.capacity }()
	case <-ctx.Done():
		writeUnavailable(response)
		return
	}

	result, err := h.ingest(ctx, input, receivedAt)
	if err != nil {
		writeUnavailable(response)
		return
	}

	status := "accepted"
	statusCode := http.StatusAccepted
	if !result.Inserted {
		status = "duplicate"
		statusCode = http.StatusOK
	}
	writeJSON(response, statusCode, struct {
		Status  string `json:"status"`
		EventID int64  `json:"event_id"`
	}{status, result.EventID})
}

func writeUnavailable(response http.ResponseWriter) {
	response.Header().Set("Retry-After", "1")
	writeError(response, http.StatusServiceUnavailable, "overloaded", "event was not accepted; retry later")
}

func writeError(response http.ResponseWriter, status int, code, message string) {
	writeJSON(response, status, struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{code, message})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
