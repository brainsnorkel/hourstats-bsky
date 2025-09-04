#!/bin/bash

# Test script for local Lambda function testing
# Usage: ./test-lambda-local.sh [function-name]

set -e

FUNCTION_NAME=${1:-"orchestrator"}
BUILD_DIR="build"
EVENT_FILE="test-event.json"

echo "🧪 Testing Lambda function: $FUNCTION_NAME"

# Create test event
cat > $EVENT_FILE << EOF
{
  "source": "aws.events",
  "time": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
}
EOF

echo "📦 Building Lambda function..."

# Build the specific Lambda function
case $FUNCTION_NAME in
  "orchestrator")
    go build -o $BUILD_DIR/lambda-orchestrator ./cmd/lambda-orchestrator
    BINARY="$BUILD_DIR/lambda-orchestrator"
    ;;
  "fetcher")
    go build -o $BUILD_DIR/lambda-fetcher ./cmd/lambda-fetcher
    BINARY="$BUILD_DIR/lambda-fetcher"
    ;;
  "analyzer")
    go build -o $BUILD_DIR/lambda-analyzer ./cmd/lambda-analyzer
    BINARY="$BUILD_DIR/lambda-analyzer"
    ;;
  "aggregator")
    go build -o $BUILD_DIR/lambda-aggregator ./cmd/lambda-aggregator
    BINARY="$BUILD_DIR/lambda-aggregator"
    ;;
  "poster")
    go build -o $BUILD_DIR/lambda-poster ./cmd/lambda-poster
    BINARY="$BUILD_DIR/lambda-poster"
    ;;
  *)
    echo "❌ Unknown function: $FUNCTION_NAME"
    echo "Available functions: orchestrator, fetcher, analyzer, aggregator, poster"
    exit 1
    ;;
esac

echo "✅ Built: $BINARY"

# Test with AWS Lambda Runtime Interface Emulator (if available)
if command -v awslocal &> /dev/null; then
    echo "🚀 Testing with LocalStack..."
    awslocal lambda invoke \
        --function-name $FUNCTION_NAME \
        --payload file://$EVENT_FILE \
        response.json
    echo "📄 Response:"
    cat response.json
    echo ""
elif command -v sam &> /dev/null; then
    echo "🚀 Testing with SAM Local..."
    sam local invoke $FUNCTION_NAME --event $EVENT_FILE
else
    echo "🔧 Testing with direct binary execution..."
    echo "Note: This will only work for functions that don't require AWS services"
    
    # For orchestrator, we can test the basic structure
    if [ "$FUNCTION_NAME" = "orchestrator" ]; then
        echo "📋 Event payload:"
        cat $EVENT_FILE
        echo ""
        echo "⚠️  Note: This will fail without DynamoDB access, but tests the code structure"
        timeout 10s $BINARY 2>&1 || echo "Expected failure due to AWS dependencies"
    else
        echo "⚠️  This function requires AWS services and cannot be tested locally without LocalStack/SAM"
    fi
fi

# Cleanup
rm -f $EVENT_FILE response.json
echo "🧹 Cleaned up test files"
