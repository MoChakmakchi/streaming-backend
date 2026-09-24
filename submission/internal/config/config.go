package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddr        string
	DatabaseURL     string
	DatabaseMaxConn int32
	IngestDeadline  time.Duration
	LogLevel        slog.Level
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:        ":8080",
		DatabaseURL:     "postgres://teton:teton@localhost:5433/teton?sslmode=disable",
		DatabaseMaxConn: 32,
		IngestDeadline:  2 * time.Second,
		LogLevel:        slog.LevelInfo,
	}

	if value := os.Getenv("HTTP_ADDR"); value != "" {
		cfg.HTTPAddr = value
	}
	if value := os.Getenv("DATABASE_URL"); value != "" {
		cfg.DatabaseURL = value
	}
	if value := os.Getenv("DATABASE_MAX_CONNECTIONS"); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 32)
		if err != nil || parsed < 1 {
			return Config{}, fmt.Errorf("DATABASE_MAX_CONNECTIONS must be a positive integer")
		}
		cfg.DatabaseMaxConn = int32(parsed)
	}
	if value := os.Getenv("INGEST_DEADLINE"); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed <= 0 {
			return Config{}, fmt.Errorf("INGEST_DEADLINE must be a positive duration")
		}
		cfg.IngestDeadline = parsed
	}
	if value := os.Getenv("LOG_LEVEL"); value != "" {
		if err := cfg.LogLevel.UnmarshalText([]byte(value)); err != nil {
			return Config{}, fmt.Errorf("parse LOG_LEVEL: %w", err)
		}
	}
	return cfg, nil
}
