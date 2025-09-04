# TrendJournal Makefile

.PHONY: build run test clean deps install

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

# Format code
fmt:
	go fmt ./...

# Lint code
lint:
	golangci-lint run

# Help
help:
	@echo "Available targets:"
	@echo "  build     - Build the application"
	@echo "  run       - Run the application locally"
	@echo "  test      - Run tests"
	@echo "  clean     - Clean build artifacts"
	@echo "  deps      - Install dependencies"
	@echo "  install   - Install the application"
	@echo "  dry-run   - Run in dry-run mode for testing"
	@echo "  fmt       - Format code"
	@echo "  lint      - Lint code"
	@echo "  help      - Show this help message"
