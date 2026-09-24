package eventstore_test

import (
	"context"
	"fmt"
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
	"teton/internal/processing"
)

func TestTransactionalIngest(t *testing.T) {
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
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatalf("apply River migrations: %v", err)
	}

	store := eventstore.New(pool)
	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	ingestor := processing.NewIngestor(pool, store, riverClient)
	receivedAt := time.Now().UTC().Truncate(time.Millisecond)
	prefix := fmt.Sprintf("test_%d", receivedAt.UnixNano())

	heartbeat := event.Event{
		DeviceID: prefix + "_heartbeat", RoomID: "room_test", Type: event.TypeHeartbeat,
		Time: receivedAt.Add(30 * time.Minute), Sequence: 1,
	}
	first, err := ingestor.Ingest(ctx, heartbeat, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ingestor.Ingest(ctx, heartbeat, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Inserted || second.Inserted || first.EventID != second.EventID {
		t.Fatalf("duplicate results = %#v then %#v", first, second)
	}

	var eventCount, jobCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE device_id = $1`, heartbeat.DeviceID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE (args->>'event_id')::bigint = $1`, first.EventID).Scan(&jobCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 || jobCount != 1 {
		t.Fatalf("duplicate retry stored %d events and %d jobs, want 1 and 1", eventCount, jobCount)
	}
	var scheduledAt time.Time
	if err := pool.QueryRow(ctx, `SELECT scheduled_at FROM river_job WHERE (args->>'event_id')::bigint = $1`, first.EventID).Scan(&scheduledAt); err != nil {
		t.Fatal(err)
	}
	if !scheduledAt.Equal(heartbeat.Time) {
		t.Fatalf("scheduled_at = %s, want %s", scheduledAt, heartbeat.Time)
	}

	inRoom := true
	nonJobEvents := []event.Event{
		{DeviceID: prefix + "_motion", RoomID: "room_test", Type: event.TypeMotion, Time: receivedAt, Sequence: 1, Magnitude: floatPointer(0.5)},
		{DeviceID: prefix + "_sleep", RoomID: "room_test", Type: event.TypeSleepState, Time: receivedAt, Sequence: 1, SleepState: stringPointer("awake")},
		{DeviceID: prefix + "_fall", RoomID: "room_test", Type: event.TypeFallWarn, Time: receivedAt, Sequence: 1, Confidence: floatPointer(0.9)},
		{DeviceID: prefix + "_network", RoomID: "room_test", Type: event.TypeNetStatus, Time: receivedAt, Sequence: 1, RSSI: intPointer(-68)},
	}
	presence := event.Event{DeviceID: prefix + "_presence", RoomID: "room_test", Type: event.TypePresence, Time: receivedAt, Sequence: 1, InRoom: &inRoom}
	presenceResult, err := ingestor.Ingest(ctx, presence, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE (args->>'event_id')::bigint = $1`, presenceResult.EventID).Scan(&jobCount); err != nil {
		t.Fatal(err)
	}
	if jobCount != 1 {
		t.Fatalf("presence jobs = %d, want 1", jobCount)
	}
	for _, input := range nonJobEvents {
		result, err := ingestor.Ingest(ctx, input, receivedAt)
		if err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE (args->>'event_id')::bigint = $1`, result.EventID).Scan(&jobCount); err != nil {
			t.Fatal(err)
		}
		if jobCount != 0 {
			t.Fatalf("%s jobs = %d, want 0", input.Type, jobCount)
		}
	}

	if _, err := store.Get(ctx, first.EventID); err != nil {
		t.Fatalf("read inserted event: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		CREATE OR REPLACE FUNCTION reject_test_river_job() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'forced River insert failure'; END $$;
		DROP TRIGGER IF EXISTS reject_test_river_job ON river_job;
		CREATE TRIGGER reject_test_river_job BEFORE INSERT ON river_job
		FOR EACH ROW EXECUTE FUNCTION reject_test_river_job()`); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DROP TRIGGER IF EXISTS reject_test_river_job ON river_job; DROP FUNCTION IF EXISTS reject_test_river_job()`)

	failingDevice := prefix + "_rollback"
	_, err = ingestor.Ingest(ctx, event.Event{
		DeviceID: failingDevice, RoomID: "room_test", Type: event.TypeHeartbeat,
		Time: receivedAt, Sequence: 1,
	}, receivedAt)
	if err == nil {
		t.Fatal("Ingest() error = nil, want forced River insert failure")
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE device_id = $1`, failingDevice).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 0 {
		t.Fatalf("events after River insert failure = %d, want 0", eventCount)
	}
}

func floatPointer(value float64) *float64 { return &value }
func stringPointer(value string) *string  { return &value }
func intPointer(value int) *int           { return &value }
