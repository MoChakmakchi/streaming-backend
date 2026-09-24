GO ?= go
COMPOSE ?= docker compose -f deployment/compose.yaml
DB_USER ?= teton
DB_NAME ?= teton
DATABASE_URL ?= postgres://teton:teton@localhost:5433/teton?sslmode=disable
RIVER_CLI_VERSION ?= v0.37.0

.PHONY: help build migrate api test test-integration docker-init docker-up docker-down docker-destroy

help:
	@echo "Targets:"
	@echo "  build             compile all Go packages"
	@echo "  migrate           apply application and River migrations"
	@echo "  api               run the API locally"
	@echo "  test              run Go tests"
	@echo "  test-integration  run PostgreSQL ingestion tests"
	@echo "  docker-init       start containers and apply migrations"
	@echo "  docker-up         start containers without migrating"
	@echo "  docker-down       stop containers and preserve data"
	@echo "  docker-destroy    stop containers and delete data"

build:
	$(GO) build ./...

migrate:
	$(COMPOSE) exec -T postgres psql -U $(DB_USER) -d $(DB_NAME) -v ON_ERROR_STOP=1 < migrations/001_initial.sql
	$(GO) run github.com/riverqueue/river/cmd/river@$(RIVER_CLI_VERSION) migrate-up --database-url "$(DATABASE_URL)"

api:
	$(GO) run ./cmd/api

test:
	$(GO) test ./...

test-integration:
	REQUIRE_TEST_DATABASE=1 $(GO) test ./internal/eventstore -count=1

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
