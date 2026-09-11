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
	"github.com/klauspost/compress/zstd"
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

	// DefaultMaxPostAge bounds how far a post record's createdAt may lag the
	// event's witness time before the create is treated as a repo backfill
	// rather than a live post. Jetstream v2 delivers backfills through the live
	// tail as ordinary creates with fresh seq numbers and a current witness
	// time, so only the record's own clock tells them apart.
	DefaultMaxPostAge = 2 * time.Hour

	// staleCutoffLayout is the second-precision RFC 3339 prefix the bytes-level
	// pre-filter compares lexicographically, so a rejected frame can be aged
	// without parsing its createdAt.
	staleCutoffLayout = "2006-01-02T15:04:05"

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

// EventHandler is called for events that carry no post record — a post delete
// or an account going inactive.
type EventHandler func(event *Event)

// CursorSaver persists the latest cursor value.
type CursorSaver func(ctx context.Context, cursor int64) error

// CursorLoader retrieves the last saved cursor value (0 = no cursor).
type CursorLoader func(ctx context.Context) (int64, error)

// CursorSaverV2 persists the latest v2 cursor: the seq to resume from and the
// event time it was witnessed at, which is what the startup age check reads.
type CursorSaverV2 func(ctx context.Context, seq, timeUS int64) error

// CursorLoaderV2 retrieves the last saved v2 cursor (0, 0 = no cursor).
type CursorLoaderV2 func(ctx context.Context) (seq, timeUS int64, err error)

// ConsumerConfig holds configuration for the Jetstream consumer.
type ConsumerConfig struct {
	// Protocol selects the wire protocol: ProtocolV2 (the default) or
	// ProtocolV1. It also selects the default endpoint list and which pair of
	// cursor callbacks is used.
	Protocol string

	Endpoint       string   // Single endpoint (backwards compat; ignored if Endpoints is set)
	Endpoints      []string // Ordered list of endpoints to rotate through on failure
	Collections    []string
	CursorInterval time.Duration
	OnPost         PostHandler
	SaveCursor     CursorSaver
	LoadCursor     CursorLoader

	// SaveCursorV2/LoadCursorV2 persist the v2 seq cursor. They replace
	// SaveCursor/LoadCursor when Protocol is ProtocolV2, so the two protocols
	// never share a stored row: the values are not interchangeable.
	SaveCursorV2 CursorSaverV2
	LoadCursorV2 CursorLoaderV2

	// DisableCompression turns off dictionary zstd framing on v2. Compression
	// is on by default; a failed dictionary fetch also falls back to plain
	// text frames. It has no effect on v1, which is always uncompressed.
	DisableCompression bool

	// ExtraCollections are measured, not consumed: the v2 subscription asks
	// for them so their volume can be counted, but a frame belonging to one is
	// counted and dropped before any parsing beyond a byte scan for its NSID.
	ExtraCollections []string

	// ExtraKinds are v2 event kinds measured the same way: they are added to
	// the subscription's kinds params on top of the commit and account kinds
	// the bot consumes, and a frame of one of them is counted and dropped
	// after a byte scan for its payload $type. Only "identity" and "sync" are
	// meaningful; "commit" and "account" are already requested and ignored
	// here, and anything else is dropped with a warning. Empty (the default)
	// leaves the subscription exactly as it was.
	ExtraKinds []string

	// OnDelete is called for each post delete commit, so the caller can drop
	// the post before it is scored. The event carries no record.
	OnDelete EventHandler

	// OnAccountInactive is called when an account stops being served
	// (deactivated, deleted, suspended, takendown), so the caller can purge
	// that author's buffered posts.
	OnAccountInactive EventHandler

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

	// MaxPostAge drops a post create whose record createdAt lags the event's
	// witness time by more than this. Zero selects DefaultMaxPostAge; a
	// negative value disables the check.
	MaxPostAge time.Duration

	// OnStale is called, instead of OnPost, for each post create the MaxPostAge
	// guard drops, with how far the record lagged its witness time.
	OnStale func(event *Event, record *PostRecord, age time.Duration)

	// StaleSamplePerHour turns on the stale-create diagnostic: up to this many
	// sampled "stale create sample" lines an hour, plus an hourly summary of
	// the DIDs behind the drops. 0 (the default) leaves it off, which is what
	// production runs; it is meant for a staging soak.
	StaleSamplePerHour int
}

