package main

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/store"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "usage: import-dynamodb <db-path> <seed-dir>\n")
		fmt.Fprintf(os.Stderr, "  e.g. import-dynamodb /data/hourstats-staging.db /data/seed/backup-2026-02-08T01-57-11Z\n")
		os.Exit(1)
	}

	dbPath := os.Args[1]
	seedDir := os.Args[2]

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	db, err := store.New(dbPath)
	if err != nil {
		slog.Error("failed to open database", "path", dbPath, "error", err)
		os.Exit(1)
	}
	defer db.Close()

	ctx := context.Background()

	sentimentFile := filepath.Join(seedDir, "hourstats-sentiment-history.jsonl.gz")
	if _, err := os.Stat(sentimentFile); err == nil {
		n, err := importSentimentHistory(ctx, db, sentimentFile)
		if err != nil {
			slog.Error("import sentiment history failed", "error", err)
			os.Exit(1)
		}
		slog.Info("imported sentiment history", "count", n, "file", sentimentFile)
	}

	dailyFile := filepath.Join(seedDir, "hourstats-daily-sentiment.jsonl.gz")
	if _, err := os.Stat(dailyFile); err == nil {
		n, err := importDailySentiment(ctx, db, dailyFile)
		if err != nil {
			slog.Error("import daily sentiment failed", "error", err)
			os.Exit(1)
		}
		slog.Info("imported daily sentiment", "count", n, "file", dailyFile)
	}

	slog.Info("import complete", "db", dbPath)
}

// ---------------------------------------------------------------------------
// DynamoDB JSONL format: each field is {"Value": "string_value"}
// ---------------------------------------------------------------------------

type dynField struct {
	Value string `json:"Value"`
}

type dynSentimentHistory struct {
	RunID                dynField `json:"runId"`
	Timestamp            dynField `json:"timestamp"`
	AverageCompoundScore dynField `json:"averageCompoundScore"`
	NetSentimentPercent  dynField `json:"netSentimentPercent"`
	SentimentCategory    dynField `json:"sentimentCategory"`
	TotalPosts           dynField `json:"totalPosts"`
	CreatedAt            dynField `json:"createdAt"`
	TTL                  dynField `json:"ttl"`
}

type dynDailySentiment struct {
	Date             dynField `json:"date"`
	RunID            dynField `json:"runId"`
	AverageSentiment dynField `json:"averageSentiment"`
	MinSentiment     dynField `json:"minSentiment"`
	MaxSentiment     dynField `json:"maxSentiment"`
	TotalRuns        dynField `json:"totalRuns"`
	TotalPosts       dynField `json:"totalPosts"`
	CreatedAt        dynField `json:"createdAt"`
	TTL              dynField `json:"ttl"`
}

func importSentimentHistory(ctx context.Context, db *store.Store, path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return 0, fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	dec := json.NewDecoder(gz)
	count := 0

	for dec.More() {
		var rec dynSentimentHistory
		if err := dec.Decode(&rec); err != nil {
			return count, fmt.Errorf("decode line %d: %w", count+1, err)
		}

		ts, _ := time.Parse(time.RFC3339Nano, rec.Timestamp.Value)
		created, _ := time.Parse(time.RFC3339Nano, rec.CreatedAt.Value)

		dp := store.SentimentDataPoint{
			RunID:                rec.RunID.Value,
			Timestamp:            ts,
			AverageCompoundScore: parseFloat(rec.AverageCompoundScore.Value),
			NetSentimentPercent:  parseFloat(rec.NetSentimentPercent.Value),
			SentimentCategory:    rec.SentimentCategory.Value,
			TotalPosts:           parseInt(rec.TotalPosts.Value),
			CreatedAt:            created,
			TTL:                  parseInt64(rec.TTL.Value),
		}

		if err := db.StoreSentimentDataPoint(ctx, dp); err != nil {
			return count, fmt.Errorf("store sentiment point %d: %w", count+1, err)
		}
		count++
	}

	return count, nil
}

func importDailySentiment(ctx context.Context, db *store.Store, path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return 0, fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	dec := json.NewDecoder(gz)
	count := 0

	for dec.More() {
		var rec dynDailySentiment
		if err := dec.Decode(&rec); err != nil {
			return count, fmt.Errorf("decode line %d: %w", count+1, err)
		}

		created, _ := time.Parse(time.RFC3339Nano, rec.CreatedAt.Value)
		avg := parseFloat(rec.AverageSentiment.Value)

		dp := store.DailySentimentDataPoint{
			Date:             rec.Date.Value,
			RunID:            rec.RunID.Value,
			AverageSentiment: avg,
			MinSentiment:     parseFloat(rec.MinSentiment.Value),
			MaxSentiment:     parseFloat(rec.MaxSentiment.Value),
			Q1Sentiment:      avg, // Not in old DynamoDB schema — approximate with avg
			MedianSentiment:  avg,
			Q3Sentiment:      avg,
			TotalRuns:        parseInt(rec.TotalRuns.Value),
			TotalPosts:       parseInt(rec.TotalPosts.Value),
			CreatedAt:        created,
			TTL:              parseInt64(rec.TTL.Value),
		}

		if err := db.StoreDailySentiment(ctx, dp); err != nil {
			return count, fmt.Errorf("store daily sentiment %d: %w", count+1, err)
		}
		count++
	}

	return count, nil
}

func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func parseInt(s string) int {
	v, _ := strconv.Atoi(s)
	return v
}

func parseInt64(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}
