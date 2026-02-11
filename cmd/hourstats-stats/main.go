package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"
)

// JSON response types matching API output
type Snapshot struct {
	ID                      int64   `json:"id"`
	SnapshotTime            string  `json:"snapshot_time"`
	ActiveEndpoint          string  `json:"active_endpoint"`
	EndpointRotations       int     `json:"endpoint_rotations"`
	ReconnectCount          int     `json:"reconnect_count"`
	ConnectionUptimeSeconds int     `json:"connection_uptime_seconds"`
	EventsReceived          int     `json:"events_received"`
	PostsProcessed          int     `json:"posts_processed"`
	EventsSkipped           int     `json:"events_skipped"`
	ConsumerErrors          int     `json:"consumer_errors"`
	TotalFirehosePosts      int     `json:"total_firehose_posts"`
	EnglishPostsStored      int     `json:"english_posts_stored"`
	RootPosts               int     `json:"root_posts"`
	ReplyPosts              int     `json:"reply_posts"`
	PostsPerMinuteAvg       float64 `json:"posts_per_minute_avg"`
	AnalysisRan             int     `json:"analysis_ran"`
	PostsConsidered         int     `json:"posts_considered"`
	PostsHydrated           int     `json:"posts_hydrated"`
	HydrationErrors         int     `json:"hydration_errors"`
	SentimentResult         string  `json:"sentiment_result"`
	PostingSkipped          int     `json:"posting_skipped"`
}

type Event struct {
	ID        int64  `json:"id"`
	EventTime string `json:"event_time"`
	EventType string `json:"event_type"`
	Details   string `json:"details"`
}

type PostingEntry struct {
	LastPosted      string `json:"last_posted"`
	Summary         string `json:"summary"`
	URI             string `json:"uri,omitempty"`
	NextAnticipated string `json:"next_anticipated,omitempty"`
}

type PostingActivity struct {
	SentimentSummary *PostingEntry `json:"sentiment_summary"`
	YearlyChart      *PostingEntry `json:"yearly_chart"`
	DailyQuote       *PostingEntry `json:"daily_quote"`
	TrendingTopics   *PostingEntry `json:"trending_topics"`
}

type TopicSnapshot struct {
	ID             int64    `json:"id"`
	SnapshotTime   string   `json:"snapshot_time"`
	Rank           int      `json:"rank"`
	TopicID        string   `json:"topic_id"`
	Label          string   `json:"label"`
	Description    string   `json:"description"`
	PostCount      int      `json:"post_count"`
	Keywords       []string `json:"keywords"`
	ExemplarURI    string   `json:"exemplar_uri"`
	ExemplarHandle string   `json:"exemplar_handle"`
	IsMeme         bool     `json:"is_meme"`
	Justification  string   `json:"justification"`
}

var (
	addr      = flag.String("addr", "http://localhost:9111", "API address")
	hours     = flag.Int("hours", 24, "Hours of history")
	limit     = flag.Int("limit", 48, "Max records")
	eventType = flag.String("type", "", "Event type filter (events command only)")
	jsonOut   = flag.Bool("json", false, "Output raw JSON")
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: hourstats-stats [flags] <command>\n\n")
		fmt.Fprintf(os.Stderr, "Commands:\n")
		fmt.Fprintf(os.Stderr, "  summary   Show a summary dashboard (default)\n")
		fmt.Fprintf(os.Stderr, "  latest    Show the most recent stats snapshot\n")
		fmt.Fprintf(os.Stderr, "  history   Show stats snapshot history\n")
		fmt.Fprintf(os.Stderr, "  events    Show event log\n")
		fmt.Fprintf(os.Stderr, "  topics    Show recent topic reasoning\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	command := "summary"
	if flag.NArg() > 0 {
		command = flag.Arg(0)
	}

	switch command {
	case "summary":
		cmdSummary()
	case "latest":
		cmdLatest()
	case "history":
		cmdHistory()
	case "events":
		cmdEvents()
	case "topics":
		cmdTopics()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		flag.Usage()
		os.Exit(1)
	}
}

func apiGet(path string) ([]byte, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	url := *addr + path
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot connect to stats API at %s — is 'fly proxy 9111:9111' running?\n", *addr)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		if strings.Contains(path, "/latest") {
			fmt.Fprintf(os.Stderr, "No stats snapshots recorded yet. First snapshot appears after 30 minutes of uptime.\n")
			os.Exit(1)
		}
		return nil, fmt.Errorf("not found")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}

	return body, nil
}