func (c *ConsumerConfig) setDefaults() {
	if c.Protocol != ProtocolV1 {
		c.Protocol = ProtocolV2
	}
	if len(c.Endpoints) == 0 {
		switch {
		case c.Endpoint != "":
			c.Endpoints = []string{c.Endpoint}
		case c.Protocol == ProtocolV2:
			c.Endpoints = AllEndpointsV2
		default:
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
	if c.MaxPostAge == 0 {
		c.MaxPostAge = DefaultMaxPostAge
	}
	c.ExtraKinds = normalizeExtraKinds(c.ExtraKinds)
}

// Consumer connects to a Jetstream WebSocket endpoint and processes post events.
type Consumer struct {
	cfg    ConsumerConfig
	cursor atomic.Int64
	mu     sync.Mutex
	conn   *websocket.Conn
	stats  Stats

	// seq is the v2 cursor: the highest seq delivered, used both as the
	// resume point and as the dedup floor for the inclusive replay. 0 on v1.
	seq atomic.Int64

	// Dictionary zstd state (v2 only), owned by the Run goroutine; compressed
	// mirrors "decoder != nil" for GetStatsReport. dictRejected latches when
	// the server keeps refusing the dictionary, degrading this run to an
	// uncompressed tail rather than looping on a 400.
	dictID       uint32
	decoder      *zstd.Decoder
	dictRejected bool
	compressed   atomic.Bool

	// Measurement mode: per-collection counters for cfg.ExtraCollections and
	// per-kind counters for cfg.ExtraKinds, all guarded by extraMu. needles
	// are the precomputed `"collection":"<nsid>"` byte patterns and
	// kindNeedles the `"$type":"<nsid>#<kind>"` ones. kindSummaryBase holds
	// the kind counts as of the last stale hourly summary, so that line can
	// report the hour rather than the whole run.
	extraMu         sync.Mutex
	extraCounts     map[string]int64
	extraBytes      map[string]int64
	needles         [][]byte
	kindCounts      map[string]int64
	kindBytes       map[string]int64
	kindNeedles     [][]byte
	kindSummaryBase map[string]int64

	// diag is the stale-create diagnostic, nil unless StaleSamplePerHour > 0.
	diag *staleDiag

	// Stale-frame cutoff, owned by the read loop: the RFC 3339 second prefix a
	// rejected frame's createdAt must reach to count as live, recomputed at
	// most once a second rather than per frame.
	staleCutoff   string
	staleCutoffAt time.Time

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
	PostsDeleted            atomic.Int64
	AccountsInactive        atomic.Int64

	// PostsStale counts post creates dropped by the MaxPostAge guard: repo
	// backfill delivered through the live tail. It counts both the creates
	// dropped after parsing and the ones the language pre-filter had already
	// rejected, which is why they no longer reach OnEarlyReject.
	PostsStale atomic.Int64

	// BytesReceived counts bytes as they arrive on the wire (compressed, on a
	// dictionary zstd connection); BytesDecompressed counts the JSON the
	// decoder produced. On an uncompressed connection the two are equal.
	BytesReceived     atomic.Int64
	BytesDecompressed atomic.Int64
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
	PostsDeleted            int64
	AccountsInactive        int64
	PostsStale              int64

	Protocol          string
	Compressed        bool
	BytesReceived     int64
	BytesDecompressed int64

	// EventsByCollection counts frames for each of ConsumerConfig's
	// ExtraCollections. It is nil when measurement mode is off.
	EventsByCollection map[string]int64

	// EventsByKind counts frames for each of ConsumerConfig's ExtraKinds. It
	// is nil when kind measurement is off.
	EventsByKind map[string]int64
}

// NewConsumer creates a new Jetstream consumer.
func NewConsumer(cfg ConsumerConfig) *Consumer {
	cfg.setDefaults()
	c := &Consumer{cfg: cfg}
	if len(cfg.ExtraCollections) > 0 {
		c.extraCounts = make(map[string]int64, len(cfg.ExtraCollections))
		c.extraBytes = make(map[string]int64, len(cfg.ExtraCollections))
		c.needles = make([][]byte, len(cfg.ExtraCollections))
		for i, nsid := range cfg.ExtraCollections {
			c.needles[i] = []byte(`"collection":"` + nsid + `"`)
		}
	}
	if len(c.cfg.ExtraKinds) > 0 {
		c.kindCounts = make(map[string]int64, len(c.cfg.ExtraKinds))
		c.kindBytes = make(map[string]int64, len(c.cfg.ExtraKinds))
		c.kindSummaryBase = make(map[string]int64, len(c.cfg.ExtraKinds))
		c.kindNeedles = make([][]byte, len(c.cfg.ExtraKinds))
		for i, kind := range c.cfg.ExtraKinds {
			c.kindNeedles[i] = []byte(`"$type":"` + subscribeNSID + "#" + kind + `"`)
		}
	}
	c.diag = newStaleDiag(cfg.StaleSamplePerHour)
	return c
}

// ActiveEndpoint returns the currently active endpoint URL.
func (c *Consumer) ActiveEndpoint() string {
	return c.cfg.Endpoints[c.endpointIdx]
}

// Run connects to Jetstream and processes events until ctx is cancelled.
// It automatically reconnects with exponential backoff on failures and
// rotates to alternative endpoints when repeated drops are detected.
func (c *Consumer) Run(ctx context.Context) error {
	c.resumeFromStoredCursor(ctx)
	defer c.closeDecoder()

	cursorCtx, cursorCancel := context.WithCancel(ctx)
	defer cursorCancel()
	go c.cursorPersistLoop(cursorCtx)
	if len(c.cfg.ExtraCollections) > 0 || len(c.cfg.ExtraKinds) > 0 {
		go c.extraVolumeLogLoop(cursorCtx)
	}

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

		// A v2 pre-upgrade refusal names a condition the next dial must not
		// repeat: an unusable cursor is dropped, a rotated dictionary is
		// refetched. Both then fall through to the normal backoff.
		c.handleDialRefusal(ctx, err)

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
			if c.cfg.Protocol == ProtocolV2 {
				// Two v2 instances are not guaranteed to share a seq space, so
				// a floor learned from the old endpoint can sit above
				// everything the new one emits — every frame would fail the
				// dedup and ingest would stall with no error anywhere. Take
				// the new endpoint's live tip instead.
				c.seq.Store(0)
				c.cursor.Store(0)
				slog.Warn("jetstream cursor reset for the endpoint rotation, starting from live tip",
					"endpoint", c.ActiveEndpoint())
			}
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
		PostsDeleted:            c.stats.PostsDeleted.Load(),
		AccountsInactive:        c.stats.AccountsInactive.Load(),
		PostsStale:              c.stats.PostsStale.Load(),
		Protocol:                c.cfg.Protocol,
		Compressed:              c.compressed.Load(),
		BytesReceived:           c.stats.BytesReceived.Load(),
		BytesDecompressed:       c.stats.BytesDecompressed.Load(),
		EventsByCollection:      c.eventsByCollection(),
		EventsByKind:            c.eventsByKind(),
	}
}

// eventsByCollection copies the measurement-mode counters under the mutex, so
// the caller never shares the map the read loop keeps writing to. It returns
// nil when measurement mode is off.
func (c *Consumer) eventsByCollection() map[string]int64 {
	if len(c.cfg.ExtraCollections) == 0 {
		return nil
	}
	c.extraMu.Lock()
	defer c.extraMu.Unlock()
	out := make(map[string]int64, len(c.extraCounts))
	for k, v := range c.extraCounts {
		out[k] = v
	}
	return out
}

// eventsByKind copies the per-kind counters under the same mutex, and returns
// nil when kind measurement is off.
func (c *Consumer) eventsByKind() map[string]int64 {
	if len(c.cfg.ExtraKinds) == 0 {
		return nil
	}
	c.extraMu.Lock()
	defer c.extraMu.Unlock()
	out := make(map[string]int64, len(c.kindCounts))
	for k, v := range c.kindCounts {
		out[k] = v
	}
	return out
}

// kindCountsSinceSummary returns each measured kind's frame count since the
// previous stale hourly summary and moves the baseline forward, so the two
// diagnostics describe the same hour. It returns nil when kind measurement is
// off.
func (c *Consumer) kindCountsSinceSummary() map[string]int64 {
	if len(c.cfg.ExtraKinds) == 0 {
		return nil
	}
	c.extraMu.Lock()
	defer c.extraMu.Unlock()
	out := make(map[string]int64, len(c.cfg.ExtraKinds))
	for _, kind := range c.cfg.ExtraKinds {
		count := c.kindCounts[kind]
		out[kind] = count - c.kindSummaryBase[kind]
		c.kindSummaryBase[kind] = count
	}
	return out
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
	if c.cfg.Protocol == ProtocolV2 {
		return c.buildURLV2()
	}
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
	if c.cfg.Protocol == ProtocolV2 {
		return c.connectAndConsumeV2(ctx)
	}
	return c.connectAndConsumeV1(ctx)
}

func (c *Consumer) connectAndConsumeV1(ctx context.Context) error {
	wsURL := c.buildURL()
	slog.Info("connecting to jetstream", "url", wsURL, "protocol", ProtocolV1)

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

	stopPings, err := startLiveness(ctx, conn)
	if err != nil {
		return err
	}
	defer stopPings()

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
			// A rejected backfill frame must not reach OnEarlyReject either,
			// or the firehose and per-language totals carry it instead.
			if c.rejectedFrameIsStale(message) {
				c.stats.PostsStale.Add(1)
				c.recordStaleFrame(message, firstLang)
				continue
			}
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
		c.dispatch(&event)
	}
}

