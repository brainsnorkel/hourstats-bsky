package analyzer

import (
	"fmt"

	"github.com/jonreiter/govader"
)

type AnalyzedPost struct {
	Post
	Sentiment       string
	SentimentScore  float64
	EngagementScore float64
}

// Post represents a social media post for analysis
type Post struct {
	URI       string
	CID       string
	Text      string
	Author    string
	Likes     int
	Reposts   int
	Replies   int
	CreatedAt string
	IsReply   bool
}

type SentimentAnalyzer struct {
	analyzer *govader.SentimentIntensityAnalyzer
}

func New() *SentimentAnalyzer {
	return &SentimentAnalyzer{
		analyzer: govader.NewSentimentIntensityAnalyzer(),
	}
}

func (sa *SentimentAnalyzer) AnalyzePosts(posts []Post) ([]AnalyzedPost, error) {
	analyzedPosts := make([]AnalyzedPost, 0, len(posts))

	for _, post := range posts {
		analyzedPost, err := sa.analyzePost(post)
		if err != nil {
			return nil, fmt.Errorf("failed to analyze post %s: %w", post.URI, err)
		}
		analyzedPosts = append(analyzedPosts, analyzedPost)
	}

	return analyzedPosts, nil
}

func (sa *SentimentAnalyzer) analyzePost(post Post) (AnalyzedPost, error) {
	sentiment := sa.analyzer.PolarityScores(post.Text)
	sentimentCategory := sa.categorizeSentiment(sentiment)
	engagementScore := sa.calculateEngagementScore(post, sentiment.Compound)

	return AnalyzedPost{
		Post:            post,
		Sentiment:       sentimentCategory,
		SentimentScore:  sentiment.Compound,
		EngagementScore: engagementScore,
	}, nil
}

func (sa *SentimentAnalyzer) categorizeSentiment(sentiment govader.Sentiment) string {
	compound := sentiment.Compound

	// Use more nuanced thresholds for better emotion detection
	// Adjusted thresholds to better handle neutral language like "okay"
	if compound >= 0.3 {
		return "positive"
	} else if compound <= -0.3 {
		return "negative"
	}
	return "neutral"
}

func (sa *SentimentAnalyzer) calculateEngagementScore(post Post, sentimentScore float64) float64 {
	// Engagement score calculation based on replies + likes + reposts
	// This matches the README specification for ranking posts

	return float64(post.Replies + post.Likes + post.Reposts)
}
