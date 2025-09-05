#!/bin/bash

# Query runs utility script
# Usage: ./scripts/query-runs.sh [list|analyze] [options]

set -e

# Change to project root
cd "$(dirname "$0")/.."

case "${1:-help}" in
    "list")
        echo "📋 Listing runs..."
        go run cmd/query-runs/main.go -list -limit="${2:-10}" -details
        ;;
    "analyze")
        if [ -z "$2" ]; then
            echo "❌ Error: Run ID required for analyze command"
            echo "Usage: $0 analyze <runID>"
            exit 1
        fi
        echo "🔍 Analyzing run: $2"
        go run cmd/query-runs/main.go -run "$2"
        ;;
    "help"|*)
        echo "Bluesky HourStats Query Utility"
        echo ""
        echo "Usage:"
        echo "  $0 list [limit]           - List recent runs (default limit: 10)"
        echo "  $0 analyze <runID>        - Analyze a specific run and show what would be posted"
        echo "  $0 help                   - Show this help message"
        echo ""
        echo "Examples:"
        echo "  $0 list 5                 - List last 5 runs with details"
        echo "  $0 analyze run-1234567890 - Analyze specific run"
        ;;
esac
