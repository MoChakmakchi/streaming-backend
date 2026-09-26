GO ?= go
K6 ?= k6
PYTHON ?= python
COMPOSE ?= docker compose -f deployment/compose.yaml
DB_USER ?= teton
DB_NAME ?= teton
SERVICE_URL ?= http://localhost:8090
DEVICES ?= 50
K6_DEVICES ?= 5000
RUN_ID ?= $(shell date +%s)
PROBE_DELAY ?= 0
ALARM_SAMPLES ?= 20
BASELINE_ONLY ?= 0

.PHONY: help build migrate api test test-integration test-race test-load test-probe test-restart test-evaluator docker-init docker-up docker-down docker-destroy

help:
	@echo "Targets:"
	@echo "  build             compile all Go packages"
	@echo "  migrate           apply the application migration"
	@echo "  api               run the API locally"
	@echo "  test              run Go tests"
	@echo "  test-integration  run PostgreSQL integration tests"
	@echo "  test-race         run Go tests with the race detector"
	@echo "  test-load         run the full k6 baseline and burst scenario"
	@echo "  test-probe        verify aggregations and alarms, optionally during load"
	@echo "  test-restart      hard-kill the API and verify durable recovery"
	@echo "  test-evaluator    run the supplied smoke evaluator"
	@echo "  docker-init       start containers and apply migrations"
	@echo "  docker-up         start containers without migrating"
	@echo "  docker-down       stop containers and preserve data"
	@echo "  docker-destroy    stop containers and delete data"

build:
	$(GO) build ./...

migrate:
	$(COMPOSE) exec -T postgres psql -U $(DB_USER) -d $(DB_NAME) -v ON_ERROR_STOP=1 < migrations/001_initial.sql

api:
	$(GO) run ./cmd/api

test:
	$(GO) test ./...

test-integration:
	REQUIRE_TEST_DATABASE=1 $(GO) test -tags=integration -p=1 ./internal/... -count=1 -run '$(if $(TEST),(?i)$(TEST),.)'

test-race:
	$(GO) test -race ./...

test-load:
	SERVICE_URL=$(SERVICE_URL) RUN_ID=$(RUN_ID) DEVICES=$(K6_DEVICES) BASELINE_ONLY=$(BASELINE_ONLY) \
	BASELINE_RATE=$(BASELINE_RATE) BURST_RATE=$(BURST_RATE) \
	BASELINE_DURATION=$(BASELINE_DURATION) BURST_DURATION=$(BURST_DURATION) \
	FIRST_BURST_START=$(FIRST_BURST_START) SECOND_BURST_START=$(SECOND_BURST_START) \
	PRE_ALLOCATED_VUS=$(PRE_ALLOCATED_VUS) MAX_VUS=$(MAX_VUS) \
	$(K6) run test/load/events.js

test-probe:
	$(PYTHON) test/load/verify.py --target $(SERVICE_URL) --delay $(PROBE_DELAY) --alarm-samples $(ALARM_SAMPLES)

test-restart:
	SERVICE_URL=$(SERVICE_URL) bash test/load/restart.sh

test-evaluator:
	$(MAKE) -C .. smoke SERVICE_URL=$(SERVICE_URL) DEVICES=$(DEVICES)

docker-init:
	$(COMPOSE) up -d postgres adminer
	$(MAKE) migrate
	$(COMPOSE) up -d --build api

docker-up:
	$(COMPOSE) up -d postgres adminer api

docker-down:
	$(COMPOSE) down --remove-orphans

docker-destroy:
	$(COMPOSE) down --volumes --remove-orphans
