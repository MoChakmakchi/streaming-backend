package occupancy

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("room occupancy not found")

type Service struct {
	store *store
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{store: &store{pool: pool}}
}

func (s *Service) Get(
	ctx context.Context,
	roomID string,
	window time.Duration,
	queryTime time.Time,
) (Occupancy, error) {
	inRoom, occupiedSeconds, err := s.store.snapshot(ctx, roomID, queryTime.Add(-window), queryTime)
	if errors.Is(err, pgx.ErrNoRows) {
		return Occupancy{}, ErrNotFound
	}
	if err != nil {
		return Occupancy{}, err
	}

	return Occupancy{
		InRoom:        inRoom,
		OccupiedPct:   occupiedSeconds / window.Seconds(),
		WindowSeconds: int(window.Seconds()),
	}, nil
}
