package topics

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// headlineFeeds are Google News RSS feeds for English-language headlines
// across multiple regions to avoid US-only bias.
var headlineFeeds = []string{
	"https://news.google.com/rss?hl=en&gl=US&ceid=US:en",
	"https://news.google.com/rss?hl=en&gl=GB&ceid=GB:en",
	"https://news.google.com/rss?hl=en&gl=AU&ceid=AU:en",
}

const (
	headlineFetchTimeout = 3 * time.Second
	maxHeadlines         = 30
)

type rssResponse struct {
	Channel struct {
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
}

type rssItem struct {
	Title string `xml:"title"`
}

// FetchHeadlines retrieves current news headlines from Google News RSS feeds.
// Returns deduplicated headlines from multiple regional feeds.
// On any failure, returns nil — callers should treat empty headlines as a no-op.
func FetchHeadlines(ctx context.Context) []string {
	ctx, cancel := context.WithTimeout(ctx, headlineFetchTimeout)
	defer cancel()

	seen := make(map[string]bool)
	var headlines []string

	client := &http.Client{Timeout: headlineFetchTimeout}

	for _, feedURL := range headlineFeeds {
		items, err := fetchFeed(ctx, client, feedURL)
		if err != nil {
			slog.Debug("headlines: feed fetch failed", "url", feedURL, "error", err)
			continue
		}
		for _, item := range items {
			title := cleanHeadlineTitle(item.Title)
			if title == "" {
				continue
			}
			lower := strings.ToLower(title)
			if seen[lower] {
				continue
			}
			seen[lower] = true
			headlines = append(headlines, title)
		}
	}

	if len(headlines) > maxHeadlines {
		headlines = headlines[:maxHeadlines]
	}

	if len(headlines) > 0 {
		slog.Info("headlines: fetched for topic context", "count", len(headlines))
	}

	return headlines
}

func fetchFeed(ctx context.Context, client *http.Client, url string) ([]rssItem, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	var rss rssResponse
	if err := xml.Unmarshal(body, &rss); err != nil {
		return nil, fmt.Errorf("parse XML: %w", err)
	}

	return rss.Channel.Items, nil
}

// cleanHeadlineTitle removes the trailing " - Source Name" that Google News
// appends to headlines (e.g. "Trump proposes tariffs - Reuters").
func cleanHeadlineTitle(title string) string {
	title = strings.TrimSpace(title)
	if idx := strings.LastIndex(title, " - "); idx > 0 {
		title = strings.TrimSpace(title[:idx])
	}
	return title
}
