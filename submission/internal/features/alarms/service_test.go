//go:build integration

package alarms_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"teton/internal/event"
	"teton/internal/features/alarms"
)

func TestAlarmsDeduplicationAndHistory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := testPool(t, ctx)
	defer pool.Close()

	feed, err := alarms.NewFeed(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	defer feed.Close()
	service := alarms.NewService(pool, feed)

	receivedAt := time.Now().UTC().Truncate(time.Millisecond)
	prefix := fmt.Sprintf("alarm_dedupe_%d", receivedAt.UnixNano())
	fall := func(sequence int64, eventTime time.Time) event.Event {
		confidence := 0.92
		return event.Event{
			DeviceID:   prefix + "_device",
			RoomID:     prefix + "_room",
			Type:       event.TypeFallWarn,
			Time:       eventTime,
			Sequence:   sequence,
			Confidence: &confidence,
		}
	}

	first, err := service.Ingest(ctx, fall(1, receivedAt), receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Ingest(ctx, fall(2, receivedAt.Add(2*time.Second)), receivedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Ingest(ctx, fall(3, receivedAt.Add(4*time.Second)), receivedAt); err != nil {
		t.Fatal(err)
	}
	retry, err := service.Ingest(ctx, fall(1, receivedAt), receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	if retry.Inserted || retry.EventID != first.EventID {
		t.Fatalf("retry result = %#v, want existing event %d", retry, first.EventID)
	}

	items, err := service.List(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	items = alarmsForDevice(items, prefix+"_device")
	if len(items) != 2 {
		t.Fatalf("alarms = %d, want 2", len(items))
	}
	if !items[0].EventTime.Equal(receivedAt) || !items[1].EventTime.Equal(receivedAt.Add(4*time.Second)) {
		t.Fatalf("alarm timestamps = %s and %s", items[0].EventTime, items[1].EventTime)
	}
	if items[0].EventID == items[1].EventID {
		t.Fatal("distinct alarms have the same stable ID")
	}
	since := items[0].CreatedAt
	inclusive, err := service.List(ctx, &since)
	if err != nil {
		t.Fatal(err)
	}
	inclusive = alarmsForDevice(inclusive, prefix+"_device")
	if len(inclusive) != 2 || inclusive[0].EventID != items[0].EventID {
		t.Fatalf("inclusive history = %#v", inclusive)
	}

	late := fall(4, receivedAt.Add(-30*time.Minute))
	late.DeviceID = prefix + "_late"
	late.RoomID = prefix + "_late_room"
	future := fall(5, receivedAt.Add(30*time.Minute))
	future.DeviceID = prefix + "_future"
	future.RoomID = prefix + "_future_room"
	if _, err := service.Ingest(ctx, late, receivedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Ingest(ctx, future, receivedAt); err != nil {
		t.Fatal(err)
	}

	items, err = service.List(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(alarmsForDevice(items, late.DeviceID)) != 1 || len(alarmsForDevice(items, future.DeviceID)) != 1 {
		t.Fatal("accepted late and future falls were not created immediately")
	}
}

func TestAlarmsConcurrentPublicationUsesCommittedOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := testPool(t, ctx)
	defer pool.Close()

	feed, err := alarms.NewFeed(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	defer feed.Close()
	service := alarms.NewService(pool, feed)
	updates, unsubscribe := feed.Subscribe()
	defer unsubscribe()

	receivedAt := time.Now().UTC().Truncate(time.Millisecond)
	prefix := fmt.Sprintf("alarm_concurrent_%d", receivedAt.UnixNano())
	errorsByEvent := make(chan error, 8)
	for sequence := int64(1); sequence <= 8; sequence++ {
		go func() {
			confidence := 0.9
			_, err := service.Ingest(ctx, event.Event{
				DeviceID:   prefix + "_device",
				RoomID:     prefix + "_room",
				Type:       event.TypeFallWarn,
				Time:       receivedAt,
				Sequence:   sequence,
				Confidence: &confidence,
			}, receivedAt)
			errorsByEvent <- err
		}()
	}
	for range 8 {
		if err := <-errorsByEvent; err != nil {
			t.Fatal(err)
		}
	}

	first := receiveAlarm(t, updates)
	items, err := service.List(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	items = alarmsForDevice(items, prefix+"_device")
	if len(items) != 1 {
		t.Fatalf("concurrent alarms = %d, want 1", len(items))
	}
	if first.EventID != items[0].EventID {
		t.Fatalf("published alarm ID = %d, committed alarm ID = %d", first.EventID, items[0].EventID)
	}

	confidence := 0.95
	if _, err := service.Ingest(ctx, event.Event{
		DeviceID:   prefix + "_device",
		RoomID:     prefix + "_room",
		Type:       event.TypeFallWarn,
		Time:       receivedAt.Add(4 * time.Second),
		Sequence:   9,
		Confidence: &confidence,
	}, receivedAt); err != nil {
		t.Fatal(err)
	}
	second := receiveAlarm(t, updates)
	if second.EventID <= first.EventID || second.CreatedAt.Before(first.CreatedAt) {
		t.Fatalf("publication order = %#v then %#v", first, second)
	}
}

func testPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://teton:teton@localhost:5433/teton?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		if os.Getenv("REQUIRE_TEST_DATABASE") == "1" {
			t.Fatalf("PostgreSQL integration database unavailable: %v", err)
		}
		t.Skipf("PostgreSQL integration database unavailable: %v", err)
	}

	migration, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "001_initial.sql"))
	if err != nil {
		pool.Close()
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(migration)); err != nil {
		pool.Close()
		t.Fatalf("apply application migration: %v", err)
	}
	return pool
}

func alarmsForDevice(items []alarms.Alarm, deviceID string) []alarms.Alarm {
	matches := []alarms.Alarm{}
	for _, item := range items {
		if item.DeviceID == deviceID {
			matches = append(matches, item)
		}
	}
	return matches
}

func receiveAlarm(t *testing.T, updates <-chan alarms.Alarm) alarms.Alarm {
	t.Helper()
	select {
	case alarm, open := <-updates:
		if !open {
			t.Fatal("alarm feed closed before delivery")
		}
		return alarm
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for alarm")
		return alarms.Alarm{}
	}
}
