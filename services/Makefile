SERVICE ?= api
APP_ENV ?= local
HTTP_ADDR ?= :8080
DATABASE_URL ?= postgres://enjoythings:enjoythings_dev_password@localhost:5432/enjoythings?sslmode=disable
JWT_SECRET ?= local-dev-jwt-secret-change-me
DB_MAX_CONNS ?= 10
DEV_CMD=go run ./cmd/$(SERVICE)

.PHONY: dev dev-db test vet lint sqlc

dev: dev-db
	@command -v air >/dev/null || (echo "air is required. Install it with: go install github.com/air-verse/air@latest" && exit 1)
	APP_ENV="$(APP_ENV)" \
	HTTP_ADDR="$(HTTP_ADDR)" \
	DATABASE_URL="$(DATABASE_URL)" \
	JWT_SECRET="$(JWT_SECRET)" \
	DB_MAX_CONNS="$(DB_MAX_CONNS)" \
	DEV_CMD="$(DEV_CMD)" \
	air -c .air.toml

dev-db:
	docker compose -f docker-compose.dev.yml up -d postgres

test:
	go test ./...

vet:
	go vet ./...

lint:
	golangci-lint run

sqlc:
	sqlc generate
