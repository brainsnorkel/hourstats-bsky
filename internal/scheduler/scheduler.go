package scheduler

import (
	"log"
	"time"

	"github.com/christophergentle/trendjournal/internal/analyzer"
	"github.com/christophergentle/trendjournal/internal/client"
)

type Scheduler struct {
	client   *client.BlueskyClient
	analyzer *analyzer.SentimentAnalyzer
}

func New(handle, password string) *Scheduler {
	blueskyClient := client.New(handle, password)
	sentimentAnalyzer := analyzer.New()
	
	return &Scheduler{
		client:   blueskyClient,
		analyzer: sentimentAnalyzer,
	}
}

func (s *Scheduler) Start() error {
	// Authenticate with Bluesky
	if err := s.client.Authenticate(); err != nil {
		return err
	}

	log.Println("Successfully authenticated with Bluesky")

	// Start the hourly ticker
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	// Run immediately on startup
	if err := s.runAnalysis(); err != nil {
		log.Printf("Error in initial analysis: %v", err)
	}

	// Run every hour
	for {
		select {
		case <-ticker.C:
			if err := s.runAnalysis(); err != nil {
				log.Printf("Error in scheduled analysis: %v", err)
			}
		}
	}
}

func (s *Scheduler) runAnalysis() error {
	log.Println("Starting trend analysis...")

	// Fetch trending posts
	clientPosts, err := s.client.GetTrendingPosts()
	if err != nil {
		return err
	}

	// Convert client posts to analyzer posts
	analyzerPosts := s.convertToAnalyzerPosts(clientPosts)

	// Analyze sentiment and extract topics
	analyzedPosts, err := s.analyzer.AnalyzePosts(analyzerPosts)
	if err != nil {
		return err
	}

	// Get top 5 posts
	topPosts := s.getTopPosts(analyzedPosts, 5)

	// Convert back to client posts for posting
	clientTopPosts := s.convertToClientPosts(topPosts)

	// Post the results
	if err := s.client.PostTrendingSummary(clientTopPosts); err != nil {
		return err
	}

	log.Printf("Successfully posted trending summary with %d posts", len(clientTopPosts))
	return nil
}

func (s *Scheduler) convertToAnalyzerPosts(clientPosts []client.Post) []analyzer.Post {
	var analyzerPosts []analyzer.Post
	for _, post := range clientPosts {
		analyzerPosts = append(analyzerPosts, analyzer.Post{
			URI:       post.URI,
			Text:      post.Text,
			Author:    post.Author,
			Likes:     post.Likes,
			Reposts:   post.Reposts,
			Replies:   post.Replies,
			CreatedAt: post.CreatedAt,
		})
	}
	return analyzerPosts
}

func (s *Scheduler) convertToClientPosts(analyzedPosts []analyzer.AnalyzedPost) []client.Post {
	var clientPosts []client.Post
	for _, post := range analyzedPosts {
		clientPosts = append(clientPosts, client.Post{
			URI:       post.URI,
			Text:      post.Text,
			Author:    post.Author,
			Likes:     post.Likes,
			Reposts:   post.Reposts,
			Replies:   post.Replies,
			CreatedAt: post.CreatedAt,
		})
	}
	return clientPosts
}

func (s *Scheduler) getTopPosts(posts []analyzer.AnalyzedPost, count int) []analyzer.AnalyzedPost {
	// Sort by engagement score (combination of likes, reposts, and sentiment)
	// For now, just return the first N posts
	if len(posts) < count {
		return posts
	}
	return posts[:count]
}
