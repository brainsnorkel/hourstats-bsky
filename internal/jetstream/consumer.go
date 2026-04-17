package jetstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	DefaultEndpoint       = "wss://jetstream2.us-west.bsky.network/subscribe"
	DefaultCollection     = "app.bsky.feed.post"
	DefaultCursorInterval = 10 * time.Second

	maxBackoff     = 30 * time.Second
	initialBackoff = 1 * time.Second

	// Endpoint rotation: if we see this many drops within the window,
	// rotate to the next endpoint.
	rotateAfterDrops  = 3
	rotateDropWindow  = 3 * time.Minute
	healthyResetAfter = 2 * time.Minute
)

// AllEndpoints lists the four public Jetstream instances, us-west first
// since the Fly.io app runs in sjc.
var AllEndpoints = []string{
	"wss://jetstream2.us-west.bsky.network/subscribe",
	"wss://jetstream1.us-west.bsky.network/subscribe",
	"wss://jetstream2.us-east.bsky.network/subscribe",
	"wss://jetstream1.us-east.bsky.network/subscribe",
}

// PostHandler is called for each new post event.
type PostHandler func(event *Event, record *PostRecord)

// CursorSaver persists the latest cursor value.
type CursorSaver func(ctx context.Context, cursor int64) error

// CursorLoader retrieves the last saved cursor value (0 = no cursor).
type CursorLoader func(ctx context.Context) (int64, error)

// ConsumerConfig holds configuration for the Jetstream consumer.
type ConsumerConfig struct {
	Endpoint       string   // Single endpoint (backwards compat; ignored if Endpoints is set)
	Endpoints      []string // Ordered list of endpoints to rotate through on failure
	Collections    []string
	CursorInterval time.Duration
	OnPost         PostHandler
	SaveCursor     CursorSaver
	LoadCursor     CursorLoader
}

func (c *ConsumerConfig) setDefaults() {
	if len(c.Endpoints) == 0 {
		if c.Endpoint != "" {
			c.Endpoints = []string{c.Endpoint}
		} else {
			c.Endpoints = AllEndpoints
		}
	}
	if c.Endpoint == "" {
		c.Endpoint = c.Endpoints[0]
	}
	if len(c.Collections) == 0 {
		c.Collections = []string{DefaultCollection}
	}
	if c.CursorInterval == 0 {
		c.CursorInterval = DefaultCursorInterval
	}
}

// Consumer connects to a Jetstream WebSocket endpoint and processes post events.
type Consumer struct {
	cfg    ConsumerConfig
	cursor atomic.Int64
	mu     sync.Mutex
	conn   *websocket.Conn
	stats  Stats

	// Endpoint rotation state.
	endpointIdx       int          // index into cfg.Endpoints
	endpointRotations atomic.Int64 // count of endpoint rotations
	dropTimes         []time.Time  // timestamps of recent drops
	connectedAt       time.Time    // when current connection was established (protected by mu)
}

// Stats tracks consumer metrics.
type Stats struct {
	EventsReceived        atomic.Int64
	PostsProcessed        atomic.Int64
	EventsSkipped         atomic.Int64
	Reconnects            atomic.Int64
	Errors                atomic.Int64
	EarlyRejectedNonEnglish atomic.Int64
}

// StatsReport is an exported snapshot of consumer statistics.
type StatsReport struct {
	EventsReceived          int64
	PostsProcessed          int64
	EventsSkipped           int64
	Reconnects              int64
	Errors                  int64
	EndpointRotations       int64
	ActiveEndpoint          string
	ConnectionUptime        time.Duration
	EarlyRejectedNonEnglish int64
}

// NewConsumer creates a new Jetstream consumer.
func NewConsumer(cfg ConsumerConfig) *Consumer {
	cfg.setDefaults()
	return &Consumer{cfg: cfg}
}

// ActiveEndpoint returns the currently active endpoint URL.
func (c *Consumer) ActiveEndpoint() string {
	return c.cfg.Endpoints[c.endpointIdx]
}

