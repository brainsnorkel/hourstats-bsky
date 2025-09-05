# TrendJournal Makefile

.PHONY: build run test clean deps install setup

# Setup the application (create config.yaml)
setup:
	@./setup.sh

# Build the application
build:
	go build -o bin/trendjournal cmd/trendjournal/main.go

# Run the application locally
run:
	go run cmd/trendjournal/main.go

# Run tests
test:
	go test ./...

# Clean build artifacts
clean:
	rm -rf bin/

# Install dependencies
deps:
	go mod download
	go mod tidy

# Install the application
install: build
	cp bin/trendjournal /usr/local/bin/

# Run with dry run mode (for testing)
dry-run:
	BLUESKY_HANDLE="test" BLUESKY_PASSWORD="test" go run cmd/trendjournal/main.go

# Test individual Lambda functions locally
test-lambdas:
	@echo "Testing individual Lambda functions..."
	@for dir in cmd/lambda-*; do \
		echo "Testing $$dir..."; \
		cd "$$dir" && go test -v . && cd ../..; \
	done

# Test complete workflow locally (requires AWS credentials)
test-workflow:
	@echo "Testing complete Step Functions workflow..."
	@echo "This requires AWS credentials and will test the actual deployed workflow"
	@echo "Starting test execution..."
	@aws stepfunctions start-execution \
		--state-machine-arn "arn:aws:states:us-east-1:478250316157:stateMachine:hourstats-workflow" \
		--name "local-test-$(shell date +%s)" \
		--region us-east-1 \
		--query 'executionArn' \
		--output text

# Format code
fmt:
	go fmt ./...

# Lint code
lint:
	golangci-lint run

# Help
help:
	@echo "Available targets:"
	@echo "  setup        - Set up configuration file (first time setup)"
	@echo "  build        - Build the application"
	@echo "  run          - Run the application locally"
	@echo "  test         - Run tests"
	@echo "  clean        - Clean build artifacts"
	@echo "  deps         - Install dependencies"
	@echo "  install      - Install the application"
	@echo "  dry-run      - Run in dry-run mode for testing"
	@echo "  test-lambdas - Test individual Lambda functions locally"
	@echo "  test-workflow- Test complete Step Functions workflow (requires AWS)"
	@echo "  fmt          - Format code"
	@echo "  lint         - Lint code"
	@echo "  help         - Show this help message"
