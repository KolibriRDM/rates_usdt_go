.DEFAULT_GOAL := help

COMPOSE ?= docker compose
IMAGE ?= rapira_rates:local
ARGS ?=

export RAPIRA_TEST_DATABASE_URL

.PHONY: help up run build test test-race test-integration lint docker-build fmt tidy db-config db-up db-stop db-status db-logs db-ready db-shell db-schema migrate-up migrate-down proto proto-check

help:
	@echo "up / run / build / test / test-race / test-integration / lint / docker-build"
	@echo "fmt / tidy / db-config / db-up / db-stop / db-status / db-logs / db-ready / db-shell"
	@echo "db-schema / migrate-up / migrate-down (drops the rates table and its data)"
	@echo "proto / proto-check"

up:
	$(COMPOSE) up -d --wait --wait-timeout 60 postgres
	$(MAKE) migrate-up
	$(COMPOSE) up -d --build app

run:
	go run ./cmd $(ARGS)

build:
	go build -trimpath -o bin/app ./cmd

test:
	go test ./...

test-race:
	go test -race ./...

test-integration:
	@if [ -z "$$RAPIRA_TEST_DATABASE_URL" ]; then echo "Set RAPIRA_TEST_DATABASE_URL to run integration tests"; exit 1; fi
	go test -race -count=1 -timeout=90s -run Integration ./...

lint:
	golangci-lint run ./...

docker-build:
	docker build -t $(IMAGE) .

fmt:
	go fmt ./...

tidy:
	go mod tidy

db-config:
	$(COMPOSE) config --quiet

db-up:
	$(COMPOSE) up -d --wait --wait-timeout 60 postgres

db-stop:
	$(COMPOSE) stop postgres

db-status:
	$(COMPOSE) ps

db-logs:
	$(COMPOSE) logs postgres

db-ready:
	$(COMPOSE) exec -T postgres pg_isready -U rates -d rapira_rates

db-shell:
	$(COMPOSE) exec postgres psql -U rates -d rapira_rates

db-schema:
	$(COMPOSE) exec -T postgres psql -U rates -d rapira_rates -c '\d rate_results'

migrate-up:
	$(COMPOSE) exec -T postgres psql -U rates -d rapira_rates -v ON_ERROR_STOP=1 --single-transaction -f - < migrations/001_create_rate_results.up.sql

migrate-down:
	$(COMPOSE) exec -T postgres psql -U rates -d rapira_rates -v ON_ERROR_STOP=1 --single-transaction -f - < migrations/001_create_rate_results.down.sql

proto:
	buf generate

proto-check:
	buf build
