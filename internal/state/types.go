// Package state holds the sentiment data point types shared between the store,
// the analysis cycle and the chart generators.
package state

import "time"

// SentimentDataPoint represents a single sentiment measurement at a point in time
type SentimentDataPoint struct {
	RunID                string    `json:"runId" dynamodbav:"runId"`
	Timestamp            time.Time `json:"timestamp" dynamodbav:"timestamp"`
	AverageCompoundScore float64   `json:"averageCompoundScore" dynamodbav:"averageCompoundScore"`
	NetSentimentPercent  float64   `json:"netSentimentPercent" dynamodbav:"netSentimentPercent"`
	SentimentCategory    string    `json:"sentimentCategory" dynamodbav:"sentimentCategory"`
	TotalPosts           int       `json:"totalPosts" dynamodbav:"totalPosts"`
	TopTopic             string    `json:"topTopic,omitempty" dynamodbav:"topTopic,omitempty"` // rank-1 trending topic for the cycle, if known
	CreatedAt            time.Time `json:"createdAt" dynamodbav:"createdAt"`
	TTL                  int64     `json:"ttl" dynamodbav:"ttl"`
}

// YearlySparklineDataPoint represents a data point for yearly sparkline visualization
type YearlySparklineDataPoint struct {
	Date                string    `json:"date"`
	AverageSentiment    float64   `json:"averageSentiment"`
	MinSentiment        float64   `json:"minSentiment"`
	MaxSentiment        float64   `json:"maxSentiment"`
	Q1Sentiment         float64   `json:"q1Sentiment"`
	MedianSentiment     float64   `json:"medianSentiment"`
	Q3Sentiment         float64   `json:"q3Sentiment"`
	Timestamp           time.Time `json:"timestamp"`
	NetSentimentPercent float64   `json:"netSentimentPercent"` // Alias for AverageSentiment
}
