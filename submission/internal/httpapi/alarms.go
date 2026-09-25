package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
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
	subscribe alarmSubscribeFunc
}

func NewAlarmStreamHandler(subscribe alarmSubscribeFunc) http.Handler {
	return &alarmStreamHandler{subscribe: subscribe}
}

func (h *alarmStreamHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	flusher, ok := response.(http.Flusher)
	if !ok {
		writeError(response, http.StatusInternalServerError, "stream_unsupported", "streaming is not supported")
		return
	}

	updates, unsubscribe := h.subscribe()
	defer unsubscribe()

	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("Cache-Control", "no-cache")
	response.WriteHeader(http.StatusOK)
	flusher.Flush()

	for {
		select {
		case <-request.Context().Done():
			return
		case alarm, open := <-updates:
			if !open {
				return
			}
			data, err := json.Marshal(alarm)
			if err != nil {
				return
			}
			if _, err := fmt.Fprintf(response, "event: alarm\ndata: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
