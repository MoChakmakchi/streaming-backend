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
	"teton/internal/features/health"
	"teton/internal/features/occupancy"
	"teton/internal/httpapi"
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
	healthService := health.NewService(pool)
	occupancyService := occupancy.NewService(pool)
	eventsHandler := httpapi.NewEventsHandler(
		events.Ingest,
		int(cfg.DatabaseMaxConn),
		cfg.IngestDeadline,
		time.Now,
	)
	healthHandler := httpapi.NewHealthHandler(healthService.Get, time.Now)
	occupancyHandler := httpapi.NewOccupancyHandler(occupancyService.Get, time.Now)
	server := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: httpapi.LogRequests(
			logger,
			httpapi.NewServer(eventsHandler, healthHandler, occupancyHandler),
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
