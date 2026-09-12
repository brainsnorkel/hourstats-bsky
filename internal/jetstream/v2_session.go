package jetstream

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
)

// extraCollectionLogInterval is how often measurement mode reports the volume
// of the collections it is only counting.
const extraCollectionLogInterval = time.Minute

// maxConsecutiveSeqDrops is how many frames in a row the seq dedup may reject
// before the floor itself is treated as wrong. The floor is reset on every
// dial, so it should only ever reject a frame this connection already
// delivered; thousands in a row means it is wrong for this stream and nothing
// will ever clear it.
const maxConsecutiveSeqDrops = 10000

// minTimestampCursorV2 is the lexicon's cursor threshold: a value at or above
// it is read as unix microseconds and translated to the first seq the instance
// witnessed at or after that instant; below it the value is a sequence number.
// A rewind that would take a cursor under the threshold is dropped rather than
// sent, since an instance would read it as a seq from its own space.
const minTimestampCursorV2 = 1_000_000_000_000_000

// The decompression buffer is reused for the life of the connection, so a
// single outsized frame would otherwise pin its capacity until the socket
// drops. Past maxRetainedDecBuf it is replaced by a fresh small one.
const (
	maxRetainedDecBuf = 1 << 20
	initialDecBuf     = 64 << 10
)

// resumeFromStoredCursorV2 loads the persisted (seq, time) pair and resumes
// from the time. v2 seq numbers are per instance -- two connections to the
// same hostname at the same moment can be tens of millions apart -- so the
// witnessed time is the only value that means the same thing to whichever
// instance answers the next dial. The seq is loaded for the log line and for
// nothing else.
func (c *Consumer) resumeFromStoredCursorV2(ctx context.Context) {
	if c.cfg.LoadCursorV2 == nil {
		return
	}
	seq, timeUS, err := c.cfg.LoadCursorV2(ctx)
	if err != nil {
		slog.Warn("failed to load cursor, starting from live tip", "error", err)
		return
	}
	if timeUS <= 0 {
		return
	}
	start, age, discarded := resolveStartCursor(timeUS, c.cfg.MaxCursorAge, time.Now())
	if discarded {
		slog.Warn("persisted cursor too old, starting from live tip",
			"seq", seq,
			"cursor_time", formatCursorTime(timeUS),
			"cursor_age", age.Round(time.Second),
			"max_cursor_age", c.cfg.MaxCursorAge,
		)
		return
	}
	c.cursor.Store(start)
	slog.Info("resuming from cursor",
		"seq", seq,
		"cursor_time", formatCursorTime(start),
		"cursor_age", age.Round(time.Second),
	)
}

// dialCursorV2 is the cursor value the next dial sends: the last witnessed
// event time rewound by CursorRewind, or 0 for the live tip. It is always a
// unix-microsecond timestamp, never a seq.
func (c *Consumer) dialCursorV2() int64 {
	timeUS := c.cursor.Load()
	if timeUS <= 0 {
		return 0
	}
	if c.cfg.CursorRewind > 0 {
		timeUS -= c.cfg.CursorRewind.Microseconds()
	}
	if timeUS < minTimestampCursorV2 {
		return 0
	}
	return timeUS
}

// formatCursorTime renders a unix-microsecond cursor for a log line; "" means
// no cursor.
func formatCursorTime(timeUS int64) string {
	if timeUS <= 0 {
		return ""
	}
	return time.UnixMicro(timeUS).UTC().Format(time.RFC3339)
}

