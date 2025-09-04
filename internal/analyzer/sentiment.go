package analyzer

import (
	"fmt"
	"strings"

	"github.com/jonreiter/govader"
)

type AnalyzedPost struct {
	Post
	Sentiment       string
	SentimentScore  float64
	Topics          []string
	EngagementScore float64
}

// Post represents a social media post for analysis
type Post struct {
	URI       string
	Text      string
	Author    string
	Likes     int
	Reposts   int
	Replies   int
	CreatedAt string
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
	var analyzedPosts []AnalyzedPost

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
	// Analyze sentiment using VADER
	sentiment := sa.analyzer.PolarityScores(post.Text)

	// Also do keyword-based sentiment analysis as a fallback
	keywordSentiment := sa.analyzeKeywordSentiment(post.Text)

	// Determine sentiment category (combine both approaches)
	sentimentCategory := sa.categorizeSentiment(sentiment)

	// If VADER is neutral but keywords suggest otherwise, use keyword sentiment
	if sentimentCategory == "neutral" && keywordSentiment != "neutral" {
		sentimentCategory = keywordSentiment
	}

	// Extract topics (simple keyword extraction for now)
	topics := sa.extractTopics(post.Text)

	// Calculate engagement score
	engagementScore := sa.calculateEngagementScore(post, sentiment.Compound)

	return AnalyzedPost{
		Post:            post,
		Sentiment:       sentimentCategory,
		SentimentScore:  sentiment.Compound,
		Topics:          topics,
		EngagementScore: engagementScore,
	}, nil
}

func (sa *SentimentAnalyzer) categorizeSentiment(sentiment govader.Sentiment) string {
	compound := sentiment.Compound

	// Use more nuanced thresholds for better emotion detection
	if compound >= 0.2 {
		return "positive"
	} else if compound <= -0.2 {
		return "negative"
	}
	return "neutral"
}

func (sa *SentimentAnalyzer) extractTopics(text string) []string {
	// Simple topic extraction based on hashtags and common keywords
	// In a more sophisticated implementation, we'd use NLP libraries
	// or machine learning models for better topic extraction

	// Clean the text and split into words
	cleaned := strings.ToLower(text)
	words := strings.Fields(cleaned)
	var topics []string

	// Extract common topic keywords (simplified)
	topicKeywords := map[string]string{
		"tech":     "technology",
		"ai":       "artificial intelligence",
		"crypto":   "cryptocurrency",
		"climate":  "climate change",
		"politics": "politics",
		"news":     "news",
		"music":    "music",
		"art":      "art",
		"science":  "science",
		"health":   "health",
	}

	// Extract hashtags and their keyword equivalents
	for _, word := range words {
		if strings.HasPrefix(word, "#") {
			topics = append(topics, word)
			// Also check if the hashtag content matches a keyword
			hashtagContent := strings.TrimLeft(word, "#")
			cleanHashtag := strings.TrimRight(hashtagContent, ".,!?;:")
			if topic, exists := topicKeywords[cleanHashtag]; exists {
				topics = append(topics, topic)
			}
		}
	}

	for _, word := range words {
		// Remove punctuation from the end of words
		cleanWord := strings.TrimRight(word, ".,!?;:")
		if topic, exists := topicKeywords[cleanWord]; exists {
			topics = append(topics, topic)
		}
	}

	// Remove duplicates
	seen := make(map[string]bool)
	var uniqueTopics []string
	for _, topic := range topics {
		if !seen[topic] {
			seen[topic] = true
			uniqueTopics = append(uniqueTopics, topic)
		}
	}
	topics = uniqueTopics

	return topics
}

func (sa *SentimentAnalyzer) calculateEngagementScore(post Post, sentimentScore float64) float64 {
	// Engagement score calculation based on replies + likes + reposts
	// This matches the README specification for ranking posts

	baseScore := float64(post.Replies + post.Likes + post.Reposts)

	// Boost positive sentiment posts slightly
	if sentimentScore > 0 {
		baseScore *= 1.1
	}

	return baseScore
}

