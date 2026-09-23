GO ?= go
COMPOSE ?= docker compose -f deployment/compose.yaml
DB_USER ?= teton
DB_NAME ?= teton

.PHONY: help build migrate api test down

help:
	@echo "Targets:"
	@echo "  build    compile all Go packages"
	@echo "  migrate  apply the application migration"
	@echo "  api      run the API locally"
	@echo "  test     run Go tests"
	@echo "  down     stop local containers"

build:
	$(GO) build ./...

migrate:
	$(COMPOSE) exec -T postgres psql -U $(DB_USER) -d $(DB_NAME) -v ON_ERROR_STOP=1 < migrations/001_initial.sql

api:
	$(GO) run ./cmd/api

test:
	$(GO) test ./...

down:
	$(COMPOSE) down --remove-orphans