func (c *Consumer) buildURLV2() string {
	u, _ := url.Parse(c.ActiveEndpoint())
	q := u.Query()
	for _, col := range c.cfg.Collections {
		q.Add("collections", col)
	}
	// Measured collections ride the same subscription: the server prunes
	// everything else, so their volume is observable without a second socket.
	for _, col := range c.cfg.ExtraCollections {
		q.Add("collections", col)
	}
	for _, kind := range v2Kinds {
		q.Add("kinds", kind)
	}
	// Measured kinds ride the same subscription as the measured collections:
	// the server sends them, the read loop counts and drops them.
	for _, kind := range c.cfg.ExtraKinds {
		q.Add("kinds", kind)
	}
	// Always a timestamp: a seq belongs to the instance that issued it, and
	// the hostname fronts several. The rewind replays the few seconds that
	// were in flight when the socket dropped; the post upsert and the deletes
	// are idempotent, so the overlap costs nothing but a little double
	// counting.
	if cursor := c.dialCursorV2(); cursor > 0 {
		q.Set("cursor", strconv.FormatInt(cursor, 10))
	}
	if c.decoder != nil {
		q.Set("zstdDictionary", strconv.FormatUint(uint64(c.dictID), 10))
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// ensureDictionary fetches the server's zstd dictionary before the first dial
// and after a rotation. A failed fetch is not fatal: compression is an
// optimisation, so the connection proceeds with plain text frames and the
// next reconnect tries again.
func (c *Consumer) ensureDictionary(ctx context.Context) {
	if c.cfg.DisableCompression || c.dictRejected || c.decoder != nil {
		return
	}
	blob, id, err := fetchDictionary(ctx, c.ActiveEndpoint())
	if err != nil {
		slog.Warn("zstd dictionary unavailable, connecting uncompressed", "error", err)
		return
	}
	dec, err := newZstdDecoder(blob)
	if err != nil {
		slog.Warn("zstd decoder unavailable, connecting uncompressed", "error", err)
		return
	}
	c.dictID = id
	c.decoder = dec
	c.compressed.Store(true)
	slog.Info("jetstream zstd dictionary loaded", "dictionary_id", id, "dictionary_bytes", len(blob))
}

// closeDecoder releases the zstd decoder's worker goroutines and buffers.
func (c *Consumer) closeDecoder() {
	if c.decoder == nil {
		return
	}
	c.decoder.Close()
	c.decoder = nil
	c.dictID = 0
	c.compressed.Store(false)
}

// handleDialRefusal reacts to a v2 pre-upgrade HTTP 400 before the reconnect
// backoff runs. Anything else (including every v1 error) falls through.
func (c *Consumer) handleDialRefusal(ctx context.Context, err error) {
	switch {
	case errors.Is(err, errCursorTooOld):
		// The cursor is below the server's retention floor and will not become
		// valid by retrying, so drop it and take the live tip.
		slog.Warn("jetstream refused the cursor as too old, starting from live tip",
			"cursor_time", formatCursorTime(c.cursor.Load()), "error", err)
		c.seq.Store(0)
		c.cursor.Store(0)
	case errors.Is(err, errUnknownZstdDictionary):
		rejected := c.dictID
		slog.Warn("jetstream refused the zstd dictionary, refetching",
			"dictionary_id", rejected, "error", err)
		c.closeDecoder()
		c.ensureDictionary(ctx)
		if c.dictID == rejected {
			// A refetch that returns the very ID the server just refused
			// (a mixed-version fleet behind the load balancer) would 400-loop.
			// Compression is an optimisation; the tail must keep flowing.
			c.closeDecoder()
			c.dictRejected = true
			slog.Warn("zstd dictionary still refused after refetch, continuing uncompressed",
				"dictionary_id", rejected)
		}
	case errors.Is(err, errInvalidRequest):
		slog.Error("jetstream rejected the subscription parameters", "error", err)
	}
}

func (c *Consumer) connectAndConsumeV2(ctx context.Context) error {
	c.ensureDictionary(ctx)

	// The dedup floor only ever describes the connection that taught it.
	// Carrying a seq across a dial is what stalls ingest when the next
	// instance numbers its events lower, so the floor starts at 0 every time
	// and the replay overlap is absorbed by the idempotent writes downstream.
	c.seq.Store(0)

	dialCursor := c.dialCursorV2()
	cursorKind := "live"
	if dialCursor > 0 {
		cursorKind = "timestamp"
	}
	slog.Info("jetstream v2 dial",
		"cursor_kind", cursorKind,
		"cursor_time", formatCursorTime(dialCursor),
		"rewind", c.cfg.CursorRewind,
	)

	wsURL := c.buildURLV2()
	slog.Info("connecting to jetstream", "url", wsURL, "protocol", ProtocolV2, "compressed", c.decoder != nil)

	// permessage-deflate is deliberately not offered: v2 never negotiates it,
	// so it is dead weight on the handshake. Compression is the dictionary
	// zstd scheme negotiated by the zstdDictionary query param.
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{subscribeSubprotocol}
	dialer.EnableCompression = false

	conn, resp, err := dialer.DialContext(ctx, wsURL, nil)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		if refusal := classifyDialRefusal(resp); refusal != nil {
			return refusal
		}
		return fmt.Errorf("dial: %w", err)
	}
	// RFC 6455 §4.1: a server that selects a token we did not offer is a
	// failed connection. An empty echo is the lexicon default, i.e. identical
	// framing.
	if echoed := conn.Subprotocol(); echoed != "" && echoed != subscribeSubprotocol {
		conn.Close()
		return fmt.Errorf("jetstream: server selected unoffered subprotocol %q", echoed)
	}
	// The read limit is set by startLiveness below, so both protocols inherit
	// the same bound.

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

	// One reusable decompression buffer for the life of the connection.
	var decBuf []byte
	// seqDrops counts frames the dedup floor rejected back to back.
	var seqDrops int

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if err := conn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
			return fmt.Errorf("set read deadline: %w", err)
		}
		msgType, message, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}

		c.stats.EventsReceived.Add(1)
		c.stats.BytesReceived.Add(int64(len(message)))

		switch {
		case c.decoder != nil && msgType == websocket.BinaryMessage:
			// On a dictionary zstd connection every event frame is one zstd
			// frame whose decompressed bytes are the text frame.
			out, derr := c.decoder.DecodeAll(message, decBuf[:0])
			if derr != nil {
				c.stats.Errors.Add(1)
				slog.Debug("failed to decompress frame", "error", derr)
				continue
			}
			decBuf = out
			message = out
			if cap(decBuf) > maxRetainedDecBuf {
				// message still references the big buffer for this iteration;
				// dropping our own reference lets it be collected after it.
				decBuf = make([]byte, 0, initialDecBuf)
			}
		case msgType != websocket.TextMessage:
			continue // stray binary on an uncompressed connection
		}
		c.stats.BytesDecompressed.Add(int64(len(message)))

		// Measurement mode: a frame from a collection we only count never
		// reaches the language pre-filter or the JSON parser.
		if nsid := c.matchExtraCollection(message); nsid != "" {
			c.countExtraCollection(nsid, len(message))
			continue
		}

		// Likewise for a measured kind: an identity or sync frame is counted
		// by its payload $type and dropped without being decoded.
		if kind := c.matchExtraKind(message); kind != "" {
			c.countExtraKind(kind, len(message))
			continue
		}

		// The operator denylist, applied before the language pre-filter so a
		// denied repo's creates are neither parsed nor counted under their
		// language. Only creates are denied; its deletes still fall through and
		// still remove what it already had in the buffer.
		if did := deniedCreateDID(message); did != "" {
			c.countDeniedCreate(did)
			continue
		}

		// The same bytes-level pre-filter as v1: drop frames that are clearly
		// feed.post creates with no English language tag before paying for
		// json.Unmarshal.
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

		event, info, derr := decodeV2Frame(message)
		if info != nil {
			slog.Info("jetstream info frame", "name", info.Name, "message", info.Message)
			continue
		}
		if errors.Is(derr, errSkipFrame) {
			c.stats.EventsSkipped.Add(1)
			continue
		}
		var streamErr *v2StreamError
		if errors.As(derr, &streamErr) {
			// The server closes immediately after an error frame; the caller's
			// reconnect loop handles it.
			return streamErr
		}
		if derr != nil {
			c.stats.Errors.Add(1)
			slog.Debug("failed to parse event", "error", derr)
			continue
		}

		// Within one connection seqs ascend, so a frame at or below the last
		// one dispatched is a re-delivery. The reconnect overlap the timestamp
		// cursor replays is not caught here -- the floor was reset at the dial
		// -- it is absorbed by the idempotent upsert and delete downstream.
		if event.Seq <= c.seq.Load() {
			seqDrops++
			if seqDrops < maxConsecutiveSeqDrops {
				continue
			}
			// The floor cannot be reached from this stream. Take this frame
			// as the new tip rather than dropping the rest of the session.
			slog.Error("jetstream seq floor rejected every recent frame, resetting to the live tip",
				"dropped", seqDrops, "floor", c.seq.Load(), "seq", event.Seq)
			c.seq.Store(0)
			c.cursor.Store(0)
		}
		seqDrops = 0
		c.seq.Store(event.Seq)
		c.cursor.Store(event.TimeUS)
		c.dispatch(event)
	}
}

