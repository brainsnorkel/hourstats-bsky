package jetstream

import (
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
)

// PostHandler is called for each new post event.
type PostHandler func(event *Event, record *PostRecord)

// CursorSaver persists the latest cursor value.
type CursorSaver func(ctx context.Context, cursor int64) error

// CursorLoader retrieves the last saved cursor value (0 = no cursor).
type CursorLoader func(ctx context.Context) (int64, error)

// ConsumerConfig holds configuration for the Jetstream consumer.
type ConsumerConfig struct {
	Endpoint       string
	Collections    []string
	CursorInterval time.Duration
	OnPost         PostHandler
	SaveCursor     CursorSaver
	LoadCursor     CursorLoader
}

func (c *ConsumerConfig) setDefaults() {
	if c.Endpoint == "" {
		c.Endpoint = DefaultEndpoint
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
}

// Stats tracks consumer metrics.
type Stats struct {
	EventsReceived atomic.Int64
	PostsProcessed atomic.Int64
	EventsSkipped  atomic.Int64
	Reconnects     atomic.Int64
	Errors         atomic.Int64
}

// NewConsumer creates a new Jetstream consumer.
func NewConsumer(cfg ConsumerConfig) *Consumer {
	cfg.setDefaults()
	return &Consumer{cfg: cfg}
}

// Run connects to Jetstream and processes events until ctx is cancelled.
// It automatically reconnects with exponential backoff on failures.
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
		err := c.connectAndConsume(ctx)
		if ctx.Err() != nil {
			c.persistCursorNow(context.Background())
			return ctx.Err()
		}

		c.stats.Reconnects.Add(1)
		slog.Warn("connection lost, reconnecting",
			"error", err,
			"backoff", backoff,
			"reconnects", c.stats.Reconnects.Load(),
		)

		select {
		case <-ctx.Done():
			c.persistCursorNow(context.Background())
			return ctx.Err()
		case <-time.After(backoff):
		}

		backoff = time.Duration(math.Min(float64(backoff)*2, float64(maxBackoff)))
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

func (c *Consumer) buildURL() string {
	u, _ := url.Parse(c.cfg.Endpoint)
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
