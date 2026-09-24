GO ?= go
COMPOSE ?= docker compose -f deployment/compose.yaml
DB_USER ?= teton
DB_NAME ?= teton

.PHONY: help build migrate api test test-integration docker-init docker-up docker-down docker-destroy

help:
	@echo "Targets:"
	@echo "  build             compile all Go packages"
	@echo "  migrate           apply the application migration"
	@echo "  api               run the API locally"
	@echo "  test              run Go tests"
	@echo "  test-integration  run PostgreSQL integration tests"
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
