package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/jetstream"
	"github.com/christophergentle/hourstats-bsky/internal/stats"
	"github.com/christophergentle/hourstats-bsky/internal/store"
	"github.com/christophergentle/hourstats-bsky/internal/topics"
)

const (
	// writeSendTimeout bounds how long the firehose callback blocks when the
	// write buffer is full. Blocking briefly applies backpressure all the way
	// to the TCP window instead of silently discarding a burst; posts are only
	// dropped once the flusher has been stuck for this long.
	writeSendTimeout = 2 * time.Second

	// dropWarnWindow rate-limits the "write buffer full" warning. A cold-start
	// backlog replay once produced one WARN line per dropped post (486k lines).
	dropWarnWindow = 5 * time.Second
)

// ---------------------------------------------------------------------------
// Jetstream consumer
// ---------------------------------------------------------------------------

// consumerHandle publishes the currently active consumer so the stall detector
// in main can force a reconnect on it.
type consumerHandle struct {
	mu sync.Mutex
	c  *jetstream.Consumer
}

func (h *consumerHandle) set(c *jetstream.Consumer) {
	h.mu.Lock()
	h.c = c
	h.mu.Unlock()
}

// forceReconnect drops the active connection, reporting whether one was open.
func (h *consumerHandle) forceReconnect() bool {
	h.mu.Lock()
	c := h.c
	h.mu.Unlock()
	if c == nil {
		return false
	}
	return c.ForceReconnect()
}

// dropLimiter collapses a burst of dropped posts into one warning per window,
// carrying the number dropped since the previous warning.
type dropLimiter struct {
	mu       sync.Mutex
	window   time.Duration
	lastWarn time.Time
	pending  int
}

// record registers one drop and returns the number of drops to report now,
// or 0 when the warning is suppressed by the rate limit.
func (d *dropLimiter) record(now time.Time) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pending++
	if !d.lastWarn.IsZero() && now.Sub(d.lastWarn) < d.window {
		return 0
	}
	d.lastWarn = now
	n := d.pending
	d.pending = 0
	return n
}

// sendPost enqueues pw for the write flusher, blocking up to timeout when the
// buffer is full. It reports whether the post was accepted; a false result is
// a genuine drop.
func sendPost(ctx context.Context, writeCh chan<- store.PendingWrite, pw store.PendingWrite, timeout time.Duration) bool {
	// Fast path: avoid allocating a timer on every post.
	select {
	case writeCh <- pw:
		return true
	default:
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case writeCh <- pw:
		return true
	case <-timer.C:
		return false
	case <-ctx.Done():
		return false
	}
}

func runJetstream(ctx context.Context, db *store.Store, trendingEnabled bool, collector *stats.Collector, writeCh chan<- store.PendingWrite, handle *consumerHandle) {
	drops := &dropLimiter{window: dropWarnWindow}

	cfg := jetstream.ConsumerConfig{
		// Posts the bytes-level pre-filter drops never reach OnPost, so they
		// are counted here; without this the firehose total is only English
		// plus untagged posts.
		OnEarlyReject: func(firstLang string) {
			collector.IncrementFirehosePost()
			collector.IncrementLanguage(primaryLang(firstLang))
		},
		OnPost: func(evt *jetstream.Event, rec *jetstream.PostRecord) {
			collector.IncrementFirehosePost()
			collector.IncrementLanguage(postLang(rec.Langs))

			if strings.TrimSpace(rec.Text) == "" {
				return
			}
			if !isEnglish(rec.Langs) {
				return
			}
			cid := ""
			if evt.Commit != nil {
				cid = evt.Commit.CID
			}
			createdAt := normalizeTimestamp(rec.CreatedAt)
			post := store.Post{
				URI:       evt.PostURI(),
				CID:       cid,
				Text:      rec.Text,
				AuthorDID: evt.DID,
				CreatedAt: createdAt,
				IsReply:   rec.Reply != nil,
			}

			pw := store.PendingWrite{
				Post:      post,
				CreatedAt: createdAt,
			}
			if trendingEnabled && rec.Reply == nil && !rec.HasAdultContent() {
				hashtagCount := strings.Count(rec.Text, "#")
				if hashtagCount <= 1 && !topics.IsRepetitive(rec.Text) {
					toks := topics.Tokenize(rec.Text)
					if len(toks) > 0 {
						tokJSON, _ := json.Marshal(toks)
						pw.TokensJSON = string(tokJSON)
					}
				}
			}

			// Count the post as stored only once it is actually queued, so
			// english_posts_stored no longer over-counts drops.
			if sendPost(ctx, writeCh, pw, writeSendTimeout) {
				collector.IncrementEnglishPost(rec.Reply != nil)
				return
			}
			collector.IncrementDroppedPosts(1)
			if n := drops.record(time.Now()); n > 0 {
				slog.Warn("write buffer full, dropping posts",
					"dropped_since_last_warning", n,
					"buffer_len", len(writeCh),
					"uri", post.URI,
				)
			}
		},
		SaveCursor: func(saveCtx context.Context, cursor int64) error {
			return db.SaveCursor(saveCtx, cursor)
		},
		LoadCursor: func(loadCtx context.Context) (int64, error) {
			return db.GetCursor(loadCtx)
		},
		CursorRewind: time.Duration(envInt("JETSTREAM_CURSOR_REWIND_SECONDS", 5)) * time.Second,
		MaxCursorAge: time.Duration(envInt("JETSTREAM_MAX_CURSOR_AGE_MINUTES", 360)) * time.Minute,
	}

	for {
		consumer := jetstream.NewConsumer(cfg)
		collector.SetConsumer(consumer)
		handle.set(consumer)
		err := consumer.Run(ctx)
		collector.SetConsumer(nil)
		handle.set(nil)
		if ctx.Err() != nil {
			return
		}

		// consumer.Run only returns on fatal errors not handled by its
		// internal reconnect loop; restart immediately.
		_ = collector.LogEvent(ctx, "consumer_restart", fmt.Sprintf("unexpected exit: %v", err))
		slog.Error("jetstream consumer exited unexpectedly, restarting immediately", "error", err)
	}
}

// undeterminedLang is the bucket for posts with no usable language tag
// (BCP-47 "und").
const undeterminedLang = "und"

// primaryLang reduces a BCP-47 tag to its lower-case primary subtag ("pt-BR"
// to "pt"). Anything that is not two or three ASCII letters becomes "und".
func primaryLang(tag string) string {
	tag = strings.TrimSpace(tag)
	if i := strings.IndexAny(tag, "-_"); i >= 0 {
		tag = tag[:i]
	}
	if len(tag) < 2 || len(tag) > 3 {
		return undeterminedLang
	}
	for _, r := range tag {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return undeterminedLang
		}
	}
	return strings.ToLower(tag)
}

// postLang is the language a post is counted under: "en" whenever the
// English filter would accept it, otherwise its first tag's primary subtag.
// This keeps the "en" bucket aligned with the posts the bot analyses.
func postLang(langs []string) string {
	if isEnglish(langs) {
		return "en"
	}
	if len(langs) == 0 {
		return undeterminedLang
	}
	return primaryLang(langs[0])
}

func isEnglish(langs []string) bool {
	if len(langs) == 0 {
		return false
	}
	for _, l := range langs {
		if l == "en" || strings.HasPrefix(l, "en-") {
			return true
		}
	}
	return false
}

func normalizeTimestamp(raw string) string {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, raw)
	}
	if err != nil {
		return time.Now().UTC().Format(time.RFC3339)
	}
	return t.UTC().Format(time.RFC3339)
}
