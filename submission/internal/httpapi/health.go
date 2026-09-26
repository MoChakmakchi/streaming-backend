package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"teton/internal/features/health"
)

type healthQueryFunc func(context.Context, string, time.Time) (health.Health, error)

type healthHandler struct {
	query healthQueryFunc
	now   func() time.Time
}

func NewHealthHandler(query healthQueryFunc, now func() time.Time) http.Handler {
	return &healthHandler{query: query, now: now}
}

func (h *healthHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	result, err := h.query(request.Context(), chi.URLParam(request, "device_id"), h.now().UTC())
	if errors.Is(err, health.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "device health not found")
		return
	}
	if err != nil {
		slog.ErrorContext(request.Context(), "query device health",
			"error", err,
			"device_id", chi.URLParam(request, "device_id"),
		)
		writeError(response, http.StatusInternalServerError, "internal_error", "could not read device health")
		return
	}
	writeJSON(response, http.StatusOK, result)
}