// startLiveness bounds every read by readTimeout, pushing the deadline out on
// each frame and each pong, and keeps the peer proving liveness with periodic
// pings. A peer that stops sending — including a silently black-holed TCP
// connection — therefore surfaces as a read error within readTimeout instead
// of hanging until the kernel keepalive. The returned func stops the pinger
// and must be called before the connection is closed.
func startLiveness(ctx context.Context, conn *websocket.Conn) (func(), error) {
	conn.SetCloseHandler(func(code int, text string) error {
		return nil
	})
	if err := conn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
		return nil, fmt.Errorf("set read deadline: %w", err)
	}
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(readTimeout))
	})

	// WriteControl is safe to call concurrently with the read loop.
	pingStop := make(chan struct{})
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

	var once sync.Once
	return func() { once.Do(func() { close(pingStop) }) }, nil
}

// dispatch routes one decoded event to the caller's handlers. Both wire
// protocols normalise into Event, so this is the single place that decides
// what a post create, a post delete and an account deactivation mean.
func (c *Consumer) dispatch(event *Event) {
	switch {
	case event.IsPostCreate():
		// handled below
	case event.IsPostDelete():
		if c.cfg.OnDelete != nil {
			c.cfg.OnDelete(event)
		}
		c.stats.PostsDeleted.Add(1)
		return
	case event.IsAccountInactive():
		if c.cfg.OnAccountInactive != nil {
			c.cfg.OnAccountInactive(event)
		}
		c.stats.AccountsInactive.Add(1)
		return
	default:
		c.stats.EventsSkipped.Add(1)
		return
	}

	record := event.ParsePostRecord()
	if record == nil {
		c.stats.Errors.Add(1)
		return
	}

	if age, stale := c.postAge(event, record); stale {
		c.stats.PostsStale.Add(1)
		c.recordStale(event, record, age)
		if c.cfg.OnStale != nil {
			c.cfg.OnStale(event, record, age)
		}
		return
	}

	if c.cfg.OnPost != nil {
		c.cfg.OnPost(event, record)
	}
	c.stats.PostsProcessed.Add(1)
}