// Run connects to Jetstream and processes events until ctx is cancelled.
// It automatically reconnects with exponential backoff on failures and
// rotates to alternative endpoints when repeated drops are detected.
func (c *Consumer) Run(ctx context.Context) error {
	cursor, err := c.loadInitialCursor(ctx)
	if err != nil {
		slog.Warn("failed to load cursor, starting from live tail", "error", err)
	}
	if cursor > 0 {
		c.cursor.Store(cursor)
		slog.Info("resuming from cursor", "cursor", cursor)
	}

	cursorCtx, cursorCancel := context.WithCancel(ctx)
	defer cursorCancel()
	go c.cursorPersistLoop(cursorCtx)

	backoff := initialBackoff
	for {
		c.mu.Lock()
		c.connectedAt = time.Now()
		c.mu.Unlock()
		err := c.connectAndConsume(ctx)
		if ctx.Err() != nil {
			c.persistCursorNow(context.Background())
			return ctx.Err()
		}

		c.stats.Reconnects.Add(1)
		now := time.Now()

		// Track this drop and prune old ones outside the window.
		c.dropTimes = append(c.dropTimes, now)
		cutoff := now.Add(-rotateDropWindow)
		pruned := c.dropTimes[:0]
		for _, t := range c.dropTimes {
			if t.After(cutoff) {
				pruned = append(pruned, t)
			}
		}
		c.dropTimes = pruned

		// If we were connected long enough, the endpoint is healthy —
		// don't count earlier drops against it.
		c.mu.Lock()
		connectedDuration := now.Sub(c.connectedAt)
		c.mu.Unlock()
		if connectedDuration >= healthyResetAfter {
			c.dropTimes = c.dropTimes[len(c.dropTimes)-1:] // keep only latest
			backoff = initialBackoff
		}

		rotated := false
		if len(c.dropTimes) >= rotateAfterDrops && len(c.cfg.Endpoints) > 1 {
			prev := c.ActiveEndpoint()
			c.endpointIdx = (c.endpointIdx + 1) % len(c.cfg.Endpoints)
			c.endpointRotations.Add(1)
			c.dropTimes = nil // reset counter for new endpoint
			backoff = initialBackoff
			rotated = true
			slog.Warn("rotating jetstream endpoint due to instability",
				"from", prev,
				"to", c.ActiveEndpoint(),
				"drops_in_window", rotateAfterDrops,
			)
		}

		slog.Warn("connection lost, reconnecting",
			"error", err,
			"backoff", backoff,
			"reconnects", c.stats.Reconnects.Load(),
			"endpoint", c.ActiveEndpoint(),
			"rotated", rotated,
		)

		select {
		case <-ctx.Done():
			c.persistCursorNow(context.Background())
			return ctx.Err()
		case <-time.After(backoff):
		}

		if !rotated {
			backoff = time.Duration(math.Min(float64(backoff)*2, float64(maxBackoff)))
		}
	}
}

// GetStats returns a snapshot of consumer statistics.
func (c *Consumer) GetStats() (events, posts, skipped, reconnects, errors int64) {
	return c.stats.EventsReceived.Load(),
		c.stats.PostsProcessed.Load(),
		c.stats.EventsSkipped.Load(),
		c.stats.Reconnects.Load(),
		c.stats.Errors.Load()
}

// GetStatsReport returns a comprehensive snapshot of consumer statistics.
func (c *Consumer) GetStatsReport() StatsReport {
	c.mu.Lock()
	var uptime time.Duration
	if !c.connectedAt.IsZero() {
		uptime = time.Since(c.connectedAt)
	}
	c.mu.Unlock()

	return StatsReport{
		EventsReceived:          c.stats.EventsReceived.Load(),
		PostsProcessed:          c.stats.PostsProcessed.Load(),
		EventsSkipped:           c.stats.EventsSkipped.Load(),
		Reconnects:              c.stats.Reconnects.Load(),
		Errors:                  c.stats.Errors.Load(),
		EndpointRotations:       c.endpointRotations.Load(),
		ActiveEndpoint:          c.ActiveEndpoint(),
		ConnectionUptime:        uptime,
		EarlyRejectedNonEnglish: c.stats.EarlyRejectedNonEnglish.Load(),
	}
}

// ConnectionUptime returns the duration since the current connection was established.
func (c *Consumer) ConnectionUptime() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connectedAt.IsZero() {
		return 0
	}
	return time.Since(c.connectedAt)
}

func (c *Consumer) buildURL() string {
	u, _ := url.Parse(c.ActiveEndpoint())
	q := u.Query()
	for _, col := range c.cfg.Collections {
		q.Add("wantedCollections", col)
	}
	cursor := c.cursor.Load()
	if cursor > 0 {
		q.Set("cursor", fmt.Sprintf("%d", cursor))
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (c *Consumer) connectAndConsume(ctx context.Context) error {
	wsURL := c.buildURL()
	slog.Info("connecting to jetstream", "url", wsURL)

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	defer func() {
		conn.Close()
		c.mu.Lock()
		c.conn = nil
		c.mu.Unlock()
	}()

	conn.SetCloseHandler(func(code int, text string) error {
		return nil
	})

	slog.Info("connected to jetstream")

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		_, message, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}

		c.stats.EventsReceived.Add(1)

		// Cheap bytes-level pre-filter: drop frames that are clearly feed.post
		// creates with no English language tag, before paying for json.Unmarshal.
		// Non-post events (identity, account, like, etc.) have no "langs" field
		// and are always passed through to the full parse path.
		if frameIsNonEnglishPost(message) {
			c.stats.EarlyRejectedNonEnglish.Add(1)
			continue
		}

		var event Event
		if err := json.Unmarshal(message, &event); err != nil {
			c.stats.Errors.Add(1)
			slog.Debug("failed to parse event", "error", err)
			continue
		}

		c.cursor.Store(event.TimeUS)

		if !event.IsPostCreate() {
			c.stats.EventsSkipped.Add(1)
			continue
		}

		record := event.ParsePostRecord()
		if record == nil {
			c.stats.Errors.Add(1)
			continue
		}

		if c.cfg.OnPost != nil {
			c.cfg.OnPost(&event, record)
		}
		c.stats.PostsProcessed.Add(1)
	}
}

