SERVICE ?= api
APP_ENV ?= local
HTTP_ADDR ?= :8080
DATABASE_URL ?= postgres://enjoythings:enjoythings_dev_password@localhost:5432/enjoythings?sslmode=disable
JWT_SECRET ?= local-dev-jwt-secret-change-me
DB_MAX_CONNS ?= 10
DEV_CMD=go run ./cmd/$(SERVICE)

.PHONY: dev dev-db test test-phase3-e2e test-phase4-e2e test-phase4-python phase3-smoke phase4-observability phase4-smoke wallet-rollout-test certs vet lint sqlc generate-python-grpc

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

test-phase4-e2e: test-phase4-python
	go test ./internal/phase4e2e -v

test-phase4-python:
	cd .. && uv run pytest tests/fraud/test_phase4_e2e.py -v

phase3-smoke:
	go run ./devtools/phase3_smoke

phase4-observability:
	docker compose up -d --build
	go run ./devtools/phase3_smoke
	go run ./devtools/phase4_smoke

phase4-smoke:
	go run ./devtools/phase4_smoke

wallet-rollout-test:
	./devtools/k8s_wallet_rollout.sh

certs:
	./devtools/gen-certs.sh

vet:
	go vet ./...

lint:
	golangci-lint run

sqlc:
	sqlc generate

generate-python-grpc:
	cd .. && uv run python -m grpc_tools.protoc \
		-I services/proto \
		--python_out=app/fraud/integrations/gen \
		--grpc_python_out=app/fraud/integrations/gen \
		services/proto/ledger/v1/ledger.proto \
		services/proto/verification/v1/verification.proto
	cd .. && uv run python services/devtools/rewrite_python_grpc_imports.py