// analyzeKeywordSentiment performs simple keyword-based sentiment analysis
func (sa *SentimentAnalyzer) analyzeKeywordSentiment(text string) string {
	text = strings.ToLower(text)

	positiveWords := []string{
		"great", "awesome", "amazing", "wonderful", "fantastic", "excellent", "love", "loved", "best", "good", "nice", "happy", "excited", "thrilled", "brilliant", "perfect", "incredible", "outstanding", "superb", "marvelous", "delighted", "pleased", "satisfied", "impressed", "grateful", "blessed", "fortunate", "lucky", "successful", "victory", "win", "achievement", "progress", "improvement", "breakthrough", "innovation", "creative", "inspiring", "motivating", "encouraging", "hopeful", "optimistic", "confident", "proud", "celebrate", "cheer", "smile", "laugh", "joy", "fun", "enjoy", "wonderful", "beautiful", "gorgeous", "stunning", "magnificent", "spectacular", "breathtaking", "inspiring", "uplifting", "positive", "upbeat", "cheerful", "bright", "sunny", "warm", "cozy", "comfortable", "peaceful", "calm", "serene", "tranquil", "relaxed", "refreshed", "renewed", "rejuvenated", "energized", "vibrant", "alive", "thriving", "flourishing", "prosperous", "successful", "accomplished", "fulfilled", "content", "satisfied", "grateful", "thankful", "appreciative", "blessed", "fortunate", "lucky", "privileged", "honored", "proud", "accomplished", "achieved", "succeeded", "won", "victory", "triumph", "conquest", "breakthrough", "milestone", "landmark", "record", "best", "top", "peak", "summit", "climax", "pinnacle", "zenith", "acme", "apex", "crown", "jewel", "gem", "treasure", "prize", "reward", "gift", "blessing", "miracle", "wonder", "marvel", "phenomenon", "extraordinary", "exceptional", "remarkable", "notable", "significant", "important", "valuable", "precious", "cherished", "beloved", "adored", "treasured", "esteemed", "respected", "admired", "revered", "worshiped", "idolized", "hero", "champion", "winner", "leader", "pioneer", "trailblazer", "innovator", "creator", "artist", "genius", "master", "expert", "professional", "skilled", "talented", "gifted", "brilliant", "intelligent", "wise", "smart", "clever", "sharp", "quick", "fast", "efficient", "effective", "productive", "successful", "profitable", "beneficial", "helpful", "useful", "valuable", "worthwhile", "meaningful", "purposeful", "significant", "important", "essential", "crucial", "vital", "critical", "key", "main", "primary", "principal", "chief", "leading", "top", "first", "best", "greatest", "highest", "maximum", "optimal", "perfect", "ideal", "excellent", "outstanding", "superior", "premium", "quality", "high-quality", "top-notch", "first-class", "world-class",
	}

	negativeWords := []string{
		"bad", "terrible", "awful", "horrible", "disgusting", "hate", "hated", "worst", "evil", "nasty", "sad", "angry", "mad", "furious", "rage", "frustrated", "annoyed", "irritated", "upset", "disappointed", "devastated", "crushed", "broken", "hurt", "pain", "suffering", "agony", "torment", "torture", "nightmare", "disaster", "catastrophe", "tragedy", "crisis", "emergency", "danger", "threat", "risk", "fear", "afraid", "scared", "terrified", "panic", "anxiety", "worry", "concern", "stress", "pressure", "tension", "strain", "burden", "load", "weight", "heavy", "difficult", "hard", "tough", "challenging", "struggle", "battle", "fight", "war", "conflict", "dispute", "argument", "quarrel", "fight", "brawl", "violence", "aggression", "hostility", "anger", "rage", "fury", "wrath", "indignation", "resentment", "bitterness", "hatred", "loathing", "disgust", "revulsion", "repulsion", "abhorrence", "detestation", "aversion", "antipathy", "hostility", "animosity", "enmity", "malice", "spite", "venom", "poison", "toxic", "harmful", "damaging", "destructive", "ruinous", "devastating", "catastrophic", "tragic", "sad", "sorrowful", "mournful", "melancholy", "depressed", "dejected", "despondent", "gloomy", "bleak", "dark", "dismal", "dreary", "miserable", "wretched", "pitiful", "pathetic", "lamentable", "regrettable", "unfortunate", "unlucky", "cursed", "doomed", "fated", "destined", "inevitable", "unavoidable", "inescapable", "hopeless", "helpless", "powerless", "weak", "feeble", "frail", "fragile", "vulnerable", "exposed", "defenseless", "unprotected", "unsafe", "dangerous", "risky", "hazardous", "perilous", "precarious", "unstable", "shaky", "uncertain", "doubtful", "suspicious", "skeptical", "cynical", "pessimistic", "negative", "downbeat",
	}

	positiveCount := 0
	negativeCount := 0

	for _, word := range positiveWords {
		if strings.Contains(text, word) {
			positiveCount++
		}
	}

	for _, word := range negativeWords {
		if strings.Contains(text, word) {
			negativeCount++
		}
	}

	if positiveCount > negativeCount {
		return "positive"
	} else if negativeCount > positiveCount {
		return "negative"
	}
	return "neutral"
}
