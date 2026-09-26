//go:build integration

package occupancy_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"teton/internal/event"
	"teton/internal/eventstore"
	"teton/internal/features/occupancy"
)

func TestOccupancyQuery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

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
	service := occupancy.NewService(pool)
	queryTime := time.Now().UTC().Truncate(time.Millisecond)
	prefix := fmt.Sprintf("occupancy_test_%d", queryTime.UnixNano())
	presence := func(deviceID, roomID string, eventTime time.Time, sequence int64, inRoom bool) event.Event {
		return event.Event{
			DeviceID: deviceID,
			RoomID:   roomID,
			Type:     event.TypePresence,
			Time:     eventTime,
			Sequence: sequence,
			InRoom:   &inRoom,
		}
	}
	ingest := func(input event.Event) eventstore.InsertResult {
		t.Helper()
		result, err := events.Ingest(ctx, input, queryTime)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	assertOccupancy := func(roomID string, window time.Duration, wantInRoom bool, wantPct float64) {
		t.Helper()
		result, err := service.Get(ctx, roomID, window, queryTime)
		if err != nil {
			t.Fatal(err)
		}
		if result.InRoom != wantInRoom || math.Abs(result.OccupiedPct-wantPct) > 1e-9 {
			t.Fatalf(
				"occupancy for %s over %s = %#v, want in_room=%t occupied_pct=%v",
				roomID,
				window,
				result,
				wantInRoom,
				wantPct,
			)
		}
		if result.WindowSeconds != int(window.Seconds()) {
			t.Fatalf("window seconds = %d, want %d", result.WindowSeconds, int(window.Seconds()))
		}
	}

	correctionRoom := prefix + "_correction"
	baseEvents := []event.Event{
		presence(prefix+"_base", correctionRoom, queryTime.Add(-50*time.Minute), 1, true),
		presence(prefix+"_base", correctionRoom, queryTime.Add(-30*time.Minute), 2, false),
	}
	ingest(baseEvents[0])
	ingest(baseEvents[1])
	assertOccupancy(correctionRoom, time.Hour, false, 20.0/60.0)

	retry := ingest(baseEvents[1])
	if retry.Inserted {
		t.Fatalf("retry result = %#v, want duplicate", retry)
	}

	lateEvents := []event.Event{
		presence(prefix+"_late_a", correctionRoom, queryTime.Add(-45*time.Minute), 1, false),
		presence(prefix+"_late_b", correctionRoom, queryTime.Add(-40*time.Minute), 1, true),
	}
	errorsByEvent := make(chan error, len(lateEvents))
	for _, input := range lateEvents {
		go func() {
			_, err := events.Ingest(ctx, input, queryTime)
			errorsByEvent <- err
		}()
	}
	for range lateEvents {
		if err := <-errorsByEvent; err != nil {
			t.Fatal(err)
		}
	}
	assertOccupancy(correctionRoom, time.Minute, false, 0)
	assertOccupancy(correctionRoom, 5*time.Minute, false, 0)
	assertOccupancy(correctionRoom, time.Hour, false, 15.0/60.0)

	firstRoom := prefix + "_first"
	ingest(presence(prefix+"_first_device", firstRoom, queryTime.Add(-30*time.Second), 1, true))
	assertOccupancy(firstRoom, time.Minute, true, 0.5)

	tieRoom := prefix + "_tie"
	tieTime := queryTime.Add(-10 * time.Second)
	ingest(presence(prefix+"_tie_z", tieRoom, tieTime, 1, true))
	ingest(presence(prefix+"_tie_a", tieRoom, tieTime, 1, false))
	assertOccupancy(tieRoom, time.Minute, true, 10.0/60.0)

	futureRoom := prefix + "_future"
	ingest(presence(prefix+"_future_device", futureRoom, queryTime.Add(30*time.Minute), 1, true))
	_, err = service.Get(ctx, futureRoom, time.Minute, queryTime)
	if !errors.Is(err, occupancy.ErrNotFound) {
		t.Fatalf("future-only room error = %v, want ErrNotFound", err)
	}
}
