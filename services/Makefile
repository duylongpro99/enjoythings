SERVICE ?= api
APP_ENV ?= local
HTTP_ADDR ?= :8080
DATABASE_URL ?= postgres://enjoythings:enjoythings_dev_password@localhost:5432/enjoythings?sslmode=disable
JWT_SECRET ?= local-dev-jwt-secret-change-me
DB_MAX_CONNS ?= 10
DEV_CMD=go run ./cmd/$(SERVICE)

.PHONY: dev dev-db test test-phase3-e2e phase3-smoke wallet-rollout-test vet lint sqlc

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
	docker compose -f docker-compose.dev.yml up -d postgres kafka kafka-init

test:
	go test ./...

test-phase3-e2e:
	go test ./internal/phase3e2e -v

phase3-smoke:
	go run ./devtools/phase3_smoke

wallet-rollout-test:
	./devtools/k8s_wallet_rollout.sh

vet:
	go vet ./...

lint:
	golangci-lint run

sqlc:
	sqlc generate
