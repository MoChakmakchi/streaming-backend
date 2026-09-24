package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"teton/internal/config"
	"teton/internal/eventstore"
	"teton/internal/features/health"
	"teton/internal/processing"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	poolConfig.MaxConns = cfg.DatabaseMaxConn
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatal(err)
	}

	events := eventstore.New(pool)
	healthService := health.NewService(pool)
	workers := river.NewWorkers()
	river.AddWorker(workers, processing.NewProjectionWorker(pool, events, healthService))
	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: cfg.WorkerCount}},
		Workers: workers,
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := riverClient.Start(context.Background()); err != nil {
		log.Fatal(err)
	}

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := riverClient.Stop(shutdownCtx); err != nil {
		log.Printf("worker shutdown: %v", err)
	}
}