// matchExtraCollection reports which measured collection the raw frame belongs
// to, or "" when it belongs to none. It is a byte scan for the exact
// `"collection":"<nsid>"` token the v2 commit JSON carries, so a measured
// frame costs no parsing at all.
func (c *Consumer) matchExtraCollection(data []byte) string {
	for i, needle := range c.needles {
		if bytes.Contains(data, needle) {
			return c.cfg.ExtraCollections[i]
		}
	}
	return ""
}

func (c *Consumer) countExtraCollection(nsid string, frameBytes int) {
	c.extraMu.Lock()
	c.extraCounts[nsid]++
	c.extraBytes[nsid] += int64(frameBytes)
	c.extraMu.Unlock()
}

// matchExtraKind reports which measured kind the raw frame belongs to, or ""
// when it belongs to none. Like matchExtraCollection it is a byte scan, here
// for the payload's `"$type":"<nsid>#<kind>"` token: a post whose text quoted
// that token verbatim would be miscounted, which is an acceptable trade for a
// staging diagnostic that never parses the frame.
func (c *Consumer) matchExtraKind(data []byte) string {
	for i, needle := range c.kindNeedles {
		if bytes.Contains(data, needle) {
			return c.cfg.ExtraKinds[i]
		}
	}
	return ""
}

