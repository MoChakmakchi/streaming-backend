package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"teton/internal/event"
	"teton/internal/eventstore"
)

type ingestFunc func(context.Context, event.Event, time.Time) (eventstore.InsertResult, error)

type eventsHandler struct {
	ingest         ingestFunc
	ingestFall     ingestFunc
	normalCapacity chan struct{}
	fallCapacity   chan struct{}
	deadline       time.Duration
	now            func() time.Time
}

func NewEventsHandler(
	ingest ingestFunc,
	ingestFall ingestFunc,
	normalConcurrency int,
	fallConcurrency int,
	deadline time.Duration,
	now func() time.Time,
) http.Handler {
	return &eventsHandler{
		ingest:         ingest,
		ingestFall:     ingestFall,
		normalCapacity: make(chan struct{}, normalConcurrency),
		fallCapacity:   make(chan struct{}, fallConcurrency),
		deadline:       deadline,
		now:            now,
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

	ingest := h.ingest
	capacity := h.normalCapacity
	if input.Type == event.TypeFallWarn {
		ingest = h.ingestFall
		capacity = h.fallCapacity
	}

	select {
	case capacity <- struct{}{}:
		defer func() { <-capacity }()
	default:
		writeUnavailable(response)
		return
	}

	ctx, cancel := context.WithTimeout(request.Context(), h.deadline)
	defer cancel()
	result, err := ingest(ctx, input, receivedAt)
	if err != nil {
		slog.ErrorContext(ctx, "ingest event",
			"error", err,
			"device_id", input.DeviceID,
			"event_type", input.Type,
		)
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
	}{Status: status, EventID: result.EventID})
}

func writeUnavailable(response http.ResponseWriter) {
	response.Header().Set("Retry-After", "1")
	writeError(response, http.StatusServiceUnavailable, "overloaded", "event was not accepted; retry later")
}

func writeError(response http.ResponseWriter, status int, code, message string) {
	writeJSON(response, status, struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{Code: code, Message: message})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
