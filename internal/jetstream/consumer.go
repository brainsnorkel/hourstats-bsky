package jetstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
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

	// DefaultCursorRewind is subtracted from the cursor on every (re)connect.
	// Jetstream recommends rewinding a few seconds so events in flight at the
	// moment the connection dropped are replayed rather than lost.
	DefaultCursorRewind = 5 * time.Second

	// DefaultMaxCursorAge bounds how stale a persisted cursor may be before it
	// is discarded in favour of the live tail. Replaying many hours of backlog
	// arrives at wire speed and overruns the downstream write buffer.
	DefaultMaxCursorAge = 6 * time.Hour

	maxBackoff     = 30 * time.Second
	initialBackoff = 1 * time.Second

	// backoffJitter is the fraction by which each backoff is randomly scaled
	// (+/-), so that many instances reconnecting at once spread their retries.
	backoffJitter = 0.2

	// Liveness: a black-holed TCP connection never returns an error from
	// ReadMessage, so we bound every read and keep the peer proving liveness
	// with periodic pings. readTimeout must exceed pingInterval comfortably.
	readTimeout      = 60 * time.Second
	pingInterval     = 30 * time.Second
	pingWriteTimeout = 10 * time.Second

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

	// OnEarlyReject is called, with the frame's first language tag, for each
	// post create the bytes-level pre-filter drops before parsing. It lets the
	// caller keep counting those posts in firehose and per-language totals.
	OnEarlyReject func(firstLang string)

	// CursorRewind is subtracted from the cursor on every (re)connect.
	// Zero selects DefaultCursorRewind; a negative value disables rewinding.
	CursorRewind time.Duration

	// MaxCursorAge discards a persisted cursor older than this at startup and
	// begins from the live tail instead. Zero selects DefaultMaxCursorAge; a
	// negative value disables the age check.
	MaxCursorAge time.Duration
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
	if c.CursorRewind == 0 {
		c.CursorRewind = DefaultCursorRewind
	}
	if c.MaxCursorAge == 0 {
		c.MaxCursorAge = DefaultMaxCursorAge
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
	EventsReceived          atomic.Int64
	PostsProcessed          atomic.Int64
	EventsSkipped           atomic.Int64
	Reconnects              atomic.Int64
	Errors                  atomic.Int64
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
	start, age, discarded := resolveStartCursor(cursor, c.cfg.MaxCursorAge, time.Now())
	switch {
	case discarded:
		slog.Warn("persisted cursor too old, starting from live tail",
			"cursor", cursor,
			"cursor_age", age.Round(time.Second),
			"max_cursor_age", c.cfg.MaxCursorAge,
		)
	case start > 0:
		c.cursor.Store(start)
		slog.Info("resuming from cursor", "cursor", start, "cursor_age", age.Round(time.Second))
	}

	cursorCtx, cursorCancel := context.WithCancel(ctx)
	defer cursorCancel()
	go c.cursorPersistLoop(cursorCtx)

	// conn.ReadMessage does not observe ctx, so cancellation would otherwise
	// stall for up to readTimeout. Closing the connection unblocks it at once.
	stopCloser := make(chan struct{})
	defer close(stopCloser)
	go func() {
		select {
		case <-ctx.Done():
			c.ForceReconnect()
		case <-stopCloser:
		}
	}()

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

		wait := jitterBackoff(backoff)
		slog.Warn("connection lost, reconnecting",
			"error", err,
			"backoff", wait.Round(time.Millisecond),
			"reconnects", c.stats.Reconnects.Load(),
			"endpoint", c.ActiveEndpoint(),
			"rotated", rotated,
		)

		select {
		case <-ctx.Done():
			c.persistCursorNow(context.Background())
			return ctx.Err()
		case <-time.After(wait):
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

// resolveStartCursor decides which persisted cursor to resume from. It returns
// the cursor to use (0 = live tail), its age, and whether it was discarded for
// being older than maxAge. A non-positive maxAge disables the age check.
func resolveStartCursor(cursor int64, maxAge time.Duration, now time.Time) (start int64, age time.Duration, discarded bool) {
	if cursor <= 0 {
		return 0, 0, false
	}
	age = now.Sub(time.UnixMicro(cursor))
	if maxAge > 0 && age > maxAge {
		return 0, age, true
	}
	return cursor, age, false
}

// rewindCursor subtracts rewind from a time_us cursor so that a few seconds of
// events are replayed across a reconnect instead of being lost. It never
// returns a value below 1, which would be read as "no cursor".
func rewindCursor(cursor int64, rewind time.Duration) int64 {
	if cursor <= 0 || rewind <= 0 {
		return cursor
	}
	rewound := cursor - rewind.Microseconds()
	if rewound < 1 {
		return 1
	}
	return rewound
}

// jitterBackoff scales d by a random factor within +/-backoffJitter so that
// concurrent reconnects do not synchronise on the same retry instants.
func jitterBackoff(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	factor := 1 - backoffJitter + rand.Float64()*2*backoffJitter
	return time.Duration(float64(d) * factor)
}

// ForceReconnect closes the active WebSocket connection, which unblocks the
// read loop and hands control to the normal reconnect/backoff path. It is safe
// to call from any goroutine and reports whether a connection was closed.
func (c *Consumer) ForceReconnect() bool {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (c *Consumer) buildURL() string {
	u, _ := url.Parse(c.ActiveEndpoint())
	q := u.Query()
	for _, col := range c.cfg.Collections {
		q.Add("wantedCollections", col)
	}
	cursor := rewindCursor(c.cursor.Load(), c.cfg.CursorRewind)
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

	// Liveness. Every read is bounded by readTimeout; the deadline is pushed
	// out on each frame and on each pong. A peer that stops sending — including
	// a silently black-holed TCP connection — therefore surfaces as a read
	// error within readTimeout instead of hanging until the kernel keepalive.
	if err := conn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
		return fmt.Errorf("set read deadline: %w", err)
	}
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(readTimeout))
	})

	// WriteControl is safe to call concurrently with the read loop.
	pingStop := make(chan struct{})
	defer close(pingStop)
	go func() {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-pingStop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(pingWriteTimeout)); err != nil {
					// A routine reconnect, a stall-triggered ForceReconnect or
					// shutdown closes the connection underneath this goroutine.
					// That is not a ping failure and must not be logged as one,
					// nor closed again — the reconnect is already in progress.
					select {
					case <-pingStop:
						return
					case <-ctx.Done():
						return
					default:
					}
					slog.Warn("jetstream ping failed, forcing reconnect", "error", err)
					_ = conn.Close() // unblocks ReadMessage; the caller reconnects
					return
				}
			}
		}
	}()

	slog.Info("connected to jetstream")

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if err := conn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
			return fmt.Errorf("set read deadline: %w", err)
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
		if reject, firstLang := scanFrameLang(message); reject {
			c.stats.EarlyRejectedNonEnglish.Add(1)
			if c.cfg.OnEarlyReject != nil {
				c.cfg.OnEarlyReject(firstLang)
			}
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
	reject, _ := scanFrameLang(data)
	return reject
}

