# coreban — local tasks. Reads .env when present (MIGRATION_URL etc.).
-include .env
export

.PHONY: run build test test-unit test-integration migrate-up migrate-create sqlc sqlc-check docker-build

run:
	go run ./cmd/coreban

build:
	go build -o bin/coreban-api ./cmd/coreban

test: ## unit + Docker-backed integration (testcontainers)
	go test ./... -count=1

test-unit: ## no Docker needed
	go test ./... -short -count=1

test-integration:
	go test ./... -count=1 -run 'Smoke|Postgres'

# Applies the embedded migrations to MIGRATION_URL (owner, direct host,
# options=-c search_path=coreban). Run internal/db/bootstrap.sql once first.
migrate-up:
	go run ./cmd/coreban migrate

# Needs the goose CLI: go install github.com/pressly/goose/v3/cmd/goose@latest
migrate-create:
	@test -n "$(name)" || (echo "usage: make migrate-create name=<snake_case>" && exit 1)
	goose -dir internal/db/migrations -s create $(name) sql

# Regenerates internal/db/sqlc from internal/db/queries + the migrations.
# Needs sqlc v1.31.1 (same as coredraw/corefinance):
#   go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
sqlc:
	sqlc generate

# Fails when the generated code is stale.
sqlc-check:
	sqlc diff

docker-build:
	docker build -f Dockerfile.coreban-api --target api -t coreban-api .
