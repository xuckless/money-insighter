GO ?= go
BIN := bin/plaidsync

# Local Compose Postgres (see docker-compose.yml). Tests that need a database
# read PLAIDSYNC_TEST_DATABASE_URL and skip when it is unset.
TEST_DATABASE_URL ?= postgres://plaidsync:plaidsync@127.0.0.1:5433/plaidsync?sslmode=disable

.PHONY: all build run test test-db db-up db-down db-logs vet fmt fmt-check check clean

all: check build

build:
	$(GO) build -o $(BIN) ./cmd/plaidsync

## run: build and start the service with variables from ./.env
run: build
	@test -f .env || (echo ".env not found; copy .env.example to .env and fill it in" && exit 1)
	set -a; . ./.env; set +a; ./$(BIN)

## test: unit tests only (database-backed tests skip)
test:
	$(GO) test ./...

## test-db: all tests, including database-backed ones, against the Compose Postgres
test-db: db-up
	PLAIDSYNC_TEST_DATABASE_URL="$(TEST_DATABASE_URL)" $(GO) test ./...

db-up:
	docker compose up -d --wait postgres

db-down:
	docker compose down

db-logs:
	docker compose logs -f postgres

vet:
	$(GO) vet ./...

fmt:
	gofmt -w .

fmt-check:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

check: fmt-check vet test

clean:
	rm -rf bin