func (c *Consumer) loadInitialCursor(ctx context.Context) (int64, error) {
	if c.cfg.LoadCursor == nil {
		return 0, nil
	}
	return c.cfg.LoadCursor(ctx)
}

func (c *Consumer) cursorPersistLoop(ctx context.Context) {
	ticker := time.NewTicker(c.cfg.CursorInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.persistCursorNow(ctx)
		}
	}
}

func (c *Consumer) persistCursorNow(ctx context.Context) {
	if c.cfg.SaveCursor == nil {
		return
	}
	cursor := c.cursor.Load()
	if cursor == 0 {
		return
	}
	if err := c.cfg.SaveCursor(ctx, cursor); err != nil {
		slog.Warn("failed to persist cursor", "error", err, "cursor", cursor)
	}
}

// frameIsNonEnglishPost returns true when the raw WebSocket frame is
// definitely a feed.post create event that contains no English language tag,
// allowing the caller to skip the full json.Unmarshal.
//
// The check is intentionally CONSERVATIVE: when in doubt it returns false
// (keep the frame) so that English posts are never silently dropped.
// False-positives (a non-English post slips through) are harmless because
// the authoritative isEnglish() filter in cmd/hourstats/jetstream_consumer.go
// remains in place.
//
// Algorithm:
//  1. The frame must look like a feed.post create — we require both
//     `"app.bsky.feed.post"` and `"operation":"create"` to be present.
//     If either is absent the frame is not a post-create and we keep it.
//  2. We look for a `"langs":` key.  If absent the frame has no language
//     field at all — keep it (some clients omit the field entirely; we must
//     not drop those).
//  3. After `"langs":[` we scan forward for the first JSON string token.
//     If that token is `"en"` or starts with `"en-` (e.g. "en-US", "en-GB")
//     we keep the frame.  Otherwise we reject it.
//
// The scan is bounded so it cannot run past the end of the slice.
func frameIsNonEnglishPost(data []byte) bool {
	// Guard 1: must look like a feed.post create.
	if !bytes.Contains(data, []byte(`"app.bsky.feed.post"`)) {
		return false
	}
	if !bytes.Contains(data, []byte(`"create"`)) {
		return false
	}

	// Guard 2: must have a langs field.
	langsIdx := bytes.Index(data, []byte(`"langs":`))
	if langsIdx < 0 {
		return false
	}

	// Advance past `"langs":` (8 bytes) and skip whitespace/array-open.
	pos := langsIdx + 8
	for pos < len(data) && (data[pos] == ' ' || data[pos] == '\t' || data[pos] == '\n' || data[pos] == '\r') {
		pos++
	}
	if pos >= len(data) || data[pos] != '[' {
		// Unexpected structure — keep the frame.
		return false
	}
	pos++ // skip '['

	// Guard 3: scan up to 256 bytes into the array for the first string token.
	limit := pos + 256
	if limit > len(data) {
		limit = len(data)
	}
	for pos < limit {
		switch data[pos] {
		case ']':
			// Empty array or exhausted — no English tag found; reject.
			return true
		case '"':
			// Found a string token; read until closing quote (ignoring escapes
			// for this quick scan — lang tags never contain backslashes).
			pos++ // skip opening quote
			start := pos
			for pos < limit && data[pos] != '"' {
				pos++
			}
			tag := data[start:pos]
			// Accept "en" exactly or any "en-*" variant.
			if bytes.Equal(tag, []byte("en")) || bytes.HasPrefix(tag, []byte("en-")) {
				return false // has English tag — keep frame
			}
			// Non-English tag found; continue scanning (there may be more tags).
			if pos < limit {
				pos++ // skip closing quote
			}
		default:
			pos++
		}
	}

	// Scanned limit without finding an English tag — reject.
	return true
}