func cmdSummary() {
	body, err := apiGet("/stats/latest")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if *jsonOut {
		fmt.Println(string(body))
		return
	}

	var snap Snapshot
	if err := json.Unmarshal(body, &snap); err != nil {
		fmt.Fprintf(os.Stderr, "Parse error: %v\n", err)
		os.Exit(1)
	}

	// Get recent events for display
	eventsBody, _ := apiGet(fmt.Sprintf("/stats/events?hours=%d&limit=5", *hours))
	var events []Event
	if eventsBody != nil {
		json.Unmarshal(eventsBody, &events)
	}

	fmt.Println("=== Hourstats Stats Dashboard ===")
	fmt.Printf("Time: %s\n\n", formatTime(snap.SnapshotTime))

	fmt.Println("--- Connection ---")
	fmt.Printf("Endpoint:    %s\n", snap.ActiveEndpoint)
	fmt.Printf("Uptime:      %s\n", formatDuration(snap.ConnectionUptimeSeconds))
	fmt.Printf("Rotations:   %d\n", snap.EndpointRotations)
	fmt.Printf("Reconnects:  %d\n\n", snap.ReconnectCount)

	fmt.Println("--- Traffic (last 30 min) ---")
	fmt.Printf("Firehose:    %s posts\n", formatInt(snap.TotalFirehosePosts))
	fmt.Printf("English:     %s stored (%s root / %s reply)\n",
		formatInt(snap.EnglishPostsStored),
		formatInt(snap.RootPosts),
		formatInt(snap.ReplyPosts))
	fmt.Printf("Rate:        %.1f posts/min\n\n", snap.PostsPerMinuteAvg)

	fmt.Println("--- Last Analysis ---")
	fmt.Printf("Posts:       %s considered → %s hydrated (%d errors)\n",
		formatInt(snap.PostsConsidered),
		formatInt(snap.PostsHydrated),
		snap.HydrationErrors)
	fmt.Printf("Sentiment:   %s\n", snap.SentimentResult)
	posted := "yes"
	if snap.PostingSkipped != 0 {
		posted = "no"
	}
	fmt.Printf("Posted:      %s\n\n", posted)

	if len(events) > 0 {
		fmt.Println("--- Recent Events ---")
		for _, e := range events {
			fmt.Printf("%s  %-20s  %s\n",
				formatTime(e.EventTime),
				e.EventType,
				e.Details)
		}
		fmt.Println()
	}

	// Fetch posting activity
	postingBody, _ := apiGet("/stats/posting")
	var posting PostingActivity
	if postingBody != nil {
		json.Unmarshal(postingBody, &posting)
	}

	fmt.Println("--- Recent Posts ---")
	printPostingEntry("Sentiment", posting.SentimentSummary)
	printPostingEntry("Yearly", posting.YearlyChart)
	printPostingEntry("Daily Quote", posting.DailyQuote)
	printPostingEntry("Topics", posting.TrendingTopics)
}

