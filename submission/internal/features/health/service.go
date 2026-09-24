package health

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("device health not found")

type Service struct {
	store *store
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{store: &store{pool: pool}}
}

func (s *Service) Get(ctx context.Context, deviceID string, queryTime time.Time) (Health, error) {
	lastHeartbeat, heartbeatCount, err := s.store.snapshot(ctx, deviceID, queryTime)
	if errors.Is(err, pgx.ErrNoRows) {
		return Health{}, ErrNotFound
	}
	if err != nil {
		return Health{}, err
	}

	availability := float64(heartbeatCount) / 300
	if availability > 1 {
		availability = 1
	}
	return Health{LastHeartbeatAt: lastHeartbeat, Availability5m: availability}, nil
}
