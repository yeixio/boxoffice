.DEFAULT_GOAL := help
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/yeixio/boxoffice/internal/version.Version=$(VERSION)
STATICCHECK ?= 2026.2.2

.PHONY: help web build run dev test test-postgres lint docker helm-lint

help: ## Show targets
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

web: ## Build the web UI into web/dist
	cd web && pnpm install --frozen-lockfile && pnpm build

build: web ## Build bin/boxoffice with the web UI embedded
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/boxoffice ./cmd/boxoffice

run: build ## Build and run on http://localhost:7272 with data in ./data
	BOXOFFICE_DATA_DIR=./data ./bin/boxoffice

dev: ## Run the web UI with hot reload (start the server with `make run` first)
	cd web && pnpm dev

test: ## Go and web tests
	go test ./...
	cd web && pnpm typecheck && pnpm test

test-postgres: ## Go tests against PostgreSQL (set BOXOFFICE_TEST_DATABASE_URL)
	go test -count=1 ./...

lint: ## go vet, gofmt and staticcheck (its checks are in staticcheck.conf)
	go vet ./...
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)
	go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK) ./...

docker: ## Build the Docker image
	docker build --build-arg VERSION=$(VERSION) -t ghcr.io/yeixio/boxoffice:$(VERSION) .

helm-lint: ## Lint the Helm chart
	helm lint deploy/helm/boxoffice
