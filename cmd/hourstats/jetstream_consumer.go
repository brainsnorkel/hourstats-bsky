package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/christophergentle/hourstats-bsky/internal/denylist"
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

	// defaultMaxPostRunes bounds a create's text before it is tokenised or
	// stored. The lexicon caps a post at 300 graphemes, so anything an order of
	// magnitude past that is a client bug or an attempt to make the tokeniser
	// and the analyzer do unbounded work on one row.
	defaultMaxPostRunes = 3000

	// denyListKey is the key_value row holding the operator denylist as a comma
	// list, re-read every few minutes so a DID can be added over `fly ssh`
	// without a deploy.
	denyListKey = "firehose_deny_dids"
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

	// A reconnect replays from before the last event seen — both protocols
	// rewind their cursor a few seconds — so a create can arrive again after
	// its delete has been applied. The tombstone set drops those creates.
	tombs := newTombstones()

	protocol := jetstream.ProtocolV2
	if envBool("JETSTREAM_LEGACY", false) {
		protocol = jetstream.ProtocolV1
	}
	// The delete and account-purge handlers are the one part of the firehose
	// path that removes rows, so they get their own rollback lever: with this
	// false and JETSTREAM_LEGACY true the consumer behaves exactly as it did
	// before the branch.
	deletesEnabled := envBool("FIREHOSE_DELETES_ENABLED", true)
	extraCollections := envList("JETSTREAM_EXTRA_COLLECTIONS")
	// v2 delivers repo backfills through the live tail as ordinary creates, so
	// only the record's own createdAt separates them from live posts.
	maxPostAgeMinutes := envInt("JETSTREAM_MAX_POST_AGE_MINUTES", 120)
	// Staging-only diagnostics for where those backfills come from, both off
	// by default so production is unaffected.
	staleSamplePerHour := envInt("JETSTREAM_STALE_SAMPLE_PER_HOUR", 0)
	extraKinds := envList("JETSTREAM_EXTRA_KINDS")
	// Intake clamps. A record dated ahead of its witness time is a forged or
	// badly skewed clock, not a live post; the per-DID cap clips a single loud
	// repo; the denylist is the operator's manual lever, unioned with the
	// key_value row so it can be edited without a deploy.
	maxPostRunes := envInt("FIREHOSE_MAX_POST_RUNES", defaultMaxPostRunes)
	maxPostsPerDID := envInt("FIREHOSE_MAX_POSTS_PER_DID_PER_MINUTE", 60)
	maxPostFutureMinutes := envInt("FIREHOSE_MAX_POST_FUTURE_MINUTES", 10)
	denyDIDs := envList("FIREHOSE_DENY_DIDS")

	cfg := jetstream.ConsumerConfig{
		Protocol:           protocol,
		DisableCompression: !envBool("JETSTREAM_COMPRESS", true),
		ExtraCollections:   extraCollections,
		ExtraKinds:         extraKinds,
		StaleSamplePerHour: staleSamplePerHour,
		// Posts the bytes-level pre-filter drops never reach OnPost, so they
		// are counted here; without this the firehose total is only English
		// plus untagged posts.
		OnEarlyReject: func(firstLang string) {
			collector.IncrementFirehosePost()
			collector.IncrementLanguage(primaryLang(firstLang))
		},
		// A create the per-DID cap drops is still a post that crossed the
		// firehose, exactly like one the language pre-filter rejected. Counting
		// it here is what keeps the firehose total and the language shares from
		// drifting for the repos the cap clips.
		OnCapped: func(firstLang string) {
			collector.IncrementFirehosePost()
			collector.IncrementLanguage(primaryLang(firstLang))
		},
		// The reset happens deep inside the consumer, which holds no collector;
		// the alerts package already knows this event type and was waiting for
		// something to write it.
		OnSeqFloorReset: func(dropped, floor, seq int64) {
			_ = collector.LogEvent(ctx, "seq_floor_reset",
				fmt.Sprintf("dropped=%d floor=%d seq=%d", dropped, floor, seq))
		},
		// Backfill is neither a firehose post nor a post of its language: it
		// was already counted the day it was written.
		OnStale: func(_ *jetstream.Event, _ *jetstream.PostRecord, _ time.Duration) {
			collector.IncrementStalePosts()
		},
		OnPost: func(evt *jetstream.Event, rec *jetstream.PostRecord) {
			collector.IncrementFirehosePost()
			collector.IncrementLanguage(postLang(rec.Langs))

			// The text is measured once, before anything tokenises, scores or
			// stores it: a record far past the lexicon's 300-grapheme cap would
			// otherwise carry its whole length into the tokeniser and into
			// post_buffer.
			if textRunes, oversized := oversizedPost(rec.Text, maxPostRunes); oversized {
				collector.IncrementOversizedPosts()
				slog.Debug("dropped an oversized post",
					"uri", evt.PostURI(), "text_runes", textRunes, "limit", maxPostRunes)
				return
			}

			if strings.TrimSpace(rec.Text) == "" {
				return
			}
			if !isEnglish(rec.Langs) {
				return
			}
			uri := evt.PostURI()
			if tombs.has(uri) {
				collector.IncrementTombstoneHits()
				return
			}
			cid := ""
			if evt.Commit != nil {
				cid = evt.Commit.CID
			}
			createdAt := normalizeTimestamp(rec.CreatedAt)
			post := store.Post{
				URI:       uri,
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
		// Both protocols resume from a unix-microsecond event time, v1 from
		// the cursor table and v2 from key_value (where the seq is stored
		// alongside it for diagnostics only). The consumer calls just the pair
		// its protocol selects, so a fallback to v1 finds its own row exactly
		// as it left it.
		SaveCursor: func(saveCtx context.Context, cursor int64) error {
			return db.SaveCursor(saveCtx, cursor)
		},
		LoadCursor: func(loadCtx context.Context) (int64, error) {
			return db.GetCursor(loadCtx)
		},
		SaveCursorV2: func(saveCtx context.Context, seq, timeUS int64) error {
			return db.SaveV2Cursor(saveCtx, seq, timeUS)
		},
		LoadCursorV2: func(loadCtx context.Context) (int64, int64, error) {
			return db.GetV2Cursor(loadCtx)
		},
		// The stored denylist is the union's second half. A missing row reads as
		// the empty list, exactly like an unset FIREHOSE_DENY_DIDS.
		LoadDenyList: func(loadCtx context.Context) ([]string, error) {
			return storedDenyDIDs(loadCtx, db), nil
		},
		CursorRewind:            time.Duration(envInt("JETSTREAM_CURSOR_REWIND_SECONDS", 5)) * time.Second,
		MaxCursorAge:            time.Duration(envInt("JETSTREAM_MAX_CURSOR_AGE_MINUTES", 360)) * time.Minute,
		MaxPostAge:              time.Duration(maxPostAgeMinutes) * time.Minute,
		MaxPostFuture:           time.Duration(maxPostFutureMinutes) * time.Minute,
		MaxPostsPerDIDPerMinute: maxPostsPerDID,
		DenyDIDs:                denyDIDs,
	}

	// Left nil when disabled: the consumer still receives the frames but
	// dispatch drops them, so no delete or purge ever reaches post_buffer.
	if deletesEnabled {
		// A delete removes the post from the buffer before it can be scored,
		// and tombstones it so a replayed create does not put it back.
		cfg.OnDelete = func(evt *jetstream.Event) {
			uri := evt.PostURI()
			tombs.add(uri, time.Now())
			pw := store.PendingWrite{Op: store.WriteDelete, Post: store.Post{URI: uri}}
			if !sendPost(ctx, writeCh, pw, writeSendTimeout) {
				collector.IncrementDroppedPosts(1)
				if n := drops.record(time.Now()); n > 0 {
					slog.Warn("write buffer full, dropping posts",
						"dropped_since_last_warning", n,
						"buffer_len", len(writeCh),
						"uri", uri,
					)
				}
				return
			}
			collector.IncrementPostDeletes()
		}
		// A deactivated, deleted, suspended or taken-down account's posts must
		// not be scored or quoted, so the whole author is purged from the
		// buffer.
		cfg.OnAccountInactive = func(evt *jetstream.Event) {
			pw := store.PendingWrite{Op: store.WritePurgeAuthor, Post: store.Post{AuthorDID: evt.DID}}
			if !sendPost(ctx, writeCh, pw, writeSendTimeout) {
				collector.IncrementDroppedPosts(1)
				if n := drops.record(time.Now()); n > 0 {
					slog.Warn("write buffer full, dropping posts",
						"dropped_since_last_warning", n,
						"buffer_len", len(writeCh),
					)
				}
				return
			}
			collector.IncrementAccountPurges()
			status := ""
			if evt.Account != nil {
				status = evt.Account.Status
			}
			// Account events are continuous network-wide, so this is Debug;
			// the count is on the stats snapshot as account_purges.
			slog.Debug("account inactive, purging posts", "status", status, "did", evt.DID)
		}
	} else {
		slog.Warn("firehose deletes disabled, deleted posts stay in the buffer until hydration or the 2h purge")
	}

	endpoints := jetstream.AllEndpointsV2
	if protocol == jetstream.ProtocolV1 {
		endpoints = jetstream.AllEndpoints
	}
	slog.Info("starting jetstream consumer",
		"protocol", protocol,
		"firehose_deletes", deletesEnabled,
		"compressed", protocol == jetstream.ProtocolV2 && !cfg.DisableCompression,
		"endpoints", endpoints,
		"extra_collections", extraCollections,
		"extra_kinds", extraKinds,
		"max_post_age_minutes", maxPostAgeMinutes,
		"max_post_future_minutes", maxPostFutureMinutes,
		"max_post_runes", maxPostRunes,
		"max_posts_per_did_per_minute", maxPostsPerDID,
		// The count only: the denylist names individual accounts and never
		// reaches an Info line.
		"deny_dids", len(denyDIDs),
		"stale_sample_per_hour", staleSamplePerHour,
	)

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

// storedDenyDIDs reads the denylist row from key_value. A missing row — the
// norm — reads as the empty list, so the read error is deliberately swallowed
// rather than reported as a failed load that would keep a stale list in place.
func storedDenyDIDs(ctx context.Context, db *store.Store) []string {
	raw, _ := db.GetKeyValue(ctx, denyListKey)
	return splitList(raw)
}

// publishDenyList publishes the union of FIREHOSE_DENY_DIDS and the stored row
// as the process-wide denylist. The consumer republishes the same union before
// its first dial and every few minutes after, but it is not the only reader:
// the posting feature gate consults the same list, and a job that runs before
// Consumer.Run — a REPORTS_RUN_AT_STARTUP report — would otherwise see an empty
// one and feature an account the operator has denied.
func publishDenyList(ctx context.Context, db *store.Store) {
	union := append(envList("FIREHOSE_DENY_DIDS"), storedDenyDIDs(ctx, db)...)
	denylist.Replace(union)
	// The count only: the list names individual accounts.
	slog.Info("firehose denylist loaded at startup", "denied_dids", denylist.Len())
}

// oversizedPost reports whether a record's text is past the rune limit, and
// returns the measured length so the caller does not count it a second time.
// The limit is on runes, not bytes: the lexicon's own cap is on graphemes, and
// a byte limit would refuse ordinary posts in scripts that encode wide.
func oversizedPost(text string, maxRunes int) (int, bool) {
	runes := utf8.RuneCountInString(text)
	return runes, runes > maxRunes
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
