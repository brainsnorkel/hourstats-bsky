// scripts/export_sentiment_data.go
// Tool to export sentiment history data from DynamoDB for analysis
package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// DailySentimentDataPoint represents a single daily sentiment measurement
type DailySentimentDataPoint struct {
	Date             string    `json:"date" dynamodbav:"date"`
	RunID            string    `json:"runId" dynamodbav:"runId"`
	AverageSentiment float64   `json:"averageSentiment" dynamodbav:"averageSentiment"`
	MinSentiment     float64   `json:"minSentiment" dynamodbav:"minSentiment"`
	MaxSentiment     float64   `json:"maxSentiment" dynamodbav:"maxSentiment"`
	TotalRuns        int       `json:"totalRuns" dynamodbav:"totalRuns"`
	TotalPosts       int       `json:"totalPosts" dynamodbav:"totalPosts"`
	CreatedAt        time.Time `json:"createdAt" dynamodbav:"createdAt"`
	TTL              int64     `json:"ttl" dynamodbav:"ttl"`
}

// SentimentDataPoint represents hourly sentiment data
type SentimentDataPoint struct {
	RunID                string    `json:"runId" dynamodbav:"runId"`
	Timestamp            time.Time `json:"timestamp" dynamodbav:"timestamp"`
	AverageCompoundScore float64   `json:"averageCompoundScore" dynamodbav:"averageCompoundScore"`
	NetSentimentPercent  float64   `json:"netSentimentPercent" dynamodbav:"netSentimentPercent"`
	SentimentCategory    string    `json:"sentimentCategory" dynamodbav:"sentimentCategory"`
	TotalPosts           int       `json:"totalPosts" dynamodbav:"totalPosts"`
	CreatedAt            time.Time `json:"createdAt" dynamodbav:"createdAt"`
	TTL                  int64     `json:"ttl" dynamodbav:"ttl"`
}

func main() {
	outputDir := flag.String("output", "./analysis", "Output directory for exported data")
	dailyTable := flag.String("daily-table", "hourstats-daily-sentiment", "Daily sentiment DynamoDB table name")
	hourlyTable := flag.String("hourly-table", "hourstats-sentiment-history", "Hourly sentiment DynamoDB table name")
	format := flag.String("format", "both", "Output format: json, csv, or both")
	flag.Parse()

	ctx := context.Background()

	// Create output directory
	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		log.Fatalf("Failed to create output directory: %v", err)
	}

	// Load AWS config
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("Failed to load AWS config: %v", err)
	}

	client := dynamodb.NewFromConfig(cfg)

	// Export daily sentiment data
	fmt.Println("Exporting daily sentiment data...")
	dailyData, err := exportDailyData(ctx, client, *dailyTable)
	if err != nil {
		log.Printf("Warning: Failed to export daily data: %v", err)
	} else {
		fmt.Printf("Retrieved %d daily data points\n", len(dailyData))
		if *format == "json" || *format == "both" {
			if err := saveDailyJSON(dailyData, *outputDir); err != nil {
				log.Printf("Failed to save daily JSON: %v", err)
			}
		}
		if *format == "csv" || *format == "both" {
			if err := saveDailyCSV(dailyData, *outputDir); err != nil {
				log.Printf("Failed to save daily CSV: %v", err)
			}
		}
	}

	// Export hourly sentiment data
	fmt.Println("Exporting hourly sentiment data...")
	hourlyData, err := exportHourlyData(ctx, client, *hourlyTable)
	if err != nil {
		log.Printf("Warning: Failed to export hourly data: %v", err)
	} else {
		fmt.Printf("Retrieved %d hourly data points\n", len(hourlyData))
		if *format == "json" || *format == "both" {
			if err := saveHourlyJSON(hourlyData, *outputDir); err != nil {
				log.Printf("Failed to save hourly JSON: %v", err)
			}
		}
		if *format == "csv" || *format == "both" {
			if err := saveHourlyCSV(hourlyData, *outputDir); err != nil {
				log.Printf("Failed to save hourly CSV: %v", err)
			}
		}
	}

	// Generate statistics summary
	if len(dailyData) > 0 {
		generateStatsSummary(dailyData, hourlyData, *outputDir)
	}

	fmt.Println("Export complete!")
}

