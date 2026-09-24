package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"teton/internal/features/occupancy"
)

type occupancyQueryFunc func(context.Context, string, time.Duration, time.Time) (occupancy.Occupancy, error)

type occupancyHandler struct {
	query occupancyQueryFunc
	now   func() time.Time
}

func NewOccupancyHandler(query occupancyQueryFunc, now func() time.Time) http.Handler {
	return &occupancyHandler{query: query, now: now}
}

func (h *occupancyHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	window, err := occupancy.ParseWindow(request.URL.Query().Get("window"))
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_window", "window must be one of 1m, 5m, or 1h")
		return
	}

	result, err := h.query(
		request.Context(),
		chi.URLParam(request, "room_id"),
		window,
		h.now().UTC(),
	)
	if errors.Is(err, occupancy.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "room occupancy not found")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "internal_error", "could not read room occupancy")
		return
	}
	writeJSON(response, http.StatusOK, result)
}