// scanFrameLang is frameIsNonEnglishPost plus the first language tag seen in
// the frame's langs array, so a rejected post can still be attributed to its
// language. firstLang is "" when no tag was read.
func scanFrameLang(data []byte) (reject bool, firstLang string) {
	// Guard 1: must look like a feed.post create.
	if !bytes.Contains(data, []byte(`"app.bsky.feed.post"`)) {
		return false, ""
	}
	if !bytes.Contains(data, []byte(`"create"`)) {
		return false, ""
	}

	// Guard 2: must have a langs field.
	langsIdx := bytes.Index(data, []byte(`"langs":`))
	if langsIdx < 0 {
		return false, ""
	}

	// Advance past `"langs":` (8 bytes) and skip whitespace/array-open.
	pos := langsIdx + 8
	for pos < len(data) && (data[pos] == ' ' || data[pos] == '\t' || data[pos] == '\n' || data[pos] == '\r') {
		pos++
	}
	if pos >= len(data) || data[pos] != '[' {
		// Unexpected structure — keep the frame.
		return false, ""
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
			return true, firstLang
		case '"':
			// Found a string token; read until closing quote (ignoring escapes
			// for this quick scan — lang tags never contain backslashes).
			pos++ // skip opening quote
			start := pos
			for pos < limit && data[pos] != '"' {
				pos++
			}
			tag := data[start:pos]
			if firstLang == "" {
				firstLang = string(tag)
			}
			// Accept "en" exactly or any "en-*" variant.
			if bytes.Equal(tag, []byte("en")) || bytes.HasPrefix(tag, []byte("en-")) {
				return false, firstLang // has English tag — keep frame
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
	return true, firstLang
}