func cmdLatest() {
	body, err := apiGet("/stats/latest")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if *jsonOut {
		fmt.Println(string(body))
		return
	}

	var snap Snapshot
	if err := json.Unmarshal(body, &snap); err != nil {
		fmt.Fprintf(os.Stderr, "Parse error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("ID:                        %d\n", snap.ID)
	fmt.Printf("Snapshot Time:             %s\n", formatTime(snap.SnapshotTime))
	fmt.Printf("Active Endpoint:           %s\n", snap.ActiveEndpoint)
	fmt.Printf("Endpoint Rotations:        %d\n", snap.EndpointRotations)
	fmt.Printf("Reconnect Count:           %d\n", snap.ReconnectCount)
	fmt.Printf("Connection Uptime:         %s\n", formatDuration(snap.ConnectionUptimeSeconds))
	fmt.Printf("Events Received:           %d\n", snap.EventsReceived)
	fmt.Printf("Posts Processed:           %d\n", snap.PostsProcessed)
	fmt.Printf("Events Skipped:            %d\n", snap.EventsSkipped)
	fmt.Printf("Consumer Errors:           %d\n", snap.ConsumerErrors)
	fmt.Printf("Total Firehose Posts:      %d\n", snap.TotalFirehosePosts)
	fmt.Printf("English Posts Stored:      %d\n", snap.EnglishPostsStored)
	fmt.Printf("Root Posts:                %d\n", snap.RootPosts)
	fmt.Printf("Reply Posts:               %d\n", snap.ReplyPosts)
	fmt.Printf("Posts Per Minute Avg:      %.2f\n", snap.PostsPerMinuteAvg)
	fmt.Printf("Analysis Ran:              %t\n", snap.AnalysisRan != 0)
	fmt.Printf("Posts Considered:          %d\n", snap.PostsConsidered)
	fmt.Printf("Posts Hydrated:            %d\n", snap.PostsHydrated)
	fmt.Printf("Hydration Errors:          %d\n", snap.HydrationErrors)
	fmt.Printf("Sentiment Result:          %s\n", snap.SentimentResult)
	fmt.Printf("Posting Skipped:           %t\n", snap.PostingSkipped != 0)
}

func cmdHistory() {
	body, err := apiGet(fmt.Sprintf("/stats/history?hours=%d&limit=%d", *hours, *limit))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if *jsonOut {
		fmt.Println(string(body))
		return
	}

	var snaps []Snapshot
	if err := json.Unmarshal(body, &snaps); err != nil {
		fmt.Fprintf(os.Stderr, "Parse error: %v\n", err)
		os.Exit(1)
	}

	if len(snaps) == 0 {
		fmt.Println("No snapshots found")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TIME\tENDPOINT\tFIREHOSE\tENGLISH\tROOT\tREPLY\tPPM\tERRORS")

	for _, s := range snaps {
		fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%d\t%d\t%.1f\t%d\n",
			formatTime(s.SnapshotTime),
			abbreviateEndpoint(s.ActiveEndpoint),
			s.TotalFirehosePosts,
			s.EnglishPostsStored,
			s.RootPosts,
			s.ReplyPosts,
			s.PostsPerMinuteAvg,
			s.ConsumerErrors)
	}
	w.Flush()
}

func cmdEvents() {
	path := fmt.Sprintf("/stats/events?hours=%d&limit=%d", *hours, *limit)
	if *eventType != "" {
		path += "&type=" + *eventType
	}

	body, err := apiGet(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if *jsonOut {
		fmt.Println(string(body))
		return
	}

	var events []Event
	if err := json.Unmarshal(body, &events); err != nil {
		fmt.Fprintf(os.Stderr, "Parse error: %v\n", err)
		os.Exit(1)
	}

	if len(events) == 0 {
		fmt.Println("No events found")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TIME\tTYPE\tDETAILS")

	for _, e := range events {
		fmt.Fprintf(w, "%s\t%s\t%s\n",
			formatTime(e.EventTime),
			e.EventType,
			e.Details)
	}
	w.Flush()
}

func cmdTopics() {
	path := fmt.Sprintf("/stats/topics?hours=%d&limit=%d", *hours, *limit)
	body, err := apiGet(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if *jsonOut {
		fmt.Println(string(body))
		return
	}

	var topics []TopicSnapshot
	if err := json.Unmarshal(body, &topics); err != nil {
		fmt.Fprintf(os.Stderr, "Parse error: %v\n", err)
		os.Exit(1)
	}

	if len(topics) == 0 {
		fmt.Println("No topic snapshots found")
		return
	}

	// Group topics by snapshot_time
	grouped := make(map[string][]TopicSnapshot)
	var order []string
	for _, t := range topics {
		if _, seen := grouped[t.SnapshotTime]; !seen {
			order = append(order, t.SnapshotTime)
		}
		grouped[t.SnapshotTime] = append(grouped[t.SnapshotTime], t)
	}

	for _, ts := range order {
		fmt.Printf("=== Topics: %s ===\n", formatTime(ts))
		for _, t := range grouped[ts] {
			memeTag := ""
			if t.IsMeme {
				memeTag = " [meme]"
			}
			fmt.Printf("#%d  %s (%s posts)%s\n", t.Rank, t.Label, formatInt(t.PostCount), memeTag)
			if t.Description != "" {
				fmt.Printf("    %q\n", t.Description)
			}
			if t.Justification != "" {
				fmt.Printf("    Reasoning: %s\n", t.Justification)
			}
			if len(t.Keywords) > 0 {
				fmt.Printf("    Keywords: %s\n", strings.Join(t.Keywords, ", "))
			}
			if t.ExemplarHandle != "" {
				fmt.Printf("    Exemplar: @%s\n", t.ExemplarHandle)
			}
			fmt.Println()
		}
	}
}

// Helper functions

func formatTime(t string) string {
	parsed, err := time.Parse("2006-01-02T15:04:05Z", t)
	if err != nil {
		parsed, err = time.Parse("2006-01-02 15:04:05", t)
		if err != nil {
			return t
		}
	}
	return parsed.Format("2006-01-02 15:04")
}

func formatDuration(seconds int) string {
	d := time.Duration(seconds) * time.Second
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

func formatInt(n int) string {
	s := fmt.Sprintf("%d", n)
	if n < 1000 {
		return s
	}
	// Add commas
	var result strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result.WriteRune(',')
		}
		result.WriteRune(c)
	}
	return result.String()
}

func formatAge(timestamp string) string {
	parsed, err := time.Parse("2006-01-02T15:04:05Z", timestamp)
	if err != nil {
		parsed, err = time.Parse("2006-01-02 15:04:05", timestamp)
		if err != nil {
			return timestamp
		}
	}
	d := time.Since(parsed)
	if d < 0 {
		d = 0
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh ago", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm ago", hours, mins)
	}
	return fmt.Sprintf("%dm ago", mins)
}

func printPostingEntry(label string, entry *PostingEntry) {
	if entry == nil {
		fmt.Printf("%-14s (no data)\n", label+":")
		return
	}
	age := formatAge(entry.LastPosted)
	next := ""
	if entry.NextAnticipated != "" {
		next = " (next: " + formatCountdown(entry.NextAnticipated) + ")"
	}
	fmt.Printf("%-14s %-12s  %s%s\n", label+":", age, entry.Summary, next)
}

func formatCountdown(timestamp string) string {
	parsed, err := time.Parse("2006-01-02T15:04:05Z", timestamp)
	if err != nil {
		return timestamp
	}
	d := time.Until(parsed)
	if d <= 0 {
		return "now"
	}
	hours := int(d.Hours())
	mins := int(d.Minutes()) % 60
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

func abbreviateEndpoint(endpoint string) string {
	// Extract region from wss://jetstream2.us-west.bsky.network/subscribe
	parts := strings.Split(endpoint, ".")
	for _, p := range parts {
		if strings.Contains(p, "us-") || strings.Contains(p, "eu-") || strings.Contains(p, "ap-") {
			return p
		}
	}
	return endpoint
}
