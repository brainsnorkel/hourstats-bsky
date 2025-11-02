package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/analyzer"
	bskyclient "github.com/christophergentle/hourstats-bsky/internal/client"
	"github.com/christophergentle/hourstats-bsky/internal/config"
	"github.com/christophergentle/hourstats-bsky/internal/formatter"
)

type AnalysisResult struct {
	RunID                     string                    `json:"runId"`
	Timestamp                 string                    `json:"timestamp"`
	AnalysisIntervalMinutes   int                       `json:"analysisIntervalMinutes"`
	CutoffTime                string                    `json:"cutoffTime"`
	CurrentTime               string                    `json:"currentTime"`
	FetchStats                FetchStats                `json:"fetchStats"`
	ProcessingStats           ProcessingStats           `json:"processingStats"`
	SentimentAnalysis         SentimentAnalysis         `json:"sentimentAnalysis"`
	GeneratedPost             string                    `json:"generatedPost"`
	PostStatistics            PostStatistics            `json:"postStatistics"`
	SamplePosts               []SamplePost              `json:"samplePosts"`
}

type FetchStats struct {
	TotalAPICalls          int                    `json:"totalApiCalls"`
	TotalPostsFromAPI      int                    `json:"totalPostsFromApi"`
	PostsAfterTimeFilter   int                    `json:"postsAfterTimeFilter"`
	PostsAfterAdultFilter  int                    `json:"postsAfterAdultFilter"`
	PostsAfterDeduplication int                   `json:"postsAfterDeduplication"`
	TimeDistribution       []TimeDistributionBucket `json:"timeDistribution"`
}

type TimeDistributionBucket struct {
	BucketStart    string `json:"bucketStart"`
	BucketEnd      string `json:"bucketEnd"`
	PostCount      int    `json:"postCount"`
	SamplePosts    []string `json:"samplePosts"`
}

type ProcessingStats struct {
	PostsAnalyzed          int     `json:"postsAnalyzed"`
	TopPostsSelected       int     `json:"topPostsSelected"`
	DuplicatesRemoved      int     `json:"duplicatesRemoved"`
}

type SentimentAnalysis struct {
	OverallSentiment      string  `json:"overallSentiment"`
	NetSentimentPercent   float64 `json:"netSentimentPercent"`
	AverageCompoundScore  float64 `json:"averageCompoundScore"`
	PositiveCount         int     `json:"positiveCount"`
	NeutralCount          int     `json:"neutralCount"`
	NegativeCount         int     `json:"negativeCount"`
}

type PostStatistics struct {
	CharacterCount int    `json:"characterCount"`
	BlueskyLimit   int    `json:"blueskyLimit"`
	Remaining      int    `json:"remaining"`
	Status         string `json:"status"`
}