func (c *Consumer) countExtraKind(kind string, frameBytes int) {
	c.extraMu.Lock()
	c.kindCounts[kind]++
	c.kindBytes[kind] += int64(frameBytes)
	c.extraMu.Unlock()
}

// normalizeExtraKinds keeps the measurable v2 kinds and drops the rest:
// commit and account are already on every subscription, and an unrecognised
// value would be rejected by the server as an invalid request.
func normalizeExtraKinds(kinds []string) []string {
	var out []string
	for _, kind := range kinds {
		switch kind {
		case "identity", "sync":
			out = append(out, kind)
		case "commit", "account":
			slog.Info("jetstream extra kind already subscribed, ignoring", "kind", kind)
		default:
			slog.Warn("unknown jetstream extra kind, ignoring", "kind", kind)
		}
	}
	return out
}

// extraVolumeLogLoop reports the volume of the measured collections and kinds
// once a minute, as counts and bytes since the previous line.
func (c *Consumer) extraVolumeLogLoop(ctx context.Context) {
	ticker := time.NewTicker(extraCollectionLogInterval)
	defer ticker.Stop()

	prevCounts := make(map[string]int64, len(c.cfg.ExtraCollections))
	prevBytes := make(map[string]int64, len(c.cfg.ExtraCollections))
	prevKindCounts := make(map[string]int64, len(c.cfg.ExtraKinds))
	prevKindBytes := make(map[string]int64, len(c.cfg.ExtraKinds))

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			attrs := make([]any, 0, len(c.cfg.ExtraCollections)*4)
			kindAttrs := make([]any, 0, len(c.cfg.ExtraKinds)*4)
			c.extraMu.Lock()
			for _, nsid := range c.cfg.ExtraCollections {
				count, seen := c.extraCounts[nsid], c.extraBytes[nsid]
				attrs = append(attrs,
					nsid+".events", count-prevCounts[nsid],
					nsid+".bytes", seen-prevBytes[nsid],
				)
				prevCounts[nsid], prevBytes[nsid] = count, seen
			}
			for _, kind := range c.cfg.ExtraKinds {
				count, seen := c.kindCounts[kind], c.kindBytes[kind]
				kindAttrs = append(kindAttrs,
					kind+".events", count-prevKindCounts[kind],
					kind+".bytes", seen-prevKindBytes[kind],
				)
				prevKindCounts[kind], prevKindBytes[kind] = count, seen
			}
			c.extraMu.Unlock()
			if len(attrs) > 0 {
				slog.Info("jetstream extra collection volume", attrs...)
			}
			if len(kindAttrs) > 0 {
				slog.Info("jetstream extra kind volume", kindAttrs...)
			}
		}
	}
}
