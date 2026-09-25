package alarms

import (
	"context"
	"log/slog"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

const subscriberBuffer = 1024

type Feed struct {
	store   store
	wake    chan struct{}
	done    chan struct{}
	cancel  context.CancelFunc
	cursors map[string]cursor

	mu          sync.Mutex
	subscribers map[chan Alarm]struct{}
	pending     map[string]struct{}
}

func NewFeed(parent context.Context, pool *pgxpool.Pool) (*Feed, error) {
	feed := &Feed{
		store:       store{pool: pool},
		wake:        make(chan struct{}, 1),
		done:        make(chan struct{}),
		subscribers: map[chan Alarm]struct{}{},
		pending:     map[string]struct{}{},
	}

	latest, err := feed.store.latestCursors(parent)
	if err != nil {
		return nil, err
	}
	feed.cursors = latest

	ctx, cancel := context.WithCancel(parent)
	feed.cancel = cancel
	go feed.run(ctx)
	return feed, nil
}

func (f *Feed) Wake(roomID string) {
	f.mu.Lock()
	f.pending[roomID] = struct{}{}
	f.mu.Unlock()

	select {
	case f.wake <- struct{}{}:
	default:
	}
}

func (f *Feed) Subscribe() (<-chan Alarm, func()) {
	updates := make(chan Alarm, subscriberBuffer)
	f.mu.Lock()
	f.subscribers[updates] = struct{}{}
	f.mu.Unlock()

	unsubscribe := func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		if _, exists := f.subscribers[updates]; !exists {
			return
		}
		delete(f.subscribers, updates)
		close(updates)
	}
	return updates, unsubscribe
}

func (f *Feed) Close() {
	f.cancel()
	<-f.done
}

func (f *Feed) run(ctx context.Context) {
	defer close(f.done)
	defer f.closeSubscribers()

	for {
		select {
		case <-ctx.Done():
			return
		case <-f.wake:
			for _, roomID := range f.takePending() {
				items, err := f.store.listRoomAfter(ctx, roomID, f.cursors[roomID])
				if err != nil {
					if ctx.Err() == nil {
						slog.ErrorContext(ctx, "publish alarms", "error", err, "room_id", roomID)
					}
					continue
				}
				f.publish(roomID, items)
			}
		}
	}
}

func (f *Feed) takePending() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	rooms := make([]string, 0, len(f.pending))
	for roomID := range f.pending {
		rooms = append(rooms, roomID)
		delete(f.pending, roomID)
	}
	return rooms
}

func (f *Feed) publish(roomID string, alarms []Alarm) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, alarm := range alarms {
		for subscriber := range f.subscribers {
			select {
			case subscriber <- alarm:
			default:
				delete(f.subscribers, subscriber)
				close(subscriber)
			}
		}
		f.cursors[roomID] = cursor{CreatedAt: alarm.CreatedAt, EventID: alarm.EventID}
	}
}

func (f *Feed) closeSubscribers() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for subscriber := range f.subscribers {
		delete(f.subscribers, subscriber)
		close(subscriber)
	}
}
