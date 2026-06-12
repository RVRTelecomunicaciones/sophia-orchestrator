.PHONY: build test test-unit test-integration test-e2e test-e2e-sse test-chaos lint fmt vet vuln migrate-up migrate-down dev clean help

GO            ?= go
BIN_DIR       ?= bin
APP           := sophia-orchestator
PG_URL        ?= postgres://sophia:sophia@localhost:5432/sophia_orchestator?sslmode=disable
MIGRATE       ?= migrate

build: ## Build the binary
	$(GO) build -o $(BIN_DIR)/$(APP) ./cmd/sophia-orchestator

test: test-unit ## Run unit tests (default)

test-unit: ## Unit tests (race + count=1)
	$(GO) test ./internal/... -race -count=1

test-integration: ## Integration tests (testcontainers Postgres)
	$(GO) test -tags=integration ./test/... ./internal/adapters/outbound/pg/... -race -count=1 -timeout=5m

test-e2e: ## End-to-end SDD cycle test
	$(GO) test -tags=e2e ./test/e2e/... -count=1 -timeout=10m

test-e2e-sse: ## SSE streaming E2E
	$(GO) test -tags=e2e_sse ./test/e2e_sse/... -count=1 -timeout=10m

test-chaos: ## Chaos / recovery tests
	$(GO) test -tags=chaos ./test/chaos/... -count=1 -timeout=15m

lint: ## golangci-lint
	golangci-lint run ./...

fmt: ## Format code
	$(GO) fmt ./...
	@if command -v gofumpt >/dev/null 2>&1; then gofumpt -w .; fi

vet: ## go vet
	$(GO) vet ./...

vuln: ## govulncheck
	govulncheck ./...

migrate-up: ## Apply migrations
	$(MIGRATE) -path migrations/postgres -database "$(PG_URL)" up

migrate-down: ## Rollback one migration
	$(MIGRATE) -path migrations/postgres -database "$(PG_URL)" down 1

dev: ## Local dev stack via docker-compose
	docker compose -f ops/local/compose.yaml up -d

clean: ## Remove artifacts
	rm -rf $(BIN_DIR) coverage.out

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'
