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
	// prepare rewrites post text before scoring. Nil means identity, which
	// is what stock VADER gets.
	prepare func(string) string
}

func New() *SentimentAnalyzer {
	return &SentimentAnalyzer{
		analyzer: govader.NewSentimentIntensityAnalyzer(),
	}
}

// NewEmojiAware returns an analyzer whose emoji handling is replaced by the
// curated table in emoji.go: variation selectors, zero-width joiners and
// skin-tone modifiers are stripped from the text first, then each known emoji
// scores its curated valence instead of the sentiment of its Unicode name.
//
// Since 2026-09-11 this is the headline scorer. Every analysis cycle still
// scores its window twice — here for the headline and with New() for
// sentiment_history.net_sentiment_pct_stock — so the two series stay
// comparable across the switch. The switch shifted the distribution of net
// sentiment up by 1.77 points, so the word bands in
// internal/formatter/sentiment_100_words.go moved with it; see
// docs/SENTIMENT_REALIGNMENT_PLAN.md and
// docs/SENTIMENT_CALIBRATION_REVIEW_2026-09.md.
func NewEmojiAware() *SentimentAnalyzer {
	sa := govader.NewSentimentIntensityAnalyzer()
	applyEmojiOverrides(sa)
	return &SentimentAnalyzer{
		analyzer: sa,
		prepare:  stripEmojiModifiers,
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
	text := post.Text
	if sa.prepare != nil {
		text = sa.prepare(text)
	}
	sentiment := sa.analyzer.PolarityScores(text)
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
