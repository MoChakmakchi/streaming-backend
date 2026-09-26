package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func NewServer(events, health, occupancy, alarmHistory, alarmStream, metrics http.Handler) http.Handler {
	router := chi.NewRouter()
	router.Method(http.MethodPost, "/events", events)
	router.Method(http.MethodGet, "/devices/{device_id}/health", health)
	router.Method(http.MethodGet, "/rooms/{room_id}/occupancy", occupancy)
	router.Method(http.MethodGet, "/alarms", alarmHistory)
	router.Method(http.MethodGet, "/alarms/stream", alarmStream)
	router.Method(http.MethodGet, "/metrics", metrics)
	return router
}

func LogRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		startedAt := time.Now()
		recorder := middleware.NewWrapResponseWriter(response, request.ProtoMajor)
		next.ServeHTTP(recorder, request)

		status := recorder.Status()
		if status == 0 {
			status = http.StatusOK
		}

		level := slog.LevelDebug
		switch {
		case status == http.StatusServiceUnavailable:
			level = slog.LevelDebug
		case status >= http.StatusInternalServerError:
			level = slog.LevelError
		case status >= http.StatusBadRequest:
			level = slog.LevelInfo
		}

		logger.Log(request.Context(), level, "http request",
			"method", request.Method,
			"path", request.URL.Path,
			"status", status,
			"duration", time.Since(startedAt),
		)
	})
}
