package main

import (
	"log"
	"os"

	"github.com/christophergentle/trendjournal/internal/scheduler"
)

func main() {
	// Get configuration from environment variables
	handle := os.Getenv("BLUESKY_HANDLE")
	password := os.Getenv("BLUESKY_PASSWORD")
	
	if handle == "" || password == "" {
		log.Fatal("BLUESKY_HANDLE and BLUESKY_PASSWORD environment variables are required")
	}

	// Initialize and start the scheduler
	scheduler := scheduler.New(handle, password)
	
	log.Println("Starting TrendJournal bot...")
	if err := scheduler.Start(); err != nil {
		log.Fatalf("Failed to start scheduler: %v", err)
	}
}
