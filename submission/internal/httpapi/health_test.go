package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"teton/internal/features/health"
)

func TestGetDeviceHealth(t *testing.T) {
	now := time.Date(2026, 5, 23, 18, 53, 49, 0, time.UTC)
	healthHandler := NewHealthHandler(func(_ context.Context, deviceID string, queryTime time.Time) (health.Health, error) {
		if deviceID == "unknown" {
			return health.Health{}, health.ErrNotFound
		}
		if deviceID != "dev_1" || !queryTime.Equal(now) {
			t.Fatalf("query called with device %q at %s", deviceID, queryTime)
		}
		return health.Health{LastHeartbeatAt: now.Add(-time.Second), Availability5m: 0.5}, nil
	}, func() time.Time { return now })
	server := NewServer(http.NotFoundHandler(), healthHandler)

	t.Run("compatible response", func(t *testing.T) {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/devices/dev_1/health", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body = %s", response.Code, response.Body.String())
		}
		var body struct {
			LastHeartbeatAt time.Time `json:"last_heartbeat_ts"`
			Availability5m  float64   `json:"availability_5m"`
		}
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !body.LastHeartbeatAt.Equal(now.Add(-time.Second)) || body.Availability5m != 0.5 {
			t.Fatalf("response = %#v", body)
		}
	})

	t.Run("unknown device", func(t *testing.T) {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/devices/unknown/health", nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body = %s", response.Code, response.Body.String())
		}
	})
}
