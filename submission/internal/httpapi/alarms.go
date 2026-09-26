package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"teton/internal/features/alarms"
)

type alarmListFunc func(context.Context, *time.Time) ([]alarms.Alarm, error)
type alarmSubscribeFunc func() (<-chan alarms.Alarm, func())

type alarmHistoryHandler struct {
	list alarmListFunc
}

func NewAlarmHistoryHandler(list alarmListFunc) http.Handler {
	return &alarmHistoryHandler{list: list}
}

func (h *alarmHistoryHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	since, err := parseAlarmSince(request.URL.Query().Get("since"))
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_since", "since must be 0 or an RFC 3339 timestamp")
		return
	}

	items, err := h.list(request.Context(), since)
	if err != nil {
		slog.ErrorContext(request.Context(), "query alarm history", "error", err)
		writeError(response, http.StatusInternalServerError, "internal_error", "could not read alarms")
		return
	}
	writeJSON(response, http.StatusOK, struct {
		Alarms []alarms.Alarm `json:"alarms"`
	}{Alarms: items})
}

func parseAlarmSince(value string) (*time.Time, error) {
	if value == "" || value == "0" {
		return nil, nil
	}
	since, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, fmt.Errorf("parse alarm timestamp: %w", err)
	}
	return &since, nil
}

type alarmStreamHandler struct {
	list            alarmListFunc
	subscribe       alarmSubscribeFunc
	observeDelivery func(time.Duration)
}

func NewAlarmStreamHandler(
	list alarmListFunc,
	subscribe alarmSubscribeFunc,
	observeDelivery func(time.Duration),
) http.Handler {
	return &alarmStreamHandler{
		list:            list,
		subscribe:       subscribe,
		observeDelivery: observeDelivery,
	}
}

func (h *alarmStreamHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	sinceValue := request.URL.Query().Get("since")
	since, err := parseAlarmSince(sinceValue)
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_since", "since must be 0 or an RFC 3339 timestamp")
		return
	}

	flusher, ok := response.(http.Flusher)
	if !ok {
		writeError(response, http.StatusInternalServerError, "stream_unsupported", "streaming is not supported")
		return
	}

	updates, unsubscribe := h.subscribe()
	defer unsubscribe()

	history := []alarms.Alarm{}
	if sinceValue != "" {
		history, err = h.list(request.Context(), since)
		if err != nil {
			slog.ErrorContext(request.Context(), "query alarm stream history", "error", err)
			writeError(response, http.StatusInternalServerError, "internal_error", "could not read alarms")
			return
		}
	}

	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("Cache-Control", "no-cache")
	response.WriteHeader(http.StatusOK)
	flusher.Flush()

	replayed := make(map[int64]struct{}, len(history))
	for _, alarm := range history {
		if err := writeAlarmEvent(response, flusher, alarm); err != nil {
			return
		}
		replayed[alarm.EventID] = struct{}{}
	}

	for {
		select {
		case <-request.Context().Done():
			return
		case alarm, open := <-updates:
			if !open {
				return
			}
			if _, alreadyReplayed := replayed[alarm.EventID]; alreadyReplayed {
				continue
			}
			if err := writeAlarmEvent(response, flusher, alarm); err != nil {
				return
			}
			h.observeDelivery(time.Since(alarm.CreatedAt))
		}
	}
}

func writeAlarmEvent(response http.ResponseWriter, flusher http.Flusher, alarm alarms.Alarm) error {
	data, err := json.Marshal(alarm)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(response, "event: alarm\ndata: %s\n\n", data); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}
