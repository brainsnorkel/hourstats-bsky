# HourStats Makefile

.PHONY: build test test-unit test-lambdas clean deps fmt lint graph-lab help

# Build the application
build:
	@echo "Building Lambda functions..."
	@for dir in cmd/lambda-*; do \
		if [ -d "$$dir" ]; then \
			echo "Building $$dir..."; \
			cd "$$dir" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bootstrap . && cd ../..; \
		fi; \
	done
	@echo "All Lambda functions built successfully"

# Build DynamoDB backup utility
build-backup:
	go build -o bin/dynamodb-backup cmd/dynamodb-backup/main.go

# Build DynamoDB restore utility
build-restore:
	go build -o bin/dynamodb-restore cmd/dynamodb-restore/main.go

# Build both backup and restore utilities
build-backup-tools: build-backup build-restore

# Run tests
test-unit:
	go test ./...

# Run tests
test:
	go test ./...

# Clean build artifacts
clean:
	rm -rf bin/
	rm -f bin/dynamodb-backup bin/dynamodb-restore

# Install dependencies
deps:
	go mod download
	go mod tidy

# Build and verify individual Lambda functions
test-lambdas:
	@echo "Building and testing individual Lambda functions..."
	@for dir in cmd/lambda-*; do \
		if [ -d "$$dir" ]; then \
			echo "Testing $$dir..."; \
			cd "$$dir" && go build -o /dev/null . && echo "  ✅ Build OK" && cd ../..; \
		fi; \
	done

# Generate chart experiments with synthetic data (no AWS needed)
graph-lab:
	go run cmd/graph-lab/main.go
	@echo "Open test-results/graph-lab/ to view generated charts"

# Generate only sparkline experiments
graph-lab-sparkline:
	go run cmd/graph-lab/main.go -type sparkline

# Generate only yearly chart experiments
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

# Help
help:
	@echo "Available targets:"
	@echo "  build              - Build all Lambda functions (linux/amd64)"
	@echo "  test               - Run all tests"
	@echo "  test-unit          - Run unit tests"
	@echo "  test-lambdas       - Build-verify each Lambda function"
	@echo "  clean              - Clean build artifacts"
	@echo "  deps               - Install and tidy dependencies"
	@echo "  build-backup       - Build DynamoDB backup utility"
	@echo "  build-restore      - Build DynamoDB restore utility"
	@echo "  build-backup-tools - Build both backup and restore utilities"
	@echo "  graph-lab          - Generate chart experiments with synthetic data"
	@echo "  graph-lab-sparkline - Generate only sparkline experiments"
	@echo "  graph-lab-yearly   - Generate only yearly chart experiments"
	@echo "  fmt                - Format code"
	@echo "  lint               - Lint code (requires golangci-lint)"
	@echo "  help               - Show this help message"