func exportDailyData(ctx context.Context, client *dynamodb.Client, tableName string) ([]DailySentimentDataPoint, error) {
	var allData []DailySentimentDataPoint
	var lastKey map[string]interface{}

	for {
		input := &dynamodb.ScanInput{
			TableName: aws.String(tableName),
		}

		if lastKey != nil {
			startKey, _ := attributevalue.MarshalMap(lastKey)
			input.ExclusiveStartKey = startKey
		}

		result, err := client.Scan(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to scan daily table: %w", err)
		}

		for _, item := range result.Items {
			var dp DailySentimentDataPoint
			if err := attributevalue.UnmarshalMap(item, &dp); err != nil {
				continue
			}
			allData = append(allData, dp)
		}

		if len(result.LastEvaluatedKey) == 0 {
			break
		}
		attributevalue.UnmarshalMap(result.LastEvaluatedKey, &lastKey)
	}

	// Sort by date
	sort.Slice(allData, func(i, j int) bool {
		return allData[i].Date < allData[j].Date
	})

	return allData, nil
}

func exportHourlyData(ctx context.Context, client *dynamodb.Client, tableName string) ([]SentimentDataPoint, error) {
	var allData []SentimentDataPoint
	var lastKey map[string]interface{}

	for {
		input := &dynamodb.ScanInput{
			TableName: aws.String(tableName),
		}

		if lastKey != nil {
			startKey, _ := attributevalue.MarshalMap(lastKey)
			input.ExclusiveStartKey = startKey
		}

		result, err := client.Scan(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to scan hourly table: %w", err)
		}

		for _, item := range result.Items {
			var dp SentimentDataPoint
			if err := attributevalue.UnmarshalMap(item, &dp); err != nil {
				continue
			}
			allData = append(allData, dp)
		}

		if len(result.LastEvaluatedKey) == 0 {
			break
		}
		attributevalue.UnmarshalMap(result.LastEvaluatedKey, &lastKey)
	}

	// Sort by timestamp
	sort.Slice(allData, func(i, j int) bool {
		return allData[i].Timestamp.Before(allData[j].Timestamp)
	})

	return allData, nil
}

func saveDailyJSON(data []DailySentimentDataPoint, outputDir string) error {
	file, err := os.Create(fmt.Sprintf("%s/daily_sentiment.json", outputDir))
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(data)
}

func saveHourlyJSON(data []SentimentDataPoint, outputDir string) error {
	file, err := os.Create(fmt.Sprintf("%s/hourly_sentiment.json", outputDir))
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(data)
}

func saveDailyCSV(data []DailySentimentDataPoint, outputDir string) error {
	file, err := os.Create(fmt.Sprintf("%s/daily_sentiment.csv", outputDir))
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Write header
	writer.Write([]string{"date", "average_sentiment", "min_sentiment", "max_sentiment", "total_runs", "total_posts"})

	for _, dp := range data {
		writer.Write([]string{
			dp.Date,
			fmt.Sprintf("%.4f", dp.AverageSentiment),
			fmt.Sprintf("%.4f", dp.MinSentiment),
			fmt.Sprintf("%.4f", dp.MaxSentiment),
			fmt.Sprintf("%d", dp.TotalRuns),
			fmt.Sprintf("%d", dp.TotalPosts),
		})
	}

	return nil
}

func saveHourlyCSV(data []SentimentDataPoint, outputDir string) error {
	file, err := os.Create(fmt.Sprintf("%s/hourly_sentiment.csv", outputDir))
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Write header
	writer.Write([]string{"timestamp", "net_sentiment_percent", "compound_score", "category", "total_posts"})

	for _, dp := range data {
		writer.Write([]string{
			dp.Timestamp.Format(time.RFC3339),
			fmt.Sprintf("%.4f", dp.NetSentimentPercent),
			fmt.Sprintf("%.4f", dp.AverageCompoundScore),
			dp.SentimentCategory,
			fmt.Sprintf("%d", dp.TotalPosts),
		})
	}

	return nil
}

