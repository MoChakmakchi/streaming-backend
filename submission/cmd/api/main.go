package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"teton/internal/config"
	"teton/internal/eventstore"
	"teton/internal/features/alarms"
	"teton/internal/features/health"
	"teton/internal/features/occupancy"
	"teton/internal/httpapi"
)

const (
	normalBatchWriters    = 4
	normalBatchSize       = 400
	normalBatchWait       = 4 * time.Millisecond
	normalPendingBatches  = 15
	normalIngestCapacity  = (normalPendingBatches + normalBatchWriters + 1) * normalBatchSize
	fallIngestConcurrency = 4
)

func main() {
	if err := run(); err != nil {
		slog.Error("api stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("parse database configuration: %w", err)
	}
	poolConfig.MaxConns = cfg.DatabaseMaxConn
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return fmt.Errorf("open database pool: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	events := eventstore.New(pool)
	eventBatcher := eventstore.NewBatcher(
		events,
		normalBatchWriters,
		normalBatchSize,
		normalPendingBatches,
		normalIngestCapacity,
		normalBatchWait,
		cfg.IngestDeadline,
	)
	defer eventBatcher.Close()
	alarmFeed, err := alarms.NewFeed(ctx, pool)
	if err != nil {
		return fmt.Errorf("start alarm feed: %w", err)
	}
	defer alarmFeed.Close()
	alarmService := alarms.NewService(pool, alarmFeed)
	healthService := health.NewService(pool)
	occupancyService := occupancy.NewService(pool)
	metrics := httpapi.NewMetrics(pool)
	eventsHandler := httpapi.NewEventsHandler(
		eventBatcher.Ingest,
		alarmService.Ingest,
		normalIngestCapacity,
		fallIngestConcurrency,
		cfg.IngestDeadline,
		time.Now,
	)
	healthHandler := httpapi.NewHealthHandler(healthService.Get, time.Now)
	occupancyHandler := httpapi.NewOccupancyHandler(occupancyService.Get, time.Now)
	alarmHistoryHandler := httpapi.NewAlarmHistoryHandler(alarmService.List)
	alarmStreamHandler := httpapi.NewAlarmStreamHandler(
		alarmService.List,
		alarmFeed.Subscribe,
		metrics.ObserveAlarmDelivery,
	)
	server := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: metrics.Middleware(
			httpapi.LogRequests(
				logger,
				httpapi.NewServer(
					eventsHandler,
					healthHandler,
					occupancyHandler,
					alarmHistoryHandler,
					alarmStreamHandler,
					metrics,
				),
			),
		),
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverError := make(chan error, 1)
	go func() {
		serverError <- server.ListenAndServe()
	}()
	logger.Info("api listening", "address", cfg.HTTPAddr)

	select {
	case err := <-serverError:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	case <-ctx.Done():
	}

	logger.Info("api shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shut down HTTP server: %w", err)
	}
	logger.Info("api stopped")
	return nil
}
