package eventstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"teton/internal/event"
)

var (
	ErrBatcherClosed = errors.New("event batcher closed")
	ErrBatcherFull   = errors.New("event batcher full")
)

type batchRequest struct {
	ctx        context.Context
	input      event.Event
	receivedAt time.Time
	result     chan batchResponse
}

type batchResponse struct {
	insert InsertResult
	err    error
}

type Batcher struct {
	store            *Store
	queue            chan batchRequest
	batches          chan []batchRequest
	maxSize          int
	maxWait          time.Duration
	operationTimeout time.Duration
	ctx              context.Context
	cancel           context.CancelFunc
	routines         sync.WaitGroup
}

func NewBatcher(
	store *Store,
	writerCount int,
	maxSize int,
	pendingBatchCapacity int,
	queueCapacity int,
	maxWait time.Duration,
	operationTimeout time.Duration,
) *Batcher {
	ctx, cancel := context.WithCancel(context.Background())
	batcher := &Batcher{
		store:            store,
		queue:            make(chan batchRequest, queueCapacity),
		batches:          make(chan []batchRequest, pendingBatchCapacity),
		maxSize:          maxSize,
		maxWait:          maxWait,
		operationTimeout: operationTimeout,
		ctx:              ctx,
		cancel:           cancel,
	}
	start := func(run func()) {
		batcher.routines.Add(1)
		go func() {
			defer batcher.routines.Done()
			run()
		}()
	}
	start(batcher.collect)
	for range writerCount {
		start(batcher.write)
	}
	return batcher
}

func (b *Batcher) Close() {
	b.cancel()
	b.routines.Wait()
}

func (b *Batcher) Ingest(
	ctx context.Context,
	input event.Event,
	receivedAt time.Time,
) (InsertResult, error) {
	result := make(chan batchResponse, 1)
	request := batchRequest{ctx: ctx, input: input, receivedAt: receivedAt, result: result}

	select {
	case b.queue <- request:
	case <-ctx.Done():
		return InsertResult{}, ctx.Err()
	case <-b.ctx.Done():
		return InsertResult{}, ErrBatcherClosed
	default:
		return InsertResult{}, ErrBatcherFull
	}

	select {
	case response := <-result:
		return response.insert, response.err
	case <-ctx.Done():
		return InsertResult{}, ctx.Err()
	case <-b.ctx.Done():
		return InsertResult{}, ErrBatcherClosed
	}
}

func (b *Batcher) collect() {
	batch := make([]batchRequest, 0, b.maxSize)
	for {
		select {
		case <-b.ctx.Done():
			return
		case request := <-b.queue:
			batch = append(batch, request)
		}

		timer := time.NewTimer(b.maxWait)
		timerChannel := timer.C
		var handoff chan []batchRequest
		sent := false
	collect:
		for len(batch) < b.maxSize {
			select {
			case <-b.ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return
			case request := <-b.queue:
				batch = append(batch, request)
			case <-timerChannel:
				timerChannel = nil
				handoff = b.batches
			case handoff <- batch:
				sent = true
				break collect
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}

		if !sent {
			select {
			case b.batches <- batch:
			case <-b.ctx.Done():
				return
			}
		}
		batch = make([]batchRequest, 0, b.maxSize)
	}
}

func (b *Batcher) write() {
	for {
		select {
		case <-b.ctx.Done():
			return
		case batch := <-b.batches:
			active := batch[:0]
			for _, request := range batch {
				if err := request.ctx.Err(); err != nil {
					request.result <- batchResponse{err: err}
					continue
				}
				active = append(active, request)
			}
			if len(active) == 0 {
				continue
			}

			ctx, cancel := context.WithTimeout(b.ctx, b.operationTimeout)
			results, err := b.flush(ctx, active)
			cancel()
			for index, request := range active {
				response := batchResponse{err: err}
				if err == nil {
					response.insert = results[index]
				}
				request.result <- response
			}
		}
	}
}

func (b *Batcher) flush(
	ctx context.Context,
	requests []batchRequest,
) ([]InsertResult, error) {
	var query strings.Builder
	query.WriteString(`
		INSERT INTO events (device_id, room_id, event_type, event_time, seq, payload, received_at)
		VALUES `)
	args := make([]any, 0, len(requests)*7)
	type eventKey struct {
		deviceID string
		sequence int64
	}
	requestByKey := make(map[eventKey]int, len(requests))
	for index, request := range requests {
		payload, err := request.input.Payload()
		if err != nil {
			return nil, fmt.Errorf("encode event payload: %w", err)
		}
		input := request.input
		if index > 0 {
			query.WriteByte(',')
		}
		parameter := index*7 + 1
		fmt.Fprintf(
			&query,
			"($%d,$%d,$%d,$%d,$%d,$%d,$%d)",
			parameter,
			parameter+1,
			parameter+2,
			parameter+3,
			parameter+4,
			parameter+5,
			parameter+6,
		)
		args = append(args,
			input.DeviceID,
			input.RoomID,
			input.Type,
			input.Time,
			input.Sequence,
			payload,
			request.receivedAt,
		)
		key := eventKey{deviceID: input.DeviceID, sequence: input.Sequence}
		if _, exists := requestByKey[key]; !exists {
			requestByKey[key] = index
		}
	}
	query.WriteString(`
		ON CONFLICT (device_id, seq) DO NOTHING
		RETURNING id, device_id, seq`)
	connection, err := b.store.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire event batch connection: %w", err)
	}
	defer connection.Release()

	tx, err := connection.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin event batch: %w", err)
	}
	defer tx.Rollback(ctx)

	results := make([]InsertResult, len(requests))
	rows, err := tx.Query(ctx, query.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("insert event batch: %w", err)
	}
	for rows.Next() {
		var eventID int64
		var key eventKey
		if err := rows.Scan(&eventID, &key.deviceID, &key.sequence); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan inserted event: %w", err)
		}
		if index, exists := requestByKey[key]; exists {
			results[index] = InsertResult{EventID: eventID, Inserted: true}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("read inserted events: %w", err)
	}
	rows.Close()

	err = tx.Commit(ctx)
	if err != nil {
		return nil, fmt.Errorf("commit event batch: %w", err)
	}
	return results, nil
}
