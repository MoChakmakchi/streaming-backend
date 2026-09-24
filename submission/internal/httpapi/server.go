package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func NewServer(events http.Handler) http.Handler {
	router := chi.NewRouter()
	router.Method(http.MethodPost, "/events", events)
	return router
}