func generateStatsSummary(dailyData []DailySentimentDataPoint, hourlyData []SentimentDataPoint, outputDir string) {
	file, err := os.Create(fmt.Sprintf("%s/sentiment_analysis_summary.md", outputDir))
	if err != nil {
		log.Printf("Failed to create summary file: %v", err)
		return
	}
	defer file.Close()

	// Calculate daily statistics
	var dailySum, dailyMin, dailyMax float64
	var dailyMinMinSentiment, dailyMaxMaxSentiment float64
	dailyMin = 100
	dailyMax = -100
	dailyMinMinSentiment = 100
	dailyMaxMaxSentiment = -100
	var minDate, maxDate, minMinDate, maxMaxDate string

	for _, dp := range dailyData {
		dailySum += dp.AverageSentiment
		if dp.AverageSentiment < dailyMin {
			dailyMin = dp.AverageSentiment
			minDate = dp.Date
		}
		if dp.AverageSentiment > dailyMax {
			dailyMax = dp.AverageSentiment
			maxDate = dp.Date
		}
		if dp.MinSentiment < dailyMinMinSentiment {
			dailyMinMinSentiment = dp.MinSentiment
			minMinDate = dp.Date
		}
		if dp.MaxSentiment > dailyMaxMaxSentiment {
			dailyMaxMaxSentiment = dp.MaxSentiment
			maxMaxDate = dp.Date
		}
	}
	dailyAvg := dailySum / float64(len(dailyData))

	// Calculate hourly statistics if available
	var hourlySum, hourlyMin, hourlyMax float64
	var hourlyMinTime, hourlyMaxTime time.Time
	if len(hourlyData) > 0 {
		hourlyMin = 100
		hourlyMax = -100
		for _, dp := range hourlyData {
			hourlySum += dp.NetSentimentPercent
			if dp.NetSentimentPercent < hourlyMin {
				hourlyMin = dp.NetSentimentPercent
				hourlyMinTime = dp.Timestamp
			}
			if dp.NetSentimentPercent > hourlyMax {
				hourlyMax = dp.NetSentimentPercent
				hourlyMaxTime = dp.Timestamp
			}
		}
	}
	hourlyAvg := 0.0
	if len(hourlyData) > 0 {
		hourlyAvg = hourlySum / float64(len(hourlyData))
	}

	// Calculate distribution buckets
	buckets := make(map[string]int)
	for _, dp := range dailyData {
		var bucket string
		switch {
		case dp.AverageSentiment < -50:
			bucket = "Very Negative (<-50%)"
		case dp.AverageSentiment < -20:
			bucket = "Negative (-50% to -20%)"
		case dp.AverageSentiment < -5:
			bucket = "Slightly Negative (-20% to -5%)"
		case dp.AverageSentiment < 5:
			bucket = "Neutral (-5% to 5%)"
		case dp.AverageSentiment < 20:
			bucket = "Slightly Positive (5% to 20%)"
		case dp.AverageSentiment < 50:
			bucket = "Positive (20% to 50%)"
		default:
			bucket = "Very Positive (>50%)"
		}
		buckets[bucket]++
	}

	// Write summary
	fmt.Fprintf(file, "# Bluesky Sentiment Analysis Summary\n\n")
	fmt.Fprintf(file, "Generated: %s\n\n", time.Now().Format("2006-01-02 15:04:05 MST"))

	fmt.Fprintf(file, "## Overview\n\n")
	fmt.Fprintf(file, "This document analyzes the historical sentiment data from Bluesky to understand the typical range of sentiment and inform better sentiment word calibration.\n\n")

	fmt.Fprintf(file, "## Data Coverage\n\n")
	fmt.Fprintf(file, "- **Daily Data Points**: %d days\n", len(dailyData))
	if len(dailyData) > 0 {
		fmt.Fprintf(file, "- **Date Range**: %s to %s\n", dailyData[0].Date, dailyData[len(dailyData)-1].Date)
	}
	fmt.Fprintf(file, "- **Hourly Data Points**: %d records\n", len(hourlyData))
	if len(hourlyData) > 0 {
		fmt.Fprintf(file, "- **Hourly Date Range**: %s to %s\n",
			hourlyData[0].Timestamp.Format("2006-01-02 15:04"),
			hourlyData[len(hourlyData)-1].Timestamp.Format("2006-01-02 15:04"))
	}

	fmt.Fprintf(file, "\n## Daily Sentiment Statistics\n\n")
	fmt.Fprintf(file, "### Average Sentiment (Daily Means)\n\n")
	fmt.Fprintf(file, "| Metric | Value | Date |\n")
	fmt.Fprintf(file, "|--------|-------|------|\n")
	fmt.Fprintf(file, "| Overall Average | %.2f%% | - |\n", dailyAvg)
	fmt.Fprintf(file, "| Lowest Daily Avg | %.2f%% | %s |\n", dailyMin, minDate)
	fmt.Fprintf(file, "| Highest Daily Avg | %.2f%% | %s |\n", dailyMax, maxDate)
	fmt.Fprintf(file, "| Range (Avg) | %.2f%% | - |\n", dailyMax-dailyMin)

	fmt.Fprintf(file, "\n### Extreme Sentiment Values (Min/Max per day)\n\n")
	fmt.Fprintf(file, "| Metric | Value | Date |\n")
	fmt.Fprintf(file, "|--------|-------|------|\n")
	fmt.Fprintf(file, "| Absolute Minimum | %.2f%% | %s |\n", dailyMinMinSentiment, minMinDate)
	fmt.Fprintf(file, "| Absolute Maximum | %.2f%% | %s |\n", dailyMaxMaxSentiment, maxMaxDate)
	fmt.Fprintf(file, "| Total Range | %.2f%% | - |\n", dailyMaxMaxSentiment-dailyMinMinSentiment)

	if len(hourlyData) > 0 {
		fmt.Fprintf(file, "\n## Hourly Sentiment Statistics\n\n")
		fmt.Fprintf(file, "| Metric | Value | Time |\n")
		fmt.Fprintf(file, "|--------|-------|------|\n")
		fmt.Fprintf(file, "| Overall Average | %.2f%% | - |\n", hourlyAvg)
		fmt.Fprintf(file, "| Lowest Hourly | %.2f%% | %s |\n", hourlyMin, hourlyMinTime.Format("2006-01-02 15:04"))
		fmt.Fprintf(file, "| Highest Hourly | %.2f%% | %s |\n", hourlyMax, hourlyMaxTime.Format("2006-01-02 15:04"))
		fmt.Fprintf(file, "| Range | %.2f%% | - |\n", hourlyMax-hourlyMin)
	}

	fmt.Fprintf(file, "\n## Sentiment Distribution (Daily Averages)\n\n")
	fmt.Fprintf(file, "| Category | Count | Percentage |\n")
	fmt.Fprintf(file, "|----------|-------|------------|\n")

	// Sort bucket names for consistent output
	bucketOrder := []string{
		"Very Negative (<-50%)",
		"Negative (-50% to -20%)",
		"Slightly Negative (-20% to -5%)",
		"Neutral (-5% to 5%)",
		"Slightly Positive (5% to 20%)",
		"Positive (20% to 50%)",
		"Very Positive (>50%)",
	}
	for _, bucket := range bucketOrder {
		count := buckets[bucket]
		pct := float64(count) / float64(len(dailyData)) * 100
		fmt.Fprintf(file, "| %s | %d | %.1f%% |\n", bucket, count, pct)
	}

	fmt.Fprintf(file, "\n## Key Insights\n\n")
	fmt.Fprintf(file, "### Observed Sentiment Range\n\n")
	fmt.Fprintf(file, "Based on the historical data, Bluesky sentiment typically operates in a **narrow positive band**:\n\n")
	fmt.Fprintf(file, "- **Typical Range**: %.1f%% to %.1f%% (daily averages)\n", dailyMin, dailyMax)
	fmt.Fprintf(file, "- **Extreme Range**: %.1f%% to %.1f%% (intraday extremes)\n", dailyMinMinSentiment, dailyMaxMaxSentiment)
	fmt.Fprintf(file, "- **Central Tendency**: Around %.1f%% (slightly positive)\n\n", dailyAvg)

	fmt.Fprintf(file, "### Implications for Sentiment Word Calibration\n\n")
	fmt.Fprintf(file, "The current 100-word sentiment scale spans -100%% to +100%%, but Bluesky sentiment rarely ventures outside the %.0f%% to %.0f%% range. This means:\n\n",
		dailyMinMinSentiment-5, dailyMaxMaxSentiment+5)
	fmt.Fprintf(file, "1. **Most negative words are never used** - Words for sentiment below %.0f%% are essentially unused\n", dailyMinMinSentiment-5)
	fmt.Fprintf(file, "2. **Most positive words are never used** - Words for sentiment above %.0f%% are essentially unused\n", dailyMaxMaxSentiment+5)
	fmt.Fprintf(file, "3. **Word variety is limited** - Only a small subset of the 100 words are ever selected\n\n")

	fmt.Fprintf(file, "### Recommendations\n\n")
	fmt.Fprintf(file, "1. **Recalibrate the word scale** to focus on the actual observed range (approximately %.0f%% to %.0f%%)\n", dailyMinMinSentiment-10, dailyMaxMaxSentiment+10)
	fmt.Fprintf(file, "2. **Add more nuanced words** in the slightly positive range (5%%-15%%) where most sentiment falls\n")
	fmt.Fprintf(file, "3. **Use relative positioning** - Map words to percentiles of observed data rather than absolute percentages\n")
	fmt.Fprintf(file, "4. **Consider seasonal/event variation** - Some days show wider swings than others\n\n")

	fmt.Fprintf(file, "## Raw Data Files\n\n")
	fmt.Fprintf(file, "- `daily_sentiment.csv` - Daily aggregated sentiment data\n")
	fmt.Fprintf(file, "- `daily_sentiment.json` - Daily data in JSON format\n")
	fmt.Fprintf(file, "- `hourly_sentiment.csv` - Hourly sentiment data (14-day rolling window)\n")
	fmt.Fprintf(file, "- `hourly_sentiment.json` - Hourly data in JSON format\n")

	fmt.Printf("Generated summary at %s/sentiment_analysis_summary.md\n", outputDir)
}
