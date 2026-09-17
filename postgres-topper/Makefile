GO ?= go
BIN := bin/topper

# The local development database is plaidsync's Compose Postgres (see
# ../plaid-backend-golang/docker-compose.yml). Tests that need a database
# read TOPPER_TEST_DATABASE_URL and skip when it is unset.
DEV_COMPOSE := docker compose -f ../plaid-backend-golang/docker-compose.yml
TEST_DATABASE_URL ?= postgres://plaidsync:plaidsync@127.0.0.1:5433/plaidsync?sslmode=disable

# Password given to the topper role in the development database. It matches
# TOPPER_DATABASE_URL in .env.example.
DEV_TOPPER_PASSWORD ?= topper

.PHONY: all build run test test-db db-up db-down db-logs db-init-topper vet fmt fmt-check check clean docker-build

all: check build

build:
	$(GO) build -o $(BIN) ./cmd/topper

## run: build and start the service with variables from ./.env
run: build
	@test -f .env || (echo ".env not found; copy .env.example to .env and fill it in" && exit 1)
	set -a; . ./.env; set +a; ./$(BIN)

## test: unit tests only (database-backed tests skip)
test:
	$(GO) test ./...

## test-db: all tests, including database-backed ones, against the dev Postgres
test-db: db-up
	TOPPER_TEST_DATABASE_URL="$(TEST_DATABASE_URL)" $(GO) test ./...

db-up:
	$(DEV_COMPOSE) up -d --wait postgres

db-down:
	$(DEV_COMPOSE) down

db-logs:
	$(DEV_COMPOSE) logs -f postgres

## db-init-topper: create the topper role and schema in the dev database (idempotent)
db-init-topper: db-up
	$(DEV_COMPOSE) exec -T postgres psql -v ON_ERROR_STOP=1 -v topper_password="$(DEV_TOPPER_PASSWORD)" \
		-U plaidsync -d plaidsync -f - < deploy/postgres-init/topper.sql

vet:
	$(GO) vet ./...

fmt:
	gofmt -w .

fmt-check:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

check: fmt-check vet test

docker-build:
	docker build -t postgres-topper .

clean:
	rm -rf bin
