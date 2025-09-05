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

# Run with dry run mode (for testing) - OLD SINGLE-THREADED VERSION
dry-run:
	BLUESKY_HANDLE="test" BLUESKY_PASSWORD="test" go run cmd/trendjournal/main.go

# Run with dry run mode using real credentials (for testing) - OLD SINGLE-THREADED VERSION
dry-run-real:
	go run cmd/trendjournal/main.go

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

# Test multi-Lambda workflow with dry-run mode (requires AWS credentials)
test-multi-lambda:
	@echo "🧪 Testing Multi-Lambda Step Functions Workflow"
	@echo "=============================================="
	@echo "Setting dry-run mode for faster testing..."
	@aws ssm put-parameter \
		--name "/hourstats/settings/dry_run" \
		--value "true" \
		--type "String" \
		--overwrite \
		--region us-east-1 > /dev/null
	@echo "Starting Step Functions execution..."
	@EXECUTION_ARN=$$(aws stepfunctions start-execution \
		--state-machine-arn "arn:aws:states:us-east-1:478250316157:stateMachine:hourstats-workflow" \
		--name "local-test-$(shell date +%s)" \
		--region us-east-1 \
		--query 'executionArn' \
		--output text) && \
	echo "Execution ARN: $$EXECUTION_ARN" && \
	echo "Waiting 30 seconds to check execution..." && \
	sleep 30 && \
	EXECUTION_STATUS=$$(aws stepfunctions describe-execution \
		--execution-arn "$$EXECUTION_ARN" \
		--region us-east-1 \
		--query 'status' \
		--output text) && \
	echo "Execution status: $$EXECUTION_STATUS" && \
	if [ "$$EXECUTION_STATUS" = "RUNNING" ] || [ "$$EXECUTION_STATUS" = "SUCCEEDED" ]; then \
		echo "✅ Multi-Lambda workflow test SUCCESSFUL!"; \
		if [ "$$EXECUTION_STATUS" = "RUNNING" ]; then \
			echo "Stopping execution to save resources..."; \
			aws stepfunctions stop-execution \
				--execution-arn "$$EXECUTION_ARN" \
				--region us-east-1 > /dev/null; \
		fi; \
	else \
		echo "❌ Multi-Lambda workflow test FAILED!"; \
		echo "Final status: $$EXECUTION_STATUS"; \
		echo "Execution history:"; \
		aws stepfunctions get-execution-history \
			--execution-arn "$$EXECUTION_ARN" \
			--region us-east-1 \
			--query 'events[*].{Type:type,Time:timestamp,Details:eventDetails}' \
			--output table; \
		exit 1; \
	fi && \
	echo "Restoring dry-run setting to false..." && \
	aws ssm put-parameter \
		--name "/hourstats/settings/dry_run" \
		--value "false" \
		--type "String" \
		--overwrite \
		--region us-east-1 > /dev/null

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
	@echo "  dry-run         - Run OLD single-threaded version in dry-run mode"
	@echo "  dry-run-real    - Run OLD single-threaded version with real credentials"
	@echo "  test-lambdas    - Test individual Lambda functions locally"
	@echo "  test-workflow   - Test complete Step Functions workflow (requires AWS)"
	@echo "  test-multi-lambda - Test NEW multi-Lambda workflow with dry-run mode (requires AWS)"
	@echo "  fmt          - Format code"
	@echo "  lint         - Lint code"
	@echo "  help         - Show this help message"