// resumeFromStoredCursor loads the persisted cursor for the active protocol
// and applies the staleness gate: a cursor older than MaxCursorAge is dropped
// in favour of the live tail, since replaying many hours of backlog arrives at
// wire speed and overruns the downstream write buffer.
func (c *Consumer) resumeFromStoredCursor(ctx context.Context) {
	if c.cfg.Protocol == ProtocolV2 {
		c.resumeFromStoredCursorV2(ctx)
		return
	}

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
	if c.cfg.Protocol == ProtocolV2 {
		seq, timeUS := c.seq.Load(), c.cursor.Load()
		if c.cfg.SaveCursorV2 == nil || seq == 0 {
			return
		}
		if err := c.cfg.SaveCursorV2(ctx, seq, timeUS); err != nil {
			slog.Warn("failed to persist cursor", "error", err, "seq", seq)
		}
		return
	}

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
	// The exact operation token, not a bare `"create"`: a delete commit or an
	// account event must never be mistaken for a create and dropped here,
	// since those frames drive the delete/purge path in connectAndConsume.
	if !bytes.Contains(data, []byte(`"operation":"create"`)) {
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

// parsePostCreatedAt parses a record's createdAt, accepting the same two
// layouts cmd/hourstats normalises with. It reports false for anything it
// cannot read, which the callers treat as fresh.
func parsePostCreatedAt(raw string) (time.Time, bool) {
	if raw == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, raw)
	}
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// postAge returns how far the record's createdAt lags the event's witness
// time, and whether that exceeds MaxPostAge. An unparseable createdAt is kept,
// as it always was. So is a future-dated one: its age is negative, which is
// client clock skew rather than a backfill.
func (c *Consumer) postAge(event *Event, record *PostRecord) (time.Duration, bool) {
	if c.cfg.MaxPostAge <= 0 || event.TimeUS == 0 {
		return 0, false
	}
	created, ok := parsePostCreatedAt(record.CreatedAt)
	if !ok {
		return 0, false
	}
	age := time.UnixMicro(event.TimeUS).Sub(created)
	return age, age > c.cfg.MaxPostAge
}

// rejectedFrameIsStale reports whether a frame the language pre-filter dropped
// is repo backfill rather than a live post, using only a byte scan: the frame
// was rejected precisely so it would never be parsed.
func (c *Consumer) rejectedFrameIsStale(data []byte) bool {
	cutoff := c.frameStaleCutoff()
	if cutoff == "" {
		return false
	}
	return createdAtBefore(scanFrameCreatedAt(data), cutoff)
}

// frameStaleCutoff returns the second-precision cutoff a frame's createdAt is
// compared against, or "" when the age check is off. The witness clock is the
// last event time seen on this stream, falling back to wall clock before the
// first one. Formatting it per frame would cost more than the scan it guards,
// so it is recomputed at most once a second; the read loop owns both fields.
func (c *Consumer) frameStaleCutoff() string {
	if c.cfg.MaxPostAge <= 0 {
		return ""
	}
	witness := time.Now()
	if timeUS := c.cursor.Load(); timeUS > 0 {
		witness = time.UnixMicro(timeUS)
	}
	// A negative elapsed means the witness clock moved backwards (a cursor
	// reset), so the cached value no longer describes this stream.
	if elapsed := witness.Sub(c.staleCutoffAt); !c.staleCutoffAt.IsZero() && elapsed >= 0 && elapsed < time.Second {
		return c.staleCutoff
	}
	c.staleCutoffAt = witness
	c.staleCutoff = witness.Add(-c.cfg.MaxPostAge).UTC().Format(staleCutoffLayout)
	return c.staleCutoff
}

// createdAtBefore compares a raw createdAt with a cutoff as bytes. Only the
// canonical `YYYY-MM-DDTHH:MM:SS...Z` form is judged: a value with a numeric
// offset or an odd shape is not comparable this way and is reported as fresh,
// leaving it to the existing path.
func createdAtBefore(createdAt, cutoff string) bool {
	if len(createdAt) < len(staleCutoffLayout)+1 || createdAt[len(createdAt)-1] != 'Z' {
		return false
	}
	for i, want := range []byte(staleCutoffLayout) {
		switch want {
		case '-', 'T', ':':
			if createdAt[i] != want {
				return false
			}
		default: // a layout digit
			if createdAt[i] < '0' || createdAt[i] > '9' {
				return false
			}
		}
	}
	return createdAt[:len(staleCutoffLayout)] < cutoff
}

// scanFrameCreatedAt returns the first `"createdAt":"..."` string value in the
// frame, which in a post commit is the record's own timestamp. It returns ""
// when the key is absent or the value runs past the bound.
func scanFrameCreatedAt(data []byte) string {
	const key = `"createdAt":"`
	idx := bytes.Index(data, []byte(key))
	if idx < 0 {
		return ""
	}
	pos := idx + len(key)
	// Long enough for RFC 3339 with a nanosecond fraction and an offset.
	limit := pos + 64
	if limit > len(data) {
		limit = len(data)
	}
	for i := pos; i < limit; i++ {
		if data[i] == '"' {
			return string(data[pos:i])
		}
	}
	return ""
}
