package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/state"
)

func main() {
	var (
		list   = flag.Bool("list", false, "List all sentiment observations from the last 48 hours")
		delete = flag.String("delete", "", "Delete an observation by composite key (format: runId#timestamp)")
		add    = flag.String("add", "", "Add/restore an observation from JSON (paste output from delete command)")
	)
	flag.Parse()

	ctx := context.Background()

	// Initialize sentiment history manager
	manager, err := state.NewSentimentHistoryManager(ctx, "hourstats-sentiment-history")
	if err != nil {
		log.Fatalf("Failed to create sentiment history manager: %v", err)
	}

	if *list {
		listObservations(ctx, manager)
		return
	}

	if *delete != "" {
		deleteObservation(ctx, manager, *delete)
		return
	}

	if *add != "" {
		addObservation(ctx, manager, *add)
		return
	}

	// No command specified, show usage
	fmt.Println("Usage:")
	fmt.Println("  List observations:    go run cmd/manage-sentiment/main.go -list")
	fmt.Println("  Delete observation:   go run cmd/manage-sentiment/main.go -delete \"runId#timestamp\"")
	fmt.Println("  Add observation:      go run cmd/manage-sentiment/main.go -add '<json>'")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  go run cmd/manage-sentiment/main.go -list")
	fmt.Println("  go run cmd/manage-sentiment/main.go -delete \"run-123456789#2025-11-01T12:00:00Z\"")
	fmt.Println("  go run cmd/manage-sentiment/main.go -add '{\"runId\":\"run-123\",\"timestamp\":\"2025-11-01T12:00:00Z\",...}'")
	os.Exit(1)
}

func listObservations(ctx context.Context, manager *state.SentimentHistoryManager) {
	// Allow duration to be configured, default to 48 hours
	duration := 48 * time.Hour
	if os.Getenv("OBSERVATION_HOURS") != "" {
		if hours, err := time.ParseDuration(os.Getenv("OBSERVATION_HOURS") + "h"); err == nil {
			duration = hours
		}
	}
	
	fmt.Printf("📋 Listing sentiment observations from the last %.0f hours:\n\n", duration.Hours())

	// Get observations from specified duration
	observations, err := manager.GetSentimentHistory(ctx, duration)
	if err != nil {
		log.Fatalf("Failed to get sentiment history: %v", err)
	}

	if len(observations) == 0 {
		fmt.Println("No observations found in the last 48 hours.")
		return
	}

	fmt.Printf("Found %d observation(s):\n\n", len(observations))

	for i, obs := range observations {
		// Create composite key for easy copy-paste
		compositeKey := fmt.Sprintf("%s#%s", obs.RunID, obs.Timestamp.Format(time.RFC3339))
		
		// Format timestamp for display
		timestampDisplay := obs.Timestamp.Format("2006-01-02 15:04:05 MST")

		fmt.Printf("%d. Key: %s\n", i+1, compositeKey)
		fmt.Printf("   Timestamp: %s\n", timestampDisplay)
		fmt.Printf("   Net Sentiment: %.2f%%\n", obs.NetSentimentPercent)
		fmt.Printf("   Total Posts: %d\n", obs.TotalPosts)
		fmt.Printf("   Category: %s\n", obs.SentimentCategory)
		fmt.Println()
	}
}

func deleteObservation(ctx context.Context, manager *state.SentimentHistoryManager, key string) {
	// Parse the composite key
	runID, timestampStr, err := state.ParseCompositeKey(key)
	if err != nil {
		log.Fatalf("Failed to parse composite key: %v", err)
	}

	fmt.Printf("🗑️  Deleting observation: %s\n\n", key)

	// Delete the observation (this also retrieves it first)
	dataPoint, err := manager.DeleteSentimentData(ctx, runID, timestampStr)
	if err != nil {
		log.Fatalf("Failed to delete observation: %v", err)
	}

	fmt.Println("✅ Observation deleted successfully.")
	fmt.Println()
	fmt.Println("📋 Deleted observation (save this JSON to restore if needed):")
	fmt.Println(strings.Repeat("=", 60))
	
	// Output as JSON for easy restore
	jsonData, err := json.MarshalIndent(dataPoint, "", "  ")
	if err != nil {
		log.Fatalf("Failed to marshal observation to JSON: %v", err)
	}
	fmt.Println(string(jsonData))
	fmt.Println(strings.Repeat("=", 60))
}

func addObservation(ctx context.Context, manager *state.SentimentHistoryManager, jsonStr string) {
	fmt.Printf("➕ Adding observation from JSON:\n\n")

	// Parse JSON into SentimentDataPoint
	var dataPoint state.SentimentDataPoint
	err := json.Unmarshal([]byte(jsonStr), &dataPoint)
	if err != nil {
		log.Fatalf("Failed to parse JSON: %v", err)
	}

	// Recalculate TTL (14 days from now)
	dataPoint.CreatedAt = time.Now()
	dataPoint.TTL = dataPoint.CreatedAt.Add(14 * 24 * time.Hour).Unix()

	// Store the observation
	err = manager.StoreSentimentData(ctx, dataPoint)
	if err != nil {
		log.Fatalf("Failed to add observation: %v", err)
	}

	compositeKey := fmt.Sprintf("%s#%s", dataPoint.RunID, dataPoint.Timestamp.Format(time.RFC3339))
	fmt.Printf("✅ Observation added successfully!\n")
	fmt.Printf("   Key: %s\n", compositeKey)
	fmt.Printf("   Timestamp: %s\n", dataPoint.Timestamp.Format("2006-01-02 15:04:05 MST"))
	fmt.Printf("   Net Sentiment: %.2f%%\n", dataPoint.NetSentimentPercent)
}
