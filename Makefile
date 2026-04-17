# HourStats Makefile

.PHONY: build test test-unit clean deps fmt lint graph-lab graph-lab-sparkline graph-lab-yearly help \
	build-hourstats build-stats deploy-prod deploy-staging deploy-all \
	fly-status fly-logs-prod fly-logs-staging sync-staging

# Default build target — the Fly.io binary
build: build-hourstats

build-hourstats:
	CGO_ENABLED=0 go build -o bin/hourstats ./cmd/hourstats

build-stats:
	go build -o bin/hourstats-stats ./cmd/hourstats-stats

# Run tests
test-unit:
	go test ./...

test:
	go test ./...

# Clean build artifacts
clean:
	rm -rf bin/

# Install dependencies
deps:
	go mod download
	go mod tidy

# Generate chart experiments with synthetic data (no AWS needed)
graph-lab:
	go run cmd/graph-lab/main.go
	@echo "Open test-results/graph-lab/ to view generated charts"

graph-lab-sparkline:
	go run cmd/graph-lab/main.go -type sparkline

graph-lab-yearly:
	go run cmd/graph-lab/main.go -type yearly

# Format code
fmt:
	go fmt ./...

# Lint code (requires golangci-lint: https://golangci-lint.run/usage/install/)
lint:
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "Error: golangci-lint is not installed."; \
		echo "Install it with: curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $$(go env GOPATH)/bin latest"; \
		echo "Or on macOS: brew install golangci-lint"; \
		exit 1; \
	}
	golangci-lint run

deploy-prod:
	fly deploy -c fly.prod.toml --ha=false

deploy-staging:
	fly deploy -c fly.staging.toml --ha=false

deploy-all: deploy-prod deploy-staging

fly-status:
	@fly status -a hourstats-prod 2>&1 | grep -E "(STATE|app)" || true
	@echo "---"
	@fly status -a hourstats-staging 2>&1 | grep -E "(STATE|app)" || true

fly-logs-prod:
	fly logs -a hourstats-prod

fly-logs-staging:
	fly logs -a hourstats-staging

sync-staging:
	@scripts/sync-prod-to-staging.sh

help:
	@echo "Available targets:"
	@echo "  build              - Build the Fly.io binary (alias for build-hourstats)"
	@echo "  build-hourstats    - Build Fly.io binary (cmd/hourstats)"
	@echo "  build-stats        - Build stats CLI tool (cmd/hourstats-stats)"
	@echo "  test               - Run all tests"
	@echo "  test-unit          - Run unit tests"
	@echo "  clean              - Clean build artifacts"
	@echo "  deps               - Install and tidy dependencies"
	@echo "  graph-lab          - Generate chart experiments with synthetic data"
	@echo "  graph-lab-sparkline - Generate only sparkline experiments"
	@echo "  graph-lab-yearly   - Generate only yearly chart experiments"
	@echo "  fmt                - Format code"
	@echo "  lint               - Lint code (requires golangci-lint)"
	@echo "  deploy-prod        - Deploy to hourstats-prod on Fly.io"
	@echo "  deploy-staging     - Deploy to hourstats-staging on Fly.io"
	@echo "  deploy-all         - Deploy to both prod and staging"
	@echo "  fly-status         - Show status of both Fly.io apps"
	@echo "  fly-logs-prod      - Tail prod logs"
	@echo "  fly-logs-staging   - Tail staging logs"
	@echo "  sync-staging       - Sync prod data to staging (snapshot + restore)"
	@echo "  help               - Show this help message"
