//go:build integration

package eventstore_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"teton/internal/event"
	"teton/internal/eventstore"
)

func TestIngestPersistsAndDeduplicates(t *testing.T) {
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

	migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "001_initial.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("apply application migration: %v", err)
	}

	store := eventstore.New(pool)
	receivedAt := time.Now().UTC().Truncate(time.Millisecond)
	input := event.Event{
		DeviceID: fmt.Sprintf("ingest_test_%d", receivedAt.UnixNano()),
		RoomID:   "room_test",
		Type:     event.TypeHeartbeat,
		Time:     receivedAt.Add(30 * time.Minute),
		Sequence: 1,
	}

	first, err := store.Ingest(ctx, input, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Ingest(ctx, input, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Inserted || second.Inserted || first.EventID != second.EventID {
		t.Fatalf("duplicate results = %#v then %#v", first, second)
	}

	var eventCount int
	var eventTime time.Time
	if err := pool.QueryRow(ctx, `
		SELECT count(*), max(event_time)
		FROM events
		WHERE device_id = $1`, input.DeviceID,
	).Scan(&eventCount, &eventTime); err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 {
		t.Fatalf("stored events = %d, want 1", eventCount)
	}
	if !eventTime.Equal(input.Time) {
		t.Fatalf("event time = %s, want %s", eventTime, input.Time)
	}
}
