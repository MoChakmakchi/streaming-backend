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
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"teton/internal/event"
	"teton/internal/eventstore"
	"teton/internal/features/health"
	"teton/internal/processing"
)

func TestHealthProjectionAndQuery(t *testing.T) {
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
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatalf("apply River migrations: %v", err)
	}

	events := eventstore.New(pool)
	insertClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	ingestor := processing.NewIngestor(pool, events, insertClient)
	healthService := health.NewService(pool)

	receivedAt := time.Now().UTC().Truncate(time.Millisecond)
	deviceID := fmt.Sprintf("health_test_%d", receivedAt.UnixNano())
	newer := event.Event{
		DeviceID: deviceID, RoomID: "room_new", Type: event.TypeHeartbeat,
		Time: receivedAt.Add(-10 * time.Second), Sequence: 2,
	}
	older := event.Event{
		DeviceID: deviceID, RoomID: "room_old", Type: event.TypeHeartbeat,
		Time: receivedAt.Add(-2 * time.Minute), Sequence: 1,
	}
	future := event.Event{
		DeviceID: deviceID, RoomID: "room_future", Type: event.TypeHeartbeat,
		Time: receivedAt.Add(30 * time.Minute), Sequence: 3,
	}

	newerResult, err := ingestor.Ingest(ctx, newer, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	olderResult, err := ingestor.Ingest(ctx, older, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	futureResult, err := ingestor.Ingest(ctx, future, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := ingestor.Ingest(ctx, newer, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.Inserted || duplicate.EventID != newerResult.EventID {
		t.Fatalf("duplicate result = %#v, want existing event %d", duplicate, newerResult.EventID)
	}

	workers := river.NewWorkers()
	river.AddWorker(workers, processing.NewProjectionWorker(pool, events, healthService))
	workerClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 2}},
		Workers: workers,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := workerClient.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := workerClient.Stop(stopCtx); err != nil {
			t.Errorf("stop River worker: %v", err)
		}
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		var completed int
		err := pool.QueryRow(ctx, `
			SELECT count(*) FROM river_job
			WHERE state = 'completed'
			  AND (args->>'event_id')::bigint = ANY($1)`,
			[]int64{newerResult.EventID, olderResult.EventID},
		).Scan(&completed)
		if err != nil {
			t.Fatal(err)
		}
		if completed == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("heartbeat jobs did not complete")
		}
		time.Sleep(20 * time.Millisecond)
	}

	result, err := healthService.Get(ctx, deviceID, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !result.LastHeartbeatAt.Equal(newer.Time) {
		t.Fatalf("last heartbeat = %s, want %s", result.LastHeartbeatAt, newer.Time)
	}
	if math.Abs(result.Availability5m-2.0/300.0) > 1e-12 {
		t.Fatalf("availability = %v, want %v", result.Availability5m, 2.0/300.0)
	}

	var projectedEventID int64
	if err := pool.QueryRow(ctx, `SELECT event_id FROM device_health WHERE device_id = $1`, deviceID).Scan(&projectedEventID); err != nil {
		t.Fatal(err)
	}
	if projectedEventID != newerResult.EventID {
		t.Fatalf("projected event = %d, want newer event %d", projectedEventID, newerResult.EventID)
	}

	var futureState string
	if err := pool.QueryRow(ctx,
		`SELECT state FROM river_job WHERE (args->>'event_id')::bigint = $1`,
		futureResult.EventID,
	).Scan(&futureState); err != nil {
		t.Fatal(err)
	}
	if futureState == "completed" {
		t.Fatal("future heartbeat job completed before its event time")
	}

	var newerJobCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM river_job WHERE (args->>'event_id')::bigint = $1`,
		newerResult.EventID,
	).Scan(&newerJobCount); err != nil {
		t.Fatal(err)
	}
	if newerJobCount != 1 {
		t.Fatalf("jobs for retried heartbeat = %d, want 1", newerJobCount)
	}
}
