package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/config"
	"github.com/christophergentle/hourstats-bsky/internal/sparkline"
	"github.com/christophergentle/hourstats-bsky/internal/state"
)

func main() {
	ctx := context.Background()

	// Load configuration (from config.yaml or environment variables)
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Printf("Failed to load config file: %v", err)
		log.Println("Falling back to environment variables...")
		cfg = config.LoadConfigFromEnv()

		if cfg.Bluesky.Handle == "" || cfg.Bluesky.Password == "" {
			log.Fatal("Please set BLUESKY_HANDLE and BLUESKY_PASSWORD environment variables, or create a config.yaml file")
		}
	}

	log.Printf("Using Bluesky handle: %s", cfg.Bluesky.Handle)

	// Initialize daily sentiment manager
	dailySentimentManager, err := state.NewDailySentimentManager(ctx, "hourstats-daily-sentiment")
	if err != nil {
		log.Fatalf("Failed to create daily sentiment manager: %v", err)
	}

	// Get yearly sentiment data
	yearlyData, err := dailySentimentManager.GetYearlySentimentData(ctx)
	if err != nil {
		log.Fatalf("Failed to get yearly sentiment data: %v", err)
	}

	log.Printf("Retrieved %d days of sentiment data", len(yearlyData))

	if len(yearlyData) < 2 {
		log.Fatalf("Insufficient data for posting (need at least 2 days)")
	}

	// Initialize yearly sparkline generator
	yearlySparklineGenerator := sparkline.NewYearlySparklineGenerator(nil)

	// Generate yearly sparkline image
	imageData, err := yearlySparklineGenerator.GenerateYearlySentimentSparkline(yearlyData)
	if err != nil {
		log.Fatalf("Failed to generate yearly sparkline: %v", err)
	}

	log.Printf("Generated chart image (%d bytes)", len(imageData))

	// Calculate stats for post text and alt text
	minSentiment := yearlyData[0].AverageSentiment
	maxSentiment := yearlyData[0].AverageSentiment
	minDate := yearlyData[0].Date
	maxDate := yearlyData[0].Date
	var sum float64
	for _, point := range yearlyData {
		if point.AverageSentiment < minSentiment {
			minSentiment = point.AverageSentiment
			minDate = point.Date
		}
		if point.AverageSentiment > maxSentiment {
			maxSentiment = point.AverageSentiment
			maxDate = point.Date
		}
		sum += point.AverageSentiment
	}
	yearlyAverage := sum / float64(len(yearlyData))

	// Generate post text
	var postText string
	if len(yearlyData) > 0 {
		startDate := yearlyData[0].Timestamp.Format("2006-01-02")
		endDate := yearlyData[len(yearlyData)-1].Timestamp.Format("2006-01-02")
		postText = fmt.Sprintf("Bluesky Sentiment %s - %s", startDate, endDate)
	} else {
		postText = "Bluesky Sentiment"
	}

	// Add extreme sentiment information
	// The date + "events" text will be linked via facets (URLs not shown in post)
	extremeMessages := []string{}
	if minDate != "" {
		minDateParsed, parseErr := time.Parse("2006-01-02", minDate)
		if parseErr == nil {
			minDateDisplay := minDateParsed.Format("Jan 2")
			// Format as "Sep 18 events" which will be linked via facets
			extremeMessages = append(extremeMessages, fmt.Sprintf("Lowest: %.1f%% %s events", minSentiment, minDateDisplay))
		} else {
			extremeMessages = append(extremeMessages, fmt.Sprintf("Lowest: %.1f%%", minSentiment))
		}
	}
	if maxDate != "" {
		maxDateParsed, parseErr := time.Parse("2006-01-02", maxDate)
		if parseErr == nil {
			maxDateDisplay := maxDateParsed.Format("Jan 2")
			// Format as "Oct 10 events" which will be linked via facets
			extremeMessages = append(extremeMessages, fmt.Sprintf("Highest: %.1f%% %s events", maxSentiment, maxDateDisplay))
		} else {
			extremeMessages = append(extremeMessages, fmt.Sprintf("Highest: %.1f%%", maxSentiment))
		}
	}

	if len(extremeMessages) > 0 {
		postText += "\n\n" + strings.Join(extremeMessages, "\n")
	}

	log.Printf("Post text:\n%s", postText)

	// Generate alt text
	altText := fmt.Sprintf("Yearly Bluesky sentiment trend chart showing daily averages over the past year. Current sentiment: %.1f%% (%s). Highest sentiment: %.1f%% (%s). Lowest sentiment: %.1f%% (%s). Yearly average sentiment: %.1f%%.",
		yearlyData[len(yearlyData)-1].AverageSentiment, yearlyData[len(yearlyData)-1].Date,
		maxSentiment, maxDate,
		minSentiment, minDate,
		yearlyAverage)

	// Determine trend
	if len(yearlyData) > 1 {
		first := yearlyData[0].AverageSentiment
		last := yearlyData[len(yearlyData)-1].AverageSentiment
		trend := last - first
		if trend > 0 {
			altText += " Trending positive over the year."
		} else if trend < 0 {
			altText += " Trending negative over the year."
		} else {
			altText += " Stable sentiment over the year."
		}
	}

	log.Printf("Alt text: %s", altText)

	// Check if this is a dry run
	if cfg.Settings.DryRun {
		log.Println("⚠️  DRY RUN MODE - Not posting to Bluesky")
		log.Println("Set dry_run: false in config.yaml or unset DRY_RUN env var to post")
		return
	}

	// Initialize Bluesky client
	blueskyClient := client.New(cfg.Bluesky.Handle, cfg.Bluesky.Password)

	// Authenticate
	if err := blueskyClient.Authenticate(); err != nil {
		log.Fatalf("Failed to authenticate with Bluesky: %v", err)
	}

	log.Println("✅ Authenticated with Bluesky")

	// Create facets for Wikipedia URLs to make them clickable
	wikipediaFacets := client.CreateWikipediaLinkFacets(postText)

	// Truncate post text to 300 graphemes (Bluesky limit)
	// We need to be careful because facets reference byte positions, so we truncate before creating facets
	maxGraphemes := 300
	truncatedPostText := postText
	if len([]rune(postText)) > maxGraphemes {
		// Truncate to maxGraphemes, but try to preserve complete lines
		runes := []rune(postText)
		if len(runes) > maxGraphemes {
			// Find the last newline before the limit
			truncated := string(runes[:maxGraphemes])
			lastNewline := strings.LastIndex(truncated, "\n")
			if lastNewline > maxGraphemes/2 {
				// If we found a newline in the second half, truncate there
				truncatedPostText = truncated[:lastNewline]
			} else {
				// Otherwise, truncate at maxGraphemes
				truncatedPostText = truncated
			}
			log.Printf("Post text truncated from %d to %d graphemes", len(runes), len([]rune(truncatedPostText)))
		}
	}

	// Recreate facets based on truncated text
	wikipediaFacets = client.CreateWikipediaLinkFacets(truncatedPostText)

	// Post the chart
	var postURI, postCID string
	if len(wikipediaFacets) > 0 {
		postURI, postCID, err = blueskyClient.PostWithImage(ctx, truncatedPostText, imageData, altText, wikipediaFacets)
	} else {
		postURI, postCID, err = blueskyClient.PostWithImage(ctx, truncatedPostText, imageData, altText)
	}
	if err != nil {
		log.Fatalf("Failed to post to Bluesky: %v", err)
	}

	log.Printf("✅ Posted successfully! URI: %s", postURI)

	// Pin the post
	if err := blueskyClient.PinPost(ctx, postURI, postCID); err != nil {
		log.Printf("⚠️  Failed to pin post: %v (post was successful)", err)
	} else {
		log.Printf("✅ Post pinned successfully!")
	}

	fmt.Println("\n✅ Successfully posted and pinned yearly sentiment chart to Bluesky!")
	fmt.Printf("📍 Post URI: %s\n", postURI)
}

