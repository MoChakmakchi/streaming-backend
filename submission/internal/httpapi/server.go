package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func NewServer(events, health http.Handler) http.Handler {
	router := chi.NewRouter()
	router.Method(http.MethodPost, "/events", events)
	router.Method(http.MethodGet, "/devices/{device_id}/health", health)
	return router
}
