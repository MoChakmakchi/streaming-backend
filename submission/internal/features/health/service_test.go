//go:build integration

package health_test

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"teton/internal/event"
	"teton/internal/eventstore"
	"teton/internal/features/health"
)

func TestHealthQuery(t *testing.T) {
	ctx := context.Background()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://teton:teton@localhost:5433/teton?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		if os.Getenv("REQUIRE_TEST_DATABASE") == "1" {
			t.Fatalf("PostgreSQL integration database unavailable: %v", err)
		}
		t.Skipf("PostgreSQL integration database unavailable: %v", err)
	}

	migration, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "001_initial.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("apply application migration: %v", err)
	}

	events := eventstore.New(pool)
	service := health.NewService(pool)
	queryTime := time.Now().UTC().Truncate(time.Millisecond)
	deviceID := fmt.Sprintf("health_test_%d", queryTime.UnixNano())
	newer := event.Event{
		DeviceID: deviceID,
		RoomID:   "room_new",
		Type:     event.TypeHeartbeat,
		Time:     queryTime.Add(-10 * time.Second),
		Sequence: 2,
	}
	older := event.Event{
		DeviceID: deviceID,
		RoomID:   "room_old",
		Type:     event.TypeHeartbeat,
		Time:     queryTime.Add(-2 * time.Minute),
		Sequence: 1,
	}
	future := event.Event{
		DeviceID: deviceID,
		RoomID:   "room_future",
		Type:     event.TypeHeartbeat,
		Time:     queryTime.Add(30 * time.Minute),
		Sequence: 3,
	}

	newerResult, err := events.Ingest(ctx, newer, queryTime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := events.Ingest(ctx, older, queryTime); err != nil {
		t.Fatal(err)
	}
	if _, err := events.Ingest(ctx, future, queryTime); err != nil {
		t.Fatal(err)
	}
	duplicate, err := events.Ingest(ctx, newer, queryTime)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.Inserted || duplicate.EventID != newerResult.EventID {
		t.Fatalf("duplicate result = %#v, want existing event %d", duplicate, newerResult.EventID)
	}

	result, err := service.Get(ctx, deviceID, queryTime)
	if err != nil {
		t.Fatal(err)
	}
	if !result.LastHeartbeatAt.Equal(newer.Time) {
		t.Fatalf("last heartbeat = %s, want %s", result.LastHeartbeatAt, newer.Time)
	}
	if math.Abs(result.Availability5m-2.0/300.0) > 1e-12 {
		t.Fatalf("availability = %v, want %v", result.Availability5m, 2.0/300.0)
	}
}
