package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"teton/internal/features/occupancy"
)

func TestGetRoomOccupancy(t *testing.T) {
	now := time.Date(2026, 5, 23, 18, 53, 49, 0, time.UTC)
	occupancyHandler := NewOccupancyHandler(
		func(
			_ context.Context,
			roomID string,
			window time.Duration,
			queryTime time.Time,
		) (occupancy.Occupancy, error) {
			if roomID == "unknown" {
				return occupancy.Occupancy{}, occupancy.ErrNotFound
			}
			if roomID != "room_1" || !queryTime.Equal(now) {
				t.Fatalf("query called with room %q at %s", roomID, queryTime)
			}
			return occupancy.Occupancy{
				InRoom:        true,
				OccupiedPct:   0.5,
				WindowSeconds: int(window.Seconds()),
			}, nil
		},
		func() time.Time { return now },
	)
	server := NewServer(
		http.NotFoundHandler(),
		http.NotFoundHandler(),
		occupancyHandler,
		http.NotFoundHandler(),
		http.NotFoundHandler(),
		http.NotFoundHandler(),
	)

	for _, test := range []struct {
		window  string
		seconds int
	}{
		{window: "1m", seconds: 60},
		{window: "5m", seconds: 300},
		{window: "1h", seconds: 3600},
	} {
		t.Run(test.window, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodGet,
				"/rooms/room_1/occupancy?window="+test.window,
				nil,
			)
			server.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body = %s", response.Code, response.Body.String())
			}

			var body occupancy.Occupancy
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if !body.InRoom || body.OccupiedPct != 0.5 || body.WindowSeconds != test.seconds {
				t.Fatalf("response = %#v", body)
			}
		})
	}

	for _, target := range []string{
		"/rooms/room_1/occupancy",
		"/rooms/room_1/occupancy?window=10m",
	} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400", target, response.Code)
		}
	}

	response := httptest.NewRecorder()
	server.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/rooms/unknown/occupancy?window=1m", nil),
	)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown room status = %d, want 404", response.Code)
	}
}
