package main

import (
	"log"

	"github.com/christophergentle/trendjournal/internal/config"
	"github.com/christophergentle/trendjournal/internal/scheduler"
)

func main() {
	// Try to load configuration from file first
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Printf("Failed to load config file: %v", err)
		log.Println("Falling back to environment variables...")

		// Fall back to environment variables
		cfg = config.LoadConfigFromEnv()

		// Validate environment variables
		if cfg.Bluesky.Handle == "" || cfg.Bluesky.Password == "" {
			log.Fatal("Please set BLUESKY_HANDLE and BLUESKY_PASSWORD environment variables, or create a config.yaml file")
		}
	}

	// Initialize and start the scheduler
	scheduler := scheduler.New(cfg.Bluesky.Handle, cfg.Bluesky.Password, cfg)

	log.Printf("Starting TrendJournal bot...")
	log.Printf("Handle: %s", cfg.Bluesky.Handle)
	log.Printf("Dry run mode: %v", cfg.Settings.DryRun)

	if err := scheduler.Start(); err != nil {
		log.Fatalf("Failed to start scheduler: %v", err)
	}
}