type SamplePost struct {
	Author          string  `json:"author"`
	URI             string  `json:"uri"`
	CreatedAt       string  `json:"createdAt"`
	Likes           int     `json:"likes"`
	Reposts         int     `json:"reposts"`
	Replies         int     `json:"replies"`
	EngagementScore float64 `json:"engagementScore"`
	Sentiment       string  `json:"sentiment"`
	TextPreview     string  `json:"textPreview"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run cmd/test-sentiment-analysis/main.go <interval-minutes> [output-file.json]")
		fmt.Println("Example: go run cmd/test-sentiment-analysis/main.go 30")
		fmt.Println("Example: go run cmd/test-sentiment-analysis/main.go 30 results.json")
		os.Exit(1)
	}

	var intervalMinutes int
	_, err := fmt.Sscanf(os.Args[1], "%d", &intervalMinutes)
	if err != nil {
		log.Fatalf("Invalid interval: %v", err)
	}

	outputFile := "sentiment-analysis-results.json"
	if len(os.Args) >= 3 {
		outputFile = os.Args[2]
	}

	ctx := context.Background()

	// Load configuration - try file first, then env
	var cfg *config.Config
	cfg, err = config.LoadConfig()
	if err != nil {
		// Fallback to environment variables
		cfg = config.LoadConfigFromEnv()
		if cfg.Bluesky.Handle == "" || cfg.Bluesky.Password == "" {
			log.Fatalf("Failed to load config from file and no credentials in environment: %v", err)
		}
		log.Printf("Using credentials from environment variables")
	}

	// Create Bluesky client
	blueskyClient := bskyclient.New(cfg.Bluesky.Handle, cfg.Bluesky.Password)
	if err := blueskyClient.Authenticate(); err != nil {
		log.Fatalf("Failed to authenticate: %v", err)
	}

	fmt.Printf("🔍 Starting sentiment analysis dry-run for %d minute interval...\n\n", intervalMinutes)

	// Calculate cutoff time
	now := time.Now().UTC()
	cutoffTime := now.Add(-time.Duration(intervalMinutes) * time.Minute)

	fmt.Printf("📅 Time Range:\n")
	fmt.Printf("   Cutoff Time: %s\n", cutoffTime.Format("2006-01-02 15:04:05 UTC"))
	fmt.Printf("   Current Time: %s\n", now.Format("2006-01-02 15:04:05 UTC"))
	fmt.Printf("   Window: %d minutes\n\n", intervalMinutes)

	// Fetch posts
	result := &AnalysisResult{
		RunID:                   fmt.Sprintf("dry-run-%d", time.Now().Unix()),
		Timestamp:               now.Format(time.RFC3339),
		AnalysisIntervalMinutes: intervalMinutes,
		CutoffTime:              cutoffTime.Format(time.RFC3339),
		CurrentTime:             now.Format(time.RFC3339),
	}

	fmt.Println("📡 Fetching posts from Bluesky API...")
	fetchResult, fetchedPosts := fetchAndAnalyzePosts(ctx, blueskyClient, cutoffTime, now, intervalMinutes)
	result.FetchStats = fetchResult

	fmt.Printf("\n📊 Fetch Statistics:\n")
	fmt.Printf("   Total API Calls: %d\n", fetchResult.TotalAPICalls)
	fmt.Printf("   Total Posts from API: %d\n", fetchResult.TotalPostsFromAPI)
	fmt.Printf("   Posts After Time Filter: %d\n", fetchResult.PostsAfterTimeFilter)
	fmt.Printf("   Posts After Deduplication: %d\n", fetchResult.PostsAfterDeduplication)
	fmt.Printf("   Filtered Out: %d posts (%.1f%%)\n",
		fetchResult.TotalPostsFromAPI-fetchResult.PostsAfterDeduplication,
		100.0*float64(fetchResult.TotalPostsFromAPI-fetchResult.PostsAfterDeduplication)/float64(fetchResult.TotalPostsFromAPI))

	if len(fetchResult.TimeDistribution) > 0 {
		fmt.Printf("\n⏰ Time Distribution:\n")
		for _, bucket := range fetchResult.TimeDistribution {
			fmt.Printf("   %s - %s: %d posts\n", bucket.BucketStart, bucket.BucketEnd, bucket.PostCount)
		}
	}

	if len(fetchedPosts) == 0 {
		fmt.Println("\n❌ No posts to analyze after filtering!")
		saveResults(result, outputFile)
		return
	}

	fmt.Printf("\n🧠 Analyzing sentiment...\n")

	// Perform sentiment analysis
	sentimentResult := analyzeSentiment(fetchedPosts)
	result.SentimentAnalysis = sentimentResult
	result.ProcessingStats = ProcessingStats{
		PostsAnalyzed:    len(fetchedPosts),
		TopPostsSelected: 5,
	}

	fmt.Printf("   Overall Sentiment: %s\n", sentimentResult.OverallSentiment)
	fmt.Printf("   Net Sentiment: %.1f%%\n", sentimentResult.NetSentimentPercent)
	fmt.Printf("   Positive: %d, Neutral: %d, Negative: %d\n",
		sentimentResult.PositiveCount, sentimentResult.NeutralCount, sentimentResult.NegativeCount)

	// Generate post content
	fmt.Printf("\n📝 Generating Bluesky post...\n")
	postResult := generatePostContent(fetchedPosts, sentimentResult, intervalMinutes)
	result.GeneratedPost = postResult.PostText
	result.PostStatistics = postResult.Stats
	result.SamplePosts = postResult.SamplePosts

	fmt.Printf("   Character Count: %d/%d\n", postResult.Stats.CharacterCount, postResult.Stats.BlueskyLimit)
	fmt.Printf("   Remaining: %d characters\n", postResult.Stats.Remaining)
	fmt.Printf("   Status: %s\n", postResult.Stats.Status)

	fmt.Printf("\n📄 Generated Post:\n")
	fmt.Println(strings.Repeat("=", 80))
	fmt.Println(result.GeneratedPost)
	fmt.Println(strings.Repeat("=", 80))

	// Save results
	if err := saveResults(result, outputFile); err != nil {
		log.Fatalf("Failed to save results: %v", err)
	}

	fmt.Printf("\n✅ Results saved to: %s\n", outputFile)
}

func fetchAndAnalyzePosts(ctx context.Context, client *bskyclient.BlueskyClient, cutoffTime, now time.Time, intervalMinutes int) (FetchStats, []bskyclient.Post) {
	var stats FetchStats
	var allPosts []bskyclient.Post
	var timeDistributionPosts []bskyclient.Post // Track posts for time distribution
	currentCursor := ""
	apiCallCount := 0
	maxIterations := 100

	// Track seen URIs for deduplication
	seenURIs := make(map[string]bool)
	uriToPost := make(map[string]bskyclient.Post)

	for {
		apiCallCount++
		if apiCallCount > maxIterations {
			fmt.Printf("   ⚠️  Reached max iterations (%d), stopping\n", maxIterations)
			break
		}

		posts, nextCursor, hasMore, err := client.GetTrendingPostsBatch(ctx, currentCursor, cutoffTime)
		if err != nil {
			log.Printf("   ❌ API call %d failed: %v", apiCallCount, err)
			break
		}

		stats.TotalPostsFromAPI += len(posts)

		// Process posts from this batch
		for _, post := range posts {
			// Time filtering (client-side check)
			postTime, err := time.Parse(time.RFC3339, post.CreatedAt)
			if err != nil {
				continue
			}

			if postTime.Before(cutoffTime) {
				continue // Already filtered by API, but count anyway
			}

			stats.PostsAfterTimeFilter++
			timeDistributionPosts = append(timeDistributionPosts, post)

			// Deduplicate by URI (keep highest engagement)
			if existing, exists := uriToPost[post.URI]; exists {
				if (post.Replies + post.Likes + post.Reposts) > (existing.Replies + existing.Likes + existing.Reposts) {
					uriToPost[post.URI] = post
				}
			} else {
				uriToPost[post.URI] = post
				seenURIs[post.URI] = true
			}
		}

		fmt.Printf("   📡 API Call %d: Retrieved %d posts (Total from API: %d, After time filter: %d, After dedup: %d)\n",
			apiCallCount, len(posts), stats.TotalPostsFromAPI, stats.PostsAfterTimeFilter, len(uriToPost))

		if !hasMore || nextCursor == "" {
			break
		}

		// Check if we should stop (oldest post before cutoff)
		if len(posts) > 0 {
			oldestPost := posts[len(posts)-1]
			oldestTime, err := time.Parse(time.RFC3339, oldestPost.CreatedAt)
			if err == nil && oldestTime.Before(cutoffTime) {
				fmt.Printf("   ⏰ Oldest post is before cutoff time, stopping\n")
				break
			}
		}

		currentCursor = nextCursor
	}

	// Convert map to slice
	for _, post := range uriToPost {
		allPosts = append(allPosts, post)
	}

	stats.TotalAPICalls = apiCallCount
	stats.PostsAfterDeduplication = len(allPosts)

	// Populate time distribution with actual post timestamps
	populateTimeDistribution(&stats, timeDistributionPosts, cutoffTime, now)

	return stats, allPosts
}

type PostData struct {
	URI       string
	CID       string
	Text      string
	Author    string
	Likes     int
	Reposts   int
	Replies   int
	CreatedAt string
}

func populateTimeDistribution(stats *FetchStats, posts []bskyclient.Post, cutoffTime, now time.Time) {
	// Create 6 buckets for time distribution
	bucketCount := 6
	buckets := make([]TimeDistributionBucket, bucketCount)
	windowDuration := now.Sub(cutoffTime)
	bucketDuration := windowDuration / time.Duration(bucketCount)

	// Initialize buckets
	for i := 0; i < bucketCount; i++ {
		bucketStart := cutoffTime.Add(time.Duration(i) * bucketDuration)
		bucketEnd := cutoffTime.Add(time.Duration(i+1) * bucketDuration)
		if i == bucketCount-1 {
			bucketEnd = now // Ensure last bucket includes now
		}

		buckets[i] = TimeDistributionBucket{
			BucketStart: bucketStart.Format("15:04:05"),
			BucketEnd:   bucketEnd.Format("15:04:05"),
		}
	}

	// Distribute posts into buckets
	for _, post := range posts {
		postTime, err := time.Parse(time.RFC3339, post.CreatedAt)
		if err != nil {
			continue
		}

		// Find which bucket this post belongs to
		for i := 0; i < bucketCount; i++ {
			bucketStart := cutoffTime.Add(time.Duration(i) * bucketDuration)
			bucketEnd := cutoffTime.Add(time.Duration(i+1) * bucketDuration)
			if i == bucketCount-1 {
				bucketEnd = now
			}

			if (postTime.Equal(bucketStart) || postTime.After(bucketStart)) && postTime.Before(bucketEnd) {
				buckets[i].PostCount++
				// Store sample URIs (first 3 per bucket)
				if len(buckets[i].SamplePosts) < 3 {
					buckets[i].SamplePosts = append(buckets[i].SamplePosts, post.URI)
				}
				break
			}
		}
	}

	stats.TimeDistribution = buckets
}

func analyzeSentiment(posts []bskyclient.Post) SentimentAnalysis {
	// Convert to analyzer posts
	sentimentAnalyzer := analyzer.New()
	analyzerPosts := make([]analyzer.Post, len(posts))

	for i, post := range posts {
		analyzerPosts[i] = analyzer.Post{
			URI:       post.URI,
			CID:       post.CID,
			Text:      post.Text,
			Author:    post.Author,
			Likes:     post.Likes,
			Reposts:   post.Reposts,
			Replies:   post.Replies,
			CreatedAt: post.CreatedAt,
		}
	}

	// Analyze posts
	analyzedPosts, err := sentimentAnalyzer.AnalyzePosts(analyzerPosts)
	if err != nil {
		log.Fatalf("Failed to analyze posts: %v", err)
	}

	// Calculate overall sentiment
	var totalCompoundScore float64
	positiveCount := 0
	neutralCount := 0
	negativeCount := 0

	for _, analyzed := range analyzedPosts {
		totalCompoundScore += analyzed.SentimentScore

		if analyzed.SentimentScore >= 0.3 {
			positiveCount++
		} else if analyzed.SentimentScore <= -0.3 {
			negativeCount++
		} else {
			neutralCount++
		}
	}

	averageCompoundScore := totalCompoundScore / float64(len(analyzedPosts))
	netSentimentPercent := averageCompoundScore * 100.0

	var overallSentiment string
	if averageCompoundScore >= 0.3 {
		overallSentiment = "positive"
	} else if averageCompoundScore <= -0.3 {
		overallSentiment = "negative"
	} else {
		overallSentiment = "neutral"
	}

	return SentimentAnalysis{
		OverallSentiment:     overallSentiment,
		NetSentimentPercent:  netSentimentPercent,
		AverageCompoundScore: averageCompoundScore,
		PositiveCount:        positiveCount,
		NeutralCount:         neutralCount,
		NegativeCount:        negativeCount,
	}
}

type PostGenerationResult struct {
	PostText   string
	Stats      PostStatistics
	SamplePosts []SamplePost
}

func generatePostContent(posts []bskyclient.Post, sentiment SentimentAnalysis, intervalMinutes int) PostGenerationResult {
	// Calculate engagement scores
	type PostWithEngagement struct {
		Post           bskyclient.Post
		EngagementScore float64
	}

	postsWithEngagement := make([]PostWithEngagement, len(posts))
	for i, post := range posts {
		score := float64(post.Replies + post.Likes + post.Reposts)
		postsWithEngagement[i] = PostWithEngagement{
			Post:            post,
			EngagementScore: score,
		}
	}

	// Sort by engagement score (descending)
	sort.Slice(postsWithEngagement, func(i, j int) bool {
		return postsWithEngagement[i].EngagementScore > postsWithEngagement[j].EngagementScore
	})

	// Get top 5 posts
	topCount := 5
	if len(postsWithEngagement) < topCount {
		topCount = len(postsWithEngagement)
	}

	// Convert to formatter posts
	formatterPosts := make([]formatter.Post, topCount)
	samplePosts := make([]SamplePost, topCount)

	for i := 0; i < topCount; i++ {
		p := postsWithEngagement[i].Post
		formatterPosts[i] = formatter.Post{
			URI:             p.URI,
			CID:             p.CID,
			Author:          p.Author,
			Likes:           p.Likes,
			Reposts:         p.Reposts,
			Replies:         p.Replies,
			Sentiment:       "", // Will be set after analysis
			EngagementScore: postsWithEngagement[i].EngagementScore,
		}

		textPreview := p.Text
		if len(textPreview) > 100 {
			textPreview = textPreview[:100] + "..."
		}

		samplePosts[i] = SamplePost{
			Author:          p.Author,
			URI:             p.URI,
			CreatedAt:       p.CreatedAt,
			Likes:           p.Likes,
			Reposts:         p.Reposts,
			Replies:         p.Replies,
			EngagementScore: postsWithEngagement[i].EngagementScore,
			Sentiment:       "", // Could analyze individually
			TextPreview:     textPreview,
		}
	}

	// Generate post content
	postText := formatter.FormatPostContent(
		formatterPosts,
		sentiment.OverallSentiment,
		intervalMinutes,
		len(posts),
		sentiment.AverageCompoundScore,
	)

	// Calculate statistics
	charCount := len([]rune(postText))
	blueskyLimit := 300
	remaining := blueskyLimit - charCount

	var status string
	if remaining < 0 {
		status = "EXCEEDS_LIMIT"
	} else if remaining < 50 {
		status = "CLOSE_TO_LIMIT"
	} else {
		status = "WITHIN_LIMIT"
	}

	return PostGenerationResult{
		PostText:    postText,
		Stats:       PostStatistics{
			CharacterCount: charCount,
			BlueskyLimit:   blueskyLimit,
			Remaining:      remaining,
			Status:         status,
		},
		SamplePosts: samplePosts,
	}
}

func saveResults(result *AnalysisResult, filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}
